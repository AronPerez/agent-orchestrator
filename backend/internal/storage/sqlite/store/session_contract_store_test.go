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

func TestSessionContractSupervisionFactsAreFenced(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "contract-supervision")
	rec := sampleRecord("contract-supervision")
	deadline := time.Date(2026, time.November, 17, 18, 30, 0, 0, time.UTC)
	rec.Goal = "Ship the supervised contract"
	rec.TimeboxDeadlineAt = &deadline
	created, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}

	firstNudge := deadline.Add(-30 * time.Minute)
	if ok, err := s.RecordSessionContractStallNudge(ctx, created.ID, firstNudge); err != nil || !ok {
		t.Fatalf("first nudge ok=%v err=%v", ok, err)
	}
	if ok, err := s.RecordSessionContractStallEscalation(ctx, created.ID, firstNudge.Add(-time.Second), firstNudge.Add(time.Minute)); err != nil || ok {
		t.Fatalf("stale escalation ok=%v err=%v", ok, err)
	}
	escalatedAt := firstNudge.Add(15 * time.Minute)
	if ok, err := s.RecordSessionContractStallEscalation(ctx, created.ID, firstNudge, escalatedAt); err != nil || !ok {
		t.Fatalf("escalation ok=%v err=%v", ok, err)
	}

	secondNudge := firstNudge.Add(20 * time.Minute)
	if ok, err := s.RecordSessionContractStallNudge(ctx, created.ID, secondNudge); err != nil || !ok {
		t.Fatalf("second nudge ok=%v err=%v", ok, err)
	}
	got, ok, err := s.GetSession(ctx, created.ID)
	if err != nil || !ok || got.ContractStallNudgedAt == nil || !got.ContractStallNudgedAt.Equal(secondNudge) || got.ContractStallEscalatedAt != nil {
		t.Fatalf("session after second nudge = %+v ok=%v err=%v", got, ok, err)
	}

	if ok, err := s.ExpireSessionContract(ctx, created.ID, deadline.Add(time.Second), deadline); err != nil || ok {
		t.Fatalf("wrong deadline expiry ok=%v err=%v", ok, err)
	}
	if ok, err := s.ExpireSessionContract(ctx, created.ID, deadline, deadline.Add(-time.Second)); err != nil || ok {
		t.Fatalf("early expiry ok=%v err=%v", ok, err)
	}
	if ok, err := s.ExpireSessionContract(ctx, created.ID, deadline, deadline); err != nil || !ok {
		t.Fatalf("due expiry ok=%v err=%v", ok, err)
	}
	if ok, err := s.ExpireSessionContract(ctx, created.ID, deadline, deadline.Add(time.Minute)); err != nil || ok {
		t.Fatalf("repeat expiry ok=%v err=%v", ok, err)
	}
	got, ok, err = s.GetSession(ctx, created.ID)
	if err != nil || !ok || got.ContractOutcome != domain.SessionContractOutcomeLimitHit || got.ContractTimeboxPromptedAt != nil {
		t.Fatalf("expired session = %+v ok=%v err=%v", got, ok, err)
	}
	if ok, err := s.RecordSessionContractTimeboxPrompt(ctx, created.ID, deadline.Add(time.Second), deadline); err != nil || ok {
		t.Fatalf("wrong deadline prompt ok=%v err=%v", ok, err)
	}
	if ok, err := s.RecordSessionContractTimeboxPrompt(ctx, created.ID, deadline, deadline); err != nil || !ok {
		t.Fatalf("first prompt ok=%v err=%v", ok, err)
	}
	if ok, err := s.RecordSessionContractTimeboxPrompt(ctx, created.ID, deadline, deadline.Add(time.Minute)); err != nil || ok {
		t.Fatalf("repeat prompt ok=%v err=%v", ok, err)
	}
	got, ok, err = s.GetSession(ctx, created.ID)
	if err != nil || !ok || got.ContractTimeboxPromptedAt == nil || !got.ContractTimeboxPromptedAt.Equal(deadline) {
		t.Fatalf("prompted session = %+v ok=%v err=%v", got, ok, err)
	}
	if ok, err := s.RecordSessionContractStallNudge(ctx, created.ID, deadline.Add(time.Minute)); err != nil || ok {
		t.Fatalf("post-outcome nudge ok=%v err=%v", ok, err)
	}
}
