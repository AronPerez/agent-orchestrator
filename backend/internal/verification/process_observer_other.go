//go:build !windows

package verification

import (
	"context"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

const unixDescendantObservationInterval = 100 * time.Millisecond

type unixDescendantWatch struct {
	owned map[unixOwnedProcess]struct{}
}

type unixDescendantObserver struct {
	mu       sync.Mutex
	watches  map[*unixDescendantWatch]struct{}
	wake     chan struct{}
	running  bool
	interval time.Duration
	read     func(context.Context) (map[int]procmem.Identity, error)
}

var sharedUnixDescendantObserver = newUnixDescendantObserver(
	unixDescendantObservationInterval,
	procmem.Identities,
)

func newUnixDescendantObserver(
	interval time.Duration,
	read func(context.Context) (map[int]procmem.Identity, error),
) *unixDescendantObserver {
	return &unixDescendantObserver{
		watches: make(map[*unixDescendantWatch]struct{}),
		wake:    make(chan struct{}, 1), interval: interval, read: read,
	}
}

func (o *unixDescendantObserver) watch(root unixOwnedProcess) *unixDescendantWatch {
	watch := &unixDescendantWatch{owned: map[unixOwnedProcess]struct{}{root: {}}}
	o.mu.Lock()
	o.watches[watch] = struct{}{}
	if !o.running {
		o.running = true
		go o.run()
	}
	o.mu.Unlock()
	o.wakeScan()
	return watch
}

func (o *unixDescendantObserver) unwatch(watch *unixDescendantWatch) {
	o.mu.Lock()
	delete(o.watches, watch)
	o.mu.Unlock()
	o.wakeScan()
}

func (o *unixDescendantObserver) wakeScan() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

func (o *unixDescendantObserver) run() {
	timer := time.NewTimer(o.interval)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
		case <-o.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}

		o.mu.Lock()
		if len(o.watches) == 0 {
			o.running = false
			o.mu.Unlock()
			return
		}
		o.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		identities, err := o.read(ctx)
		cancel()
		if err == nil {
			o.observe(identities)
		}
		timer.Reset(o.interval)
	}
}

func (o *unixDescendantObserver) observe(identities map[int]procmem.Identity) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for watch := range o.watches {
		observeUnixDescendants(watch.owned, identities)
	}
}

func (o *unixDescendantObserver) current(
	ctx context.Context,
	watch *unixDescendantWatch,
) (map[int]procmem.Identity, []unixOwnedProcess, error) {
	identities, err := o.read(ctx)
	if err != nil {
		return nil, nil, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.watches[watch]; ok {
		observeUnixDescendants(watch.owned, identities)
	}
	return identities, liveUnixDescendants(watch.owned, identities), nil
}

func observeUnixDescendants(
	owned map[unixOwnedProcess]struct{},
	identities map[int]procmem.Identity,
) {
	for {
		added := false
		for pid, identity := range identities {
			if !identity.Known() {
				continue
			}
			process := unixOwnedProcess{pid: pid, start: identity.Start}
			if _, ok := owned[process]; ok {
				continue
			}
			parent, ok := identities[identity.PPID]
			if !ok || !parent.Known() {
				continue
			}
			if _, ok := owned[unixOwnedProcess{pid: identity.PPID, start: parent.Start}]; !ok {
				continue
			}
			owned[process] = struct{}{}
			added = true
		}
		if !added {
			return
		}
	}
}

func liveUnixDescendants(
	owned map[unixOwnedProcess]struct{},
	identities map[int]procmem.Identity,
) []unixOwnedProcess {
	live := make([]unixOwnedProcess, 0, len(owned))
	for process := range owned {
		identity, ok := identities[process.pid]
		if ok && identity.Start == process.start {
			live = append(live, process)
		}
	}
	return live
}
