package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
)

// MaxDecisionSyncBodyBytes caps a sync request body. A page holds at most
// 1 MiB of payload; JSON string escaping can grow it, so the cap leaves
// room for that and the envelope while still refusing anything absurd.
const MaxDecisionSyncBodyBytes = 4 << 20

// DecisionSyncService is the decision ledger sync surface the routes call.
type DecisionSyncService interface {
	Exchange(ctx context.Context, peerHostID string, req decisionsync.ExchangeRequest) (decisionsync.ExchangeResponse, error)
	Push(ctx context.Context, peerHostID string, req decisionsync.PushRequest) (decisionsync.PushResponse, error)
	Status(ctx context.Context) (decisionsync.Status, error)
}

// DecisionSyncController serves decision ledger sync to linked peers and its
// freshness to this machine's own clients.
type DecisionSyncController struct {
	Svc DecisionSyncService
	// Devices, when set, is checked again after a request's body is read and
	// before a response with ledger data is written, so a token revoked while
	// a request was in flight (by unlinking, or by the person) neither writes
	// entries nor receives them.
	Devices interface{ Has(deviceID string) bool }
}

// stillValid writes 401 and reports false when the request's sync device was
// revoked after the listener accepted it.
func (c *DecisionSyncController) stillValid(w http.ResponseWriter, r *http.Request) bool {
	if c.Devices == nil || c.Devices.Has(requestscope.CredentialFrom(r.Context()).DeviceID) {
		return true
	}
	envelope.WriteAPIError(w, r, http.StatusUnauthorized, "unauthorized", "DEVICE_REVOKED",
		"This decision sync credential was revoked.", nil)
	return false
}

// Register mounts the sync routes under /api/v1.
func (c *DecisionSyncController) Register(r chi.Router) {
	r.Post("/decision-sync/exchange", c.exchange)
	r.Post("/decision-sync/entries", c.entries)
	r.Get("/decision-sync/status", c.status)
}

// syncPeer returns the peer host id bound to a decision-sync device token, or
// writes 403. Any other credential, and the loopback listener (which carries
// none), is refused: these routes write ledger entries on a peer's behalf.
func syncPeer(w http.ResponseWriter, r *http.Request) (string, bool) {
	credential := requestscope.CredentialFrom(r.Context())
	if credential.Kind != requestscope.CredentialDevice || !credential.HasScope(mobilebridge.DeviceScopeDecisionSync) || credential.ClientID == "" {
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "DECISION_SYNC_CREDENTIAL_REQUIRED",
			"This route needs a decision sync credential issued to a linked machine.", nil)
		return "", false
	}
	return credential.ClientID, true
}

func decodeSyncBody(w http.ResponseWriter, r *http.Request, into any) bool {
	if r.ContentLength > MaxDecisionSyncBodyBytes {
		envelope.WriteAPIError(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "DECISION_SYNC_BODY_TOO_LARGE",
			"The sync request is too large.", nil)
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxDecisionSyncBodyBytes)).Decode(into); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			envelope.WriteAPIError(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "DECISION_SYNC_BODY_TOO_LARGE",
				"The sync request is too large.", nil)
			return false
		}
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return false
	}
	return true
}

func (c *DecisionSyncController) exchange(w http.ResponseWriter, r *http.Request) {
	peer, ok := syncPeer(w, r)
	if !ok {
		return
	}
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/decision-sync/exchange")
		return
	}
	var req DecisionSyncExchangeRequest
	if !decodeSyncBody(w, r, &req) || !c.stillValid(w, r) {
		return
	}
	resp, err := c.Svc.Exchange(r.Context(), peer, decisionsync.ExchangeRequest{
		Protocol: req.Protocol, HostID: req.HostID, MachineLabel: req.MachineLabel,
		Have: req.Have, Heads: headsFromDTO(req.Heads), WaitSeconds: req.WaitSeconds,
	})
	if err != nil {
		writeDecisionSyncError(w, r, err)
		return
	}
	// A waiting exchange may have been revoked while it waited.
	if !c.stillValid(w, r) {
		return
	}
	writeSyncJSON(w, DecisionSyncExchangeResponse{
		Protocol: resp.Protocol, HostID: resp.HostID, MachineLabel: resp.MachineLabel,
		Entries: entriesToDTO(resp.Entries), More: resp.More, Have: vectorToDTO(resp.Have),
		Heads: headsToDTO(resp.Heads), ServerTime: resp.ServerTime.UTC(),
	})
}

func (c *DecisionSyncController) entries(w http.ResponseWriter, r *http.Request) {
	peer, ok := syncPeer(w, r)
	if !ok {
		return
	}
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/decision-sync/entries")
		return
	}
	var req DecisionSyncPushRequest
	if !decodeSyncBody(w, r, &req) {
		return
	}
	push := decisionsync.PushRequest{
		Protocol: req.Protocol, HostID: req.HostID, Entries: EntriesFromDTO(req.Entries), Heads: headsFromDTO(req.Heads),
	}
	ctx := r.Context()
	// Each write of the page runs while the sending device is held live, so
	// revoking it returns only after that write, and a write after that is
	// refused. Waits (the plan gate) happen outside, so revoking never stalls.
	if live, ok := c.Devices.(interface {
		WhileLive(string, func() error) (bool, error)
	}); ok {
		deviceID := requestscope.CredentialFrom(ctx).DeviceID
		ctx = decisionsync.WithApplyGuard(ctx, func(fn func() error) error {
			found, err := live.WhileLive(deviceID, fn)
			if !found {
				return decisionsync.ErrCredentialRevoked
			}
			return err
		})
	} else if !c.stillValid(w, r) {
		return
	}
	resp, err := c.Svc.Push(ctx, peer, push)
	if err != nil {
		writeDecisionSyncError(w, r, err)
		return
	}
	writeSyncJSON(w, DecisionSyncPushResponse{
		Applied: resp.Result.Applied, Duplicates: resp.Result.Duplicates,
		Gaps: gapsToDTO(resp.Result.Gaps), Conflicts: ConflictsToDTO(resp.Result.Conflicts),
		Have: vectorToDTO(resp.Have), Heads: headsToDTO(resp.Heads),
	})
}

// status serves this machine's own clients (loopback) and full remote
// credentials. A scoped token never reaches it: the LAN scope filter admits
// sync tokens only on the two sync routes.
func (c *DecisionSyncController) status(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/decision-sync/status")
		return
	}
	status, err := c.Svc.Status(r.Context())
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "DECISION_SYNC_STATUS",
			"could not read decision sync status", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, StatusToDTO(status))
}

// writeSyncJSON encodes without HTML escaping so payload strings stay as
// compact on the wire as they are stored.
func writeSyncJSON(w http.ResponseWriter, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		http.Error(w, "encode failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

func writeDecisionSyncError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "DECISION_SYNC_INTERRUPTED",
			"The sync request ended before it finished.", nil)
	case errors.Is(err, decisionsync.ErrCredentialRevoked):
		envelope.WriteAPIError(w, r, http.StatusUnauthorized, "unauthorized", "DEVICE_REVOKED",
			"This decision sync credential was revoked.", nil)
	case errors.Is(err, decisionsync.ErrSameIdentity):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "DECISION_SYNC_SAME_IDENTITY",
			"The linked machine has this machine's own identity. One of them is a copy of the other's data directory.", nil)
	case errors.Is(err, decisionsync.ErrPeerIdentityMismatch):
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "DECISION_SYNC_HOST_MISMATCH",
			"hostId does not match the machine this credential was issued to.", nil)
	case errors.Is(err, decisionsync.ErrPageTooLarge):
		envelope.WriteAPIError(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "DECISION_SYNC_PAGE_TOO_LARGE",
			"The page holds more entries or bytes than one sync page may.", nil)
	case errors.Is(err, domain.ErrDecisionEntryInvalid):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "DECISION_SYNC_ENTRY_INVALID",
			"A received entry is malformed; nothing from the page was stored.", nil)
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "DECISION_SYNC",
			"could not complete the decision sync request", nil)
	}
}

// entriesToDTO converts entries to their wire form. The DTO and
// decisionsync.WireEntry share one field set, so the conversion is direct.
func entriesToDTO(entries []domain.DecisionEntry) []DecisionSyncEntry {
	out := make([]DecisionSyncEntry, len(entries))
	for i, e := range entries {
		out[i] = DecisionSyncEntry(decisionsync.ToWire(e))
	}
	return out
}

// EntriesFromDTO converts wire entries back, keeping payload bytes.
func EntriesFromDTO(entries []DecisionSyncEntry) []domain.DecisionEntry {
	out := make([]domain.DecisionEntry, len(entries))
	for i, e := range entries {
		out[i] = decisionsync.FromWire(decisionsync.WireEntry(e))
	}
	return out
}

func headsToDTO(heads domain.DecisionHeads) map[string]DecisionSyncHead {
	out := make(map[string]DecisionSyncHead, len(heads))
	for origin, h := range heads {
		out[origin] = DecisionSyncHead{Seq: h.Seq, ID: h.ID, WrittenAt: h.WrittenAt.UTC(), Stopped: h.Stopped}
	}
	return out
}

func headsFromDTO(heads map[string]DecisionSyncHead) domain.DecisionHeads {
	out := make(domain.DecisionHeads, len(heads))
	for origin, h := range heads {
		out[origin] = domain.DecisionHead{Seq: h.Seq, ID: h.ID, WrittenAt: h.WrittenAt, Stopped: h.Stopped}
	}
	return out
}

func vectorToDTO(v domain.DecisionVector) map[string]int64 {
	out := make(map[string]int64, len(v))
	for origin, seq := range v {
		out[origin] = seq
	}
	return out
}

func gapsToDTO(gaps []domain.DecisionSyncGap) []DecisionSyncGap {
	out := make([]DecisionSyncGap, len(gaps))
	for i, g := range gaps {
		out[i] = DecisionSyncGap{Origin: g.Origin, Have: g.Have, Got: g.Got}
	}
	return out
}

// ConflictsToDTO converts recorded conflicts to their wire form.
func ConflictsToDTO(conflicts []domain.DecisionSyncConflict) []DecisionSyncConflict {
	out := make([]DecisionSyncConflict, len(conflicts))
	for i, c := range conflicts {
		out[i] = DecisionSyncConflict{
			Origin: c.Origin, OriginSeq: c.OriginSeq, LocalEntryID: c.LocalEntryID,
			RemoteEntryID: c.RemoteEntryID, PeerHostID: c.PeerHostID, DetectedAt: c.DetectedAt.UTC(),
		}
	}
	return out
}

// StatusToDTO converts sync freshness to its response form. It copies only
// named, secret-free fields; peer URLs and device ids stay out.
func StatusToDTO(status decisionsync.Status) DecisionSyncStatusResponse {
	out := DecisionSyncStatusResponse{
		HostID: status.HostID, MachineLabel: status.MachineLabel,
		Peers:     make([]DecisionSyncPeerStatus, 0, len(status.Peers)),
		Origins:   make([]DecisionSyncOriginStatus, 0, len(status.Origins)),
		Conflicts: ConflictsToDTO(status.Conflicts),
	}
	for _, p := range status.Peers {
		out.Peers = append(out.Peers, DecisionSyncPeerStatus{
			HostID: p.HostID, Label: p.Label, Direction: string(p.Direction),
			LastSuccessAt: optionalTime(p.LastSuccessAt.UTC()), LastAttemptAt: optionalTime(p.LastAttemptAt.UTC()),
			LastErrorCode: p.LastErrorCode, InSync: p.InSync, ClockAheadMs: p.ClockAheadMS, State: string(p.State),
		})
	}
	for _, o := range status.Origins {
		out.Origins = append(out.Origins, DecisionSyncOriginStatus{HostID: o.HostID, MachineLabel: o.MachineLabel, LastHeardAt: o.LastHeardAt.UTC()})
	}
	return out
}
