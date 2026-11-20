package domain

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"time"
)

// DecisionApplyView is one ledger inside a write transaction, as the sync
// apply rules see it. The SQLite store and the in-memory test ledger both
// implement it, so the rules below are the only copy.
type DecisionApplyView interface {
	// EntryByID returns the stored entry with id, if any.
	EntryByID(id string) (DecisionEntry, bool, error)
	// EntryAt returns the stored entry at (origin, seq), if any.
	EntryAt(origin string, seq int64) (DecisionEntry, bool, error)
	// Insert stores e verbatim.
	Insert(e DecisionEntry) error
	// RecordConflict stores c once per (origin, seq) and reports whether this
	// call recorded it.
	RecordConflict(c DecisionSyncConflict) (bool, error)
}

// ApplyDecisionSyncBatch applies received entries to view, origin by origin in
// sequence order. highest is the view's current vector and stopped the origins
// with a recorded conflict; neither is modified. The returned result has no
// Calls: the caller reprojects the returned call ids once each.
//
// Per entry: the same id and identical contents is a duplicate and skipped;
// the same id with any difference, or another id at an (origin, seq) already
// held, is a conflict that is recorded and stops the origin; a sequence that
// does not extend the origin's highest by one is a gap that stops the origin
// for this batch; anything else is stored verbatim. Every entry is validated
// first, and an invalid one fails the whole batch before anything is stored.
func ApplyDecisionSyncBatch(view DecisionApplyView, peerHostID string, entries []DecisionEntry, highest DecisionVector, stopped map[string]bool, now time.Time) (DecisionApplyResult, []string, error) {
	var result DecisionApplyResult
	for _, e := range entries {
		if err := ValidateDecisionSyncEntry(e); err != nil {
			return DecisionApplyResult{}, nil, fmt.Errorf("entry %q: %w", e.ID, err)
		}
	}
	ordered := slices.Clone(entries)
	slices.SortStableFunc(ordered, func(a, b DecisionEntry) int {
		return cmp.Or(cmp.Compare(a.Origin, b.Origin), cmp.Compare(a.OriginSeq, b.OriginSeq))
	})
	held := highest.Clone()
	halted := map[string]bool{}
	affected := map[string]bool{}
	conflict := func(e DecisionEntry, localID string) error {
		c := DecisionSyncConflict{
			Origin: e.Origin, OriginSeq: e.OriginSeq, LocalEntryID: localID,
			RemoteEntryID: e.ID, PeerHostID: peerHostID, DetectedAt: now,
		}
		recorded, err := view.RecordConflict(c)
		if err != nil {
			return err
		}
		if recorded {
			result.Conflicts = append(result.Conflicts, c)
		}
		halted[e.Origin] = true
		return nil
	}
	for _, e := range ordered {
		if stopped[e.Origin] || halted[e.Origin] {
			continue
		}
		existing, ok, err := view.EntryByID(e.ID)
		if err != nil {
			return DecisionApplyResult{}, nil, err
		}
		if ok {
			if SameDecisionEntry(existing, e) {
				result.Duplicates++
				continue
			}
			if err := conflict(e, existing.ID); err != nil {
				return DecisionApplyResult{}, nil, err
			}
			continue
		}
		have := held[e.Origin]
		if e.OriginSeq <= have {
			at, ok, err := view.EntryAt(e.Origin, e.OriginSeq)
			if err != nil {
				return DecisionApplyResult{}, nil, err
			}
			if ok {
				if err := conflict(e, at.ID); err != nil {
					return DecisionApplyResult{}, nil, err
				}
				continue
			}
		}
		if e.OriginSeq != have+1 {
			result.Gaps = append(result.Gaps, DecisionSyncGap{Origin: e.Origin, Have: have, Got: e.OriginSeq})
			halted[e.Origin] = true
			continue
		}
		if err := view.Insert(e); err != nil {
			return DecisionApplyResult{}, nil, err
		}
		held[e.Origin] = e.OriginSeq
		affected[e.CallID] = true
		result.Applied++
	}
	callIDs := make([]string, 0, len(affected))
	for id := range affected {
		callIDs = append(callIDs, id)
	}
	slices.Sort(callIDs)
	return result, callIDs, nil
}

// CheckDecisionSyncHeads compares a peer's heads with view: wherever view holds
// an entry at a head's sequence and its id differs, the origin forked, and the
// conflict is recorded. Heads beyond what view holds cannot be compared here;
// the peer compares this ledger's heads instead.
func CheckDecisionSyncHeads(view DecisionApplyView, peerHostID string, heads DecisionHeads, now time.Time) ([]DecisionSyncConflict, error) {
	origins := make([]string, 0, len(heads))
	for origin := range heads {
		origins = append(origins, origin)
	}
	slices.Sort(origins)
	var out []DecisionSyncConflict
	for _, origin := range origins {
		head := heads[origin]
		if head.Seq < 1 || head.ID == "" {
			continue
		}
		at, ok, err := view.EntryAt(origin, head.Seq)
		if err != nil {
			return nil, err
		}
		if !ok || at.ID == head.ID {
			continue
		}
		c := DecisionSyncConflict{
			Origin: origin, OriginSeq: head.Seq, LocalEntryID: at.ID,
			RemoteEntryID: head.ID, PeerHostID: peerHostID, DetectedAt: now,
		}
		recorded, err := view.RecordConflict(c)
		if err != nil {
			return nil, err
		}
		if recorded {
			out = append(out, c)
		}
	}
	return out, nil
}

// LowestDecisionSyncConflicts returns, per origin, the lowest conflicted
// sequence: the point past which the origin is neither applied nor served.
func LowestDecisionSyncConflicts(conflicts []DecisionSyncConflict) map[string]int64 {
	out := map[string]int64{}
	for _, c := range conflicts {
		if low, ok := out[c.Origin]; !ok || c.OriginSeq < low {
			out[c.Origin] = c.OriginSeq
		}
	}
	return out
}

// PageDecisionEntries is the sync page reader shared by every ledger. vector
// is the ledger's own, stoppedAt its conflicted origins (with their lowest
// conflicted sequence), and have what the other side holds. read returns up to
// limit entries of origin with after < seq < before, in sequence order. See
// ListDecisionEntriesAfter on the store for the paging rules.
//
// A conflicted origin is not served at all. The recorded sequence is only
// where the fork was noticed: two copies that forked at 3 and compared heads
// at 10 already differ from 3 on, so nothing of that origin can be vouched for.
func PageDecisionEntries(vector DecisionVector, stoppedAt map[string]int64, have DecisionVector, maxEntries, maxBytes int,
	read func(origin string, after, before int64, limit int) ([]DecisionEntry, error)) ([]DecisionEntry, bool, error) {
	origins := make([]string, 0, len(vector))
	for origin := range vector {
		origins = append(origins, origin)
	}
	slices.Sort(origins)
	var page []DecisionEntry
	bytes := 0
	for _, origin := range origins {
		if _, quarantined := stoppedAt[origin]; quarantined {
			continue
		}
		highest, before := vector[origin], int64(math.MaxInt64)
		after := have[origin]
		if highest <= after {
			continue
		}
		if len(page) >= maxEntries {
			return page, true, nil
		}
		rows, err := read(origin, after, before, maxEntries-len(page))
		if err != nil {
			return nil, false, err
		}
		for _, e := range rows {
			if len(page) > 0 && bytes+len(e.Payload) > maxBytes {
				return page, true, nil
			}
			page = append(page, e)
			bytes += len(e.Payload)
		}
		if len(rows) > 0 && rows[len(rows)-1].OriginSeq < highest {
			return page, true, nil
		}
	}
	return page, false, nil
}
