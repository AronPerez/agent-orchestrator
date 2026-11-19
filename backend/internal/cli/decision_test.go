package cli

import (
	"encoding/json"
	"fmt"
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
		{"bad urgency", []string{"decision", "raise", "--title", "x", "--context", "y", "--option", opt1, "--option", opt2, "--recommend", "1", "--urgency", "soon"}, "--urgency is blocking or can-wait"},
		{"why without prediction", []string{"decision", "raise", "--title", "x", "--context", "y", "--option", opt1, "--option", opt2, "--recommend", "1", "--why", "w"}, "--why and --undo go with --would-decide"},
		{"undo without prediction", []string{"decision", "raise", "--title", "x", "--context", "y", "--option", opt1, "--option", opt2, "--recommend", "1", "--undo", "u"}, "--why and --undo go with --would-decide"},
		{"file with kind", []string{"decision", "raise", "--file", "call.json", "--kind", "design"}, "--file cannot be combined"},
		{"search without words", []string{"decision", "search"}, "say what to search for"},
		{"search with whitespace only", []string{"decision", "search", "  ", "\t"}, "say what to search for"},
		{"show without id", []string{"decision", "show"}, "ao decision show <id>"},
		{"show with malformed link", []string{"decision", "show", "ao://decisions/"}, "valid decision ID or ao://decisions/ link"},
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
		Urgency:   "can_wait",
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

func TestDecisionRaiseFileRejectsUnknownFields(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "shop-1")
	requests := 0
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/decisions" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		requests++
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"dec_1"}`)
	}))
	deps.In = strings.NewReader(`{"title":"T","context":"C","options":[{"label":"A","consequence":"a","recommended":true},{"label":"B","consequence":"b"}],"ref":"SKY-12"}`)
	_, _, err := executeCLI(t, deps, "decision", "raise", "--file", "-")
	if err == nil || !strings.Contains(err.Error(), `unknown field "ref"`) {
		t.Fatalf("err=%v", err)
	}
	if requests != 0 {
		t.Fatalf("requests=%d, want 0", requests)
	}
}

func TestDecisionSessionContextRefusesRemoteDaemonWithCollidingSessionID(t *testing.T) {
	const collidingSessionID = "shop-1"
	t.Setenv("AO_SESSION_ID", collidingSessionID)
	t.Setenv("AO_TOKEN", "test-token")
	remoteRequests := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteRequests++
		if got := r.Header.Get(caller.Header); got != collidingSessionID {
			t.Errorf("remote caller=%q, want colliding session %q", got, collidingSessionID)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"dec_remote","ownerSessionId":"shop-1"}`)
			return
		}
		_, _ = io.WriteString(w, `{"calls":[{"id":"dec_remote","ownerSessionId":"shop-1"}]}`)
	}))
	t.Cleanup(remote.Close)
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("local daemon should not be called")
		w.WriteHeader(http.StatusInternalServerError)
	}))

	tests := [][]string{
		{"decision", "raise", "--title", "T", "--context", "C", "--option", "A :: a", "--option", "B :: b", "--recommend", "1"},
		{"decision", "ls"},
		{"decision", "search", "auth"},
		{"decision", "outcome", "dec_1", "--text", "done"},
		{"decision", "withdraw", "dec_1"},
	}
	for _, args := range tests {
		args = append(args, "--url", remote.URL)
		_, _, err := executeCLI(t, deps, args...)
		if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "shop-1") || !strings.Contains(err.Error(), "remote daemon") {
			t.Fatalf("%v err=%v exit=%d", args, err, ExitCode(err))
		}
	}
	if remoteRequests != 0 {
		t.Fatalf("remote requests=%d, want 0", remoteRequests)
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
	callJSON := `{"id":"dec_1","link":"ao://decisions/dec_1","state":"in_progress","stateLabel":"In progress","title":"Which auth scheme should the proxy use?","context":"Choose one.","options":[],"links":[],"projectName":"Shop","streamName":"Checkout epic","machineLabel":"mac-mini","hostId":"h_a","ownerSessionId":"w-1","ownerSessionName":"Worker one","raisedAt":"2026-11-16T15:00:00Z","standingEntryId":"dent_2","choice":"option","option":1,"chosenLabel":"OAuth client credentials","note":"Rotate monthly","retired":false}`
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
		`Outcome owed: ao decision outcome dec_1 --for-answer dent_2 --text "<what happened>"`,
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
	out, _, err = executeCLI(t, deps, "decision", "show", "ao://decisions/dec_1")
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

func TestDecisionActionUsage(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "answer without id", args: []string{"decision", "answer"}, want: "ao decision <command> <id>"},
		{name: "answer without a choice", args: []string{"decision", "answer", "dec_1"}, want: "--pick <n> or --other"},
		{name: "answer with both choices", args: []string{"decision", "answer", "dec_1", "--pick", "1", "--other"}, want: "--pick <n> or --other"},
		{name: "outcome without text", args: []string{"decision", "outcome", "dec_1"}, want: "--text is required"},
		{name: "withdraw with two ids", args: []string{"decision", "withdraw", "dec_1", "dec_2"}, want: "ao decision <command> <id>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := executeCLI(t, Deps{}, tt.args...)
			if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err=%v exit=%d want=%q", err, ExitCode(err), tt.want)
			}
		})
	}
}

func TestDecisionActionsSendTheRightRequest(t *testing.T) {
	tests := []struct {
		name       string
		session    string
		args       []string
		path       string
		body       string
		wantCaller string
		wantOut    string
	}{
		{
			name: "answer option", args: []string{"decision", "answer", "ao://decisions/dec_1", "--pick", "2", "--note", "Rotate monthly"},
			path: "/api/v1/decisions/dec_1/answer", body: `{"choice":"option","option":2,"note":"Rotate monthly"}`,
			wantOut: "Answered dec_1: Per-tenant API keys\n",
		},
		{
			name: "answer other", args: []string{"decision", "answer", "ao://decisions/dec_1", "--other", "--note", "Use the vault"},
			path: "/api/v1/decisions/dec_1/answer", body: `{"choice":"other","note":"Use the vault"}`,
			wantOut: "Answered dec_1: Something else\n",
		},
		{
			name: "dismiss", args: []string{"decision", "dismiss", "ao://decisions/dec_1", "--note", "Not enough to go on"},
			path: "/api/v1/decisions/dec_1/dismiss", body: `{"note":"Not enough to go on"}`,
			wantOut: "Dismissed dec_1\n",
		},
		{
			name: "outcome", session: "w-1", args: []string{"decision", "outcome", "ao://decisions/dec_1", "--for-answer", "dent_2", "--text", "Shipped in PR 412", "--link", "https://example.com/pr/412"},
			path: "/api/v1/decisions/dec_1/outcome", body: `{"forEntry":"dent_2","text":"Shipped in PR 412","link":"https://example.com/pr/412"}`,
			wantCaller: "w-1", wantOut: "Recorded the outcome of dec_1\n",
		},
		{
			name: "withdraw", session: "w-1", args: []string{"decision", "withdraw", "ao://decisions/dec_1"},
			path: "/api/v1/decisions/dec_1/withdraw", wantCaller: "w-1", wantOut: "Withdrew dec_1\n",
		},
		{
			name: "retire", args: []string{"decision", "retire", "ao://decisions/dec_1"},
			path: "/api/v1/decisions/dec_1/retire", body: `{"retired":true}`, wantOut: "dec_1 no longer applies\n",
		},
		{
			name: "unretire", args: []string{"decision", "retire", "ao://decisions/dec_1", "--undo"},
			path: "/api/v1/decisions/dec_1/retire", body: `{"retired":false}`, wantOut: "dec_1 applies again\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", tt.session)
			var gotMethod, gotPath, gotBody, gotCaller string
			deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasPrefix(r.URL.Path, "/api/v1/decisions/") {
					w.WriteHeader(http.StatusAccepted)
					return
				}
				gotMethod, gotPath, gotCaller = r.Method, r.URL.Path, r.Header.Get(caller.Header)
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read body: %v", err)
				}
				gotBody = strings.TrimSpace(string(raw))
				chosen := "Per-tenant API keys"
				if strings.Contains(gotBody, `"choice":"other"`) {
					chosen = "Something else"
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"id":"dec_1","chosenLabel":%q,"state":"in_progress","stateLabel":"In progress"}`, chosen)
			}))

			out, _, err := executeCLI(t, deps, tt.args...)
			if err != nil {
				t.Fatal(err)
			}
			if gotMethod != http.MethodPost || gotPath != tt.path || gotCaller != tt.wantCaller || out != tt.wantOut {
				t.Fatalf("method=%s path=%s caller=%q out=%q", gotMethod, gotPath, gotCaller, out)
			}
			assertDecisionJSONBody(t, gotBody, tt.body)
		})
	}
}

func TestDecisionAnswerFromASessionIsRefusedByTheDaemon(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "w-1")
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/decisions/dec_1/answer" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"kind":"forbidden","code":"DECISION_PERSON_ONLY","message":"Only the person can answer, dismiss or retire a call. A session cannot.","requestId":"req-7"}`)
	}))
	_, _, err := executeCLI(t, deps, "decision", "answer", "dec_1", "--pick", "1")
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "Only the person can answer") {
		t.Fatalf("err=%v exit=%d", err, ExitCode(err))
	}
}

func assertDecisionJSONBody(t *testing.T, got, want string) {
	t.Helper()
	if want == "" {
		if got != "" {
			t.Fatalf("body=%q, want empty", got)
		}
		return
	}
	var gotJSON, wantJSON any
	if err := json.Unmarshal([]byte(got), &gotJSON); err != nil {
		t.Fatalf("decode body %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantJSON); err != nil {
		t.Fatalf("decode wanted body %q: %v", want, err)
	}
	if !reflect.DeepEqual(gotJSON, wantJSON) {
		t.Fatalf("body=%s want=%s", got, want)
	}
}

func TestDecisionRaiseSendsUrgencyKindAndPrediction(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "w-1")
	var got decisionRaiseAPIRequest
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/decisions" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		got = decisionRaiseAPIRequest{}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"dec_1","link":"ao://decisions/dec_1"}`)
	}))
	_, _, err := executeCLI(t, deps,
		"decision", "raise", "--title", "Close the duplicate PR?", "--context", "It repeats an open one.",
		"--option", "Close it :: Branch kept", "--option", "Keep both :: Two reviews", "--recommend", "1",
		"--urgency", "can-wait", "--kind", "pr_hygiene",
		"--would-decide", "1", "--why", "It is a duplicate.", "--undo", "Reopen the PR.",
	)
	if err != nil {
		t.Fatal(err)
	}
	want := &decisionPredictionAPI{Option: 1, Why: "It is a duplicate.", Undo: "Reopen the PR."}
	if got.Urgency != "can_wait" || got.Kind != "pr_hygiene" || !reflect.DeepEqual(got.Prediction, want) {
		t.Fatalf("request=%+v prediction=%+v", got, got.Prediction)
	}

	_, _, err = executeCLI(t, deps,
		"decision", "raise", "--title", "Ship it?", "--context", "c",
		"--option", "Yes :: y", "--option", "No :: n", "--recommend", "1", "--urgency", "blocking",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Urgency != "blocking" || got.Kind != "" || got.Prediction != nil {
		t.Fatalf("request=%+v", got)
	}
}

func TestDecisionShowPrintsAutonomyFields(t *testing.T) {
	callJSON := `{"id":"dec_1","link":"ao://decisions/dec_1","state":"in_progress","stateLabel":"In progress","title":"Close the duplicate PR?","context":"c","options":[],"links":[],"hostId":"h_a","machineLabel":"m","ownerSessionId":"w-1","ownerSessionName":"w-1","raisedAt":"2026-11-16T15:00:00Z","retired":false,` +
		`"urgency":"blocking","kind":"pr_hygiene","kindLabel":"Pull request hygiene","autonomyMode":"observe","prediction":{"option":1,"label":"Close it","why":"It is a duplicate.","undo":"Reopen the PR."},"predictionAgreed":false}`
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/decisions/dec_1" {
			_, _ = io.WriteString(w, `{"call":`+callJSON+`,"history":[]}`)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	out, _, err := executeCLI(t, deps, "decision", "show", "dec_1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Urgency: blocking",
		"Kind: Pull request hygiene (pr_hygiene)",
		"Reserved: false",
		"Autonomy mode: observe",
		"Would have decided: 1. Close it — It is a duplicate. (undo: Reopen the PR.)",
		"Prediction: differed from the answer",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q:\n%s", want, out)
		}
	}
}

func TestDecisionKinds(t *testing.T) {
	var paths []string
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/decisions/kinds" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		paths = append(paths, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"projectId":"shop","mode":"observe","kinds":[{"id":"production","label":"Production changes and deploys","reserved":true},{"id":"pr_hygiene","label":"Pull request hygiene","reserved":false}]}`)
	}))
	t.Setenv("AO_SESSION_ID", "w-1")
	out, _, err := executeCLI(t, deps, "decision", "kinds")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Project shop · autonomy mode: observe",
		"Reserved for the person:",
		"  production  Production changes and deploys",
		"Not reserved:",
		"  pr_hygiene  Pull request hygiene",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("kinds output missing %q:\n%s", want, out)
		}
	}
	t.Setenv("AO_SESSION_ID", "")
	if _, _, err := executeCLI(t, deps, "decision", "kinds", "--project", "shop", "--json"); err != nil {
		t.Fatal(err)
	}
	want := []string{"/api/v1/decisions/kinds", "/api/v1/decisions/kinds?project=shop"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths=%q want=%q", paths, want)
	}
}

func TestDecisionObserve(t *testing.T) {
	var paths []string
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/decisions/observe" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		paths = append(paths, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"projectId":"shop","mode":"observe","rows":[{"kind":"design","label":"Design and product behaviour","reserved":true,"calls":1,"answered":0,"agreed":0,"differed":0},{"kind":"pr_hygiene","label":"Pull request hygiene","reserved":false,"calls":4,"answered":3,"agreed":1,"differed":2}]}`)
	}))
	t.Setenv("AO_SESSION_ID", "")
	out, _, err := executeCLI(t, deps, "decision", "observe", "--project", "shop")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Project shop · autonomy mode: observe",
		"Design and product behaviour (design), reserved: 1 call",
		"Pull request hygiene (pr_hygiene): 4 calls, 3 answered, 1 agreed, 2 differed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("observe output missing %q:\n%s", want, out)
		}
	}
	if !reflect.DeepEqual(paths, []string{"/api/v1/decisions/observe?project=shop"}) {
		t.Fatalf("paths=%q", paths)
	}
}

func TestDecisionObserveWithNothingToShow(t *testing.T) {
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"projectId":"shop","mode":"off","rows":[]}`)
	}))
	t.Setenv("AO_SESSION_ID", "w-1")
	out, _, err := executeCLI(t, deps, "decision", "observe")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No calls raised in observe mode yet.") {
		t.Fatalf("output=%q", out)
	}
}

func TestDecisionDecideOverruleAndList(t *testing.T) {
	type seen struct{ method, uri, caller, body string }
	var requests []seen
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/decisions") {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, seen{r.Method, r.URL.RequestURI(), r.Header.Get(caller.Header), string(body)})
		w.Header().Set("Content-Type", "application/json")
		call := `{"id":"dec_2","link":"ao://decisions/dec_2","state":"decided","stateLabel":"Decided for you","title":"Closed the duplicate pull request #12","context":"It repeats #11.","options":[],"links":[],"hostId":"h_a","machineLabel":"m","ownerSessionId":"w-1","ownerSessionName":"w-1","raisedAt":"2026-11-18T15:00:00Z","retired":false,"kind":"pr_hygiene","kindLabel":"Pull request hygiene","autonomyMode":"on","decided":{"undo":"Reopen #12.","verify":"One open PR remains."}}`
		switch {
		case r.URL.Path == "/api/v1/decisions/dec_2":
			_, _ = io.WriteString(w, `{"call":`+call+`,"history":[]}`)
		case r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `{"calls":[`+call+`]}`)
		case r.URL.Path == "/api/v1/decisions/decided":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, call)
		default:
			_, _ = io.WriteString(w, call)
		}
	}))

	t.Setenv("AO_SESSION_ID", "w-1")
	out, _, err := executeCLI(t, deps, "decision", "decide", "--key", "close-pr-12", "--kind", "pr_hygiene", "--what", "Closed the duplicate pull request #12",
		"--why", "It repeats #11.", "--undo", "Reopen #12.", "--verify", "One open PR remains.", "--link", "PR=https://example.com/12", "--ref", "SKY-1")
	if err != nil {
		t.Fatal(err)
	}
	if out != "Recorded dec_2\nao://decisions/dec_2\nNow carry it out, then confirm: ao decision carried-out dec_2\nIf you do not carry it out, close it: ao decision outcome dec_2 --text \"Not carried out: <why>\"\n" {
		t.Fatalf("decide output=%q", out)
	}
	var decide decisionDecideAPIRequest
	if err := json.Unmarshal([]byte(requests[0].body), &decide); err != nil {
		t.Fatal(err)
	}
	wantDecide := decisionDecideAPIRequest{Key: "close-pr-12", Kind: "pr_hygiene", What: "Closed the duplicate pull request #12", Why: "It repeats #11.", Undo: "Reopen #12.",
		Verify: "One open PR remains.", Links: []decisionLinkAPI{{Label: "PR", URL: "https://example.com/12"}}, Reference: "SKY-1"}
	if requests[0].method != http.MethodPost || requests[0].uri != "/api/v1/decisions/decided" || requests[0].caller != "w-1" || !reflect.DeepEqual(decide, wantDecide) {
		t.Fatalf("decide request = %+v (%+v)", requests[0], decide)
	}

	t.Setenv("AO_SESSION_ID", "")
	if _, _, err := executeCLI(t, deps, "decision", "overrule", "ao://decisions/dec_2", "--note", "Keep both open."); err != nil {
		t.Fatal(err)
	}
	if got := requests[1]; got.method != http.MethodPost || got.uri != "/api/v1/decisions/dec_2/overrule" || got.body != `{"note":"Keep both open."}` {
		t.Fatalf("overrule request = %+v", got)
	}
	out, _, err = executeCLI(t, deps, "decision", "ls", "--decided")
	if err != nil {
		t.Fatal(err)
	}
	if requests[2].uri != "/api/v1/decisions?scope=decided" || !strings.Contains(out, "dec_2  Decided for you  Closed the duplicate pull request #12") ||
		!strings.Contains(out, "Not confirmed carried out: ao decision carried-out dec_2") {
		t.Fatalf("ls --decided uri=%q out=%s", requests[2].uri, out)
	}
	out, _, err = executeCLI(t, deps, "decision", "show", "dec_2")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Undo: Reopen #12.", "Verify: One open PR remains.", "Carried out: false"} {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q:\n%s", want, out)
		}
	}
}

func TestDecisionDecideAndOverruleUsage(t *testing.T) {
	for name, args := range map[string][]string{
		"overrule without note": {"decision", "overrule", "dec_2"},
		"ls with both scopes":   {"decision", "ls", "--decided", "--needs-you"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := executeCLI(t, Deps{}, args...)
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("err=%v exit=%d", err, ExitCode(err))
			}
		})
	}
}

func TestDecisionDecideWarnsWhenOutsidePolicy(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "w-1")
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"dec_3","link":"ao://decisions/dec_3","state":"decided","decided":{"undo":"u","outsidePolicy":"kind_reserved"}}`)
	}))
	out, _, err := executeCLI(t, deps, "decision", "decide", "--kind", "pr_hygiene", "--what", "w", "--why", "y", "--undo", "u")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Recorded dec_3",
		"Kept, but this kind of decision is now reserved for the person.",
		"If you have not acted yet, do not: close this record with ao decision outcome dec_3 --text \"Not carried out: policy changed\" and raise a call instead.",
		"If you already acted, do not close it: confirm with ao decision carried-out dec_3 and tell the person, so they can overrule it.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("out missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Now carry it out") {
		t.Fatalf("an outside-policy record must not be carried out: %q", out)
	}
}

func TestDecisionCarriedOut(t *testing.T) {
	var uri, method string
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uri, method = r.URL.RequestURI(), r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"dec_2","link":"ao://decisions/dec_2","state":"decided","decided":{"undo":"u","carriedOut":true}}`)
	}))
	t.Setenv("AO_SESSION_ID", "w-1")
	out, _, err := executeCLI(t, deps, "decision", "carried-out", "ao://decisions/dec_2")
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || uri != "/api/v1/decisions/dec_2/carried-out" || out != "Confirmed dec_2 as carried out\n" {
		t.Fatalf("method=%s uri=%s out=%q", method, uri, out)
	}
}

func TestDecisionListMarksOutsidePolicyDecisions(t *testing.T) {
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"calls":[{"id":"dec_5","state":"decided","stateLabel":"Decided for you","title":"Closed #12","decided":{"undo":"u","outsidePolicy":"kind_reserved","carriedOut":true}}]}`)
	}))
	t.Setenv("AO_SESSION_ID", "")
	out, _, err := executeCLI(t, deps, "decision", "ls", "--decided")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Outside policy: this kind of decision is now reserved for the person") || strings.Contains(out, "Not confirmed carried out") {
		t.Fatalf("out = %q", out)
	}
}

func TestDecisionDecideRetryReportsTheRecordsCurrentState(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"overruled": {
			`{"id":"dec_7","link":"ao://decisions/dec_7","state":"in_progress","standingEntryId":"dent_o","note":"Keep it open.","decided":{"undo":"u","key":"k"}}`,
			"The person overruled this decision. Do not act on it. Note: Keep it open.",
		},
		"closed": {
			`{"id":"dec_7","link":"ao://decisions/dec_7","state":"done","outcome":"Not carried out.","decided":{"undo":"u","key":"k"}}`,
			"This decision is already closed: Not carried out. Do not act on it.",
		},
		"already confirmed": {
			`{"id":"dec_7","link":"ao://decisions/dec_7","state":"decided","decided":{"undo":"u","key":"k","carriedOut":true}}`,
			"Already confirmed as carried out. Nothing more to do.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", "w-1")
			deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, tc.body)
			}))
			out, _, err := executeCLI(t, deps, "decision", "decide", "--key", "k", "--kind", "pr_hygiene", "--what", "w", "--why", "y", "--undo", "u")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, tc.want) || strings.Contains(out, "Now carry it out") {
				t.Fatalf("out = %q", out)
			}
		})
	}
}

func TestDecisionCarriedOutReportsAReopenedDecision(t *testing.T) {
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"dec_2","state":"decided","changedAfterClose":true,"decided":{"undo":"u","carriedOut":true}}`)
	}))
	t.Setenv("AO_SESSION_ID", "w-1")
	out, _, err := executeCLI(t, deps, "decision", "carried-out", "dec_2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Confirmed dec_2 as carried out") || !strings.Contains(out, "It had been closed as not carried out, so it is open again for the person to review.") {
		t.Fatalf("out = %q", out)
	}
}
