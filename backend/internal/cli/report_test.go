package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestReportValidation(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "worker-1")
	long := strings.Repeat("x", 1001)
	tests := []struct {
		name string
		args []string
	}{
		{"empty", []string{"report"}},
		{"legacy positional status", []string{"report", "working"}},
		{"state missing note", []string{"report", "--done"}},
		{"mutually exclusive states", []string{"report", "--done", "--stuck", "--note", "x"}},
		{"free form with note", []string{"report", "hello", "--note", "x"}},
		{"free form with empty note flag", []string{"report", "hello", "--note="}},
		{"free form with state", []string{"report", "hello", "--done", "--note", "x"}},
		{"free form with output", []string{"report", "hello", "--artifact", "ref"}},
		{"note with output only", []string{"report", "--note", "x", "--artifact", "ref"}},
		{"free form too long", []string{"report", long}},
		{"note too long", []string{"report", "--checkpoint", "--note", long}},
		{"empty artifact", []string{"report", "--artifact="}},
		{"invalid created PR", []string{"report", "--pr-created", "git://github.com/o/r/pull/1"}},
		{"invalid reviewed PR host", []string{"report", "--pr-reviewed", "https://example.com/o/r/pull/1"}},
		{"invalid PR path", []string{"report", "--pr-created", "https://github.com/o/r/issues/1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := executeCLI(t, Deps{}, tc.args...)
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("err=%v exit=%d", err, ExitCode(err))
			}
		})
	}
}

func TestReportModesAndBoundaries(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "worker-1")
	pr1 := "https://github.com/o/r/pull/12"
	pr2 := "http://github.com/o/r/pull/13"
	tests := []struct {
		name string
		args []string
		want reportAPIRequest
	}{
		{name: "free form", args: []string{"report", "hello", "world"}, want: reportAPIRequest{SessionID: "worker-1", Message: "hello world"}},
		{name: "free form boundary", args: []string{"report", strings.Repeat("界", 1000)}, want: reportAPIRequest{SessionID: "worker-1", Message: strings.Repeat("界", 1000)}},
		{name: "output only repeated", args: []string{"report", "--artifact", "one", "--artifact", "two", "--pr-created", pr1, "--pr-reviewed", pr2}, want: reportAPIRequest{SessionID: "worker-1", Outputs: []reportAPIOutput{{Kind: "artifact", Reference: "one"}, {Kind: "artifact", Reference: "two"}, {Kind: "pr_created", Reference: pr1}, {Kind: "pr_reviewed", Reference: pr2}}}},
		{name: "GitLab merge request", args: []string{"report", "--pr-created", "https://gitlab.com/team/app/-/merge_requests/4"}, want: reportAPIRequest{SessionID: "worker-1", Outputs: []reportAPIOutput{{Kind: "pr_created", Reference: "https://gitlab.com/team/app/-/merge_requests/4"}}}},
		{name: "checkpoint with output", args: []string{"report", "--checkpoint", "--note", "x", "--artifact", "opaque://anything"}, want: reportAPIRequest{SessionID: "worker-1", State: "checkpoint", Note: "x", Outputs: []reportAPIOutput{{Kind: "artifact", Reference: "opaque://anything"}}}},
		{name: "needs input", args: []string{"report", "--needs-input", "--note", "x"}, want: reportAPIRequest{SessionID: "worker-1", State: "needs_input", Note: "x"}},
		{name: "stuck", args: []string{"report", "--stuck", "--note", "x"}, want: reportAPIRequest{SessionID: "worker-1", State: "stuck", Note: "x"}},
		{name: "done without output", args: []string{"report", "--done", "--note", strings.Repeat("x", 1000)}, want: reportAPIRequest{SessionID: "worker-1", State: "done", Note: strings.Repeat("x", 1000)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			var got reportAPIRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/reports" {
					_ = json.NewDecoder(r.Body).Decode(&got)
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, `{"id":"rpt_1"}`)
			}))
			defer srv.Close()
			writeRunFileFor(t, cfg, srv)
			_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("request=%+v want=%+v", got, tc.want)
			}
		})
	}
}

func TestReportPreservesAPIEnvelopeAsRuntimeError(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "worker-1")
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"message":"bad report","code":"INVALID_REPORT","requestId":"req-7"}`)
	}))
	defer srv.Close()
	writeRunFileFor(t, cfg, srv)
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "report", "hello")
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "bad report (INVALID_REPORT) [request req-7]") {
		t.Fatalf("err=%v", err)
	}
}

func TestReportShowPrintsOnlyTheRequestedBatch(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_PROJECT_ID", "demo")
	t.Setenv("AO_SESSION_ID", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"project":{"id":"demo","kind":"single_repo"}}`)
		case "/api/v1/reports":
			if r.URL.Query().Get("projectId") != "demo" {
				t.Errorf("projectId = %q", r.URL.Query().Get("projectId"))
			}
			_, _ = io.WriteString(w, `{"reports":[
				{"id":"rpt_a","sessionId":"w-1","state":"needs_input","note":"pick a schema","repeatCount":1,"deliveryBatchId":"report-batch:rpt_a"},
				{"id":"rpt_b","sessionId":"w-2","message":"tests green","outputs":[{"kind":"pr_created","reference":"https://github.com/o/r/pull/1"}],"repeatCount":2,"deliveryBatchId":"report-batch:rpt_a"},
				{"id":"rpt_c","sessionId":"w-3","state":"done","note":"other batch","repeatCount":1,"deliveryBatchId":"report-batch:rpt_c"}]}`)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "report", "show", "report-batch:rpt_a")
	if err != nil {
		t.Fatal(err)
	}
	want := "Reports in report-batch:rpt_a:\n\n[needs_input] w-1\npick a schema\n\n[information] w-2 (repeated 2 times)\ntests green\npr_created: https://github.com/o/r/pull/1\n"
	if out != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "report", "show", "report-batch:missing"); err == nil || !strings.Contains(err.Error(), "no reports in batch") {
		t.Fatalf("missing batch err = %v", err)
	}
}
