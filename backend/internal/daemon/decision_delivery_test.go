package daemon

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	decisionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/decision"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

type fakeDecisionSessionSender struct {
	id         domain.SessionID
	message    string
	attachment *ports.SpawnAttachment
	options    ports.MessageDeliveryOptions
	err        error
}

func (s *fakeDecisionSessionSender) SendWithOptions(_ context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment, options ports.MessageDeliveryOptions) error {
	s.id, s.message, s.attachment, s.options = id, message, attachment, options
	return s.err
}

func TestDecisionMessengerSendsAsThePersonAndNamesEndedSessions(t *testing.T) {
	at := time.Date(2026, 11, 17, 12, 30, 0, 0, time.UTC)
	sender := &fakeDecisionSessionSender{}
	messenger := decisionMessenger{sessions: sender}
	if err := messenger.DeliverAsPerson(context.Background(), "w-1", "hello", at); err != nil {
		t.Fatal(err)
	}
	if sender.id != "w-1" || sender.message != "hello" || sender.attachment != nil || !sender.options.AuthoredByUser || !sender.options.InteractionAt.Equal(at) {
		t.Fatalf("send = %+v", sender)
	}

	for _, ended := range []error{sessionmanager.ErrNotFound, sessionmanager.ErrTerminated, sessionmanager.ErrAgentExited} {
		sender.err = fmt.Errorf("send failed: %w", ended)
		err := messenger.DeliverAsPerson(context.Background(), "w-1", "hello", at)
		if !errors.Is(err, decisionsvc.ErrSessionEnded) || !errors.Is(err, ended) {
			t.Errorf("DeliverAsPerson(%v) = %v, want ErrSessionEnded wrapping source", ended, err)
		}
	}

	busy := errors.New("composer busy")
	sender.err = busy
	if err := messenger.DeliverAsPerson(context.Background(), "w-1", "hello", at); !errors.Is(err, busy) || errors.Is(err, decisionsvc.ErrSessionEnded) {
		t.Fatalf("DeliverAsPerson(other) = %v", err)
	}

	sender.err = fmt.Errorf("send failed: %w", sessionmanager.ErrResumeInProgress)
	if err := messenger.DeliverAsPerson(context.Background(), "w-1", "hello", at); !errors.Is(err, sessionmanager.ErrResumeInProgress) || errors.Is(err, decisionsvc.ErrSessionEnded) {
		t.Fatalf("DeliverAsPerson(resuming) = %v, want transient ErrResumeInProgress", err)
	}
}
