// Package browserruntime brokers browser commands between the loopback daemon
// and the Electron process that owns AO's per-session WebContentsView targets.
package browserruntime

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// ProtocolVersion identifies the daemon-to-Electron browser bridge contract.
	// v4 adds session ownership; v3 adds heartbeat. Legacy local peers remain supported.
	ProtocolVersion = 4
	// minSupportedProtocolVersion is the oldest hello the broker accepts, so a
	// freshly built daemon keeps working against a not-yet-upgraded desktop app.
	minSupportedProtocolVersion = 2
	// RuntimeTokenStdinEnv tells an app-spawned daemon to read its token once
	// from the inherited private stdin pipe instead of an environment variable.
	RuntimeTokenStdinEnv = "AO_BROWSER_RUNTIME_TOKEN_STDIN" //nolint:gosec // Environment variable name, not a credential.
	// RuntimeAddressEnv carries the exact listener address into running.json so
	// Electron never has to duplicate the backend's platform-specific naming.
	RuntimeAddressEnv     = "AO_BROWSER_RUNTIME_ADDRESS"
	helloTimeout          = 5 * time.Second
	helloReplyTimeout     = time.Second
	runtimeReconnectGrace = 2 * time.Second
	maxRuntimeFrameBytes  = 8 << 20
)

// ReadRuntimeToken reads the one-line token handoff used by the desktop app.
// The token is never placed in a file, command line, or daemon environment, and
// the daemon does not pass the consumed stdin handle to worker processes.
func ReadRuntimeToken(r io.Reader) (string, error) {
	const maxTokenBytes = 256
	line, err := bufio.NewReader(io.LimitReader(r, maxTokenBytes+1)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read browser runtime token: %w", err)
	}
	token := strings.TrimSpace(line)
	if token == "" {
		return "", errors.New("browser runtime token handoff was empty")
	}
	if len(token) > maxTokenBytes {
		return "", errors.New("browser runtime token handoff was too long")
	}
	return token, nil
}

// ErrUnavailable indicates that no Electron browser runtime can accept a command.
var ErrUnavailable = errors.New("browser runtime is unavailable")

// ErrReconnecting indicates that the desktop runtime is temporarily between
// connections. It wraps ErrUnavailable so existing callers retain the
// unavailable classification while newer callers can offer a retry action.
var ErrReconnecting = errors.New("browser runtime is reconnecting")

// ErrUnresponsive is the disconnect cause recorded when the runtime stops
// answering liveness pings.
var ErrUnresponsive = errors.New("browser runtime is unresponsive")

// livenessConfig tunes the broker heartbeat. A runtime whose desktop process
// wedged (busy event loop, system sleep, wedged network stack) keeps its
// socket open but stops answering pings, so the stall check — not the socket
// state — is what actually detects a dead bridge.
type livenessConfig struct {
	interval   time.Duration
	stallLimit time.Duration
}

var defaultLiveness = livenessConfig{interval: 5 * time.Second, stallLimit: 15 * time.Second}

// Status describes whether a runtime is connected to the browser command broker.
type Status struct {
	Connected   bool
	ConnectedAt time.Time
	// Transport names the runtime kind this broker serves.
	Transport string
	// Client and ClientVersion are what the connected runtime reported in its
	// hello, for example the AO Chrome extension and its version.
	Client        string
	ClientVersion string
	// Target is set by the session router: which browser the session uses.
	Target string
}

// Command is one session-scoped operation sent to the Electron browser runtime.
type Command struct {
	RequestID string                 `json:"requestId"`
	SessionID domain.SessionID       `json:"sessionId"`
	Action    string                 `json:"action"`
	Args      map[string]interface{} `json:"args,omitempty"`
}

// CommandError is a stable browser failure returned by the Electron runtime.
type CommandError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e CommandError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s (%s)", e.Message, e.Code)
}

// Result contains the correlated response to a browser command.
type Result struct {
	RequestID string
	Value     interface{}
}

type wireMessage struct {
	Type      string             `json:"type"`
	RuntimeID string             `json:"runtimeId,omitempty"`
	Sessions  []domain.SessionID `json:"sessions,omitempty"`
	Takeover  bool               `json:"takeover,omitempty"`
	Owned     bool               `json:"owned"`
	Revision  uint64             `json:"revision"`
	Version   int                `json:"version,omitempty"`
	Token     string             `json:"token,omitempty"`
	// Client and ClientVersion are optional hello metadata.
	Client        string                 `json:"client,omitempty"`
	ClientVersion string                 `json:"clientVersion,omitempty"`
	RequestID     string                 `json:"requestId,omitempty"`
	SessionID     domain.SessionID       `json:"sessionId,omitempty"`
	Action        string                 `json:"action,omitempty"`
	Args          map[string]interface{} `json:"args,omitempty"`
	OK            bool                   `json:"ok,omitempty"`
	Result        json.RawMessage        `json:"result,omitempty"`
	Error         *CommandError          `json:"error,omitempty"`
}

type pendingResult struct {
	value interface{}
	err   error
}

// Broker routes session commands to independent desktop connections. Chrome
// retains its exclusive single-runtime policy.
type Broker struct {
	log               *slog.Logger
	mu                sync.Mutex
	peers             map[string]*runtimePeer
	local             *runtimePeer
	owners            map[domain.SessionID]string
	revision          uint64
	connectionChanged chan struct{}
	token             string
	transport         string
	verifyPeer        func(net.Conn) error
	exclusive         bool
	liveness          livenessConfig
}

type runtimePeer struct {
	conn                  net.Conn
	id                    string
	remote                bool
	version               int
	connectedAt           time.Time
	client, clientVersion string
	pongAt                time.Time
	writeGate             chan struct{}
	pending               map[string]pendingRequest
	sessions              map[domain.SessionID]struct{}
}
type pendingRequest struct {
	sessionID   domain.SessionID
	ownerScoped bool
	result      chan pendingResult
}

func newRuntimePeer(conn net.Conn, id string, remote bool, hello wireMessage) *runtimePeer {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return &runtimePeer{conn: conn, id: id, remote: remote, version: hello.Version, connectedAt: time.Now().UTC(),
		client: hello.Client, clientVersion: hello.ClientVersion, pongAt: time.Now(), writeGate: gate,
		pending: make(map[string]pendingRequest), sessions: make(map[domain.SessionID]struct{})}
}

// New creates an empty browser command broker.
func New(log *slog.Logger, token ...string) *Broker {
	if log == nil {
		log = slog.Default()
	}
	runtimeToken := ""
	if len(token) > 0 {
		runtimeToken = token[0]
	}
	return &Broker{log: log, peers: make(map[string]*runtimePeer), owners: make(map[domain.SessionID]string),
		connectionChanged: make(chan struct{}), token: runtimeToken, transport: TransportElectron, liveness: defaultLiveness}
}

// TransportElectron is the desktop app's WebContentsView runtime.
const TransportElectron = "electron-webcontents-debugger"

// SetTransport names the runtime kind this broker serves. Call before Serve.
func (b *Broker) SetTransport(name string) {
	b.mu.Lock()
	b.transport = name
	b.mu.Unlock()
}

// SetExclusive makes the first connected runtime keep the bridge: a second
// runtime is told it was rejected and closed instead of replacing the first.
// The Chrome runtime uses this so two Chrome profiles cannot keep displacing
// each other. Call before Serve.
func (b *Broker) SetExclusive(exclusive bool) {
	b.mu.Lock()
	b.exclusive = exclusive
	b.mu.Unlock()
}

// SetPeerVerifier installs a check that runs on every accepted connection
// before its hello is read. A failing check closes the connection. Call
// before Serve.
func (b *Broker) SetPeerVerifier(verify func(net.Conn) error) {
	b.mu.Lock()
	b.verifyPeer = verify
	b.mu.Unlock()
}

// NewToken returns a per-daemon-launch secret used to authenticate the desktop
// browser runtime before it can replace the active bridge connection.
func NewToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate browser runtime token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// Serve accepts Electron runtime connections until ctx is cancelled. A new
// valid runtime replaces an older connection and fails its in-flight commands;
// this makes renderer reload/restart recovery deterministic.
func (b *Broker) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil //nolint:nilerr // listener closure is the expected shutdown path
			}
			return fmt.Errorf("accept browser runtime: %w", err)
		}
		go b.serveConn(ctx, conn)
	}
}

// ServeRemoteConn accepts a connection authenticated by the LAN HTTP listener.
// Session ownership, not connection order, chooses the command destination.
// Never expose this entry point without bearer auth.
func (b *Broker) ServeRemoteConn(ctx context.Context, conn net.Conn) {
	b.serveRuntimeConn(ctx, conn, true)
}

func (b *Broker) serveConn(ctx context.Context, conn net.Conn) {
	b.serveRuntimeConn(ctx, conn, false)
}

func (b *Broker) serveRuntimeConn(ctx context.Context, conn net.Conn, remote bool) {
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	b.mu.Lock()
	verify := b.verifyPeer
	b.mu.Unlock()
	if verify != nil && !remote {
		if err := verify(conn); err != nil {
			b.log.Warn("browser runtime peer rejected", "err", err)
			return
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(helloTimeout))
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), maxRuntimeFrameBytes)
	if !scanner.Scan() {
		return
	}
	token := b.token
	if remote {
		token = ""
	}
	if rejection := helloRejection(scanner.Bytes(), token); rejection != nil {
		_ = writeHelloReply(conn, wireMessage{Type: "hello-rejected", Error: rejection})
		return
	}
	var hello wireMessage
	_ = json.Unmarshal(scanner.Bytes(), &hello)
	if remote && hello.Version < 4 {
		_ = writeHelloReply(conn, wireMessage{Type: "hello-rejected", Error: &CommandError{Code: "BROWSER_RUNTIME_PROTOCOL_MISMATCH", Message: "Update the remote desktop to use session-scoped browsers"}})
		return
	}
	id := hello.RuntimeID
	if id == "" && (!remote && (hello.Version < 4 || b.exclusive)) {
		id = "local"
	}
	if id == "" || len(id) > 128 || (remote && id == "local") {
		_ = writeHelloReply(conn, wireMessage{Type: "hello-rejected", Error: &CommandError{Code: "BROWSER_RUNTIME_BAD_HELLO", Message: "A bounded runtime ID is required"}})
		return
	}
	p := newRuntimePeer(conn, id, remote, hello)
	b.mu.Lock()
	busy := b.exclusive && len(b.peers) > 0
	collision := b.peers[id] != nil && b.peers[id].remote != remote
	b.mu.Unlock()
	if busy || collision {
		_ = writeHelloReply(conn, wireMessage{Type: "hello-rejected", Error: errRuntimeBusy})
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	if err := writeHelloReply(conn, wireMessage{Type: "hello-ok"}); err != nil {
		return
	}
	b.mu.Lock()
	if (b.exclusive && len(b.peers) > 0) || (b.peers[id] != nil && b.peers[id].remote != remote) {
		b.mu.Unlock()
		_ = writeHelloReply(conn, wireMessage{Type: "rejected", Error: errRuntimeBusy})
		return
	}
	old := b.peers[id]
	if !remote && b.local != nil {
		old = b.local
		delete(b.peers, old.id)
	}
	b.peers[id] = p
	if !remote {
		b.local = p
	}
	var notices []sessionNotice
	for _, sessionID := range hello.Sessions {
		notices = append(notices, b.claimLocked(p, sessionID, false, "")...)
	}
	b.signalConnectionChangedLocked()
	b.mu.Unlock()
	if old != nil {
		b.disconnect(old, nil)
	}
	defer b.disconnect(p, nil)
	b.sendNotices(ctx, notices)
	b.log.Info("browser runtime connected", "transport", b.transport, "remote", remote, "client", hello.Client)
	if hello.Version >= 3 {
		go b.runLiveness(ctx, p)
	}
	go func() { <-ctx.Done(); _ = conn.Close() }()
	for scanner.Scan() {
		var msg wireMessage
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			continue
		}
		b.mu.Lock()
		current := b.peers[id] == p
		b.mu.Unlock()
		if !current {
			return
		}
		switch msg.Type {
		case "pong":
			b.mu.Lock()
			p.pongAt = time.Now()
			b.mu.Unlock()
		case "attach-session":
			if hello.Version < 4 || b.exclusive {
				continue
			}
			b.mu.Lock()
			notices := b.claimLocked(p, msg.SessionID, msg.Takeover, msg.RequestID)
			b.mu.Unlock()
			b.sendNotices(ctx, notices)
		case "result":
			if msg.RequestID != "" {
				b.resolve(p, msg)
			}
		}
	}
	b.disconnect(p, scanner.Err())
}

func (b *Broker) runLiveness(ctx context.Context, p *runtimePeer) {
	ticker := time.NewTicker(b.liveness.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		b.mu.Lock()
		current := b.peers[p.id] == p
		stalled := time.Since(p.pongAt) > b.liveness.stallLimit
		b.mu.Unlock()
		if !current {
			return
		}
		if stalled {
			b.disconnect(p, ErrUnresponsive)
			return
		}
		pingCtx, cancel := context.WithTimeout(ctx, time.Second)
		err := p.write(pingCtx, wireMessage{Type: "ping"})
		cancel()
		if err != nil {
			b.log.Debug("browser runtime ping failed", "err", err)
		}
	}
}

func (p *runtimePeer) write(ctx context.Context, msg wireMessage) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.writeGate:
	}
	defer func() { p.writeGate <- struct{}{} }()
	return p.writeFrame(ctx, msg)
}

// The caller holds writeGate, including when registering a command for cancellation.
func (p *runtimePeer) writeFrame(ctx context.Context, msg wireMessage) error {
	conn := p.conn
	deadline := time.Now().Add(30 * time.Second)
	if requested, ok := ctx.Deadline(); ok && requested.Before(deadline) {
		deadline = requested
	}
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	interruptDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = conn.SetWriteDeadline(time.Now())
		close(interruptDone)
	})
	frame, err := json.Marshal(msg)
	if err == nil && len(frame)+1 > maxRuntimeFrameBytes {
		err = fmt.Errorf("browser runtime frame exceeds %d bytes", maxRuntimeFrameBytes)
	}
	if err == nil {
		frame = append(frame, '\n')
		for len(frame) > 0 && err == nil {
			var written int
			written, err = conn.Write(frame)
			if written == 0 && err == nil {
				err = io.ErrShortWrite
				break
			}
			frame = frame[written:]
		}
	}
	writeComplete := err == nil && len(frame) == 0
	if !stop() {
		<-interruptDone
	}
	_ = conn.SetWriteDeadline(time.Time{})
	// Once the complete frame reached the runtime, let Execute observe a
	// simultaneous cancellation and send the matching cancel frame. Returning
	// ctx.Err here would make the delivered command impossible to cancel —
	// Execute would bail out of the error path without writing the cancel
	// frame, hanging any test/client waiting to read it.
	if writeComplete {
		return nil
	}
	// The write failed. Surface a context error when applicable rather than
	// a raw i/o timeout. The write deadline (derived from the context
	// deadline) and the context's own timer are independent — the write
	// deadline can fire microseconds before the context timer, so ctx.Err()
	// may still be nil when we reach here.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if requested, ok := ctx.Deadline(); ok && !time.Now().Before(requested) {
		return context.DeadlineExceeded
	}
	return err
}

var errRuntimeBusy = &CommandError{Code: "BROWSER_RUNTIME_BUSY", Message: "Another browser runtime is already connected"}

// helloRejection reports why a runtime's first frame is not an acceptable hello,
// or nil if it is.
func helloRejection(frame []byte, token string) *CommandError {
	var hello wireMessage
	switch {
	case json.Unmarshal(frame, &hello) != nil || hello.Type != "hello":
		return &CommandError{Code: "BROWSER_RUNTIME_BAD_HELLO", Message: "first frame was not a hello"}
	case hello.Version < minSupportedProtocolVersion || hello.Version > ProtocolVersion:
		return &CommandError{
			Code:    "BROWSER_RUNTIME_PROTOCOL_MISMATCH",
			Message: fmt.Sprintf("daemon speaks browser runtime protocol %d-%d, runtime sent %d", minSupportedProtocolVersion, ProtocolVersion, hello.Version),
		}
	case !validRuntimeToken(token, hello.Token):
		return &CommandError{
			Code:    "BROWSER_RUNTIME_TOKEN_REJECTED",
			Message: "browser runtime token does not match the one this daemon was started with",
		}
	}
	return nil
}

func writeHelloReply(conn net.Conn, msg wireMessage) error {
	frame, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if err := conn.SetWriteDeadline(time.Now().Add(helloReplyTimeout)); err != nil {
		return err
	}
	defer func() { _ = conn.SetWriteDeadline(time.Time{}) }()
	_, err = conn.Write(append(frame, '\n'))
	return err
}

func validRuntimeToken(expected, actual string) bool {
	if expected == "" {
		return actual == ""
	}
	return hmac.Equal([]byte(expected), []byte(actual))
}

func (b *Broker) resolve(p *runtimePeer, msg wireMessage) {
	b.mu.Lock()
	request := p.pending[msg.RequestID]
	delete(p.pending, msg.RequestID)
	ch := request.result
	b.mu.Unlock()
	if ch == nil {
		return
	}
	if !msg.OK {
		if msg.Error == nil {
			msg.Error = &CommandError{Code: "BROWSER_COMMAND_FAILED", Message: "Browser command failed"}
		}
		ch <- pendingResult{err: *msg.Error}
		return
	}
	var value interface{} = map[string]interface{}{}
	if len(msg.Result) > 0 && string(msg.Result) != "null" {
		if err := json.Unmarshal(msg.Result, &value); err != nil {
			ch <- pendingResult{err: CommandError{Code: "BROWSER_RUNTIME_PROTOCOL_ERROR", Message: "Browser runtime returned an invalid result"}}
			return
		}
	}
	ch <- pendingResult{value: value}
}

func (b *Broker) disconnect(p *runtimePeer, cause error) {
	b.mu.Lock()
	current := b.peers[p.id] == p
	if current {
		delete(b.peers, p.id)
		if b.local == p {
			b.local = nil
		}
		b.signalConnectionChangedLocked()
	}
	pending := p.pending
	p.pending = make(map[string]pendingRequest)
	b.mu.Unlock()
	_ = p.conn.Close()
	for _, r := range pending {
		r.result <- pendingResult{err: ErrUnavailable}
	}
	if current {
		if cause != nil && !errors.Is(cause, io.EOF) && !errors.Is(cause, net.ErrClosed) {
			b.log.Warn("browser runtime disconnected", "err", cause)
		} else {
			b.log.Info("browser runtime disconnected")
		}
	}
}
func (b *Broker) signalConnectionChangedLocked() {
	close(b.connectionChanged)
	b.connectionChanged = make(chan struct{})
}
