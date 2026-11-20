package controllers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/caller"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
)

// DecisionIdentityService repairs a decision identity shared with a copied
// data directory.
type DecisionIdentityService interface {
	Identity() (hostID, machineLabel string)
	Status() (decisionsync.RepairRecord, bool, error)
	Propose(ctx context.Context) (decisionsync.RepairProposal, error)
	Stage(ctx context.Context, fromSeq int64, forkIDs []string) (decisionsync.RepairRecord, error)
}

// DecisionIdentityController serves identity repair. Like the peer routes it
// is loopback-only (the LAN listener blocks /api/v1/decision-identity) and
// person-only.
type DecisionIdentityController struct {
	Svc DecisionIdentityService
}

// Register mounts the identity routes under /api/v1.
func (c *DecisionIdentityController) Register(r chi.Router) {
	r.Get("/decision-identity", c.status)
	r.Post("/decision-identity/repair", c.repair)
}

// DecisionIdentityResponse is this machine's decision identity and its repair.
type DecisionIdentityResponse struct {
	HostID       string                `json:"hostId"`
	MachineLabel string                `json:"machineLabel"`
	Repair       *DecisionRepairStatus `json:"repair,omitempty"`
}

// DecisionRepairStatus is a staged or finished identity repair.
type DecisionRepairStatus struct {
	State     string    `json:"state" description:"staged, identity_switched, reissued, archived or done. Every state but done finishes when Orchestrator restarts."`
	Origin    string    `json:"origin" description:"The identity given up."`
	FromSeq   int64     `json:"fromSeq"`
	NewHostID string    `json:"newHostId"`
	Entries   int       `json:"entries" description:"How many of this machine's entries are re-issued under the new identity."`
	StagedAt  time.Time `json:"stagedAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// DecisionRepairRequest asks for a repair proposal, or stages the proposal
// the person confirmed.
type DecisionRepairRequest struct {
	Stage   bool     `json:"stage,omitempty" description:"Stage the repair. fromSeq and forkIds must match a fresh proposal."`
	FromSeq int64    `json:"fromSeq,omitempty"`
	ForkIDs []string `json:"forkIds,omitempty"`
}

// DecisionRepairLink is an outbound link the repair removes.
type DecisionRepairLink struct {
	HostID string `json:"hostId"`
	Label  string `json:"label"`
}

// DecisionRepairResponse is what a repair would do, and the staged repair
// once confirmed.
type DecisionRepairResponse struct {
	Origin      string                `json:"origin"`
	FromSeq     int64                 `json:"fromSeq"`
	ForkIDs     []string              `json:"forkIds"`
	CallIDs     []string              `json:"callIds"`
	Links       []DecisionRepairLink  `json:"links" description:"Outbound links the repair removes; add them again afterwards."`
	PeersAsked  int                   `json:"peersAsked"`
	PeersFailed int                   `json:"peersFailed"`
	Staged      *DecisionRepairStatus `json:"staged,omitempty"`
}

func repairStatusToDTO(rec decisionsync.RepairRecord) *DecisionRepairStatus {
	return &DecisionRepairStatus{
		State: string(rec.State), Origin: rec.Plan.Origin, FromSeq: rec.Plan.FromSeq, NewHostID: rec.Plan.NewOrigin,
		Entries: len(rec.Plan.ForkIDs), StagedAt: rec.StagedAt.UTC(), UpdatedAt: rec.UpdatedAt.UTC(),
	}
}

// decisionPersonOnLoopback refuses remote requests (as if unmounted) and
// requests from an agent session.
func decisionPersonOnLoopback(w http.ResponseWriter, r *http.Request, available bool, method, path, refusal string) bool {
	if requestscope.IsLAN(r.Context()) {
		http.NotFound(w, r)
		return false
	}
	if caller.From(r.Context()) != "" {
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "DECISION_PERSON_ONLY", refusal, nil)
		return false
	}
	if !available {
		apispec.NotImplemented(w, r, method, path)
		return false
	}
	return true
}

const identityRefusal = "Only the person can repair this machine's decision identity. A session cannot."

func (c *DecisionIdentityController) status(w http.ResponseWriter, r *http.Request) {
	if !decisionPersonOnLoopback(w, r, c.Svc != nil, http.MethodGet, "/api/v1/decision-identity", identityRefusal) {
		return
	}
	rec, found, err := c.Svc.Status()
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "DECISION_REPAIR", "could not read the identity repair", nil)
		return
	}
	hostID, label := c.Svc.Identity()
	out := DecisionIdentityResponse{HostID: hostID, MachineLabel: label}
	if found {
		out.Repair = repairStatusToDTO(rec)
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *DecisionIdentityController) repair(w http.ResponseWriter, r *http.Request) {
	if !decisionPersonOnLoopback(w, r, c.Svc != nil, http.MethodPost, "/api/v1/decision-identity/repair", identityRefusal) {
		return
	}
	var req DecisionRepairRequest
	if !decodeSyncBody(w, r, &req) {
		return
	}
	proposal, err := c.Svc.Propose(r.Context())
	if err != nil {
		writeDecisionRepairError(w, r, err)
		return
	}
	out := DecisionRepairResponse{
		Origin: proposal.Origin, FromSeq: proposal.FromSeq, ForkIDs: proposal.ForkIDs, CallIDs: proposal.CallIDs,
		Links: make([]DecisionRepairLink, len(proposal.OutboundLinks)), PeersAsked: proposal.PeersAsked, PeersFailed: proposal.PeersFailed,
	}
	for i, link := range proposal.OutboundLinks {
		out.Links[i] = DecisionRepairLink{HostID: link.HostID, Label: link.Label}
	}
	if req.Stage {
		rec, err := c.Svc.Stage(r.Context(), req.FromSeq, req.ForkIDs)
		if err != nil {
			writeDecisionRepairError(w, r, err)
			return
		}
		out.Staged = repairStatusToDTO(rec)
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func writeDecisionRepairError(w http.ResponseWriter, r *http.Request, err error) {
	var code string
	switch {
	case errors.Is(err, decisionsync.ErrRepairNothingToDo):
		code = "DECISION_REPAIR_NOTHING_TO_DO"
	case errors.Is(err, decisionsync.ErrRepairNeedsPeer):
		code = "DECISION_REPAIR_NEEDS_PEER"
	case errors.Is(err, decisionsync.ErrRepairPending):
		code = "DECISION_REPAIR_PENDING"
	case errors.Is(err, decisionsync.ErrRepairChanged):
		code = "DECISION_REPAIR_CHANGED"
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "DECISION_REPAIR", "could not plan the identity repair", nil)
		return
	}
	envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", code, err.Error(), nil)
}
