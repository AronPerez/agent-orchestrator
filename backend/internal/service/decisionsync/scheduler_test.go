package decisionsync_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync/ledgertest"
)

// fakeClock hands out timers the test fires by advancing time.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []fakeClockTimer
	asked  []time.Duration
}

type fakeClockTimer struct {
	at time.Time
	ch chan time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: t0} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.asked = append(c.asked, d)
	c.timers = append(c.timers, fakeClockTimer{at: c.now.Add(d), ch: ch})
	return ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.timers[:0]
	for _, tm := range c.timers {
		if !tm.at.After(c.now) {
			tm.ch <- c.now
			continue
		}
		kept = append(kept, tm)
	}
	c.timers = kept
}

func (c *fakeClock) Asked() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.asked...)
}

func (c *fakeClock) waitAsked(t *testing.T, n int) []time.Duration {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := c.Asked(); len(got) >= n {
			return got
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timers asked = %v, want at least %d", c.Asked(), n)
	return nil
}

// countingRemote answers empty exchanges as peer hostID, or fails.
type countingRemote struct {
	hostID    string
	exchanges *atomic.Int64
	fail      func() error
	block     <-chan struct{}
}

func (r *countingRemote) Exchange(ctx context.Context, _ decisionsync.ExchangeRequest) (decisionsync.ExchangeResponse, error) {
	r.exchanges.Add(1)
	if r.block != nil {
		select {
		case <-r.block:
		case <-ctx.Done():
			return decisionsync.ExchangeResponse{}, ctx.Err()
		}
	}
	if r.fail != nil {
		if err := r.fail(); err != nil {
			return decisionsync.ExchangeResponse{}, err
		}
	}
	return decisionsync.ExchangeResponse{Protocol: 1, HostID: r.hostID, MachineLabel: r.hostID + "-label", Have: domain.DecisionVector{}, Heads: domain.DecisionHeads{}}, nil
}

func (r *countingRemote) Push(context.Context, decisionsync.PushRequest) (decisionsync.PushResponse, error) {
	return decisionsync.PushResponse{}, nil
}

type staticLinks []decisionsync.StoredPeer

func (l staticLinks) Linked() []decisionsync.StoredPeer { return l }

func (l staticLinks) Get(hostID string) (decisionsync.StoredPeer, bool) {
	for _, p := range l {
		if p.HostID == hostID {
			return p, true
		}
	}
	return decisionsync.StoredPeer{}, false
}

type schedFixture struct {
	clock   *fakeClock
	sched   *decisionsync.Scheduler
	counts  map[string]*atomic.Int64
	store   *memPeerStore
	cancel  context.CancelFunc
	done    <-chan struct{}
	failMu  sync.Mutex
	failErr error
	block   chan struct{}
}

func newSchedFixture(t *testing.T, hosts []string, rnd float64) *schedFixture {
	t.Helper()
	f := &schedFixture{clock: newFakeClock(), counts: map[string]*atomic.Int64{}, store: newMemPeerStore()}
	var links staticLinks
	for _, h := range hosts {
		links = append(links, decisionsync.StoredPeer{HostID: h, URL: "http://" + h, Token: "aoc_" + h})
		f.counts[h] = &atomic.Int64{}
		_ = f.store.UpsertDecisionSyncPeer(context.Background(), domain.DecisionSyncPeer{HostID: h, Direction: domain.DecisionSyncOutbound})
	}
	engine := decisionsync.NewEngine(decisionsync.EngineDeps{Ledger: ledgertest.NewMemLedger("h_me"), HostID: "h_me", Now: f.clock.Now})
	f.sched = decisionsync.NewScheduler(decisionsync.SchedulerDeps{
		Engine: engine, Links: links, Store: f.store, Now: f.clock.Now, After: f.clock.After,
		Rand: func() float64 { return rnd },
		Remote: func(p decisionsync.StoredPeer) decisionsync.Remote {
			return &countingRemote{hostID: p.HostID, exchanges: f.counts[p.HostID], fail: f.failure, block: f.block}
		},
	})
	return f
}

func (f *schedFixture) failure() error {
	f.failMu.Lock()
	defer f.failMu.Unlock()
	return f.failErr
}

func (f *schedFixture) setFailure(err error) {
	f.failMu.Lock()
	f.failErr = err
	f.failMu.Unlock()
}

func (f *schedFixture) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.done = f.sched.Start(ctx)
	t.Cleanup(func() {
		cancel()
		select {
		case <-f.done:
		case <-time.After(3 * time.Second):
			t.Error("scheduler did not stop")
		}
	})
}

func waitCount(t *testing.T, c *atomic.Int64, want int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c.Load() >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("exchanges = %d, want %d", c.Load(), want)
}

func TestSchedulerIdleWithNoPeers(t *testing.T) {
	f := newSchedFixture(t, nil, 0.5)
	f.start(t)
	f.sched.Poke("")
	time.Sleep(30 * time.Millisecond)
	if got := f.clock.Asked(); len(got) != 0 {
		t.Fatalf("timers armed with no links: %v", got)
	}
	if results, err := f.sched.SyncNow(context.Background(), ""); err != nil || len(results) != 0 {
		t.Fatalf("SyncNow with no links = %v, %v", results, err)
	}
}

func TestSchedulerPokeCoalesces(t *testing.T) {
	f := newSchedFixture(t, []string{"h_a"}, 0.5)
	f.start(t)
	f.clock.waitAsked(t, 1) // the initial sync ran and armed the idle timer
	for i := 0; i < 10; i++ {
		f.sched.Poke("")
	}
	if got := f.clock.waitAsked(t, 2); got[1] != time.Second {
		t.Fatalf("poke timer = %v, want 1s", got)
	}
	f.clock.Advance(time.Second)
	waitCount(t, f.counts["h_a"], 2)
	f.clock.waitAsked(t, 3)
	time.Sleep(20 * time.Millisecond)
	if n := f.counts["h_a"].Load(); n != 2 {
		t.Fatalf("exchanges after ten pokes = %d, want one sync after the initial one", n)
	}
}

func TestSchedulerPokeSkipsSourcePeer(t *testing.T) {
	f := newSchedFixture(t, []string{"h_a", "h_b"}, 0.5)
	f.start(t)
	f.clock.waitAsked(t, 2)
	f.sched.Poke("h_a")
	f.clock.waitAsked(t, 3)
	f.clock.Advance(time.Second)
	waitCount(t, f.counts["h_b"], 2)
	time.Sleep(20 * time.Millisecond)
	if n := f.counts["h_a"].Load(); n != 1 {
		t.Fatalf("source peer synced %d times, want only its initial sync", n)
	}
}

func TestSchedulerTimerJitter(t *testing.T) {
	for _, tc := range []struct {
		rnd  float64
		want time.Duration
	}{{0, 54 * time.Second}, {0.5, 60 * time.Second}, {0.999999, 66 * time.Second}} {
		f := newSchedFixture(t, []string{"h_a"}, tc.rnd)
		f.start(t)
		got := f.clock.waitAsked(t, 1)[0]
		if got < 54*time.Second || got > 66*time.Second || (got-tc.want).Abs() > 10*time.Millisecond {
			t.Fatalf("rand %v: idle wait = %v, want %v (54s-66s)", tc.rnd, got, tc.want)
		}
		f.cancel()
		<-f.done
	}
}

func TestSchedulerBacksOff(t *testing.T) {
	f := newSchedFixture(t, []string{"h_a"}, 0.5)
	f.setFailure(&decisionsync.RemoteError{Code: decisionsync.ErrorUnreachable})
	f.start(t)
	want := []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	for i, wait := range want {
		got := f.clock.waitAsked(t, i+1)
		if got[i] != wait {
			t.Fatalf("backoff %d = %v, want %v (all: %v)", i+1, got[i], wait, got)
		}
		if i == 0 {
			// A failing peer ignores pokes: no poke timer is armed.
			f.sched.Poke("")
			time.Sleep(20 * time.Millisecond)
			if len(f.clock.Asked()) != 1 {
				t.Fatalf("poke armed a timer during backoff: %v", f.clock.Asked())
			}
		}
		f.clock.Advance(wait)
	}
	f.clock.waitAsked(t, len(want)+1)
	f.setFailure(nil)
	results, err := f.sched.SyncNow(context.Background(), "h_a")
	if err != nil || len(results) != 1 || results[0].ErrorCode != "" || results[0].Label != "h_a-label" {
		t.Fatalf("SyncNow = %+v, %v (an unlabelled link reports the peer's own name)", results, err)
	}
	got := f.clock.waitAsked(t, len(want)+2)
	if last := got[len(got)-1]; last != time.Minute {
		t.Fatalf("wait after a successful SyncNow = %v, want the idle minute", last)
	}
}

func TestSchedulerNeverBlocksAppend(t *testing.T) {
	f := newSchedFixture(t, []string{"h_a"}, 0.5)
	f.block = make(chan struct{})
	defer close(f.block)
	f.start(t)
	waitCount(t, f.counts["h_a"], 1) // the worker is stuck inside a sync
	start := time.Now()
	for i := 0; i < 100; i++ {
		f.sched.Poke("")
	}
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Fatalf("pokes during a hung sync took %v", took)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := f.sched.SyncNow(ctx, "h_a"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SyncNow during a hung sync = %v, want the caller's deadline", err)
	}
	if _, err := f.sched.SyncNow(context.Background(), "h_unknown"); !errors.Is(err, decisionsync.ErrPeerNotLinked) {
		t.Fatalf("SyncNow unknown peer = %v", err)
	}
}

func TestSchedulerRecordsOutcome(t *testing.T) {
	f := newSchedFixture(t, []string{"h_a"}, 0.5)
	f.start(t)
	f.clock.waitAsked(t, 1)
	if row := f.store.get("h_a"); row.LastSuccessAt.IsZero() || row.LastErrorCode != "" || row.Label != "h_a-label" || !row.InSync {
		t.Fatalf("after success = %+v", row)
	}
	for _, code := range []string{decisionsync.ErrorRevoked, decisionsync.ErrorIncompatible} {
		f.setFailure(&decisionsync.RemoteError{Code: code, Status: 401})
		if results, _ := f.sched.SyncNow(context.Background(), "h_a"); len(results) != 1 || results[0].ErrorCode != code {
			t.Fatalf("SyncNow = %+v, want %s", results, code)
		}
		if row := f.store.get("h_a"); row.LastErrorCode != code || row.InSync {
			t.Fatalf("after %s = %+v", code, row)
		}
	}
}

func TestHTTPRemoteProbesBeforeBearer(t *testing.T) {
	var mu sync.Mutex
	var bearers []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		bearers = append(bearers, r.Header.Get("Authorization"))
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"hostId": "h_impostor", "apiVersion": 1})
	}))
	defer srv.Close()
	remote := decisionsync.NewHTTPClient(nil).Remote(decisionsync.StoredPeer{HostID: "h_mini", URL: srv.URL, Token: "aoc_secret"})
	_, err := remote.Exchange(context.Background(), decisionsync.ExchangeRequest{Protocol: 1, HostID: "h_me"})
	if !errors.Is(err, decisionsync.ErrPeerIdentityChanged) || decisionsync.ErrorCode(err) != decisionsync.ErrorIdentityChanged {
		t.Fatalf("err = %v", err)
	}
	if _, err := remote.Push(context.Background(), decisionsync.PushRequest{Protocol: 1, HostID: "h_me"}); !errors.Is(err, decisionsync.ErrPeerIdentityChanged) {
		t.Fatalf("push err = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, b := range bearers {
		if b != "" {
			t.Fatalf("a request carried the bearer to the wrong machine: %q", bearers)
		}
	}
}
