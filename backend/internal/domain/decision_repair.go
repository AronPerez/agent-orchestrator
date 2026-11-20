package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// DecisionKindIdentityRepaired records that a machine gave up an identity it
// shared with another (a copied data directory): its entries from FromSeq on
// under that identity were re-issued under its new identity, and every
// machine that receives this moves the originals out of its log.
const DecisionKindIdentityRepaired DecisionEntryKind = "identity_repaired"

// MaxDecisionRepairPairsPerEntry keeps each identity_repaired entry well
// under the sync payload limit; a larger fork is listed over several parts.
const MaxDecisionRepairPairsPerEntry = 300

// DecisionIdentityRepairedPayload is one part of a repair. Reissued pairs each
// original entry id with the id of its re-issued copy.
type DecisionIdentityRepairedPayload struct {
	RepairID string      `json:"repairId"`
	Origin   string      `json:"origin"`
	FromSeq  int64       `json:"fromSeq"`
	Part     int         `json:"part"`
	Parts    int         `json:"parts"`
	Reissued [][2]string `json:"reissued"`
}

// DecisionRepairCallID is the call id identity_repaired entries are filed
// under. No call is ever reduced from it: it has no raised entry.
func DecisionRepairCallID(repairID string) string { return "repair_" + repairID }

// DecisionOriginID is one (sequence, entry id) of an origin, as compared when
// looking for where two histories of one identity parted.
type DecisionOriginID struct {
	Seq int64  `json:"seq"`
	ID  string `json:"id"`
}

// DecisionForkPoint returns the first sequence that both local, this
// machine's history of an origin, and the peer hold under different entry
// ids. A sequence the peer lacks proves nothing: the peer may only be
// behind. found is false when no held sequence differs.
func DecisionForkPoint(local, peer []DecisionOriginID) (seq int64, found bool) {
	peerAt := make(map[int64]string, len(peer))
	for _, p := range peer {
		peerAt[p.Seq] = p.ID
	}
	for _, l := range local {
		if id, held := peerAt[l.Seq]; held && id != l.ID {
			return l.Seq, true
		}
	}
	return 0, false
}

// ReissueDecisionPayload returns payload with one top-level field,
// "reissuedFrom", naming the original entry. The original bytes follow
// unchanged, so nothing else about the entry is re-encoded. A payload that
// was already re-issued once names the newest original instead.
func ReissueDecisionPayload(payload json.RawMessage, from string) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return nil, errors.New("decision payload is not a JSON object")
	}
	field, err := json.Marshal(from)
	if err != nil {
		return nil, err
	}
	if prior, err := DecisionReissuedFrom(trimmed); err != nil {
		return nil, err
	} else if prior != "" {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &fields); err != nil {
			return nil, err
		}
		fields["reissuedFrom"] = field
		return json.Marshal(fields)
	}
	rest := bytes.TrimSpace(trimmed[1 : len(trimmed)-1])
	out := make([]byte, 0, len(trimmed)+len(field)+18)
	out = append(out, `{"reissuedFrom":`...)
	out = append(out, field...)
	if len(rest) > 0 {
		out = append(out, ',')
		out = append(out, trimmed[1:]...)
	} else {
		out = append(out, '}')
	}
	return out, nil
}

// DecisionReissuedFrom returns the original entry id a re-issued payload
// names, or "" for an entry that was never re-issued.
func DecisionReissuedFrom(payload json.RawMessage) (string, error) {
	var head struct {
		ReissuedFrom string `json:"reissuedFrom"`
	}
	if err := json.Unmarshal(payload, &head); err != nil {
		return "", err
	}
	return head.ReissuedFrom, nil
}

// DecisionRepairPlan is a staged identity repair: this machine gives up
// Origin, which it shares with a copy of itself, from FromSeq on, and takes
// NewOrigin. ForkIDs are its own entries under Origin from FromSeq on.
type DecisionRepairPlan struct {
	RepairID  string   `json:"repairId"`
	Origin    string   `json:"origin"`
	FromSeq   int64    `json:"fromSeq"`
	NewOrigin string   `json:"newOrigin"`
	ForkIDs   []string `json:"forkIds"`
	// StagedThrough is this machine's highest sequence under Origin when the
	// repair was staged. Sync is off from then on, so every entry above it
	// was written here and belongs to the fork too.
	StagedThrough int64 `json:"stagedThrough,omitempty"`
}

// ForkThrough is the sequence above which every entry under Origin is this
// machine's own: StagedThrough, or for a plan without it the last fork entry.
func (p DecisionRepairPlan) ForkThrough(fork []DecisionEntry) int64 {
	if p.StagedThrough > 0 {
		return p.StagedThrough
	}
	var highest int64
	for _, e := range fork {
		highest = max(highest, e.OriginSeq)
	}
	return highest
}

// DecisionRepairInput is what a machine giving up Origin knows when it
// writes the repair.
type DecisionRepairInput struct {
	RepairID  string
	Origin    string
	FromSeq   int64
	NewOrigin string
	// StartSeq is the next sequence under NewOrigin.
	StartSeq int64
	// Fork is this machine's own entries under Origin from FromSeq on, in
	// sequence order.
	Fork []DecisionEntry
	// Stamp and Now date the identity_repaired entries themselves.
	Stamp DecisionStamp
	Now   time.Time
	NewID func() string
	// PairsPerEntry defaults to MaxDecisionRepairPairsPerEntry.
	PairsPerEntry int
}

// BuildDecisionRepairEntries returns the entries a repair appends under the
// new identity: the identity_repaired parts first, so every receiver moves
// the originals out before any copy arrives, then one copy of each fork
// entry. A copy keeps its original's call, kind, stamp, written-at and
// author, so ordering and "the later answer stands" are unchanged; only its
// id, origin, sequence and the added reissuedFrom field differ.
func BuildDecisionRepairEntries(in DecisionRepairInput) ([]DecisionEntry, error) {
	if in.RepairID == "" || in.Origin == "" || in.NewOrigin == "" || in.Origin == in.NewOrigin || in.FromSeq < 1 || in.StartSeq < 1 || in.NewID == nil {
		return nil, errors.New("decision repair input is incomplete")
	}
	if len(in.Fork) == 0 {
		return nil, errors.New("decision repair has no entries to re-issue")
	}
	per := in.PairsPerEntry
	if per < 1 {
		per = MaxDecisionRepairPairsPerEntry
	}
	copies := make([]DecisionEntry, len(in.Fork))
	pairs := make([][2]string, len(in.Fork))
	for i, orig := range in.Fork {
		if orig.Origin != in.Origin || orig.OriginSeq < in.FromSeq {
			return nil, fmt.Errorf("entry %s is not in the fork of %s from %d", orig.ID, in.Origin, in.FromSeq)
		}
		payload, err := ReissueDecisionPayload(orig.Payload, orig.ID)
		if err != nil {
			return nil, fmt.Errorf("re-issue %s: %w", orig.ID, err)
		}
		c := orig
		c.ID = in.NewID()
		c.Origin = in.NewOrigin
		c.Payload = payload
		copies[i] = c
		pairs[i] = [2]string{orig.ID, c.ID}
	}
	parts := (len(pairs) + per - 1) / per
	out := make([]DecisionEntry, 0, parts+len(copies))
	for part := range parts {
		chunk := pairs[part*per : min((part+1)*per, len(pairs))]
		raw, err := json.Marshal(DecisionIdentityRepairedPayload{
			RepairID: in.RepairID, Origin: in.Origin, FromSeq: in.FromSeq,
			Part: part + 1, Parts: parts, Reissued: chunk,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, DecisionEntry{
			ID: in.NewID(), Origin: in.NewOrigin, Stamp: in.Stamp, CallID: DecisionRepairCallID(in.RepairID),
			Kind: DecisionKindIdentityRepaired, Payload: raw, WrittenAt: in.Now,
			AuthorKind: DecisionAuthorPerson, AuthorSurface: RequestSurfaceCLI,
		})
	}
	out = append(out, copies...)
	for i := range out {
		out[i].OriginSeq = in.StartSeq + int64(i)
	}
	return out, nil
}

// DecisionRepairParts decodes the identity_repaired entries among entries,
// keyed by repair id. A part that does not decode is skipped.
func DecisionRepairParts(entries []DecisionEntry) map[string][]DecisionIdentityRepairedPayload {
	out := map[string][]DecisionIdentityRepairedPayload{}
	for _, e := range entries {
		if e.Kind != DecisionKindIdentityRepaired {
			continue
		}
		var p DecisionIdentityRepairedPayload
		if json.Unmarshal(e.Payload, &p) != nil || p.RepairID == "" {
			continue
		}
		out[p.RepairID] = append(out[p.RepairID], p)
	}
	return out
}
