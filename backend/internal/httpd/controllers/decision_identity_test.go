package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/caller"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
)

type fakeIdentityService struct {
	proposal   decisionsync.RepairProposal
	proposeErr error
	staged     []int64
	record     *decisionsync.RepairRecord
}

func (f *fakeIdentityService) Identity() (string, string) { return "h_x", "mac-mini" }

func (f *fakeIdentityService) Status() (decisionsync.RepairRecord, bool, error) {
	if f.record == nil {
		return decisionsync.RepairRecord{}, false, nil
	}
	return *f.record, true, nil
}

func (f *fakeIdentityService) Propose(context.Context) (decisionsync.RepairProposal, error) {
	return f.proposal, f.proposeErr
}

func (f *fakeIdentityService) Stage(_ context.Context, fromSeq int64, forkIDs []string) (decisionsync.RepairRecord, error) {
	if fromSeq != f.proposal.FromSeq || !slices.Equal(forkIDs, f.proposal.ForkIDs) {
		return decisionsync.RepairRecord{}, decisionsync.ErrRepairChanged
	}
	f.staged = append(f.staged, fromSeq)
	rec := decisionsync.RepairRecord{
		State: decisionsync.RepairStaged, StagedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0),
		Plan: domain.DecisionRepairPlan{RepairID: "rep_1", Origin: "h_x", FromSeq: fromSeq, NewOrigin: "h_y", ForkIDs: forkIDs},
	}
	f.record = &rec
	return rec, nil
}

func identityRouter(svc controllers.DecisionIdentityService) http.Handler {
	r := chi.NewRouter()
	r.Route("/api/v1", (&controllers.DecisionIdentityController{Svc: svc}).Register)
	return r
}

func TestDecisionIdentityRoutesArePersonOnly(t *testing.T) {
	svc := &fakeIdentityService{proposal: decisionsync.RepairProposal{FromSeq: 2, ForkIDs: []string{"a"}}}
	h := identityRouter(svc)
	inSession := func(ctx context.Context) context.Context { return caller.With(ctx, "w-1") }
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/decision-identity", ""},
		{http.MethodPost, "/api/v1/decision-identity/repair", `{"stage":true,"fromSeq":2,"forkIds":["a"]}`},
	} {
		rec := peerRequest(h, tc.method, tc.path, tc.body, inSession)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "DECISION_PERSON_ONLY") {
			t.Errorf("%s %s from a session: %d %s", tc.method, tc.path, rec.Code, rec.Body)
		}
	}
	if len(svc.staged) != 0 {
		t.Fatal("a session staged a repair")
	}
}

func TestDecisionIdentityRepairProposesThenStages(t *testing.T) {
	svc := &fakeIdentityService{proposal: decisionsync.RepairProposal{
		Origin: "h_x", FromSeq: 2, ForkIDs: []string{"dent_2", "dent_3"}, CallIDs: []string{"dec_c"},
		OutboundLinks: []decisionsync.StoredPeer{{HostID: "h_b", Label: "hub", Token: "aoc_secret"}}, PeersAsked: 1,
	}}
	h := identityRouter(svc)

	rec := peerRequest(h, http.MethodPost, "/api/v1/decision-identity/repair", `{}`, nil)
	var got controllers.DecisionRepairResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("propose: %d %s", rec.Code, rec.Body)
	}
	if got.FromSeq != 2 || len(got.ForkIDs) != 2 || got.Staged != nil || len(got.Links) != 1 || got.Links[0].Label != "hub" || len(svc.staged) != 0 {
		t.Fatalf("propose = %+v", got)
	}
	if strings.Contains(rec.Body.String(), "aoc_secret") {
		t.Fatal("a link token reached the response")
	}

	rec = peerRequest(h, http.MethodPost, "/api/v1/decision-identity/repair", `{"stage":true,"fromSeq":2,"forkIds":["dent_2"]}`, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "DECISION_REPAIR_CHANGED") {
		t.Fatalf("stale stage: %d %s", rec.Code, rec.Body)
	}
	rec = peerRequest(h, http.MethodPost, "/api/v1/decision-identity/repair", `{"stage":true,"fromSeq":2,"forkIds":["dent_2","dent_3"]}`, nil)
	got = controllers.DecisionRepairResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK || got.Staged == nil || got.Staged.State != "staged" || got.Staged.NewHostID != "h_y" {
		t.Fatalf("stage: %d %s", rec.Code, rec.Body)
	}

	rec = peerRequest(h, http.MethodGet, "/api/v1/decision-identity", "", nil)
	var status controllers.DecisionIdentityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil || status.HostID != "h_x" || status.MachineLabel != "mac-mini" || status.Repair == nil || status.Repair.Entries != 2 {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}
}

func TestDecisionIdentityRepairRefusalCodes(t *testing.T) {
	for err, code := range map[error]string{
		decisionsync.ErrRepairNothingToDo: "DECISION_REPAIR_NOTHING_TO_DO",
		decisionsync.ErrRepairNeedsPeer:   "DECISION_REPAIR_NEEDS_PEER",
		decisionsync.ErrRepairPending:     "DECISION_REPAIR_PENDING",
	} {
		rec := peerRequest(identityRouter(&fakeIdentityService{proposeErr: err}), http.MethodPost, "/api/v1/decision-identity/repair", `{}`, nil)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), code) {
			t.Errorf("%v: %d %s", err, rec.Code, rec.Body)
		}
	}
}
