package browserruntime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/google/uuid"
)

var errSessionBusy = &CommandError{Code: "BROWSER_SESSION_BUSY", Message: "Browser is controlled by another desktop."}
var errSessionUnclaimed = CommandError{Code: "BROWSER_SESSION_UNCLAIMED", Message: "Open this session on a desktop to choose its browser."}
var errSessionTransferred = CommandError{Code: "BROWSER_COMMAND_CANCELED", Message: "Browser control moved to another desktop"}

type sessionNotice struct {
	peer    *runtimePeer
	message wireMessage
}

func (b *Broker) claimLocked(p *runtimePeer, id domain.SessionID, takeover bool, requestID string) []sessionNotice {
	reply := wireMessage{Type: "session-state", RequestID: requestID, SessionID: id, Revision: b.revision}
	if id == "" {
		reply.Error = &CommandError{Code: "INVALID_ARGUMENT", Message: "Session ID is required"}
		return []sessionNotice{{p, reply}}
	}
	if b.peers[p.id] != p {
		return nil
	}
	p.sessions[id] = struct{}{}
	owner := b.owners[id]
	if owner != "" && owner != p.id && b.peers[owner] != nil && !takeover {
		reply.Error = errSessionBusy
		return []sessionNotice{{p, reply}}
	}
	b.owners[id] = p.id
	b.revision++
	var notices []sessionNotice
	if old := b.peers[owner]; old != nil && old != p {
		for requestID, r := range old.pending {
			if r.sessionID == id {
				delete(old.pending, requestID)
				r.result <- pendingResult{err: errSessionTransferred}
				notices = append(notices, sessionNotice{old, wireMessage{Type: "cancel", RequestID: requestID}})
			}
		}
	}
	for _, peer := range b.peers {
		if _, ok := peer.sessions[id]; !ok || peer.version < 4 {
			continue
		}
		state := wireMessage{Type: "session-state", SessionID: id, Owned: peer == p, Revision: b.revision}
		if peer == p {
			state.RequestID = requestID
		} else {
			state.Error = errSessionBusy
		}
		notices = append(notices, sessionNotice{peer, state})
	}
	b.signalConnectionChangedLocked()
	return notices
}

func (b *Broker) sendNotices(ctx context.Context, notices []sessionNotice) {
	for _, n := range notices {
		writeCtx, cancel := context.WithTimeout(ctx, time.Second)
		err := n.peer.write(writeCtx, n.message)
		cancel()
		if err != nil {
			b.disconnect(n.peer, err)
		}
	}
}

func (b *Broker) selectPeerLocked(id domain.SessionID, action string) (*runtimePeer, error) {
	if action == "__render-check" || action == "__render-measure" {
		return b.local, nil
	}
	if owner, ok := b.owners[id]; ok {
		return b.peers[owner], nil
	}
	if len(b.peers) > 1 {
		return nil, errSessionUnclaimed
	}
	for _, peer := range b.peers {
		return peer, nil
	}
	return nil, nil
}
func (b *Broker) peerStatusLocked(p *runtimePeer) Status {
	status := Status{Transport: b.transport}
	if p != nil {
		status.Connected = true
		status.ConnectedAt = p.connectedAt
		status.Client = p.client
		status.ClientVersion = p.clientVersion
	}
	return status
}

// Status describes transport availability; command callers use StatusForSession.
func (b *Broker) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.local
	if p == nil {
		for _, peer := range b.peers {
			p = peer
			break
		}
	}
	return b.peerStatusLocked(p)
}

// StatusForSession uses the same owner selection as Execute, without claiming it.
func (b *Broker) StatusForSession(id domain.SessionID) Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, _ := b.selectPeerLocked(id, "")
	return b.peerStatusLocked(p)
}
func (b *Broker) waitForConnection(ctx context.Context, id domain.SessionID, action string) (*runtimePeer, error) {
	waitCtx, cancel := context.WithTimeout(ctx, runtimeReconnectGrace)
	defer cancel()
	for {
		b.mu.Lock()
		p, err := b.selectPeerLocked(id, action)
		changed := b.connectionChanged
		b.mu.Unlock()
		if err != nil || p != nil {
			return p, err
		}
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("%w: %w", ErrReconnecting, ErrUnavailable)
		case <-changed:
		}
	}
}

// Execute sends to one selected peer; registering under its write gate makes a
// handoff's cancel follow the command even if ownership changes during a write.
func (b *Broker) Execute(ctx context.Context, id domain.SessionID, action string, args map[string]interface{}) (Result, error) {
	for {
		p, err := b.waitForConnection(ctx, id, action)
		if err != nil {
			return Result{}, err
		}
		result, err := b.executeOnPeer(ctx, p, id, action, args, true)
		if errors.Is(err, errPeerChanged) {
			continue
		}
		return result, err
	}
}

var errPeerChanged = errors.New("browser owner changed before dispatch")

func (b *Broker) executeOnPeer(ctx context.Context, p *runtimePeer, id domain.SessionID, action string, args map[string]interface{}, checkOwner bool) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-p.writeGate:
	}
	requestID := uuid.NewString()
	ch := make(chan pendingResult, 1)
	b.mu.Lock()
	selected, selectErr := b.selectPeerLocked(id, action)
	if b.peers[p.id] != p || (checkOwner && (selectErr != nil || selected != p)) {
		b.mu.Unlock()
		p.writeGate <- struct{}{}
		return Result{}, errPeerChanged
	}
	if checkOwner && action != "__render-check" && action != "__render-measure" {
		b.owners[id] = p.id
		p.sessions[id] = struct{}{}
	}
	p.pending[requestID] = pendingRequest{sessionID: id, result: ch}
	b.mu.Unlock()
	err := p.writeFrame(ctx, wireMessage{Type: "command", RequestID: requestID, SessionID: id, Action: action, Args: args})
	p.writeGate <- struct{}{}
	if err != nil {
		b.removePending(p, requestID)
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		b.disconnect(p, fmt.Errorf("write browser command: %w", err))
		return Result{}, ErrUnavailable
	}
	select {
	case <-ctx.Done():
		b.removePending(p, requestID)
		cancelCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = p.write(cancelCtx, wireMessage{Type: "cancel", RequestID: requestID})
		cancel()
		return Result{}, ctx.Err()
	case result := <-ch:
		if result.err != nil {
			return Result{}, result.err
		}
		return Result{RequestID: requestID, Value: result.value}, nil
	}
}
func (b *Broker) removePending(p *runtimePeer, id string) {
	b.mu.Lock()
	delete(p.pending, id)
	b.mu.Unlock()
}

// DestroySession cleans every desktop that has used the session, not just its
// current owner. Forgetting ownership must never move another session's work.
func (b *Broker) DestroySession(ctx context.Context, id domain.SessionID) error {
	b.mu.Lock()
	var peers []*runtimePeer
	for _, p := range b.peers {
		if _, ok := p.sessions[id]; ok {
			peers = append(peers, p)
		}
	}
	b.mu.Unlock()
	var errs []error
	for _, p := range peers {
		if _, err := b.executeOnPeer(ctx, p, id, "__destroy-session", nil, false); err != nil && !errors.Is(err, ErrUnavailable) && !errors.Is(err, errPeerChanged) {
			errs = append(errs, err)
		}
	}
	b.mu.Lock()
	delete(b.owners, id)
	for _, p := range b.peers {
		delete(p.sessions, id)
	}
	b.signalConnectionChangedLocked()
	b.mu.Unlock()
	return errors.Join(errs...)
}
