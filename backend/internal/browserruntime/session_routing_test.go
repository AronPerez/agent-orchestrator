package browserruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type routingPeer struct {
	conn     net.Conn
	enc      *json.Encoder
	mu       sync.Mutex
	messages chan wireMessage
}

func newRoutingPeer(t *testing.T, b *Broker, remote bool, id string, sessions ...string) *routingPeer {
	t.Helper()
	client, server := net.Pipe()
	p := &routingPeer{conn: client, enc: json.NewEncoder(client), messages: make(chan wireMessage, 32)}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); _ = client.Close() })
	go b.serveRuntimeConn(ctx, server, remote)
	go func() {
		defer close(p.messages)
		dec := json.NewDecoder(client)
		for {
			var m wireMessage
			if dec.Decode(&m) != nil {
				return
			}
			if m.Type == "ping" {
				p.mu.Lock()
				_ = p.enc.Encode(map[string]string{"type": "pong"})
				p.mu.Unlock()
				continue
			}
			select {
			case p.messages <- m:
			case <-ctx.Done():
				return
			}
		}
	}()
	token := ""
	if !remote {
		token = "local-secret"
	}
	p.send(t, map[string]any{"type": "hello", "version": 4, "runtimeId": id, "sessions": sessions, "token": token})
	if m := p.next(t, "hello-ok"); m.Type != "hello-ok" {
		t.Fatal(m)
	}
	for range sessions {
		p.next(t, "session-state")
	}
	return p
}

func (p *routingPeer) send(t *testing.T, m any) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = p.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if err := p.enc.Encode(m); err != nil {
		t.Fatal(err)
	}
}
func (p *routingPeer) next(t *testing.T, kind string) wireMessage {
	t.Helper()
	select {
	case m, ok := <-p.messages:
		if !ok || m.Type != kind {
			t.Fatalf("runtime message = %#v (open=%v), want %s", m, ok, kind)
		}
		return m
	case <-time.After(3 * time.Second):
		t.Fatalf("waiting for %s", kind)
		return wireMessage{}
	}
}
func (p *routingPeer) claim(t *testing.T, id string, takeover bool) wireMessage {
	t.Helper()
	p.send(t, map[string]any{"type": "attach-session", "requestId": uuid.NewString(), "sessionId": id, "takeover": takeover})
	return p.next(t, "session-state")
}
func executeRouting(t *testing.T, b *Broker, id string) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := b.Execute(ctx, domain.SessionID(id), "snapshot", nil)
		done <- err
	}()
	return done
}
func finishRouting(t *testing.T, p *routingPeer, done <-chan error) {
	t.Helper()
	m := p.next(t, "command")
	p.send(t, map[string]any{"type": "result", "requestId": m.RequestID, "ok": true, "result": map[string]any{}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSessionRoutingLocalAndRemote(t *testing.T) {
	b := New(nil, "local-secret")
	local := newRoutingPeer(t, b, false, uuid.NewString(), "local-session")
	remote := newRoutingPeer(t, b, true, uuid.NewString(), "remote-session")
	a, c := executeRouting(t, b, "local-session"), executeRouting(t, b, "remote-session")
	finishRouting(t, local, a)
	finishRouting(t, remote, c)
}
func TestSessionRoutingConflictAndTransfer(t *testing.T) {
	b := New(nil, "local-secret")
	local := newRoutingPeer(t, b, false, uuid.NewString(), "a", "b")
	remote := newRoutingPeer(t, b, true, uuid.NewString())
	if m := remote.claim(t, "a", false); m.Error == nil || m.Error.Code != "BROWSER_SESSION_BUSY" {
		t.Fatalf("claim: %#v", m)
	}
	pending := executeRouting(t, b, "a")
	old := local.next(t, "command")
	if m := remote.claim(t, "a", true); m.Error != nil {
		t.Fatal(m.Error)
	}
	cancel := local.next(t, "cancel")
	if cancel.RequestID != old.RequestID {
		t.Fatal(cancel)
	}
	local.next(t, "session-state")
	if err := <-pending; err == nil {
		t.Fatal("handoff left old command successful")
	}
	finishRouting(t, remote, executeRouting(t, b, "a"))
	finishRouting(t, local, executeRouting(t, b, "b"))
}
func TestSessionRoutingDisconnect(t *testing.T) {
	b := New(nil, "local-secret")
	local := newRoutingPeer(t, b, false, uuid.NewString(), "b")
	id := uuid.NewString()
	remote := newRoutingPeer(t, b, true, id, "a")
	_ = remote.conn.Close()
	if err := <-executeRouting(t, b, "a"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("disconnected owner: %v", err)
	}
	select {
	case m := <-local.messages:
		t.Fatalf("work leaked to local: %#v", m)
	default:
	}
	resumed := newRoutingPeer(t, b, true, id, "a")
	finishRouting(t, resumed, executeRouting(t, b, "a"))
}
func TestSessionRoutingStalePeer(t *testing.T) {
	b := New(nil, "local-secret")
	a := newRoutingPeer(t, b, false, uuid.NewString(), "a")
	other := newRoutingPeer(t, b, true, uuid.NewString(), "b")
	done := executeRouting(t, b, "a")
	command := a.next(t, "command")
	other.send(t, map[string]any{"type": "result", "requestId": command.RequestID, "ok": false, "error": map[string]string{"code": "WRONG_PEER", "message": "not owner"}})
	a.send(t, map[string]any{"type": "result", "requestId": command.RequestID, "ok": true})
	if err := <-done; err != nil {
		t.Fatalf("foreign reply resolved request: %v", err)
	}
}
func TestSessionRoutingUnclaimed(t *testing.T) {
	b := New(nil, "local-secret")
	local := newRoutingPeer(t, b, false, uuid.NewString())
	finishRouting(t, local, executeRouting(t, b, "first"))
	remote := newRoutingPeer(t, b, true, uuid.NewString())
	var commandErr CommandError
	if err := <-executeRouting(t, b, "unknown"); !errors.As(err, &commandErr) || commandErr.Code != "BROWSER_SESSION_UNCLAIMED" {
		t.Fatalf("ambiguous target: %v", err)
	}
	if m := remote.claim(t, "first", false); m.Error == nil || m.Error.Code != "BROWSER_SESSION_BUSY" {
		t.Fatal("first command did not pin its runtime")
	}
}
func TestSessionRoutingLocalRestart(t *testing.T) {
	b := New(nil, "local-secret")
	localID := uuid.NewString()
	local := newRoutingPeer(t, b, false, localID, "local")
	remote := newRoutingPeer(t, b, true, uuid.NewString(), "remote")
	_ = local.conn.Close()
	resumed := newRoutingPeer(t, b, false, localID, "local")
	finishRouting(t, remote, executeRouting(t, b, "remote"))
	finishRouting(t, resumed, executeRouting(t, b, "local"))
}

func TestSessionRoutingDestroy(t *testing.T) {
	b := New(nil, "local-secret")
	a := newRoutingPeer(t, b, false, uuid.NewString(), "a")
	c := newRoutingPeer(t, b, true, uuid.NewString())
	c.claim(t, "a", true)
	a.next(t, "session-state")
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		done <- b.DestroySession(ctx, "a")
	}()
	// Either peer may be first; both readers answer without depending on map order.
	var wg sync.WaitGroup
	for _, p := range []*routingPeer{a, c} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := p.next(t, "command")
			if m.Action != "__destroy-session" {
				t.Errorf("action=%s", m.Action)
			}
			p.send(t, map[string]any{"type": "result", "requestId": m.RequestID, "ok": true})
		}()
	}
	wg.Wait()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	_, owned := b.owners["a"]
	b.mu.Unlock()
	if owned {
		t.Fatal("destroy retained ownership")
	}
}

func TestSessionRoutingReplayCannotTakeOver(t *testing.T) {
	b := New(nil, "local-secret")
	a := newRoutingPeer(t, b, false, uuid.NewString(), "a")
	// A stale replay is a normal claim, never an implicit takeover.
	newRoutingPeer(t, b, true, uuid.NewString(), "a")
	finishRouting(t, a, executeRouting(t, b, "a"))
}

func TestSessionRoutingOffscreenStaysLocal(t *testing.T) {
	b := New(nil, "local-secret")
	a := newRoutingPeer(t, b, false, uuid.NewString())
	newRoutingPeer(t, b, true, uuid.NewString(), "a")
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := b.Execute(ctx, "a", "__render-check", nil)
		done <- err
	}()
	finishRouting(t, a, done)
}

func TestSessionRoutingTransferDoesNotCancelOffscreen(t *testing.T) {
	b := New(nil, "local-secret")
	local := newRoutingPeer(t, b, false, uuid.NewString(), "a")
	remote := newRoutingPeer(t, b, true, uuid.NewString())
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := b.Execute(ctx, "a", "__render-check", nil)
		done <- err
	}()
	command := local.next(t, "command")
	remote.claim(t, "a", true)
	local.next(t, "session-state")
	local.send(t, map[string]any{"type": "result", "requestId": command.RequestID, "ok": true})
	if err := <-done; err != nil {
		t.Fatalf("handoff canceled offscreen work: %v", err)
	}
}

func TestSessionRoutingDestroyDispatchesToEveryPeerBeforeWaiting(t *testing.T) {
	b := New(nil, "local-secret")
	a := newRoutingPeer(t, b, false, uuid.NewString(), "a")
	remote := newRoutingPeer(t, b, true, uuid.NewString())
	remote.claim(t, "a", true)
	a.next(t, "session-state")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.DestroySession(ctx, "a") }()
	// Neither desktop answers until both have received teardown. One slow
	// runtime must not consume the other runtime's cleanup deadline.
	commands := []wireMessage{a.next(t, "command"), remote.next(t, "command")}
	for i, p := range []*routingPeer{a, remote} {
		p.send(t, map[string]any{"type": "result", "requestId": commands[i].RequestID, "ok": true})
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
