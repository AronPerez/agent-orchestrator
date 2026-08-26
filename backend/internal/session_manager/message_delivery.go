package sessionmanager

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	messageDeliveryReadyPoll     = 150 * time.Millisecond
	messageDeliveryReadySettle   = 750 * time.Millisecond
	messageDeliveryReadyFallback = 5 * time.Second
	messageDeliveryReadyLines    = 80
)

// WaitForMessageDeliveryReady blocks until a session can safely accept an
// injected message. Runtime startup only proves that the process exists; a TUI
// can still be initializing and silently discard an otherwise-successful paste.
func (m *Manager) WaitForMessageDeliveryReady(ctx context.Context, id domain.SessionID) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if rec.IsTerminated {
		return ErrTerminated
	}
	// #114 effect 4: an exited reading may be a nested agent's lie. Verify
	// liveness at most once per call; on a genuine clear fall through and let
	// the readiness loop below re-read the healed row.
	staleExitChecked := false
	if rec.Activity.State == domain.ActivityExited {
		staleExitChecked = true
		if !m.clearedStaleExit(ctx, id) {
			return ErrAgentExited
		}
	}

	mode := domain.NormalizeSessionMode(rec.Mode)
	var detector ports.TerminalActivityDetector
	var handle ports.RuntimeHandle
	requireFirstSignal := false
	if mode == domain.SessionModeTUI {
		if rec.Metadata.RuntimeHandleID == "" {
			return ErrIncompleteHandle
		}
		agent, found := m.agents.Agent(rec.Harness)
		if !found {
			return fmt.Errorf("%w: %s", ErrUnknownHarness, rec.Harness)
		}
		detector, _ = agent.(ports.TerminalActivityDetector)
		signaler, signalsReadiness := agent.(ports.StartupInputReadinessSignaler)
		requireFirstSignal = signalsReadiness && signaler.FirstSignalProvesInputReady()
		handle = runtimeHandle(rec.Metadata)
	}

	startedAt := time.Now()
	idleSince := time.Time{}
	ticker := time.NewTicker(messageDeliveryReadyPoll)
	defer ticker.Stop()

	for {
		rec, ok, err = m.store.GetSession(ctx, id)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		if rec.IsTerminated {
			return ErrTerminated
		}
		if rec.Activity.State == domain.ActivityExited {
			// One verification per call, and never a `continue`: the ticker wait
			// lives at the bottom of this loop, so skipping it would turn a
			// session being re-poisoned into a hot loop of ps shell-outs.
			// Falling through costs one tick of latency and stays bounded.
			if staleExitChecked || !m.clearedStaleExit(ctx, id) {
				return ErrAgentExited
			}
			staleExitChecked = true
		}

		ready := rec.Activity.State == domain.ActivityIdle
		if mode == domain.SessionModeTUI {
			if detector != nil {
				ready = false
				if output, outputErr := m.runtime.GetOutput(ctx, handle, messageDeliveryReadyLines); outputErr == nil {
					state, authoritative := detector.DetectTerminalActivity(output)
					ready = authoritative && state == domain.ActivityIdle
				}
			} else {
				// MarkSpawned records idle before the TUI is necessarily ready.
				// Capability-declaring adapters require their first startup-ready
				// hook. Hookless adapters retain the bounded degraded fallback that
				// existed before the startup input gate.
				if requireFirstSignal {
					ready = ready && !rec.FirstSignalAt.IsZero()
				} else {
					ready = false
				}
				if !requireFirstSignal && rec.Activity.State == domain.ActivityIdle && time.Since(startedAt) >= messageDeliveryReadyFallback {
					m.logger.Warn("message delivery readiness timed out; falling back while session is idle",
						"sessionID", id,
						"timeout", messageDeliveryReadyFallback.String(),
					)
					return nil
				}
			}
		}

		if ready {
			if idleSince.IsZero() {
				idleSince = time.Now()
			} else if time.Since(idleSince) >= messageDeliveryReadySettle {
				return nil
			}
		} else {
			idleSince = time.Time{}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
