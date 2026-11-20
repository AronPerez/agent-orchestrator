package controllers_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync/ledgertest"
)

// blockingSync is the sync service with Push and Exchange held open until
// released, so a test can revoke the device in the middle of either.
type blockingSync struct {
	*decisionsync.Server
	entered chan struct{}
	release chan struct{}
}

func (b *blockingSync) Push(ctx context.Context, peer string, req decisionsync.PushRequest) (decisionsync.PushResponse, error) {
	b.entered <- struct{}{}
	<-b.release
	return b.Server.Push(ctx, peer, req)
}

func (b *blockingSync) Exchange(ctx context.Context, peer string, req decisionsync.ExchangeRequest) (decisionsync.ExchangeResponse, error) {
	b.entered <- struct{}{}
	<-b.release // stands in for the long-poll wait
	req.WaitSeconds = 0
	return b.Server.Exchange(ctx, peer, req)
}

func newRevokeFixture(t *testing.T) (*mobilebridge.AccessDevices, *blockingSync, *syncFixture, requestscope.Credential) {
	t.Helper()
	devices, err := mobilebridge.LoadAccessDevices(filepath.Join(t.TempDir(), "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	dev, _, err := devices.IssueScoped("Decision sync from laptop", "darwin", []string{mobilebridge.DeviceScopeDecisionSync}, syncAt)
	if err != nil {
		t.Fatal(err)
	}
	svc := &blockingSync{
		Server: decisionsync.NewServer(decisionsync.ServerDeps{
			Ledger: ledgertest.NewMemLedger("h_server"), HostID: "h_server",
			MachineLabel: func() string { return "mac-mini" }, Now: func() time.Time { return syncAt },
		}),
		entered: make(chan struct{}, 1), release: make(chan struct{}),
	}
	r := chi.NewRouter()
	r.Route("/api/v1", (&controllers.DecisionSyncController{Svc: svc, Devices: devices}).Register)
	credential := syncCredential("h_peer")
	credential.DeviceID = dev.ID
	return devices, svc, &syncFixture{router: r}, credential
}

func TestRevokeWaitsForAnAdmittedPushAndRefusesTheNext(t *testing.T) {
	devices, svc, f, credential := newRevokeFixture(t)
	body := mustJSONBody(t, controllers.DecisionSyncPushRequest{Protocol: 1, HostID: "h_peer"})
	status := make(chan int, 1)
	go func() {
		rec, _ := f.do(t, http.MethodPost, "/api/v1/decision-sync/entries", &credential, body)
		status <- rec.Code
	}()
	<-svc.entered // past the recheck, inside the apply
	revoked := make(chan struct{})
	go func() { _, _, _ = devices.Revoke(credential.DeviceID); close(revoked) }()
	select {
	case <-revoked:
		t.Fatal("revocation returned before the admitted page was applied")
	case <-time.After(100 * time.Millisecond):
	}
	close(svc.release)
	if code := <-status; code != http.StatusOK {
		t.Fatalf("admitted push = %d", code)
	}
	<-revoked
	rec, decoded := f.do(t, http.MethodPost, "/api/v1/decision-sync/entries", &credential, body)
	if rec.Code != http.StatusUnauthorized || decoded["code"] != "DEVICE_REVOKED" {
		t.Fatalf("push after revocation = %d %s", rec.Code, rec.Body)
	}
	select {
	case <-svc.entered:
		t.Fatal("a revoked device reached the apply")
	default:
	}
}

func TestRevokeDoesNotWaitOnALongPoll(t *testing.T) {
	devices, svc, f, credential := newRevokeFixture(t)
	body := mustJSONBody(t, controllers.DecisionSyncExchangeRequest{Protocol: 1, HostID: "h_peer", WaitSeconds: 25})
	status := make(chan int, 1)
	go func() {
		rec, _ := f.do(t, http.MethodPost, "/api/v1/decision-sync/exchange", &credential, body)
		status <- rec.Code
	}()
	<-svc.entered // the exchange is waiting
	revoked := make(chan struct{})
	go func() { _, _, _ = devices.Revoke(credential.DeviceID); close(revoked) }()
	select {
	case <-revoked:
	case <-time.After(2 * time.Second):
		t.Fatal("revocation waited on an idle long poll")
	}
	close(svc.release)
	if code := <-status; code != http.StatusUnauthorized {
		t.Fatalf("an exchange revoked while waiting answered %d", code)
	}
}
