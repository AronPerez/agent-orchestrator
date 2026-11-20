package ledgertest

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
)

// DirectRemote calls a peer's Server in process, as the HTTP client will over
// the network. Every message is round-tripped through JSON so tests see the
// same bytes a real transport would carry.
type DirectRemote struct {
	Server   *decisionsync.Server
	CallerID string
	// BeforeCall, when set, runs before each call with its 1-based number and
	// may return an error to fail it (simulating a cut connection).
	BeforeCall func(n int) error

	mu        sync.Mutex
	Exchanges int
	Pushes    int
}

// Exchange implements decisionsync.Remote.
func (d *DirectRemote) Exchange(ctx context.Context, req decisionsync.ExchangeRequest) (decisionsync.ExchangeResponse, error) {
	if err := d.before(&d.Exchanges); err != nil {
		return decisionsync.ExchangeResponse{}, err
	}
	resp, err := d.Server.Exchange(ctx, d.CallerID, roundTrip(req))
	entries := wireRoundTrip(resp.Entries)
	resp.Entries = nil
	resp = roundTrip(resp)
	resp.Entries = entries
	return resp, err
}

// Push implements decisionsync.Remote.
func (d *DirectRemote) Push(ctx context.Context, req decisionsync.PushRequest) (decisionsync.PushResponse, error) {
	if err := d.before(&d.Pushes); err != nil {
		return decisionsync.PushResponse{}, err
	}
	entries := wireRoundTrip(req.Entries)
	req.Entries = nil
	req = roundTrip(req)
	req.Entries = entries
	resp, err := d.Server.Push(ctx, d.CallerID, req)
	return roundTrip(resp), err
}

func (d *DirectRemote) before(counter *int) error {
	d.mu.Lock()
	*counter++
	n := d.Exchanges + d.Pushes
	d.mu.Unlock()
	if d.BeforeCall != nil {
		return d.BeforeCall(n)
	}
	return nil
}

// wireRoundTrip sends entries through JSON in their wire form, as the HTTP
// transport does.
func wireRoundTrip(entries []domain.DecisionEntry) []domain.DecisionEntry {
	if entries == nil {
		return nil
	}
	return decisionsync.FromWireEntries(roundTrip(decisionsync.ToWireEntries(entries)))
}

// roundTrip sends v through JSON. Callers strip entries first: their raw
// payloads would not survive Go's encoder byte-for-byte.
func roundTrip[T any](v T) T {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(err)
	}
	return out
}
