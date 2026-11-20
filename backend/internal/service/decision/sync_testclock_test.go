package decision

import (
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
)

type syncTestClock struct {
	mu     sync.Mutex
	now    time.Time
	asked  int
	timers []syncTestTimer
}

type syncTestTimer struct {
	at time.Time
	ch chan time.Time
}

func newSyncTestClock() *syncTestClock { return &syncTestClock{now: time.UnixMilli(1_000_000).UTC()} }

func (c *syncTestClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.asked++
	c.timers = append(c.timers, syncTestTimer{at: c.now.Add(d), ch: ch})
	return ch
}

func (c *syncTestClock) Advance(d time.Duration) {
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

func (c *syncTestClock) waitAsked(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		asked := c.asked
		c.mu.Unlock()
		if asked >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timers asked < %d", n)
}

type syncTestLinks []decisionsync.StoredPeer

func (l syncTestLinks) Linked() []decisionsync.StoredPeer { return l }

func (l syncTestLinks) Get(hostID string) (decisionsync.StoredPeer, bool) {
	for _, p := range l {
		if p.HostID == hostID {
			return p, true
		}
	}
	return decisionsync.StoredPeer{}, false
}
