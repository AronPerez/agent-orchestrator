package controllers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/caller"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
)

type fakePeerService struct {
	linked   []decisionsync.LinkInput
	unlinked []string
	synced   []string
	linkErr  error
}

func (f *fakePeerService) List(context.Context) ([]decisionsync.PeerStatus, error) {
	return []decisionsync.PeerStatus{{DecisionSyncPeer: domain.DecisionSyncPeer{HostID: "h_mini", Label: "mac-mini", URL: "http://mini:3011", Direction: domain.DecisionSyncOutbound, DeviceID: "dev_secret"}, State: decisionsync.PeerLinkedBehind}}, nil
}

func (f *fakePeerService) Link(_ context.Context, in decisionsync.LinkInput) (decisionsync.PeerStatus, []decisionsync.SecretWarning, error) {
	f.linked = append(f.linked, in)
	if f.linkErr != nil {
		return decisionsync.PeerStatus{}, nil, f.linkErr
	}
	return decisionsync.PeerStatus{DecisionSyncPeer: domain.DecisionSyncPeer{HostID: "h_mini", Label: in.Label, Direction: domain.DecisionSyncOutbound}, State: decisionsync.PeerLinkedBehind}, nil, nil
}

func (f *fakePeerService) Unlink(_ context.Context, hostID string) error {
	f.unlinked = append(f.unlinked, hostID)
	return nil
}

func (f *fakePeerService) SyncNow(_ context.Context, hostID string) ([]decisionsync.SyncResult, error) {
	f.synced = append(f.synced, hostID)
	return []decisionsync.SyncResult{{HostID: "h_mini", Label: "mac-mini", Outcome: decisionsync.Outcome{Pulled: 2, Pushed: 1, InSync: true, PeerLabel: "mac-mini"}}}, nil
}

func peersRouter(svc controllers.DecisionPeerService) http.Handler {
	r := chi.NewRouter()
	r.Route("/api/v1", (&controllers.DecisionPeersController{Svc: svc}).Register)
	return r
}

func peerRequest(h http.Handler, method, path, body string, ctx func(context.Context) context.Context) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if ctx != nil {
		req = req.WithContext(ctx(req.Context()))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestDecisionPeerRoutesArePersonOnly(t *testing.T) {
	svc := &fakePeerService{}
	h := peersRouter(svc)
	inSession := func(ctx context.Context) context.Context { return caller.With(ctx, "w-1") }
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/decision-peers", ""},
		{http.MethodPost, "/api/v1/decision-peers", `{"url":"http://mini:3011","credential":"pw"}`},
		{http.MethodPost, "/api/v1/decision-peers/sync", `{}`},
		{http.MethodDelete, "/api/v1/decision-peers/h_mini", ""},
	} {
		rec := peerRequest(h, tc.method, tc.path, tc.body, inSession)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "DECISION_PERSON_ONLY") {
			t.Errorf("%s %s from a session: %d %s", tc.method, tc.path, rec.Code, rec.Body)
		}
	}
	if len(svc.linked)+len(svc.unlinked)+len(svc.synced) != 0 {
		t.Fatal("a session reached the peer service")
	}

	rec := peerRequest(h, http.MethodPost, "/api/v1/decision-peers", `{"url":"http://mini:3011","label":"mac-mini","credential":"pw-123"}`, nil)
	if rec.Code != http.StatusOK || len(svc.linked) != 1 || svc.linked[0].Credential != "pw-123" || strings.Contains(rec.Body.String(), "pw-123") {
		t.Fatalf("link: %d %s", rec.Code, rec.Body)
	}
	rec = peerRequest(h, http.MethodGet, "/api/v1/decision-peers", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"linked_behind"`) || strings.Contains(rec.Body.String(), "dev_secret") {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	rec = peerRequest(h, http.MethodPost, "/api/v1/decision-peers/sync", `{"hostId":"h_mini"}`, nil)
	if rec.Code != http.StatusOK || len(svc.synced) != 1 || svc.synced[0] != "h_mini" || !strings.Contains(rec.Body.String(), `"pulled":2`) || !strings.Contains(rec.Body.String(), `"label":"mac-mini"`) {
		t.Fatalf("sync: %d %s", rec.Code, rec.Body)
	}
	if rec = peerRequest(h, http.MethodDelete, "/api/v1/decision-peers/h_mini", "", nil); rec.Code != http.StatusNoContent || svc.unlinked[0] != "h_mini" {
		t.Fatalf("unlink: %d %s", rec.Code, rec.Body)
	}
}

func TestDecisionPeerRoutesBlockedOnRemoteListener(t *testing.T) {
	svc := &fakePeerService{}
	h := peersRouter(svc)
	onLAN := func(ctx context.Context) context.Context {
		return requestscope.WithCredential(requestscope.WithLAN(ctx), requestscope.Credential{Kind: requestscope.CredentialPassword})
	}
	if rec := peerRequest(h, http.MethodPost, "/api/v1/decision-peers", `{"url":"http://x","credential":"pw"}`, onLAN); rec.Code != http.StatusNotFound || len(svc.linked) != 0 {
		t.Fatalf("remote link: %d %s", rec.Code, rec.Body)
	}
}

func TestLinkErrorsAreCredentialFree(t *testing.T) {
	svc := &fakePeerService{linkErr: decisionsync.ErrLinkCredentialBad}
	rec := peerRequest(peersRouter(svc), http.MethodPost, "/api/v1/decision-peers", `{"url":"http://mini:3011","credential":"pw-secret-9"}`, nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "DECISION_SYNC_LINK_REFUSED") || strings.Contains(rec.Body.String(), "pw-secret-9") {
		t.Fatalf("refused link: %d %s", rec.Code, rec.Body)
	}
}
