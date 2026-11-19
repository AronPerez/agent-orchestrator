package decisionsync

import (
	"encoding/json"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// WireEntry is a decision entry as it crosses the network: the twelve stored
// columns, with the payload as a JSON string holding the stored text. Go's
// encoder compacts and HTML-escapes embedded raw JSON, so carrying the payload
// as an object would change its bytes on every hop; as a string it survives
// byte-for-byte on every machine and version.
type WireEntry struct {
	ID            string    `json:"id"`
	Origin        string    `json:"origin"`
	OriginSeq     int64     `json:"originSeq"`
	StampWall     int64     `json:"stampWall"`
	StampCounter  int64     `json:"stampCounter"`
	CallID        string    `json:"callId"`
	Kind          string    `json:"kind"`
	Payload       string    `json:"payload"`
	WrittenAt     time.Time `json:"writtenAt"`
	AuthorKind    string    `json:"authorKind"`
	AuthorSurface string    `json:"authorSurface"`
	AuthorSession string    `json:"authorSession"`
}

// ToWire converts an entry to its wire form.
func ToWire(e domain.DecisionEntry) WireEntry {
	return WireEntry{
		ID: e.ID, Origin: e.Origin, OriginSeq: e.OriginSeq,
		StampWall: e.Stamp.WallMS, StampCounter: e.Stamp.Counter,
		CallID: e.CallID, Kind: string(e.Kind), Payload: string(e.Payload),
		WrittenAt: e.WrittenAt, AuthorKind: string(e.AuthorKind),
		AuthorSurface: string(e.AuthorSurface), AuthorSession: string(e.AuthorSession),
	}
}

// FromWire converts a wire entry back to an entry, keeping the payload bytes.
func FromWire(w WireEntry) domain.DecisionEntry {
	return domain.DecisionEntry{
		ID: w.ID, Origin: w.Origin, OriginSeq: w.OriginSeq,
		Stamp:  domain.DecisionStamp{WallMS: w.StampWall, Counter: w.StampCounter},
		CallID: w.CallID, Kind: domain.DecisionEntryKind(w.Kind), Payload: json.RawMessage(w.Payload),
		WrittenAt: w.WrittenAt, AuthorKind: domain.DecisionAuthorKind(w.AuthorKind),
		AuthorSurface: domain.RequestSurface(w.AuthorSurface), AuthorSession: domain.SessionID(w.AuthorSession),
	}
}

// ToWireEntries converts entries to their wire form.
func ToWireEntries(entries []domain.DecisionEntry) []WireEntry {
	out := make([]WireEntry, len(entries))
	for i, e := range entries {
		out[i] = ToWire(e)
	}
	return out
}

// FromWireEntries converts wire entries back to entries.
func FromWireEntries(entries []WireEntry) []domain.DecisionEntry {
	out := make([]domain.DecisionEntry, len(entries))
	for i, e := range entries {
		out[i] = FromWire(e)
	}
	return out
}
