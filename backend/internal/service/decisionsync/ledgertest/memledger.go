// Package ledgertest provides an in-memory decision ledger and the conformance
// suite that holds it and the SQLite store to the same sync behavior.
package ledgertest

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// MemLedger is an in-memory decision ledger for one machine. Local appends
// assign sequence and stamp exactly as the store does, and synced entries go
// through the same domain apply rules, so convergence tests over MemLedger
// speak for the real store as long as the conformance suite passes on both.
type MemLedger struct {
	HostID string

	mu        sync.Mutex
	byID      map[string]domain.DecisionEntry
	byOrigin  map[string]map[int64]domain.DecisionEntry
	conflicts []domain.DecisionSyncConflict
}

// NewMemLedger returns an empty ledger for hostID.
func NewMemLedger(hostID string) *MemLedger {
	return &MemLedger{
		HostID:   hostID,
		byID:     map[string]domain.DecisionEntry{},
		byOrigin: map[string]map[int64]domain.DecisionEntry{},
	}
}

// Append writes a local entry as this machine: origin, sequence and stamp are
// assigned the way the store assigns them.
func (l *MemLedger) Append(entry domain.DecisionEntry, now time.Time) domain.DecisionEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry.Origin = l.HostID
	entry.OriginSeq = l.highestLocked(l.HostID) + 1
	newest := domain.DecisionStamp{}
	if len(l.byID) > 0 {
		all := l.sortedLocked()
		last := all[len(all)-1]
		newest = last.Stamp
	}
	entry.Stamp = newest.Next(now.UnixMilli())
	if entry.WrittenAt.IsZero() {
		entry.WrittenAt = now
	}
	l.insertLocked(entry)
	return entry
}

// Entries returns every stored entry in ledger order.
func (l *MemLedger) Entries() []domain.DecisionEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sortedLocked()
}

// Calls returns every call reduced from the stored entries, keyed by id.
func (l *MemLedger) Calls() map[string]domain.DecisionCall {
	byCall := map[string][]domain.DecisionEntry{}
	for _, e := range l.Entries() {
		byCall[e.CallID] = append(byCall[e.CallID], e)
	}
	out := map[string]domain.DecisionCall{}
	for id, entries := range byCall {
		if call, ok := domain.ReduceDecisionCall(entries); ok {
			out[id] = call
		}
	}
	return out
}

// CallsJSON returns Calls encoded deterministically, for equality checks.
func (l *MemLedger) CallsJSON() string {
	raw, err := json.Marshal(l.Calls())
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// Conflicts returns the recorded conflicts.
func (l *MemLedger) Conflicts() []domain.DecisionSyncConflict {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.conflicts)
}

// DecisionVector implements decisionsync.Ledger.
func (l *MemLedger) DecisionVector(context.Context) (domain.DecisionVector, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.vectorLocked(), nil
}

// DecisionHeads implements decisionsync.Ledger.
func (l *MemLedger) DecisionHeads(context.Context) (domain.DecisionHeads, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	stopped := domain.LowestDecisionSyncConflicts(l.conflicts)
	heads := domain.DecisionHeads{}
	for origin := range l.byOrigin {
		seq := l.highestLocked(origin)
		e := l.byOrigin[origin][seq]
		_, isStopped := stopped[origin]
		heads[origin] = domain.DecisionHead{Seq: seq, ID: e.ID, WrittenAt: e.WrittenAt, Stopped: isStopped}
	}
	return heads, nil
}

// ListDecisionEntriesAfter implements decisionsync.Ledger.
func (l *MemLedger) ListDecisionEntriesAfter(_ context.Context, have domain.DecisionVector, maxEntries, maxBytes int) ([]domain.DecisionEntry, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if maxEntries < 1 {
		maxEntries = 1
	}
	return domain.PageDecisionEntries(l.vectorLocked(), domain.LowestDecisionSyncConflicts(l.conflicts), have, maxEntries, maxBytes,
		func(origin string, after, before int64, limit int) ([]domain.DecisionEntry, error) {
			var out []domain.DecisionEntry
			for seq, e := range l.byOrigin[origin] {
				if seq > after && seq < before {
					out = append(out, e)
				}
			}
			slices.SortFunc(out, func(a, b domain.DecisionEntry) int { return cmp.Compare(a.OriginSeq, b.OriginSeq) })
			if len(out) > limit {
				out = out[:limit]
			}
			return out, nil
		})
}

// ApplyDecisionEntries implements decisionsync.Ledger. Like the store, a
// failing batch stores nothing.
func (l *MemLedger) ApplyDecisionEntries(_ context.Context, peerHostID string, entries []domain.DecisionEntry, now time.Time) (domain.DecisionApplyResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	view := &memView{l: l}
	stopped := map[string]bool{}
	for _, c := range l.conflicts {
		stopped[c.Origin] = true
	}
	result, callIDs, err := domain.ApplyDecisionSyncBatch(view, peerHostID, entries, l.vectorLocked(), stopped, now)
	if err != nil {
		return domain.DecisionApplyResult{}, err
	}
	view.commit()
	all := l.sortedLocked()
	for _, id := range callIDs {
		var callEntries []domain.DecisionEntry
		for _, e := range all {
			if e.CallID == id {
				callEntries = append(callEntries, e)
			}
		}
		if call, ok := domain.ReduceDecisionCall(callEntries); ok {
			result.Calls = append(result.Calls, call)
		}
	}
	return result, nil
}

// CheckDecisionHeads implements decisionsync.Ledger.
func (l *MemLedger) CheckDecisionHeads(_ context.Context, peerHostID string, heads domain.DecisionHeads, now time.Time) ([]domain.DecisionSyncConflict, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	view := &memView{l: l}
	out, err := domain.CheckDecisionSyncHeads(view, peerHostID, heads, now)
	if err != nil {
		return nil, err
	}
	view.commit()
	return out, nil
}

func (l *MemLedger) vectorLocked() domain.DecisionVector {
	v := domain.DecisionVector{}
	for origin := range l.byOrigin {
		v[origin] = l.highestLocked(origin)
	}
	return v
}

func (l *MemLedger) highestLocked(origin string) int64 {
	var highest int64
	for seq := range l.byOrigin[origin] {
		highest = max(highest, seq)
	}
	return highest
}

func (l *MemLedger) insertLocked(e domain.DecisionEntry) {
	e.Payload = slices.Clone(e.Payload)
	l.byID[e.ID] = e
	if l.byOrigin[e.Origin] == nil {
		l.byOrigin[e.Origin] = map[int64]domain.DecisionEntry{}
	}
	l.byOrigin[e.Origin][e.OriginSeq] = e
}

func (l *MemLedger) sortedLocked() []domain.DecisionEntry {
	out := make([]domain.DecisionEntry, 0, len(l.byID))
	for _, e := range l.byID {
		out = append(out, e)
	}
	slices.SortFunc(out, domain.CompareDecisionEntries)
	return out
}

// memView stages a batch's writes so a failing batch stores nothing, like the
// store's transaction.
type memView struct {
	l         *MemLedger
	inserted  []domain.DecisionEntry
	conflicts []domain.DecisionSyncConflict
}

func (v *memView) EntryByID(id string) (domain.DecisionEntry, bool, error) {
	for _, e := range v.inserted {
		if e.ID == id {
			return e, true, nil
		}
	}
	e, ok := v.l.byID[id]
	return e, ok, nil
}

func (v *memView) EntryAt(origin string, seq int64) (domain.DecisionEntry, bool, error) {
	for _, e := range v.inserted {
		if e.Origin == origin && e.OriginSeq == seq {
			return e, true, nil
		}
	}
	e, ok := v.l.byOrigin[origin][seq]
	return e, ok, nil
}

func (v *memView) Insert(e domain.DecisionEntry) error {
	v.inserted = append(v.inserted, e)
	return nil
}

func (v *memView) RecordConflict(c domain.DecisionSyncConflict) (bool, error) {
	for _, existing := range append(slices.Clone(v.l.conflicts), v.conflicts...) {
		if existing.Origin == c.Origin && existing.OriginSeq == c.OriginSeq {
			return false, nil
		}
	}
	v.conflicts = append(v.conflicts, c)
	return true, nil
}

func (v *memView) commit() {
	for _, e := range v.inserted {
		v.l.insertLocked(e)
	}
	v.l.conflicts = append(v.l.conflicts, v.conflicts...)
}
