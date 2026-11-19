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

// The wire messages below mirror the controller DTOs field for field (a
// parity test in controllers checks the JSON names). The HTTP client uses
// them because this package cannot import the controllers.

// WireHead is DecisionSyncHead on the wire.
type WireHead struct {
	Seq       int64     `json:"seq"`
	ID        string    `json:"id"`
	WrittenAt time.Time `json:"writtenAt"`
	Stopped   bool      `json:"stopped,omitempty"`
}

// WireExchangeRequest is DecisionSyncExchangeRequest on the wire.
type WireExchangeRequest struct {
	Protocol     int                 `json:"protocol"`
	HostID       string              `json:"hostId"`
	MachineLabel string              `json:"machineLabel,omitempty"`
	Have         map[string]int64    `json:"have"`
	Heads        map[string]WireHead `json:"heads,omitempty"`
	WaitSeconds  int                 `json:"waitSeconds,omitempty"`
}

// WireExchangeResponse is DecisionSyncExchangeResponse on the wire.
type WireExchangeResponse struct {
	Protocol     int                 `json:"protocol"`
	HostID       string              `json:"hostId"`
	MachineLabel string              `json:"machineLabel"`
	Entries      []WireEntry         `json:"entries"`
	More         bool                `json:"more"`
	Have         map[string]int64    `json:"have"`
	Heads        map[string]WireHead `json:"heads"`
	ServerTime   time.Time           `json:"serverTime"`
}

// WirePushRequest is DecisionSyncPushRequest on the wire.
type WirePushRequest struct {
	Protocol int                 `json:"protocol"`
	HostID   string              `json:"hostId"`
	Entries  []WireEntry         `json:"entries"`
	Heads    map[string]WireHead `json:"heads,omitempty"`
}

// WireGap is DecisionSyncGap on the wire.
type WireGap struct {
	Origin string `json:"origin"`
	Have   int64  `json:"have"`
	Got    int64  `json:"got"`
}

// WireConflict is DecisionSyncConflict on the wire.
type WireConflict struct {
	Origin        string    `json:"origin"`
	OriginSeq     int64     `json:"originSeq"`
	LocalEntryID  string    `json:"localEntryId"`
	RemoteEntryID string    `json:"remoteEntryId"`
	PeerHostID    string    `json:"peerHostId"`
	DetectedAt    time.Time `json:"detectedAt"`
}

// WirePushResponse is DecisionSyncPushResponse on the wire.
type WirePushResponse struct {
	Applied    int                 `json:"applied"`
	Duplicates int                 `json:"duplicates"`
	Gaps       []WireGap           `json:"gaps"`
	Conflicts  []WireConflict      `json:"conflicts"`
	Have       map[string]int64    `json:"have"`
	Heads      map[string]WireHead `json:"heads"`
}

func headsToWire(heads domain.DecisionHeads) map[string]WireHead {
	out := make(map[string]WireHead, len(heads))
	for origin, h := range heads {
		out[origin] = WireHead{Seq: h.Seq, ID: h.ID, WrittenAt: h.WrittenAt.UTC(), Stopped: h.Stopped}
	}
	return out
}

func headsFromWire(heads map[string]WireHead) domain.DecisionHeads {
	out := make(domain.DecisionHeads, len(heads))
	for origin, h := range heads {
		out[origin] = domain.DecisionHead{Seq: h.Seq, ID: h.ID, WrittenAt: h.WrittenAt, Stopped: h.Stopped}
	}
	return out
}
