package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"time"
	"unicode"
	"unicode/utf8"
)

// Limits for decision ledger replication between machines.
const (
	// DecisionSyncProtocol is the version of the sync exchange this build speaks.
	DecisionSyncProtocol = 1
	// MaxDecisionSyncPayloadBytes caps one received entry's payload.
	MaxDecisionSyncPayloadBytes = 64 << 10
	// MaxDecisionSyncPageEntries caps the entries in one exchanged page.
	MaxDecisionSyncPageEntries = 500
	// MaxDecisionSyncPageBytes caps the payload bytes in one exchanged page.
	// A page always holds at least one entry, whatever its size.
	MaxDecisionSyncPageBytes = 1 << 20
	// MaxDecisionSyncIDLength caps every identity field of a received entry.
	MaxDecisionSyncIDLength = 128
)

// DecisionVector maps an origin host id to the highest origin_seq held for it.
// Because apply refuses gaps, the highest held is also the contiguous prefix.
type DecisionVector map[string]int64

// Covers reports whether v holds at least everything other holds, origin by
// origin. An origin absent from v counts as 0.
func (v DecisionVector) Covers(other DecisionVector) bool {
	for origin, seq := range other {
		if v[origin] < seq {
			return false
		}
	}
	return true
}

// Clone returns an independent copy of v.
func (v DecisionVector) Clone() DecisionVector {
	out := make(DecisionVector, len(v))
	for origin, seq := range v {
		out[origin] = seq
	}
	return out
}

// Equal reports whether v and other hold the same sequences, treating an
// absent origin as 0.
func (v DecisionVector) Equal(other DecisionVector) bool {
	return v.Covers(other) && other.Covers(v)
}

var decisionSyncKindPattern = regexp.MustCompile(`^[a-z][a-z_]{0,31}$`)

// ValidateDecisionSyncEntry checks a received entry's shape and size only. It
// never looks inside the payload beyond requiring a JSON object, so entry kinds
// and payload fields from newer machines are stored and relayed unchanged.
func ValidateDecisionSyncEntry(e DecisionEntry) error {
	for _, f := range []struct {
		name, value string
		required    bool
	}{
		{"id", e.ID, true},
		{"origin", e.Origin, true},
		{"call id", e.CallID, true},
		{"kind", string(e.Kind), true},
		{"author kind", string(e.AuthorKind), true},
		{"author surface", string(e.AuthorSurface), false},
		{"author session", string(e.AuthorSession), false},
	} {
		if err := validateDecisionSyncField(f.name, f.value, f.required); err != nil {
			return err
		}
	}
	if !decisionSyncKindPattern.MatchString(string(e.Kind)) {
		return fmt.Errorf("%w: kind %q is not a lowercase identifier", ErrDecisionEntryInvalid, e.Kind)
	}
	if !decisionSyncKindPattern.MatchString(string(e.AuthorKind)) {
		return fmt.Errorf("%w: author kind %q is not a lowercase identifier", ErrDecisionEntryInvalid, e.AuthorKind)
	}
	if e.OriginSeq < 1 {
		return fmt.Errorf("%w: origin sequence %d is below 1", ErrDecisionEntryInvalid, e.OriginSeq)
	}
	if e.Stamp.WallMS <= 0 || e.Stamp.Counter < 0 {
		return fmt.Errorf("%w: stamp %d/%d is out of range", ErrDecisionEntryInvalid, e.Stamp.WallMS, e.Stamp.Counter)
	}
	if e.WrittenAt.IsZero() {
		return fmt.Errorf("%w: written-at is missing", ErrDecisionEntryInvalid)
	}
	if len(e.Payload) > MaxDecisionSyncPayloadBytes {
		return fmt.Errorf("%w: payload is %d bytes, over %d", ErrDecisionEntryInvalid, len(e.Payload), MaxDecisionSyncPayloadBytes)
	}
	// Invalid UTF-8 would not survive a JSON round trip byte-for-byte, so it is
	// refused here rather than drifting on the next relay.
	if !utf8.Valid(e.Payload) || !json.Valid(e.Payload) {
		return fmt.Errorf("%w: payload is not valid JSON", ErrDecisionEntryInvalid)
	}
	if trimmed := bytes.TrimLeft(e.Payload, " \t\r\n"); len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("%w: payload is not a JSON object", ErrDecisionEntryInvalid)
	}
	return nil
}

func validateDecisionSyncField(name, value string, required bool) error {
	if value == "" {
		if required {
			return fmt.Errorf("%w: %s is missing", ErrDecisionEntryInvalid, name)
		}
		return nil
	}
	if len(value) > MaxDecisionSyncIDLength {
		return fmt.Errorf("%w: %s is %d bytes, over %d", ErrDecisionEntryInvalid, name, len(value), MaxDecisionSyncIDLength)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: %s is not valid UTF-8", ErrDecisionEntryInvalid, name)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: %s contains a control character", ErrDecisionEntryInvalid, name)
		}
	}
	return nil
}

// SameDecisionEntry reports whether a and b are the same stored entry: every
// column equal, the payload byte-for-byte, and WrittenAt the same instant.
func SameDecisionEntry(a, b DecisionEntry) bool {
	return a.ID == b.ID &&
		a.Origin == b.Origin &&
		a.OriginSeq == b.OriginSeq &&
		a.Stamp == b.Stamp &&
		a.CallID == b.CallID &&
		a.Kind == b.Kind &&
		bytes.Equal(a.Payload, b.Payload) &&
		a.WrittenAt.Equal(b.WrittenAt) &&
		a.AuthorKind == b.AuthorKind &&
		a.AuthorSurface == b.AuthorSurface &&
		a.AuthorSession == b.AuthorSession
}

// DecisionSyncGap reports a received entry whose sequence did not follow the
// highest held for its origin. Nothing past the gap was stored.
type DecisionSyncGap struct {
	Origin string
	Have   int64
	Got    int64
}

// DecisionSyncConflict records two different entries claiming one identity:
// the same id with different contents, or the same origin and sequence with
// another id. The origin stops syncing past OriginSeq on this machine.
type DecisionSyncConflict struct {
	Origin        string
	OriginSeq     int64
	LocalEntryID  string
	RemoteEntryID string
	PeerHostID    string
	DetectedAt    time.Time
}

// DecisionApplyResult is what applying one batch of received entries did.
type DecisionApplyResult struct {
	Applied    int
	Duplicates int
	Gaps       []DecisionSyncGap
	Conflicts  []DecisionSyncConflict
	// Calls are the recomputed calls, one per affected call id that has a
	// raised or decided entry, in call id order.
	Calls []DecisionCall
}
