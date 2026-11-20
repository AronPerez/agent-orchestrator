package controllers_test

import (
	"context"
	"net/http"
	"path/filepath"
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

// blockingSync is the sync service with Push and Exchange held open until
// released, so a test can revoke the device in the middle of either.
type blockingSync struct {
	*decisionsync.Server
	entered chan struct{}
	release chan struct{}
}

// blockingLedger holds the guarded heads check open when block is set, so a
// test can revoke the device while a write it admitted is running.
type blockingLedger struct {
	*ledgertest.MemLedger
	block            bool
	entered, release chan struct{}
}

func (l *blockingLedger) CheckDecisionHeads(ctx context.Context, peer string, heads domain.DecisionHeads, now time.Time) ([]domain.DecisionSyncConflict, error) {
	if l.block {
		l.entered <- struct{}{}
		<-l.release
	}
	return l.MemLedger.CheckDecisionHeads(ctx, peer, heads, now)
}

// gateLedger stands in for the daemon's gated ledger: it waits (as for the
// plan gate) before entering the guard, and only its store write is guarded.
type gateLedger struct {
	*ledgertest.MemLedger
	entered, release chan struct{}
}

func (gateLedger) GuardsApply() {}

func (l gateLedger) ApplyDecisionEntries(ctx context.Context, peer string, entries []domain.DecisionEntry, now time.Time) (domain.DecisionApplyResult, error) {
	l.entered <- struct{}{}
	<-l.release // the plan gate
	var result domain.DecisionApplyResult
	err := decisionsync.RunGuarded(ctx, func() error {
		var applyErr error
		result, applyErr = l.MemLedger.ApplyDecisionEntries(ctx, peer, entries, now)
		return applyErr
	})
	return result, err
}

func (b *blockingSync) Exchange(ctx context.Context, peer string, req decisionsync.ExchangeRequest) (decisionsync.ExchangeResponse, error) {
	b.entered <- struct{}{}
	<-b.release // stands in for the long-poll wait
	req.WaitSeconds = 0
	return b.Server.Exchange(ctx, peer, req)
}

func newRevokeFixture(t *testing.T) (*mobilebridge.AccessDevices, *blockingSync, *syncFixture, requestscope.Credential) {
	t.Helper()
	return newRevokeFixtureOver(t, func(*blockingSync) decisionsync.Ledger { return ledgertest.NewMemLedger("h_server") })
}

func newRevokeFixtureOver(t *testing.T, ledger func(*blockingSync) decisionsync.Ledger) (*mobilebridge.AccessDevices, *blockingSync, *syncFixture, requestscope.Credential) {
	t.Helper()
	devices, err := mobilebridge.LoadAccessDevices(filepath.Join(t.TempDir(), "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	dev, _, err := devices.IssueScoped("Decision sync from laptop", "darwin", []string{mobilebridge.DeviceScopeDecisionSync}, syncAt)
	if err != nil {
		t.Fatal(err)
	}
	svc := &blockingSync{entered: make(chan struct{}, 1), release: make(chan struct{})}
	svc.Server = decisionsync.NewServer(decisionsync.ServerDeps{
		Ledger: ledger(svc), HostID: "h_server",
		MachineLabel: func() string { return "mac-mini" }, Now: func() time.Time { return syncAt },
	})
	r := chi.NewRouter()
	r.Route("/api/v1", (&controllers.DecisionSyncController{Svc: svc, Devices: devices}).Register)
	credential := syncCredential("h_peer")
	credential.DeviceID = dev.ID
	return devices, svc, &syncFixture{router: r}, credential
}

func TestRevokeWaitsForAnAdmittedPushAndRefusesTheNext(t *testing.T) {
	var ledger *blockingLedger
	devices, svc, f, credential := newRevokeFixtureOver(t, func(b *blockingSync) decisionsync.Ledger {
		ledger = &blockingLedger{MemLedger: ledgertest.NewMemLedger("h_server"), block: true, entered: b.entered, release: b.release}
		return ledger
	})
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
	ledger.block = false
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

func TestRevokeDoesNotWaitOnAPushHeldAtThePlanGate(t *testing.T) {
	devices, svc, f, credential := newRevokeFixtureOver(t, func(b *blockingSync) decisionsync.Ledger {
		return gateLedger{MemLedger: ledgertest.NewMemLedger("h_server"), entered: b.entered, release: b.release}
	})
	body := mustJSONBody(t, controllers.DecisionSyncPushRequest{Protocol: 1, HostID: "h_peer",
		Entries: syncEntriesDTO(ledgertest.ReceivedRun("h_peer", 1, 1, "dec_1"))})
	status := make(chan int, 1)
	go func() {
		rec, _ := f.do(t, http.MethodPost, "/api/v1/decision-sync/entries", &credential, body)
		status <- rec.Code
	}()
	<-svc.entered // waiting on the gate, outside the guard
	revoked := make(chan struct{})
	go func() { _, _, _ = devices.Revoke(credential.DeviceID); close(revoked) }()
	select {
	case <-revoked:
	case <-time.After(2 * time.Second):
		t.Fatal("revocation waited behind a push held at the plan gate")
	}
	close(svc.release)
	if code := <-status; code != http.StatusUnauthorized {
		t.Fatalf("the page of a device revoked at the gate answered %d, want 401", code)
	}
}

func syncEntriesDTO(entries []domain.DecisionEntry) []controllers.DecisionSyncEntry {
	out := make([]controllers.DecisionSyncEntry, len(entries))
	for i, e := range entries {
		out[i] = controllers.DecisionSyncEntry(decisionsync.ToWire(e))
	}
	return out
}

func TestExchangeRecordsNothingAfterRevocationOnWake(t *testing.T) {
	devices, err := mobilebridge.LoadAccessDevices(filepath.Join(t.TempDir(), "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	dev, _, err := devices.IssueScoped("Decision sync from laptop", "darwin", []string{mobilebridge.DeviceScopeDecisionSync}, syncAt)
	if err != nil {
		t.Fatal(err)
	}
	ledger := ledgertest.NewMemLedger("h_server")
	server := decisionsync.NewServer(decisionsync.ServerDeps{
		Ledger: ledger, HostID: "h_server", MachineLabel: func() string { return "mac-mini" }, Now: func() time.Time { return syncAt },
	})
	r := chi.NewRouter()
	r.Route("/api/v1", (&controllers.DecisionSyncController{Svc: server, Devices: devices}).Register)
	f := &syncFixture{router: r}
	credential := syncCredential("h_peer")
	credential.DeviceID = dev.ID
	// The peer claims an entry at h_x:1 the server does not hold yet: the
	// first page records nothing, and the exchange waits.
	body := mustJSONBody(t, controllers.DecisionSyncExchangeRequest{
		Protocol: 1, HostID: "h_peer", WaitSeconds: 10,
		Heads: map[string]controllers.DecisionSyncHead{"h_x": {Seq: 1, ID: "dent_other"}},
	})
	status := make(chan int, 1)
	go func() {
		rec, _ := f.do(t, http.MethodPost, "/api/v1/decision-sync/exchange", &credential, body)
		status <- rec.Code
	}()
	time.Sleep(100 * time.Millisecond) // waiting
	if _, _, err := devices.Revoke(dev.ID); err != nil {
		t.Fatal(err)
	}
	// A local write wakes the exchange; its heads now differ at h_x:1.
	if _, err := ledger.ApplyDecisionEntries(context.Background(), "h_x", ledgertest.ReceivedRun("h_x", 1, 1, "dec_1"), syncAt); err != nil {
		t.Fatal(err)
	}
	server.Changed()
	select {
	case code := <-status:
		if code != http.StatusUnauthorized {
			t.Fatalf("the woken exchange of a revoked device answered %d, want 401", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the exchange did not wake")
	}
	if c := ledger.Conflicts(); len(c) != 0 {
		t.Fatalf("a revoked device's exchange recorded %+v", c)
	}
}
