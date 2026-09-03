package conpty

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A session with no host at all is definitively gone; the adapter's own IsAlive
// doc already says so, and the send path must say the same thing rather than
// returning an unclassified error the API can only render as a 500.
func TestSendMessageWithNoHostReportsRuntimeUnavailable(t *testing.T) {
	r := New(Options{RunFilePath: t.TempDir() + "/running.json"})
	err := r.SendMessage(context.Background(), ports.RuntimeHandle{ID: "gone-1"}, "hello")
	if !errors.Is(err, ports.ErrRuntimeUnavailable) {
		t.Fatalf("SendMessage error = %v, want ErrRuntimeUnavailable", err)
	}
}

// A stale registry entry pointing at a dead host is the shape the reported bug
// actually takes: resolve() succeeds, and the dial is refused.
func TestSendMessageToDeadHostReportsRuntimeUnavailable(t *testing.T) {
	// Bind and immediately release a loopback port so dials to it are refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	r := New(Options{RunFilePath: t.TempDir() + "/running.json"})
	r.mu.Lock()
	r.sessions["dead-1"] = &hostSession{addr: addr, pid: 1, protocolResolved: true}
	r.mu.Unlock()

	err = r.SendMessage(context.Background(), ports.RuntimeHandle{ID: "dead-1"}, "hello")
	if !errors.Is(err, ports.ErrRuntimeUnavailable) {
		t.Fatalf("SendMessage error = %v, want ErrRuntimeUnavailable", err)
	}
}

// Only the definitive reading is labelled: an error that does not prove the
// host is gone must not be relabelled as one.
func TestClassifyHostFailureLeavesOtherErrorsAlone(t *testing.T) {
	other := errors.New("pty-host: malformed response")
	if got := classifyHostFailure("s-1", other); !errors.Is(got, other) || errors.Is(got, ports.ErrRuntimeUnavailable) {
		t.Fatalf("classifyHostFailure = %v, want the original error unlabelled", got)
	}
	if got := classifyHostFailure("s-1", nil); got != nil {
		t.Fatalf("classifyHostFailure(nil) = %v, want nil", got)
	}
}
