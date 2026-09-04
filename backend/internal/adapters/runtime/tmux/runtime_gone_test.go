package tmux

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A vanished tmux server is exactly as knowable when a command path hits it as
// when the liveness probe does. Before classifyRuntimeFailure, SendMessage
// returned a bare `exit status 1`, which the session service could only render
// as an opaque 500 with the cause left in the daemon log.
func TestSendMessageOnDeadServerReportsRuntimeUnavailable(t *testing.T) {
	r := New(Options{Binary: "tmux-test", SocketName: "ao", Timeout: time.Second})
	r.runner = &fakeRunnerSequence{results: []fakeRunnerResult{{
		out: []byte("no server running on /private/tmp/tmux-501/ao"),
		err: &exec.ExitError{},
	}}}

	err := r.SendMessage(context.Background(), ports.RuntimeHandle{ID: "sess-1"}, "hello")
	if !errors.Is(err, ports.ErrRuntimeUnavailable) {
		t.Fatalf("SendMessage error = %v, want ErrRuntimeUnavailable", err)
	}
	if !strings.Contains(err.Error(), "no server running") {
		t.Fatalf("error must carry tmux's own diagnosis, got %q", err)
	}
}

// A socket that cannot be connected to is inconclusive, not absence: the
// session may still be alive behind a server AO could not reach.
func TestSendMessageOnUnreachableSocketReportsProbeInconclusive(t *testing.T) {
	r := New(Options{
		Binary:       "tmux-test",
		LegacyBinary: "system-tmux-test",
		SocketName:   "ao",
		Timeout:      time.Second,
	})
	r.runner = &fakeRunnerSequence{results: []fakeRunnerResult{
		{}, // socket discovery: session lives on the private socket
		{
			out: []byte("error connecting to /private/tmp/tmux-501/ao (No such file or directory)"),
			err: &exec.ExitError{},
		},
	}}

	err := r.SendMessage(context.Background(), ports.RuntimeHandle{ID: "sess-1"}, "hello")
	if !errors.Is(err, ports.ErrRuntimeProbeInconclusive) {
		t.Fatalf("SendMessage error = %v, want ErrRuntimeProbeInconclusive", err)
	}
}

// Only knowable runtime absence may be labelled as such: an ordinary tmux
// command failure must keep passing through unchanged.
func TestSendMessageLeavesOrdinaryFailuresUnclassified(t *testing.T) {
	r := New(Options{Binary: "tmux-test", SocketName: "ao", Timeout: time.Second})
	r.runner = &fakeRunnerSequence{results: []fakeRunnerResult{{
		out: []byte("can't find pane: sess-1"),
		err: &exec.ExitError{},
	}}}

	err := r.SendMessage(context.Background(), ports.RuntimeHandle{ID: "sess-1"}, "hello")
	if err == nil {
		t.Fatal("SendMessage error = nil, want failure")
	}
	if errors.Is(err, ports.ErrRuntimeUnavailable) || errors.Is(err, ports.ErrRuntimeProbeInconclusive) {
		t.Fatalf("ordinary failure was labelled a runtime-reachability fact: %v", err)
	}
}

// IsAlive keeps its own contract after delegating classification: a dead server
// is still an error (never a per-session death conclusion), and a missing
// session is still a definitive false.
func TestIsAliveKeepsItsContractAfterDelegatingClassification(t *testing.T) {
	for _, tc := range []struct {
		name      string
		out       string
		wantAlive bool
		wantErr   error
	}{
		{name: "session missing", out: "can't find session: sess-1", wantAlive: false, wantErr: nil},
		{name: "server gone", out: "no server running on /private/tmp/tmux-501/ao", wantErr: ports.ErrRuntimeUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := New(Options{Binary: "tmux-test", SocketName: "ao", Timeout: time.Second})
			r.runner = &fakeRunnerSequence{results: []fakeRunnerResult{{
				out: []byte(tc.out),
				err: &exec.ExitError{},
			}}}

			alive, err := r.IsAlive(context.Background(), ports.RuntimeHandle{ID: "sess-1"})
			if alive != tc.wantAlive {
				t.Fatalf("alive = %v, want %v", alive, tc.wantAlive)
			}
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
