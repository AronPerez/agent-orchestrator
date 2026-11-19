package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/caller"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
)

// DecisionPeerService links machines for decision sync and syncs them on request.
type DecisionPeerService interface {
	List(ctx context.Context) ([]decisionsync.PeerStatus, error)
	Link(ctx context.Context, in decisionsync.LinkInput) (decisionsync.PeerStatus, []decisionsync.SecretWarning, error)
	Unlink(ctx context.Context, hostID string) error
	SyncNow(ctx context.Context, hostID string) ([]decisionsync.SyncResult, error)
}

// DecisionPeersController manages this machine's decision sync links. Its
// routes are loopback-only (the LAN listener blocks /api/v1/decision-peers)
// and person-only: a request from inside an agent session is refused.
type DecisionPeersController struct {
	Svc DecisionPeerService
}

// Register mounts the peer routes under /api/v1.
func (c *DecisionPeersController) Register(r chi.Router) {
	r.Get("/decision-peers", c.list)
	r.Post("/decision-peers", c.link)
	r.Post("/decision-peers/sync", c.sync)
	r.Delete("/decision-peers/{hostId}", c.unlink)
}

// personOnLoopback refuses remote requests (as if unmounted) and requests from
// an agent session.
func (c *DecisionPeersController) personOnLoopback(w http.ResponseWriter, r *http.Request, method, path string) bool {
	if requestscope.IsLAN(r.Context()) {
		http.NotFound(w, r)
		return false
	}
	if caller.From(r.Context()) != "" {
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "DECISION_PERSON_ONLY",
			"Only the person can link machines or sync decisions. A session cannot.", nil)
		return false
	}
	if c.Svc == nil {
		apispec.NotImplemented(w, r, method, path)
		return false
	}
	return true
}

func (c *DecisionPeersController) list(w http.ResponseWriter, r *http.Request) {
	if !c.personOnLoopback(w, r, http.MethodGet, "/api/v1/decision-peers") {
		return
	}
	peers, err := c.Svc.List(r.Context())
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "DECISION_PEERS", "could not list linked machines", nil)
		return
	}
	out := ListDecisionPeersResponse{Peers: make([]DecisionPeer, 0, len(peers))}
	for _, p := range peers {
		out.Peers = append(out.Peers, decisionPeerDTO(p))
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *DecisionPeersController) link(w http.ResponseWriter, r *http.Request) {
	if !c.personOnLoopback(w, r, http.MethodPost, "/api/v1/decision-peers") {
		return
	}
	var req LinkDecisionPeerRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if req.URL == "" || req.Credential == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "DECISION_SYNC_LINK_INVALID",
			"url and credential are required.", nil)
		return
	}
	peer, warnings, err := c.Svc.Link(r.Context(), decisionsync.LinkInput{
		URL: req.URL, ExpectedHostID: req.ExpectedHostID, Label: req.Label, Credential: req.Credential,
	})
	if err != nil {
		writeLinkError(w, r, err)
		return
	}
	out := LinkDecisionPeerResponse{Peer: decisionPeerDTO(peer), Warnings: make([]DecisionSecretWarning, 0, len(warnings))}
	for _, warning := range warnings {
		out.Warnings = append(out.Warnings, DecisionSecretWarning{CallID: warning.CallID, Field: warning.Field})
	}
	w.Header().Set("Cache-Control", "no-store")
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *DecisionPeersController) unlink(w http.ResponseWriter, r *http.Request) {
	if !c.personOnLoopback(w, r, http.MethodDelete, "/api/v1/decision-peers/{hostId}") {
		return
	}
	switch err := c.Svc.Unlink(r.Context(), chi.URLParam(r, "hostId")); {
	case errors.Is(err, decisionsync.ErrPeerNotLinked):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "DECISION_PEER_NOT_FOUND", err.Error(), nil)
	case err != nil:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "DECISION_PEERS", "could not unlink the machine", nil)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (c *DecisionPeersController) sync(w http.ResponseWriter, r *http.Request) {
	if !c.personOnLoopback(w, r, http.MethodPost, "/api/v1/decision-peers/sync") {
		return
	}
	var req SyncDecisionPeersRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
			return
		}
	}
	results, err := c.Svc.SyncNow(r.Context(), req.HostID)
	switch {
	case errors.Is(err, decisionsync.ErrPeerNotLinked):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "DECISION_PEER_NOT_FOUND", err.Error(), nil)
		return
	case err != nil:
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "DECISION_SYNC_INTERRUPTED",
			"The sync ended before it finished.", nil)
		return
	}
	out := SyncDecisionPeersResponse{Results: make([]DecisionPeerSyncResult, 0, len(results))}
	for _, res := range results {
		out.Results = append(out.Results, DecisionPeerSyncResult{
			HostID: res.HostID, Label: res.Label, Pulled: res.Outcome.Pulled, Pushed: res.Outcome.Pushed,
			InSync: res.Outcome.InSync, ErrorCode: res.ErrorCode,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func writeLinkError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := http.StatusBadRequest, "DECISION_SYNC_LINK_INVALID"
	switch {
	case errors.Is(err, decisionsync.ErrLinkSelf):
		status, code = http.StatusConflict, "DECISION_SYNC_SAME_IDENTITY"
	case errors.Is(err, decisionsync.ErrLinkHostChanged):
		status, code = http.StatusConflict, "DECISION_SYNC_HOST_CHANGED"
	case errors.Is(err, decisionsync.ErrLinkCredentialBad):
		status, code = http.StatusBadRequest, "DECISION_SYNC_LINK_REFUSED"
	case errors.Is(err, decisionsync.ErrLinkUnreachable):
		status, code = http.StatusBadGateway, "DECISION_SYNC_PEER_UNREACHABLE"
	case errors.Is(err, decisionsync.ErrLinkIncompatible):
		status, code = http.StatusBadGateway, "DECISION_SYNC_PEER_INCOMPATIBLE"
	case errors.Is(err, decisionsync.ErrLinkFailed):
		status, code = http.StatusInternalServerError, "DECISION_SYNC_LINK_FAILED"
	}
	// Every link error's text is fixed and credential-free (decisionsync).
	envelope.WriteAPIError(w, r, status, "link_failed", code, err.Error(), nil)
}

func decisionPeerDTO(p decisionsync.PeerStatus) DecisionPeer {
	return DecisionPeer{
		HostID: p.HostID, Label: p.Label, URL: p.URL, Direction: string(p.Direction), State: string(p.State),
		LastSuccessAt: optionalTime(p.LastSuccessAt.UTC()), LastAttemptAt: optionalTime(p.LastAttemptAt.UTC()),
		LastErrorCode: p.LastErrorCode, InSync: p.InSync, ClockAheadMs: p.ClockAheadMS,
	}
}
