package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/caller"
)

func decisionCLIServer(t *testing.T, handler http.Handler) Deps {
	t.Helper()
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	return Deps{ProcessAlive: func(int) bool { return true }}
}

func TestDecisionRaiseUsage(t *testing.T) {
	opt1 := "OAuth :: One registration per tenant"
	opt2 := "API keys :: Rotate by hand"
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"file with flags", []string{"decision", "raise", "--file", "call.json", "--title", "x"}, "--file cannot be combined"},
		{"bad option", []string{"decision", "raise", "--title", "x", "--context", "y", "--option", "OAuth", "--option", opt2, "--recommend", "1"}, "write each --option as"},
		{"recommend too high", []string{"decision", "raise", "--title", "x", "--context", "y", "--option", opt1, "--option", opt2, "--recommend", "3"}, "--recommend takes the position"},
		{"recommend zero", []string{"decision", "raise", "--title", "x", "--context", "y", "--option", opt1, "--option", opt2, "--recommend", "0"}, "--recommend takes the position"},
		{"bad link", []string{"decision", "raise", "--title", "x", "--context", "y", "--option", opt1, "--option", opt2, "--recommend", "1", "--link", "PR"}, "write each --link as"},
		{"search without words", []string{"decision", "search"}, "say what to search for"},
		{"show without id", []string{"decision", "show"}, "ao decision show <id>"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := executeCLI(t, Deps{}, tc.args...)
			if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v exit=%d want=%q", err, ExitCode(err), tc.want)
			}
		})
	}
}

func TestDecisionRaiseSendsTheCallAndPrintsItsLink(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "w-1")
	var (
		got       decisionRaiseAPIRequest
		gotMethod string
		gotPath   string
		gotCaller string
	)
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/decisions" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		gotMethod, gotPath, gotCaller = r.Method, r.URL.Path, r.Header.Get(caller.Header)
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"dec_1","link":"ao://decisions/dec_1","state":"needs_you","stateLabel":"Needs you"}`)
	}))

	out, _, err := executeCLI(t, deps,
		"decision", "raise",
		"--title", "Which auth scheme should the proxy use?",
		"--context", "The connector calls the proxy for each tenant.",
		"--option", "OAuth client credentials :: One app registration per tenant :: no keys to rotate",
		"--option", " Per-tenant API keys::Customers rotate keys by hand ",
		"--recommend", "1",
		"--link", "Design=https://example.com/d?x=1",
		"--ref", "SKY-12",
	)
	if err != nil {
		t.Fatal(err)
	}
	if out != "Raised dec_1\nao://decisions/dec_1\n" {
		t.Fatalf("output=%q", out)
	}
	want := decisionRaiseAPIRequest{
		Title:     "Which auth scheme should the proxy use?",
		Context:   "The connector calls the proxy for each tenant.",
		Reference: "SKY-12",
		Options: []decisionOptionAPI{
			{Label: "OAuth client credentials", Consequence: "One app registration per tenant :: no keys to rotate", Recommended: true},
			{Label: "Per-tenant API keys", Consequence: "Customers rotate keys by hand"},
		},
		Links: []decisionLinkAPI{{Label: "Design", URL: "https://example.com/d?x=1"}},
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/decisions" || gotCaller != "w-1" || !reflect.DeepEqual(got, want) {
		t.Fatalf("method=%s path=%s caller=%q request=%+v want=%+v", gotMethod, gotPath, gotCaller, got, want)
	}
}

func TestDecisionRaiseFromAFileAndStandardInput(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "w-1")
	doc := `{"title":"T","context":"C","options":[{"label":"A","consequence":"a","recommended":true},{"label":"B","consequence":"b"}]}`
	var requests []decisionRaiseAPIRequest
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/decisions" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var got decisionRaiseAPIRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		requests = append(requests, got)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"dec_1","link":"ao://decisions/dec_1","state":"needs_you","stateLabel":"Needs you"}`)
	}))
	path := filepath.Join(t.TempDir(), "call.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCLI(t, deps, "decision", "raise", "--file", path); err != nil {
		t.Fatal(err)
	}
	deps.In = strings.NewReader(doc)
	if _, _, err := executeCLI(t, deps, "decision", "raise", "--file", "-"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests=%d", len(requests))
	}
	for i, got := range requests {
		if got.Title != "T" || len(got.Options) != 2 || !got.Options[0].Recommended {
			t.Errorf("request %d=%+v", i, got)
		}
	}
}

func TestDecisionRaiseShowsTheRuleTheDaemonNames(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "w-1")
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/decisions" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"kind":"validation","code":"DECISION_INVALID","message":"Mark exactly one option as recommended; this call marks 2.","requestId":"req-7"}`)
	}))
	deps.In = strings.NewReader(`{}`)
	_, _, err := executeCLI(t, deps, "decision", "raise", "--file", "-")
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "Mark exactly one option as recommended; this call marks 2.") {
		t.Fatalf("err=%v exit=%d", err, ExitCode(err))
	}
}

func TestDecisionListSearchAndShow(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "w-1")
	callJSON := `{"id":"dec_1","link":"ao://decisions/dec_1","state":"in_progress","stateLabel":"In progress","title":"Which auth scheme should the proxy use?","context":"Choose one.","options":[],"links":[],"projectName":"Shop","streamName":"Checkout epic","machineLabel":"mac-mini","hostId":"h_a","ownerSessionId":"w-1","ownerSessionName":"Worker one","raisedAt":"2026-11-16T15:00:00Z","choice":"option","option":1,"chosenLabel":"OAuth client credentials","note":"Rotate monthly","retired":false}`
	var paths []string
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/decisions") {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		paths = append(paths, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/decisions/dec_1" {
			_, _ = io.WriteString(w, `{"call":`+callJSON+`,"history":[{"id":"dent_1","kind":"raised","writtenAt":"2026-11-16T15:00:00Z","hostId":"h_a","authorKind":"session","authorSession":"w-1"},{"id":"dent_2","kind":"answered","writtenAt":"2026-11-16T15:05:00Z","hostId":"h_a","authorKind":"person","authorSurface":"desktop","choice":"option","option":1,"note":"Rotate monthly"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"calls":[`+callJSON+`]}`)
	}))

	out, _, err := executeCLI(t, deps, "decision", "ls")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"dec_1  In progress  Which auth scheme should the proxy use?",
		"Shop · Checkout epic · mac-mini · 2026-11-16",
		"Chose: OAuth client credentials · Note: Rotate monthly",
		`Outcome owed: ao decision outcome dec_1 --text "<what happened>"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q:\n%s", want, out)
		}
	}
	out, _, err = executeCLI(t, deps, "decision", "ls", "--needs-you")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Outcome owed") {
		t.Fatalf("needs-you output=%s", out)
	}
	if _, _, err := executeCLI(t, deps, "decision", "search", "auth", "scheme"); err != nil {
		t.Fatal(err)
	}
	out, _, err = executeCLI(t, deps, "decision", "show", "dec_1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ao://decisions/dec_1", "History:", "raised", "answered", "person (desktop)"} {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q:\n%s", want, out)
		}
	}
	out, _, err = executeCLI(t, deps, "decision", "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(out), &decoded) != nil || !strings.Contains(out, "dec_1") {
		t.Fatalf("json output=%q", out)
	}
	t.Setenv("AO_SESSION_ID", "")
	if _, _, err := executeCLI(t, deps, "decision", "ls"); err != nil {
		t.Fatal(err)
	}

	wantPaths := []string{
		"/api/v1/decisions?scope=mine",
		"/api/v1/decisions?scope=needs_you",
		"/api/v1/decisions?q=auth+scheme",
		"/api/v1/decisions/dec_1",
		"/api/v1/decisions?scope=mine",
		"/api/v1/decisions?scope=needs_you",
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("paths=%q want=%q", paths, wantPaths)
	}
}
