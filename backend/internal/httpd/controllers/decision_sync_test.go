package controllers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync/ledgertest"
)

var syncAt = time.UnixMilli(1_700_000_000_000).UTC()

func syncCredential(clientID string) requestscope.Credential {
	return requestscope.Credential{
		Kind: requestscope.CredentialDevice, DeviceID: "dev_sync", ClientID: clientID,
		Scopes: []string{mobilebridge.DeviceScopeDecisionSync},
	}
}

type syncFixture struct {
	router http.Handler
	ledger *ledgertest.MemLedger
}

func newSyncFixture(t *testing.T, status decisionsync.StatusSource) *syncFixture {
	t.Helper()
	ledger := ledgertest.NewMemLedger("h_server")
	server := decisionsync.NewServer(decisionsync.ServerDeps{
		Ledger: ledger, HostID: "h_server", MachineLabel: func() string { return "mac-mini" },
		Now: func() time.Time { return syncAt }, StatusSource: status,
	})
	r := chi.NewRouter()
	r.Route("/api/v1", (&controllers.DecisionSyncController{Svc: server}).Register)
	return &syncFixture{router: r, ledger: ledger}
}

func (f *syncFixture) do(t *testing.T, method, path string, credential *requestscope.Credential, body []byte) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if credential != nil {
		req = req.WithContext(requestscope.WithCredential(req.Context(), *credential))
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec, decoded
}

func mustJSONBody(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDecisionSyncRoutesRefuseLoopbackAndFullCredentials(t *testing.T) {
	f := newSyncFixture(t, nil)
	body := mustJSONBody(t, controllers.DecisionSyncExchangeRequest{Protocol: 1, HostID: "h_peer"})
	coordination := requestscope.Credential{Kind: requestscope.CredentialDevice, DeviceID: "dev_c", ClientID: "h_peer", Scopes: []string{mobilebridge.DeviceScopeAgentCoordination}}
	noClient := syncCredential("")
	for name, credential := range map[string]*requestscope.Credential{
		"loopback":            nil,
		"password":            {Kind: requestscope.CredentialPassword},
		"account":             {Kind: requestscope.CredentialAccount},
		"full device":         {Kind: requestscope.CredentialDevice, DeviceID: "dev_full"},
		"coordination scoped": &coordination,
		"sync without peer":   &noClient,
	} {
		for _, path := range []string{"/api/v1/decision-sync/exchange", "/api/v1/decision-sync/entries"} {
			rec, decoded := f.do(t, http.MethodPost, path, credential, body)
			if rec.Code != http.StatusForbidden || decoded["code"] != "DECISION_SYNC_CREDENTIAL_REQUIRED" {
				t.Errorf("%s %s: status=%d body=%s", name, path, rec.Code, rec.Body)
			}
		}
	}
	ok := syncCredential("h_peer")
	if rec, _ := f.do(t, http.MethodPost, "/api/v1/decision-sync/exchange", &ok, body); rec.Code != http.StatusOK {
		t.Fatalf("sync credential: status=%d body=%s", rec.Code, rec.Body)
	}
}

func TestDecisionSyncRefusesHostIDMismatch(t *testing.T) {
	f := newSyncFixture(t, nil)
	credential := syncCredential("h_peer")
	body := mustJSONBody(t, controllers.DecisionSyncExchangeRequest{Protocol: 1, HostID: "h_someone_else"})
	rec, decoded := f.do(t, http.MethodPost, "/api/v1/decision-sync/exchange", &credential, body)
	if rec.Code != http.StatusForbidden || decoded["code"] != "DECISION_SYNC_HOST_MISMATCH" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	push := mustJSONBody(t, controllers.DecisionSyncPushRequest{Protocol: 1, HostID: "h_someone_else"})
	rec, decoded = f.do(t, http.MethodPost, "/api/v1/decision-sync/entries", &credential, push)
	if rec.Code != http.StatusForbidden || decoded["code"] != "DECISION_SYNC_HOST_MISMATCH" {
		t.Fatalf("push: status=%d body=%s", rec.Code, rec.Body)
	}
}

func TestDecisionSyncRefusesSameIdentity(t *testing.T) {
	f := newSyncFixture(t, nil)
	credential := syncCredential("h_server")
	body := mustJSONBody(t, controllers.DecisionSyncExchangeRequest{Protocol: 1, HostID: "h_server"})
	rec, decoded := f.do(t, http.MethodPost, "/api/v1/decision-sync/exchange", &credential, body)
	if rec.Code != http.StatusConflict || decoded["code"] != "DECISION_SYNC_SAME_IDENTITY" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
}

func TestDecisionSyncPayloadRoundTripsAsString(t *testing.T) {
	payload := `{"z":1,  "note":"<script>&amp; \"quoted\" é \u00e9","unknown":{"deep":[1,2.50]}}`
	entry := domain.DecisionEntry{
		ID: "dent_1", Origin: "h_peer", OriginSeq: 1, Stamp: domain.DecisionStamp{WallMS: syncAt.UnixMilli()},
		CallID: "dec_1", Kind: "future_kind", Payload: json.RawMessage(payload),
		WrittenAt: syncAt, AuthorKind: domain.DecisionAuthorPerson, AuthorSurface: "phone",
	}
	f := newSyncFixture(t, nil)
	credential := syncCredential("h_peer")
	body := mustJSONBody(t, controllers.DecisionSyncPushRequest{
		Protocol: 1, HostID: "h_peer", Entries: []controllers.DecisionSyncEntry{controllers.DecisionSyncEntry(decisionsync.ToWire(entry))},
	})
	rec, decoded := f.do(t, http.MethodPost, "/api/v1/decision-sync/entries", &credential, body)
	if rec.Code != http.StatusOK || decoded["applied"] != float64(1) {
		t.Fatalf("push: status=%d body=%s", rec.Code, rec.Body)
	}
	stored := f.ledger.Entries()
	if len(stored) != 1 || string(stored[0].Payload) != payload {
		t.Fatalf("stored payload = %q, want %q", stored[0].Payload, payload)
	}
	// And back out through an exchange, decoded as a client would.
	rec, _ = f.do(t, http.MethodPost, "/api/v1/decision-sync/exchange", &credential, mustJSONBody(t, controllers.DecisionSyncExchangeRequest{Protocol: 1, HostID: "h_peer"}))
	var resp controllers.DecisionSyncExchangeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	back := controllers.EntriesFromDTO(resp.Entries)
	if len(back) != 1 || string(back[0].Payload) != payload || !domain.SameDecisionEntry(back[0], entry) {
		t.Fatalf("exchanged entry = %+v", back)
	}
	if strings.Contains(rec.Body.String(), `\u003c`) {
		t.Fatalf("response HTML-escaped the payload string: %s", rec.Body)
	}
}

type secretStatus struct{}

func (secretStatus) ListDecisionSyncPeers(context.Context) ([]domain.DecisionSyncPeer, error) {
	return []domain.DecisionSyncPeer{{
		HostID: "h_peer", Label: "laptop", URL: "http://user:password@10.0.0.2:3011/?token=aoc_leak",
		Direction: domain.DecisionSyncOutbound, DeviceID: "dev_secret", LastSuccessAt: syncAt, InSync: true,
	}}, nil
}

func (secretStatus) DecisionOriginsLastHeard(context.Context) (map[string]time.Time, error) {
	return map[string]time.Time{"h_peer": syncAt, "h_far": syncAt.Add(-time.Hour)}, nil
}

func (secretStatus) DecisionOriginLabels(context.Context) (map[string]string, error) {
	return map[string]string{"h_far": "vm-2"}, nil
}

func (secretStatus) ListDecisionSyncConflicts(context.Context) ([]domain.DecisionSyncConflict, error) {
	return nil, nil
}

func TestDecisionSyncStatusHasNoSecrets(t *testing.T) {
	f := newSyncFixture(t, secretStatus{})
	rec, _ := f.do(t, http.MethodGet, "/api/v1/decision-sync/status", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	text := rec.Body.String()
	for _, secret := range []string{"token", "password", "aoc_", "aod_", "dev_secret", "10.0.0.2"} {
		if strings.Contains(text, secret) {
			t.Fatalf("status response contains %q: %s", secret, text)
		}
	}
	var resp controllers.DecisionSyncStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.HostID != "h_server" || resp.MachineLabel != "mac-mini" || len(resp.Peers) != 1 || resp.Peers[0].State != "linked_in_sync" || len(resp.Origins) != 2 {
		t.Fatalf("status = %+v", resp)
	}
	if resp.Origins[0].HostID != "h_far" || resp.Origins[0].MachineLabel != "vm-2" || resp.Origins[1].MachineLabel != "laptop" {
		t.Fatalf("origins = %+v", resp.Origins)
	}
}

func TestDecisionSyncRejectsOversizedBody(t *testing.T) {
	f := newSyncFixture(t, nil)
	credential := syncCredential("h_peer")
	big := bytes.Repeat([]byte(" "), controllers.MaxDecisionSyncBodyBytes+1)
	rec, decoded := f.do(t, http.MethodPost, "/api/v1/decision-sync/entries", &credential, big)
	if rec.Code != http.StatusRequestEntityTooLarge || decoded["code"] != "DECISION_SYNC_BODY_TOO_LARGE" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if len(f.ledger.Entries()) != 0 {
		t.Fatal("oversized body stored entries")
	}
	invalid := mustJSONBody(t, controllers.DecisionSyncPushRequest{Protocol: 1, HostID: "h_peer", Entries: []controllers.DecisionSyncEntry{{ID: "x"}}})
	rec, decoded = f.do(t, http.MethodPost, "/api/v1/decision-sync/entries", &credential, invalid)
	if rec.Code != http.StatusBadRequest || decoded["code"] != "DECISION_SYNC_ENTRY_INVALID" {
		t.Fatalf("invalid entry: status=%d body=%s", rec.Code, rec.Body)
	}
}
