package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSessionContractRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "contract")
	rec := sampleRecord("contract")
	deadline := time.Date(2026, time.November, 17, 18, 30, 0, 0, time.UTC)
	rec.Goal = "Ship the verified contract foundation"
	rec.TimeboxDeadlineAt = &deadline
	rec.ContractOutcome = domain.SessionContractOutcomeMet

	created, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	got, found, err := s.GetSession(ctx, created.ID)
	if err != nil || !found {
		t.Fatalf("get session: found=%v err=%v", found, err)
	}
	if got.Goal != rec.Goal || got.TimeboxDeadlineAt == nil || !got.TimeboxDeadlineAt.Equal(deadline) || got.ContractOutcome != domain.SessionContractOutcomeMet {
		t.Fatalf("contract = goal %q deadline %v outcome %q", got.Goal, got.TimeboxDeadlineAt, got.ContractOutcome)
	}
}
