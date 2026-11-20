package cli

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const remoteCallJSON = `{"id":"dec_r","link":"ao://decisions/dec_r","state":"needs_you","stateLabel":"Needs you","title":"Which cache?","context":"c","options":[],"links":[],"projectName":"Shop","machineLabel":"mac-mini","hostId":"h_mini","ownerSessionId":"w-1","ownerSessionName":"w-1","raisedAt":"2026-11-19T09:00:00Z","retired":false}`
const localCallJSON = `{"id":"dec_l","link":"ao://decisions/dec_l","state":"needs_you","stateLabel":"Needs you","title":"Local?","context":"c","options":[],"links":[],"projectName":"Shop","machineLabel":"laptop","hostId":"h_laptop","ownerSessionId":"w-2","ownerSessionName":"w-2","raisedAt":"2026-11-19T09:00:00Z","retired":false}`

func machinesDaemon(t *testing.T) Deps {
	t.Helper()
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/decision-sync/status":
			_, _ = io.WriteString(w, `{"hostId":"h_laptop","machineLabel":"laptop","peers":[],"origins":[{"hostId":"h_mini","machineLabel":"mac-mini","lastHeardAt":"2026-11-19T10:00:00Z"}],"conflicts":[]}`)
		case r.URL.Path == "/api/v1/decisions/dec_r":
			_, _ = io.WriteString(w, `{"call":`+remoteCallJSON+`,"history":[]}`)
		case strings.HasPrefix(r.URL.Path, "/api/v1/decisions"):
			_, _ = io.WriteString(w, `{"calls":[`+remoteCallJSON+`,`+localCallJSON+`]}`)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	deps.Now = func() time.Time { return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) }
	return deps
}

func TestDecisionShowPrintsOriginMachine(t *testing.T) {
	out, _, err := executeCLI(t, machinesDaemon(t), "decision", "show", "dec_r")
	if err != nil {
		t.Fatal(err)
	}
	want := "dec_r  Needs you  Which cache?\n  Shop · mac-mini · 2026-11-19\n  On mac-mini\n  Answer goes to mac-mini · last heard 2h ago\n"
	if !strings.HasPrefix(out, want) {
		t.Fatalf("show =\n%s\nwant prefix\n%s", out, want)
	}
}

func TestDecisionSearchMarksOtherMachine(t *testing.T) {
	out, _, err := executeCLI(t, machinesDaemon(t), "decision", "search", "cache")
	if err != nil {
		t.Fatal(err)
	}
	remote, local, _ := strings.Cut(out, "dec_l")
	if !strings.Contains(remote, "  On mac-mini\n") {
		t.Fatalf("remote call not marked:\n%s", out)
	}
	if strings.Contains(local, "On ") || strings.Contains(local, "Answer goes to") {
		t.Fatalf("local call marked as remote:\n%s", out)
	}
}
