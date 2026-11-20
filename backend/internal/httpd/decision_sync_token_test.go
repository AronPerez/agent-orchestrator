package httpd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

const decisionSyncTokenPath = "/api/v1/remote-host/decision-sync-token"

// newDecisionSyncHost is a pairing host whose router also serves the two
// sync routes, so the scope filter's allowlist is visible end to end.
func newDecisionSyncHost(t *testing.T) *pairingHost {
	t.Helper()
	h := newPairingHost(t)
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	h.router.Post("/api/v1/decision-sync/exchange", ok)
	h.router.Post("/api/v1/decision-sync/entries", ok)
	h.router.Post("/api/v1/decision-sync/origin-ids", ok)
	h.router.Get("/api/v1/decision-sync/status", ok)
	return h
}

func (h *pairingHost) issueDecisionSync(bearer, clientID string) (int, map[string]any) {
	h.t.Helper()
	return h.remote(http.MethodPost, decisionSyncTokenPath, bearer, map[string]string{
		"name": "Decision sync from laptop", "platform": "darwin", "clientId": clientID,
	})
}

func (h *pairingHost) mustIssueDecisionSync(bearer, clientID string) (deviceID, token string) {
	h.t.Helper()
	status, body := h.issueDecisionSync(bearer, clientID)
	deviceID, _ = body["deviceId"].(string)
	token, _ = body["token"].(string)
	if status != http.StatusOK || body["hostId"] != "h_test" || body["scope"] != mobilebridge.DeviceScopeDecisionSync ||
		deviceID == "" || !strings.HasPrefix(token, mobilebridge.CoordinationTokenPrefix) {
		h.t.Fatalf("issue decision sync token: status=%d body=%v", status, body)
	}
	return deviceID, token
}

func TestDecisionSyncScopeAllowsOnlySyncRoutes(t *testing.T) {
	h := newDecisionSyncHost(t)
	_, token := h.mustIssueDecisionSync("secret12", "h_laptop")

	for _, path := range []string{"/api/v1/decision-sync/exchange", "/api/v1/decision-sync/entries", "/api/v1/decision-sync/origin-ids"} {
		if status, body := h.remote(http.MethodPost, path, token, map[string]string{}); status != http.StatusOK {
			t.Errorf("POST %s: status=%d body=%v", path, status, body)
		}
	}
	denied := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/decision-sync/exchange"},
		{http.MethodPost, "/api/v1/decision-sync/exchange/"},
		{http.MethodGet, "/api/v1/decision-sync/origin-ids"},
		{http.MethodGet, "/api/v1/decision-sync/status"},
		{http.MethodGet, "/api/v1/coordination/capabilities"},
		{http.MethodGet, "/api/v1/projects"},
		{http.MethodGet, "/api/v1/projects/p1"},
		{http.MethodPost, "/api/v1/agents/readiness/ensure"},
		{http.MethodGet, "/api/v1/sessions"},
		{http.MethodPost, "/api/v1/sessions"},
		{http.MethodGet, "/api/v1/sessions/s1"},
		{http.MethodGet, "/api/v1/sessions/s1/pr"},
		{http.MethodPost, "/api/v1/sessions/s1/send"},
		{http.MethodPost, "/api/v1/sessions/s1/conversation/steer-or-send"},
		{http.MethodPost, "/api/v1/sessions/s1/kill"},
		{http.MethodGet, "/api/v1/endpoints"},
		{http.MethodPost, "/api/v1/remote-host/coordination-token"},
		{http.MethodPost, decisionSyncTokenPath},
		{http.MethodPost, "/api/v1/remote-host/account-token"},
	}
	for _, tc := range denied {
		if status, body := h.remote(tc.method, tc.path, token, map[string]string{}); status != http.StatusForbidden || body["code"] != "CREDENTIAL_SCOPE_DENIED" {
			t.Errorf("%s %s: status=%d body=%v", tc.method, tc.path, status, body)
		}
	}
}

func TestCoordinationScopeCannotReachSyncRoutes(t *testing.T) {
	h := newDecisionSyncHost(t)
	_, issued := h.remote(http.MethodPost, "/api/v1/remote-host/coordination-token", "secret12", map[string]string{"clientId": "h_desk"})
	coordination, _ := issued["token"].(string)
	for _, path := range []string{"/api/v1/decision-sync/exchange", "/api/v1/decision-sync/entries", "/api/v1/decision-sync/origin-ids"} {
		if status, body := h.remote(http.MethodPost, path, coordination, map[string]string{}); status != http.StatusForbidden || body["code"] != "CREDENTIAL_SCOPE_DENIED" {
			t.Errorf("coordination token reached %s: status=%d body=%v", path, status, body)
		}
	}
}

func TestIssueDecisionSyncTokenNeedsFullCredential(t *testing.T) {
	h := newDecisionSyncHost(t)
	fullID, full := h.pair("secret12", "Laptop")

	_, fromPassword := h.mustIssueDecisionSync("secret12", "h_password")
	_, fromDevice := h.mustIssueDecisionSync(full, "h_device")
	if device, ok := h.bridge.Devices.Match(fromDevice); !ok || device.Issuer != mobilebridge.DeviceIssuer(fullID) || device.ClientID != "h_device" || device.Name != "Decision sync from laptop" {
		t.Fatalf("device-issued sync token = %+v, %v", device, ok)
	}
	_, accountBody := h.remote(http.MethodPost, "/api/v1/remote-host/account-token", full, nil)
	account, _ := accountBody["token"].(string)
	_, fromAccount := h.mustIssueDecisionSync(account, "h_account")
	if device, ok := h.bridge.Devices.Match(fromAccount); !ok || device.Issuer != mobilebridge.SharedAccount {
		t.Fatalf("account-issued sync token = %+v, %v", device, ok)
	}

	_, coordinationBody := h.remote(http.MethodPost, "/api/v1/remote-host/coordination-token", full, map[string]string{"clientId": "h_agent"})
	coordination, _ := coordinationBody["token"].(string)
	for name, scoped := range map[string]string{"decision sync": fromPassword, "coordination": coordination} {
		if status, body := h.issueDecisionSync(scoped, "h_scoped"); status != http.StatusForbidden || body["code"] != "CREDENTIAL_SCOPE_DENIED" {
			t.Fatalf("%s token minted a sync token: status=%d body=%v", name, status, body)
		}
	}
	if status, _ := h.local(http.MethodPost, decisionSyncTokenPath, map[string]string{"clientId": "h_local"}); status != http.StatusForbidden {
		t.Fatalf("loopback issued a sync token: %d", status)
	}
	for _, bad := range []string{"", "laptop", "h_", "h_bad/slash", "h_" + strings.Repeat("a", 121)} {
		if status, body := h.issueDecisionSync("secret12", bad); status != http.StatusBadRequest || body["code"] != "INVALID_DECISION_SYNC_CLIENT_ID" {
			t.Fatalf("clientId %q: status=%d body=%v", bad, status, body)
		}
	}
}

func TestIssueDecisionSyncTokenIsIdempotentPerClient(t *testing.T) {
	h := newDecisionSyncHost(t)
	firstID, first := h.mustIssueDecisionSync("secret12", "h_laptop")
	secondID, second := h.mustIssueDecisionSync("secret12", "h_laptop")
	if firstID == secondID || first == second {
		t.Fatal("reissue returned the same token")
	}
	if status, _ := h.remote(http.MethodPost, "/api/v1/decision-sync/exchange", first, map[string]string{}); status != http.StatusUnauthorized {
		t.Fatalf("replaced token still authenticates: %d", status)
	}
	if status, _ := h.remote(http.MethodPost, "/api/v1/decision-sync/exchange", second, map[string]string{}); status != http.StatusOK {
		t.Fatalf("new token refused: %d", status)
	}
	// A coordination token for the same client is a different capability and survives.
	_, issued := h.remote(http.MethodPost, "/api/v1/remote-host/coordination-token", "secret12", map[string]string{"clientId": "h_laptop"})
	coordination, _ := issued["token"].(string)
	h.mustIssueDecisionSync("secret12", "h_laptop")
	if h.sessions(coordination) != http.StatusOK {
		t.Fatal("reissuing a sync token revoked the same client's coordination token")
	}
}

func TestRevokingIssuerRevokesDecisionSyncToken(t *testing.T) {
	h := newDecisionSyncHost(t)
	fullID, full := h.pair("secret12", "Laptop")
	_, token := h.mustIssueDecisionSync(full, "h_laptop")
	if _, _, err := h.bridge.RevokeDevice(fullID); err != nil {
		t.Fatal(err)
	}
	if status, _ := h.remote(http.MethodPost, "/api/v1/decision-sync/exchange", token, map[string]string{}); status != http.StatusUnauthorized {
		t.Fatalf("sync token outlived its issuer: %d", status)
	}
}

func TestIssueDecisionSyncTokenWorksWhenPasswordIsOff(t *testing.T) {
	h := newDecisionSyncHost(t)
	_, full := h.pair("secret12", "Laptop")
	if status, body := h.local(http.MethodPost, "/api/v1/mobile/require-device-credentials", map[string]bool{"enabled": true}); status != http.StatusOK {
		t.Fatalf("turn password off: status=%d body=%v", status, body)
	}
	h.mustIssueDecisionSync(full, "h_laptop")
	if status, _ := h.issueDecisionSync("secret12", "h_other"); status != http.StatusUnauthorized {
		t.Fatalf("password minted a sync token while turned off: %d", status)
	}
}

func TestRevokeDecisionSyncTokenOnlyRevokesSyncTokens(t *testing.T) {
	h := newDecisionSyncHost(t)
	fullID, full := h.pair("secret12", "Laptop")
	syncID, token := h.mustIssueDecisionSync(full, "h_laptop")
	_, issued := h.remote(http.MethodPost, "/api/v1/remote-host/coordination-token", full, map[string]string{"clientId": "h_agent"})
	coordinationID, _ := issued["deviceId"].(string)

	path := decisionSyncTokenPath + "/"
	_, other := h.mustIssueDecisionSync(full, "h_other")
	if status, body := h.remote(http.MethodDelete, path+syncID, other, nil); status != http.StatusForbidden || body["code"] != "DECISION_SYNC_TOKEN_ISSUER" {
		t.Fatalf("sync token revoked another peer's token: status=%d body=%v", status, body)
	}
	for _, other := range []string{fullID, coordinationID} {
		if status, body := h.remote(http.MethodDelete, path+other, full, nil); status != http.StatusNotFound || body["code"] != "DECISION_SYNC_TOKEN_NOT_FOUND" {
			t.Fatalf("revoked a non-sync device %s: status=%d body=%v", other, status, body)
		}
	}
	if status, body := h.remote(http.MethodDelete, path+syncID, full, nil); status != http.StatusNoContent {
		t.Fatalf("revoke sync token: status=%d body=%v", status, body)
	}
	if status, _ := h.remote(http.MethodPost, "/api/v1/decision-sync/exchange", token, map[string]string{}); status != http.StatusUnauthorized {
		t.Fatalf("revoked sync token still authenticates: %d", status)
	}
}

func TestDecisionSyncTokenRevokesItself(t *testing.T) {
	h := newDecisionSyncHost(t)
	id, token := h.mustIssueDecisionSync("secret12", "h_laptop")
	_, coordination := h.remote(http.MethodPost, "/api/v1/remote-host/coordination-token", "secret12", map[string]string{"clientId": "h_agent"})
	coordinationID, _ := coordination["deviceId"].(string)
	if status, body := h.remote(http.MethodDelete, decisionSyncTokenPath+"/"+coordinationID, token, nil); status != http.StatusForbidden {
		t.Fatalf("sync token reached another device: status=%d body=%v", status, body)
	}
	if status, body := h.remote(http.MethodDelete, "/api/v1/remote-host/coordination-token/"+coordinationID, token, nil); status != http.StatusForbidden || body["code"] != "CREDENTIAL_SCOPE_DENIED" {
		t.Fatalf("sync token reached the coordination revoke route: status=%d body=%v", status, body)
	}
	if status, body := h.remote(http.MethodDelete, decisionSyncTokenPath+"/"+id, token, nil); status != http.StatusNoContent {
		t.Fatalf("self revoke: status=%d body=%v", status, body)
	}
	if status, _ := h.remote(http.MethodPost, "/api/v1/decision-sync/exchange", token, map[string]string{}); status != http.StatusUnauthorized {
		t.Fatalf("self-revoked token still authenticates: %d", status)
	}
}
