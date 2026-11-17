package httpd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

// pairingHost serves remote requests through a real LAN listener and local ones on the router.
type pairingHost struct {
	t        *testing.T
	bridge   *controllers.BridgeService
	lan      *LANManager
	router   chi.Router
	registry *mobilebridge.DeviceRegistry
	port     int
}

func newPairingHost(t *testing.T) *pairingHost {
	t.Helper()
	dir := t.TempDir()
	devices, err := mobilebridge.LoadAccessDevices(mobilebridge.AccessDevicesPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := mobilebridge.LoadRegistry(mobilebridge.PushDevicesPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	codes := mobilebridge.NewPairingCodes(nil)
	bridge := &controllers.BridgeService{ConfigPath: mobilebridge.Path(dir), HostID: "h_test", Devices: devices, Codes: codes, PushDevices: registry}
	if err := mobilebridge.Save(bridge.ConfigPath, mobilebridge.State{Enabled: true, Password: "secret12", LoopbackOnly: true}); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	mountMobile(router, &controllers.MobileController{Bridge: bridge})
	mountMobileDevices(router, &controllers.MobileDevicesController{Registry: registry, Access: bridge})
	router.Route("/api/v1", func(r chi.Router) {
		(&controllers.PushController{Registry: registry, Devices: devices}).Register(r)
	})
	router.Get("/api/v1/sessions", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router.Get("/api/v1/sessions/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router.Get("/api/v1/sessions/{id}/pr", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router.Post("/api/v1/sessions", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })
	router.Post("/api/v1/sessions/{id}/send", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	router.Post("/api/v1/sessions/{id}/conversation/steer-or-send", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router.Post("/api/v1/sessions/{id}/kill", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router.Get("/api/v1/projects", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router.Get("/api/v1/projects/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router.Post("/api/v1/projects/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router.Post("/api/v1/agents/readiness/ensure", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	lan := NewMobileLAN(router, bridge.HostID, 0, nil, nil)
	lan.UseDeviceCredentials(devices, codes)
	bridge.LAN = lan
	lan.SetPasswordHash(mobilebridge.HashPassword("secret12"))
	port, err := lan.StartLoopback(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lan.Stop(context.Background()) })
	return &pairingHost{t: t, bridge: bridge, lan: lan, router: router, registry: registry, port: port}
}

func (h *pairingHost) remote(method, path, bearer string, body any) (int, map[string]any) {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", h.port, path), reader)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	// A new connection per request, so a closed one is never reused.
	resp, err := (&http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp.StatusCode, decoded
}

func (h *pairingHost) local(method, path string, body any) (int, map[string]any) {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, httptest.NewRequest(method, path, reader))
	var decoded map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&decoded)
	return rec.Code, decoded
}

func (h *pairingHost) code() string {
	h.t.Helper()
	status, body := h.local(http.MethodPost, "/api/v1/mobile/pairing-code", nil)
	code, _ := body["code"].(string)
	if status != http.StatusOK || len(code) != 8 {
		h.t.Fatalf("pairing code: status=%d body=%v", status, body)
	}
	return code
}

func (h *pairingHost) pair(bearer, name string) (string, string) {
	h.t.Helper()
	status, body := h.remote(http.MethodPost, devicePairPath, bearer, map[string]string{"name": name, "platform": "ios"})
	id, _ := body["deviceId"].(string)
	token, _ := body["token"].(string)
	if status != http.StatusOK || id == "" || !strings.HasPrefix(token, mobilebridge.DeviceTokenPrefix) || body["hostId"] != "h_test" {
		h.t.Fatalf("pair with %q: status=%d body=%v", name, status, body)
	}
	return id, token
}

func (h *pairingHost) sessions(bearer string) int {
	h.t.Helper()
	status, _ := h.remote(http.MethodGet, "/api/v1/sessions", bearer, nil)
	return status
}

func (h *pairingHost) rows() []map[string]any {
	h.t.Helper()
	status, body := h.local(http.MethodGet, "/api/v1/mobile/devices", nil)
	if status != http.StatusOK {
		h.t.Fatalf("list devices: status=%d body=%v", status, body)
	}
	var rows []map[string]any
	for _, row := range body["devices"].([]any) {
		rows = append(rows, row.(map[string]any))
	}
	return rows
}

func TestPairingCodePairsOneDevice(t *testing.T) {
	h := newPairingHost(t)
	code := h.code()
	if got := h.sessions(code); got != http.StatusUnauthorized {
		t.Fatalf("pairing code on another route: %d", got)
	}
	_, token := h.pair(code, "Phone")
	if got := h.sessions(token); got != http.StatusOK {
		t.Fatalf("device token: %d", got)
	}
	// The answer says which credential paired, so a device knows a code is spent.
	for bearer, want := range map[string]string{h.code(): "pairing-code", "secret12": "password"} {
		if _, body := h.remote(http.MethodPost, devicePairPath, bearer, nil); body["credential"] != want {
			t.Fatalf("credential = %v, want %s", body["credential"], want)
		}
	}
	if status, body := h.remote(http.MethodPost, devicePairPath, code, nil); status != http.StatusUnauthorized {
		t.Fatalf("a pairing code worked twice: status=%d body=%v", status, body)
	}
	// Without the remote listener's check there is no credential to pair with.
	if status, _ := h.local(http.MethodPost, devicePairPath, nil); status != http.StatusForbidden {
		t.Fatalf("pairing on the loopback listener: %d", status)
	}
	// The loopback-only routes are not served to a paired device.
	for _, path := range []string{"/api/v1/mobile/pairing-code", "/api/v1/mobile/require-device-credentials", "/api/v1/mobile/devices"} {
		if status, _ := h.remote(http.MethodPost, path, token, nil); status != http.StatusNotFound {
			t.Fatalf("%s reachable by a paired device: %d", path, status)
		}
	}
}

func TestOnlyACodeOrThePasswordPairs(t *testing.T) {
	h := newPairingHost(t)
	_, token := h.pair("secret12", "Laptop")
	if status, _ := h.remote(http.MethodPost, devicePairPath, token, nil); status != http.StatusForbidden {
		t.Fatalf("a device token paired another device: %d", status)
	}
	// A device token is enough to link the host into an account.
	status, issued := h.remote(http.MethodPost, "/api/v1/remote-host/account-token", token, nil)
	account, _ := issued["token"].(string)
	if status != http.StatusOK || len(account) != 64 {
		t.Fatalf("account token for a device: status=%d body=%v", status, issued)
	}
	if got := h.sessions(account); got != http.StatusOK {
		t.Fatalf("account token: %d", got)
	}
	if status, _ := h.remote(http.MethodPost, devicePairPath, account, nil); status != http.StatusForbidden {
		t.Fatalf("the account token paired a device: %d", status)
	}
	for _, wrong := range []string{"", "wrongcod", mobilebridge.DeviceTokenPrefix + strings.Repeat("0", 64)} {
		if status, _ := h.remote(http.MethodPost, devicePairPath, wrong, nil); status != http.StatusUnauthorized {
			t.Fatalf("pairing with %q: %d", wrong, status)
		}
	}
}

func TestCoordinationTokenIsLimitedToAgentCoordinationRoutes(t *testing.T) {
	h := newPairingHost(t)
	status, issued := h.remote(http.MethodPost, "/api/v1/remote-host/coordination-token", "secret12", map[string]string{
		"name": "desk agent", "platform": "darwin",
	})
	token, _ := issued["token"].(string)
	if status != http.StatusOK || issued["hostId"] != "h_test" || issued["scope"] != mobilebridge.DeviceScopeAgentCoordination || !strings.HasPrefix(token, mobilebridge.CoordinationTokenPrefix) {
		t.Fatalf("issue coordination token: status=%d body=%v", status, issued)
	}

	allowed := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/projects"},
		{http.MethodGet, "/api/v1/projects/p1"},
		{http.MethodPost, "/api/v1/agents/readiness/ensure"},
		{http.MethodGet, "/api/v1/sessions"},
		{http.MethodPost, "/api/v1/sessions"},
		{http.MethodGet, "/api/v1/sessions/s1"},
		{http.MethodGet, "/api/v1/sessions/s1/pr"},
		{http.MethodPost, "/api/v1/sessions/s1/send"},
		{http.MethodPost, "/api/v1/sessions/s1/conversation/steer-or-send"},
	}
	for _, tc := range allowed {
		status, body := h.remote(tc.method, tc.path, token, map[string]string{})
		if status < 200 || status >= 300 {
			t.Errorf("%s %s: status=%d body=%v", tc.method, tc.path, status, body)
		}
	}

	denied := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/sessions/s1/kill"},
		{http.MethodDelete, "/api/v1/sessions/s1"},
		{http.MethodPost, "/api/v1/projects/p1"},
		{http.MethodGet, "/api/v1/endpoints"},
		{http.MethodPost, "/api/v1/remote-host/coordination-token"},
	}
	for _, tc := range denied {
		status, body := h.remote(tc.method, tc.path, token, map[string]string{})
		if status != http.StatusForbidden || body["code"] != "CREDENTIAL_SCOPE_DENIED" {
			t.Errorf("%s %s: status=%d body=%v", tc.method, tc.path, status, body)
		}
	}

	// An ordinary paired-device token is still a full app credential.
	_, full := h.pair("secret12", "Laptop")
	if status, body := h.remote(http.MethodPost, "/api/v1/sessions/s1/kill", full, nil); status != http.StatusOK {
		t.Fatalf("full device credential was narrowed: status=%d body=%v", status, body)
	}
}

type coordinationProjectManager struct {
	projectsvc.Manager
	project *projectsvc.Project
}

func (m coordinationProjectManager) Get(context.Context, domain.ProjectID) (projectsvc.GetResult, error) {
	return projectsvc.GetResult{Status: "ok", Project: m.project}, nil
}

func TestCoordinationProjectDetailIsASecretFreeProjection(t *testing.T) {
	project := &projectsvc.Project{
		ID: "p1", Name: "Private", Kind: domain.ProjectKindSingleRepo,
		Path: "/private/repo", Repo: "https://token@github.example/owner/repo.git",
		Config: &domain.ProjectConfig{
			Env:                map[string]string{"GITHUB_TOKEN": "env-secret"},
			PostCreate:         []string{"use-post-create-secret"},
			AgentRules:         "rules-secret",
			OrchestratorPrompt: "prompt-secret",
			Worker:             domain.RoleOverride{Harness: domain.HarnessCodex},
			Orchestrator:       domain.RoleOverride{Harness: domain.HarnessClaudeCode},
		},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := NewRouterWithControl(config.Config{}, log, nil, APIDeps{
		Projects: coordinationProjectManager{project: project},
	}, ControlDeps{})
	devices, err := mobilebridge.LoadAccessDevices(mobilebridge.AccessDevicesPath(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := devices.IssueScoped("Agent coordination", "cli", []string{mobilebridge.DeviceScopeAgentCoordination}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	lan := NewMobileLAN(router, "h_test", 0, log, nil)
	lan.UseDeviceCredentials(devices, nil)
	lan.SetPasswordHash(mobilebridge.HashPassword("secret12"))
	port, err := lan.StartLoopback(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lan.Stop(context.Background()) })
	host := &pairingHost{t: t, lan: lan, router: router, port: port}

	status, scoped := host.remote(http.MethodGet, "/api/v1/projects/p1", token, nil)
	if status != http.StatusOK {
		t.Fatalf("scoped project detail: status=%d body=%v", status, scoped)
	}
	scopedJSON, _ := json.Marshal(scoped)
	for _, secret := range []string{"env-secret", "post-create-secret", "rules-secret", "prompt-secret", "/private/repo", "token@github"} {
		if strings.Contains(string(scopedJSON), secret) {
			t.Errorf("scoped project detail exposed %q: %s", secret, scopedJSON)
		}
	}
	gotProject := scoped["project"].(map[string]any)
	gotConfig := gotProject["config"].(map[string]any)
	if gotProject["id"] != "p1" || gotProject["kind"] != string(domain.ProjectKindSingleRepo) {
		t.Fatalf("coordination identity fields = %v", gotProject)
	}
	if gotConfig["worker"].(map[string]any)["agent"] != string(domain.HarnessCodex) || gotConfig["orchestrator"].(map[string]any)["agent"] != string(domain.HarnessClaudeCode) {
		t.Fatalf("coordination agent selectors = %v", gotConfig)
	}

	// The HTTP projection must not mutate the service object or change the full
	// response returned to ordinary app credentials.
	status, full := host.remote(http.MethodGet, "/api/v1/projects/p1", "secret12", nil)
	fullJSON, _ := json.Marshal(full)
	if status != http.StatusOK || !strings.Contains(string(fullJSON), "env-secret") || project.Config.Env["GITHUB_TOKEN"] != "env-secret" {
		t.Fatalf("full project detail was redacted or mutated: status=%d body=%s project=%+v", status, fullJSON, project)
	}
}

func TestOnlyFullCredentialsIssueCoordinationTokens(t *testing.T) {
	h := newPairingHost(t)
	_, full := h.pair("secret12", "Laptop")
	status, body := h.remote(http.MethodPost, "/api/v1/remote-host/coordination-token", full, nil)
	coordination, _ := body["token"].(string)
	if status != http.StatusOK || coordination == "" {
		t.Fatalf("full device token: status=%d body=%v", status, body)
	}
	if status, body = h.remote(http.MethodPost, "/api/v1/remote-host/coordination-token", coordination, nil); status != http.StatusForbidden || body["code"] != "CREDENTIAL_SCOPE_DENIED" {
		t.Fatalf("coordination token minted another token: status=%d body=%v", status, body)
	}
	_, accountBody := h.remote(http.MethodPost, "/api/v1/remote-host/account-token", full, nil)
	account, _ := accountBody["token"].(string)
	if status, body = h.remote(http.MethodPost, "/api/v1/remote-host/coordination-token", account, nil); status != http.StatusOK || body["token"] == "" {
		t.Fatalf("account credential: status=%d body=%v", status, body)
	}
	if status, body = h.remote(http.MethodPost, "/api/v1/remote-host/coordination-token", h.code(), nil); status != http.StatusUnauthorized {
		t.Fatalf("pairing code minted a coordination token: status=%d body=%v", status, body)
	}
	if status, _ := h.local(http.MethodPost, "/api/v1/remote-host/coordination-token", nil); status != http.StatusForbidden {
		t.Fatalf("loopback issued a remote coordination token: %d", status)
	}
}

func TestRequireDeviceCredentialsRefusesThePassword(t *testing.T) {
	h := newPairingHost(t)
	_, token := h.pair(h.code(), "Phone")
	_, issued := h.remote(http.MethodPost, "/api/v1/remote-host/account-token", "secret12", nil)
	account, _ := issued["token"].(string)

	status, body := h.local(http.MethodPost, "/api/v1/mobile/require-device-credentials", map[string]bool{"enabled": true})
	if status != http.StatusOK || body["requireDeviceCredentials"] != true {
		t.Fatalf("turn on: status=%d body=%v", status, body)
	}
	// More attempts than the lockout allows: the device is never locked out.
	for range 7 {
		status, body := h.remote(http.MethodGet, "/api/v1/sessions", "secret12", nil)
		if status != http.StatusUnauthorized || body["code"] != "DEVICE_CREDENTIAL_REQUIRED" {
			t.Fatalf("password after the switch: status=%d body=%v", status, body)
		}
	}
	if status, _ := h.remote(http.MethodPost, devicePairPath, "secret12", nil); status != http.StatusUnauthorized {
		t.Fatalf("the password still pairs: %d", status)
	}
	if h.sessions(token) != http.StatusOK || h.sessions(account) != http.StatusOK {
		t.Fatal("a device token or the account token stopped working")
	}
	h.pair(h.code(), "Tablet")

	// The setting survives a daemon restart.
	state, err := mobilebridge.Load(h.bridge.ConfigPath)
	if err != nil || !state.RequireDeviceCredentials {
		t.Fatalf("saved state: %+v %v", state, err)
	}
	restarted := NewMobileLAN(h.router, "h_test", 0, nil, nil)
	if err := (&controllers.BridgeService{LAN: restarted}).RestoreOnBoot(mobilebridge.State{Enabled: true, Password: "secret12", LoopbackOnly: true, RequireDeviceCredentials: true}); err != nil {
		t.Fatal(err)
	}
	defer restarted.Stop(context.Background())
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/v1/sessions", restarted.BoundPort()), nil)
	req.Header.Set("Authorization", "Bearer secret12")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("password after a restart: %d", resp.StatusCode)
	}

	if status, body := h.local(http.MethodPost, "/api/v1/mobile/require-device-credentials", map[string]bool{"enabled": false}); status != http.StatusOK || body["requireDeviceCredentials"] != false {
		t.Fatalf("turn off: status=%d body=%v", status, body)
	}
	if got := h.sessions("secret12"); got != http.StatusOK {
		t.Fatalf("password after turning the switch off: %d", got)
	}
}

// openConnection returns a connection authenticated with bearer and kept open.
func (h *pairingHost) openConnection(bearer string) net.Conn {
	h.t.Helper()
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", h.port))
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = conn.Close() })
	_, _ = fmt.Fprintf(conn, "GET /api/v1/sessions HTTP/1.1\r\nHost: host\r\nAuthorization: Bearer %s\r\n\r\n", bearer)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		h.t.Fatalf("open connection: %v %v", resp, err)
	}
	resp.Body.Close()
	return conn
}

func closedByHost(conn net.Conn) bool {
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, err := conn.Read(make([]byte, 1))
	return errors.Is(err, io.EOF)
}

func TestRemovingADeviceRevokesItAlone(t *testing.T) {
	h := newPairingHost(t)
	phoneID, phone := h.pair(h.code(), "Phone")
	laptopID, laptop := h.pair(h.code(), "Laptop")
	push := func(bearer, installID string) {
		status, body := h.remote(http.MethodPost, "/api/v1/push/devices", bearer, map[string]string{"installId": installID, "token": "ExponentPushToken[" + installID + "]", "platform": "ios", "deviceName": installID})
		if status != http.StatusOK {
			t.Fatalf("register %s: status=%d body=%v", installID, status, body)
		}
	}
	push(phone, "install-phone")
	push("secret12", "install-old")
	_, issued := h.remote(http.MethodPost, "/api/v1/remote-host/account-token", laptop, nil)
	account, _ := issued["token"].(string)

	rows := map[string]map[string]any{}
	for _, row := range h.rows() {
		rows[row["id"].(string)] = row
	}
	if len(rows) != 4 {
		t.Fatalf("rows = %v", rows)
	}
	if row := rows[phoneID]; row["kind"] != "device" || row["installId"] != "install-phone" || row["notificationsEnabled"] != true || row["deviceName"] != "Phone" {
		t.Fatalf("phone row = %v", row)
	}
	if row := rows[laptopID]; row["kind"] != "device" || row["installId"] != "" || row["notificationsEnabled"] != false {
		t.Fatalf("laptop row = %v", row)
	}
	if row := rows["install-old"]; row["kind"] != "password" {
		t.Fatalf("password phone row = %v", row)
	}
	if row := rows[controllers.AccountDevicesID]; row["kind"] != "account" {
		t.Fatalf("account row = %v", row)
	}

	phoneConn, laptopConn := h.openConnection(phone), h.openConnection(laptop)
	if status, _ := h.local(http.MethodDelete, "/api/v1/mobile/devices/"+phoneID, nil); status != http.StatusNoContent {
		t.Fatalf("remove phone: %d", status)
	}
	// More attempts than the lockout allows: a removed device is never locked out.
	for range 7 {
		if status, body := h.remote(http.MethodGet, "/api/v1/sessions", phone, nil); status != http.StatusUnauthorized || body["code"] != "DEVICE_REVOKED" {
			t.Fatalf("a removed device: status=%d body=%v", status, body)
		}
	}
	if !closedByHost(phoneConn) {
		t.Fatal("the removed device's connection stayed open")
	}
	if closedByHost(laptopConn) {
		t.Fatal("removing one device closed another's connection")
	}
	if h.sessions(laptop) != http.StatusOK || h.sessions("secret12") != http.StatusOK || h.sessions(account) != http.StatusOK {
		t.Fatal("removing one device cut off another credential")
	}
	for _, device := range h.registry.List() {
		if device.InstallID == "install-phone" {
			t.Fatal("the removed device still has a push registration")
		}
	}

	// The account row revokes the shared account token and nothing else.
	if status, _ := h.local(http.MethodDelete, "/api/v1/mobile/devices/"+controllers.AccountDevicesID, nil); status != http.StatusNoContent {
		t.Fatalf("remove account row: %d", status)
	}
	if got := h.sessions(account); got != http.StatusUnauthorized {
		t.Fatalf("the account token after its row was removed: %d", got)
	}
	if h.sessions(laptop) != http.StatusOK {
		t.Fatal("unlinking the account cut off a device")
	}
	// A phone on the pairing password only loses its notifications.
	if status, _ := h.local(http.MethodDelete, "/api/v1/mobile/devices/install-old", nil); status != http.StatusNoContent {
		t.Fatalf("remove password phone: %d", status)
	}
	if got := len(h.rows()); got != 1 {
		t.Fatalf("rows left = %d, want the laptop only", got)
	}
}

func TestAPhoneThatForgetsTheHostLosesItsToken(t *testing.T) {
	h := newPairingHost(t)
	_, phone := h.pair(h.code(), "Phone")
	if status, _ := h.remote(http.MethodPost, "/api/v1/push/devices", phone, map[string]string{"installId": "install-phone"}); status != http.StatusOK {
		t.Fatalf("register: %d", status)
	}
	if status, _ := h.remote(http.MethodDelete, "/api/v1/push/pairings/install-phone", phone, nil); status != http.StatusNoContent {
		t.Fatalf("unpair: %d", status)
	}
	if got := h.sessions(phone); got != http.StatusUnauthorized {
		t.Fatalf("token after the phone forgot the host: %d", got)
	}
	if got := len(h.rows()); got != 0 {
		t.Fatalf("rows left = %d", got)
	}
}

func TestRegenerateRevokesEveryDevice(t *testing.T) {
	h := newPairingHost(t)
	_, phone := h.pair(h.code(), "Phone")
	_, issued := h.remote(http.MethodPost, "/api/v1/remote-host/account-token", phone, nil)
	account, _ := issued["token"].(string)
	unused := h.code()
	for installID, bearer := range map[string]string{"install-phone": phone, "install-old": "secret12"} {
		if status, _ := h.remote(http.MethodPost, "/api/v1/push/devices", bearer, map[string]string{"installId": installID}); status != http.StatusOK {
			t.Fatalf("register %s: %d", installID, status)
		}
	}
	// Both phones and the account.
	if got := len(h.rows()); got != 3 {
		t.Fatalf("rows before regenerate = %d", got)
	}
	phoneConn := h.openConnection(phone)

	status, body := h.local(http.MethodPost, "/api/v1/mobile/regenerate", nil)
	password, _ := body["password"].(string)
	if status != http.StatusOK || password == "" || password == "secret12" {
		t.Fatalf("regenerate: status=%d body=%v", status, body)
	}
	if !closedByHost(phoneConn) {
		t.Fatal("the device's connection stayed open after regenerate")
	}
	for name, credential := range map[string]string{"device token": phone, "account token": account, "old password": "secret12"} {
		if got := h.sessions(credential); got != http.StatusUnauthorized {
			t.Fatalf("%s after regenerate: %d", name, got)
		}
	}
	if status, _ := h.remote(http.MethodPost, devicePairPath, unused, nil); status != http.StatusUnauthorized {
		t.Fatalf("an unused pairing code survived regenerate: %d", status)
	}
	if got := len(h.rows()); got != 0 {
		t.Fatalf("rows left = %d", got)
	}
	h.pair(password, "Phone")
	h.pair(h.code(), "Laptop")
}

func TestAFailedRegenerateKeepsEveryDevice(t *testing.T) {
	h := newPairingHost(t)
	_, phone := h.pair(h.code(), "Phone")
	// The listener is bound to loopback, so starting it for the LAN fails.
	if err := mobilebridge.Save(h.bridge.ConfigPath, mobilebridge.State{Enabled: true, Password: "secret12"}); err != nil {
		t.Fatal(err)
	}
	if status, body := h.local(http.MethodPost, "/api/v1/mobile/regenerate", nil); status == http.StatusOK {
		t.Fatalf("regenerate succeeded: %v", body)
	}
	for name, credential := range map[string]string{"device token": phone, "password": "secret12"} {
		if got := h.sessions(credential); got != http.StatusOK {
			t.Fatalf("%s after a failed regenerate: %d", name, got)
		}
	}
}
