package decisionsync

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Scheduling (plan decision 13).
const (
	// pokeDelay coalesces a burst of local writes into one sync per peer.
	pokeDelay = time.Second
	// idleInterval is how often an idle, healthy link syncs, give or take
	// idleJitter of it.
	idleInterval = time.Minute
	idleJitter   = 0.10
	// syncTimeout bounds one whole sync, however many pages it moves.
	syncTimeout = 5 * time.Minute
	// quickEmptyWait: a waiting exchange that comes back empty sooner than
	// this means the peer does not hold waits; the loop then rests for the
	// idle interval instead of asking again at once.
	quickEmptyWait = 5 * time.Second
)

// backoffSteps are the waits after one, two, three... failures in a row.
var backoffSteps = []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute}

// LinkSource is where the scheduler finds outbound links and their tokens.
type LinkSource interface {
	Linked() []StoredPeer
	Get(hostID string) (StoredPeer, bool)
}

// AttemptRecorder stores each sync's result in the peer's status row.
type AttemptRecorder interface {
	RecordDecisionSyncAttempt(ctx context.Context, hostID string, attempt domain.DecisionSyncAttempt) (bool, error)
}

// SchedulerDeps configures the scheduler.
type SchedulerDeps struct {
	Engine *Engine
	Links  LinkSource
	Remote func(StoredPeer) Remote
	Store  AttemptRecorder
	Now    func() time.Time
	// After is the timer; tests replace it. Defaults to time.After.
	After func(time.Duration) <-chan time.Time
	// Rand returns a number in [0, 1) for jitter. Defaults to math/rand.
	Rand func() float64
	Log  *slog.Logger
}

// SyncResult is one peer's outcome from SyncNow.
type SyncResult struct {
	HostID string
	// Label is the name the person gave the link, else the peer's own name.
	Label   string
	Outcome Outcome
	// ErrorCode is empty on success.
	ErrorCode string
}

// Scheduler runs each outbound link's syncs in its own worker: at start,
// shortly after a poke, on an idle timer, and on request. A peer that fails
// backs off; nothing it does ever blocks a local write or a request.
type Scheduler struct {
	deps SchedulerDeps

	mu      sync.Mutex
	ctx     context.Context
	workers map[string]*peerWorker
	wg      sync.WaitGroup
}

// NewScheduler returns a scheduler over deps. Call Start to run it.
func NewScheduler(deps SchedulerDeps) *Scheduler {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.After == nil {
		deps.After = time.After
	}
	if deps.Rand == nil {
		deps.Rand = rand.Float64
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Scheduler{deps: deps, workers: map[string]*peerWorker{}}
}

// Start runs a worker per link until ctx ends. The returned channel closes
// when every worker has stopped.
func (s *Scheduler) Start(ctx context.Context) <-chan struct{} {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	s.Refresh()
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		for id, w := range s.workers {
			w.cancel()
			delete(s.workers, id)
		}
		s.mu.Unlock()
		s.wg.Wait()
		close(done)
	}()
	return done
}

// Refresh starts a worker for each new link, which syncs at once, and stops
// the workers of removed links. Call it after linking or unlinking.
func (s *Scheduler) Refresh() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx == nil || s.ctx.Err() != nil {
		return
	}
	linked := map[string]bool{}
	for _, peer := range s.deps.Links.Linked() {
		linked[peer.HostID] = true
		if _, running := s.workers[peer.HostID]; running {
			continue
		}
		ctx, cancel := context.WithCancel(s.ctx)
		w := &peerWorker{
			s: s, hostID: peer.HostID, cancel: cancel,
			poke: make(chan struct{}, 1), now: make(chan syncRequest), healthy: make(chan struct{}, 1),
		}
		s.workers[peer.HostID] = w
		s.wg.Add(2)
		go func() {
			defer s.wg.Done()
			w.run(ctx)
		}()
		go func() {
			defer s.wg.Done()
			w.waitLoop(ctx)
		}()
	}
	for id, w := range s.workers {
		if !linked[id] {
			w.cancel()
			delete(s.workers, id)
		}
	}
}

// Poke asks every link except exceptHostID to sync soon. It never blocks.
func (s *Scheduler) Poke(exceptHostID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, w := range s.workers {
		if id == exceptHostID {
			continue
		}
		select {
		case w.poke <- struct{}{}:
		default:
		}
	}
}

// SyncNow syncs one link (or every link when hostID is empty) right away and
// resets its backoff.
func (s *Scheduler) SyncNow(ctx context.Context, hostID string) ([]SyncResult, error) {
	s.mu.Lock()
	var targets []*peerWorker
	for id, w := range s.workers {
		if hostID == "" || id == hostID {
			targets = append(targets, w)
		}
	}
	s.mu.Unlock()
	if hostID != "" && len(targets) == 0 {
		return nil, ErrPeerNotLinked
	}
	results := make([]SyncResult, 0, len(targets))
	for _, w := range targets {
		reply := make(chan SyncResult, 1)
		select {
		case w.now <- syncRequest{reply: reply}:
		case <-ctx.Done():
			return results, ctx.Err()
		}
		select {
		case r := <-reply:
			results = append(results, r)
		case <-ctx.Done():
			return results, ctx.Err()
		}
	}
	return results, nil
}

type syncRequest struct {
	reply chan SyncResult
}

type peerWorker struct {
	s        *Scheduler
	hostID   string
	cancel   context.CancelFunc
	poke     chan struct{}
	now      chan syncRequest
	failures int
	// healthy is signalled after each successful sync; it (re)starts the
	// waiting loop.
	healthy chan struct{}
}

// waitLoop keeps one waiting exchange open on the peer while the link is
// healthy, so a change there reaches this machine within seconds even though
// the peer cannot reach this machine. Any failure ends the loop until the
// next successful sync; the timer and backoff cover the gap.
func (w *peerWorker) waitLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.healthy:
		}
		for ctx.Err() == nil {
			peer, ok := w.s.deps.Links.Get(w.hostID)
			if !ok {
				return
			}
			started := w.s.deps.Now()
			res, err := w.s.deps.Engine.Wait(ctx, w.hostID, w.s.deps.Remote(peer), MaxWaitSeconds)
			if err != nil {
				break
			}
			if res.More {
				select {
				case w.poke <- struct{}{}:
				default:
				}
			}
			if res.Empty && w.s.deps.Now().Sub(started) < quickEmptyWait {
				select {
				case <-ctx.Done():
					return
				case <-w.s.deps.After(idleInterval):
				}
			}
		}
	}
}

func (w *peerWorker) run(ctx context.Context) {
	w.sync(ctx)
	next := w.s.deps.After(w.wait())
	var poked <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.poke:
			// A failing peer keeps its backoff: local writes do not hammer it.
			if w.failures == 0 && poked == nil {
				poked = w.s.deps.After(pokeDelay)
			}
			continue
		case <-poked:
			w.sync(ctx)
		case <-next:
			w.sync(ctx)
		case req := <-w.now:
			w.failures = 0
			req.reply <- w.sync(ctx)
		}
		poked = nil
		next = w.s.deps.After(w.wait())
	}
}

// wait is the delay before the next unprompted sync.
func (w *peerWorker) wait() time.Duration {
	if w.failures == 0 {
		spread := 1 + idleJitter*(2*w.s.deps.Rand()-1)
		return time.Duration(float64(idleInterval) * spread)
	}
	return backoffSteps[min(w.failures, len(backoffSteps))-1]
}

func (w *peerWorker) sync(ctx context.Context) SyncResult {
	result := SyncResult{HostID: w.hostID}
	peer, ok := w.s.deps.Links.Get(w.hostID)
	if !ok {
		result.ErrorCode = ErrorServer
		return result
	}
	syncCtx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	out, err := w.s.deps.Engine.Sync(syncCtx, w.hostID, w.s.deps.Remote(peer))
	result.Outcome, result.Label = out, peer.Label
	if result.Label == "" {
		result.Label = out.PeerLabel
	}
	if ctx.Err() != nil {
		// Shutting down or unlinked: record nothing.
		return result
	}
	attempt := domain.DecisionSyncAttempt{At: w.s.deps.Now()}
	if err != nil {
		w.failures++
		result.ErrorCode = ErrorCode(err)
		attempt.ErrorCode = result.ErrorCode
		w.s.deps.Log.Info("decision sync failed", "peer", w.hostID, "code", result.ErrorCode, "failures", w.failures)
	} else {
		w.failures = 0
		select {
		case w.healthy <- struct{}{}:
		default:
		}
		attempt.InSync, attempt.PeerLabel, attempt.PeerVector = out.InSync, out.PeerLabel, out.PeerVector
		attempt.ClockAheadMS, attempt.PeerProtocol = out.ClockAhead.Milliseconds(), domain.DecisionSyncProtocol
		if out.Pulled+out.Pushed > 0 || len(out.Conflicts) > 0 {
			w.s.deps.Log.Info("decision sync", "peer", w.hostID, "pulled", out.Pulled, "pushed", out.Pushed,
				"inSync", out.InSync, "conflicts", len(out.Conflicts), "gaps", len(out.Gaps))
		}
	}
	if w.s.deps.Store != nil {
		if _, recErr := w.s.deps.Store.RecordDecisionSyncAttempt(context.WithoutCancel(ctx), w.hostID, attempt); recErr != nil {
			w.s.deps.Log.Warn("decision sync: record attempt", "peer", w.hostID, "err", recErr)
		}
	}
	return result
}
