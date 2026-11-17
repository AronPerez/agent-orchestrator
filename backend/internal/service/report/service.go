package report

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
	"github.com/aoagents/agent-orchestrator/backend/internal/verification"
)

// Store is the report service's persistence and ownership boundary.
type Store interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	GetProject(context.Context, string) (domain.ProjectRecord, bool, error)
	CreateReportWithContractOutcome(context.Context, domain.ReportRecord, domain.SessionContractOutcome) (domain.ReportRecord, error)
	CreateReportWithContractOutcomeOnce(context.Context, domain.ReportRecord, domain.SessionContractOutcome) (domain.ReportRecord, bool, error)
}

// Verifier runs project-owned commands through the daemon-wide per-project
// gate. The concrete verification.Runner is shared with future verification
// callers rather than recreated per report.
type Verifier interface {
	Run(context.Context, verification.Request) (verification.Result, error)
}

// EvidenceArtifacts registers generated verification evidence with the
// session artifact index. Unregister is the compensating operation when the
// report transaction fails after registration.
type EvidenceArtifacts interface {
	Register(context.Context, domain.SessionID, string) error
	Unregister(context.Context, domain.SessionID, string) error
}

// CreateInput is the validated caller payload before AO-derived ownership and deadlines.
type CreateInput struct {
	SessionID domain.SessionID
	State     domain.ReportState
	Note      string
	Message   string
	Outputs   []domain.ReportOutput
}

// ArtifactRecorder shows a reported artifact in the worker's chat thread. It
// is best effort: it skips what it cannot show and never fails the report.
type ArtifactRecorder interface {
	RecordReportedArtifact(ctx context.Context, id domain.SessionID, reference string)
}

// Service validates and creates durable worker reports.
type Service struct {
	store             Store
	now               func() time.Time
	newID             func() string
	onCreated         func(domain.ReportRecord)
	artifacts         ArtifactRecorder
	verifier          Verifier
	evidenceArtifacts EvidenceArtifacts
	dataDir           string
}

// Deps configures a Service. Artifacts is optional.
type Deps struct {
	Store             Store
	Now               func() time.Time
	NewID             func() string
	OnCreated         func(domain.ReportRecord)
	Artifacts         ArtifactRecorder
	Verifier          Verifier
	EvidenceArtifacts EvidenceArtifacts
	DataDir           string
}

// New constructs a report Service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.NewID == nil {
		d.NewID = func() string { return "rpt_" + uuid.NewString() }
	}
	return &Service{
		store: d.Store, now: d.Now, newID: d.NewID, onCreated: d.OnCreated, artifacts: d.Artifacts,
		verifier: d.Verifier, evidenceArtifacts: d.EvidenceArtifacts, dataDir: d.DataDir,
	}
}

// CreateImmediateOnce persists an internal coordination report with an
// immediate delivery deadline and a deterministic ID derived from key. Replays
// return the existing report and do not publish a second creation event.
func (s *Service) CreateImmediateOnce(ctx context.Context, input CreateInput, key string) (domain.ReportRecord, error) {
	if s == nil || s.store == nil {
		return domain.ReportRecord{}, errors.New("report: store is required")
	}
	if key == "" {
		return domain.ReportRecord{}, errors.New("report: idempotency key is required")
	}
	if input.SessionID == "" {
		return domain.ReportRecord{}, apierr.Invalid("INVALID_REPORT_SESSION", "Worker session id is required", nil)
	}
	if err := domain.ValidateReportContent(input.State, input.Note, input.Message, input.Outputs); err != nil {
		return domain.ReportRecord{}, apierr.Invalid("INVALID_REPORT", "Report state, note, message, or outputs are invalid", nil)
	}
	session, ok, err := s.store.GetSession(ctx, input.SessionID)
	if err != nil {
		return domain.ReportRecord{}, err
	}
	if !ok {
		return domain.ReportRecord{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if session.Kind != domain.KindWorker {
		return domain.ReportRecord{}, apierr.Invalid("REPORT_WORKER_REQUIRED", "Reports can only be created by worker sessions", nil)
	}
	now := s.now().UTC()
	sum := sha256.Sum256([]byte(key))
	rec := domain.ReportRecord{
		ID: fmt.Sprintf("rpt_contract_%x", sum[:16]), SessionID: session.ID, ProjectID: session.ProjectID,
		State: input.State, Note: input.Note, Message: input.Message, Outputs: input.Outputs,
		CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1,
	}
	created, inserted, err := s.store.CreateReportWithContractOutcomeOnce(ctx, rec, domain.SessionContractOutcomeNone)
	if err != nil {
		return created, err
	}
	if inserted && s.onCreated != nil {
		s.onCreated(created)
	}
	if inserted {
		s.recordArtifacts(ctx, created)
	}
	return created, nil
}

// ListProject returns persisted report facts ordered by created_at then id,
// with each report's outputs in persisted position order. It never claims,
// acknowledges, schedules, or otherwise mutates report delivery state.
func (s *Service) ListProject(ctx context.Context, projectID domain.ProjectID) ([]domain.ReportRecord, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("report: store is required")
	}
	if projectID == "" {
		return nil, apierr.Invalid("REPORT_PROJECT_REQUIRED", "Project id is required", nil)
	}
	reader, ok := s.store.(interface {
		ListReportsByProject(context.Context, domain.ProjectID) ([]domain.ReportRecord, error)
	})
	if !ok {
		return nil, errors.New("report: project reader is required")
	}
	reports, err := reader.ListReportsByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(reports, func(i, j int) bool {
		if reports[i].CreatedAt.Equal(reports[j].CreatedAt) {
			return reports[i].ID < reports[j].ID
		}
		return reports[i].CreatedAt.Before(reports[j].CreatedAt)
	})
	return reports, nil
}

// Create validates ownership and persists one pending report. Reports are
// coordination claims and never mutate authoritative worker lifecycle state.
func (s *Service) Create(ctx context.Context, input CreateInput) (domain.ReportRecord, error) {
	if s == nil || s.store == nil {
		return domain.ReportRecord{}, errors.New("report: store is required")
	}
	if input.SessionID == "" {
		return domain.ReportRecord{}, apierr.Invalid("INVALID_REPORT_SESSION", "Worker session id is required", nil)
	}
	if err := domain.ValidateReportContent(input.State, input.Note, input.Message, input.Outputs); err != nil {
		return domain.ReportRecord{}, apierr.Invalid("INVALID_REPORT", "Report state, note, message, or outputs are invalid", nil)
	}
	session, ok, err := s.store.GetSession(ctx, input.SessionID)
	if err != nil {
		return domain.ReportRecord{}, err
	}
	if !ok {
		return domain.ReportRecord{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if session.Kind != domain.KindWorker {
		return domain.ReportRecord{}, apierr.Invalid("REPORT_WORKER_REQUIRED", "Reports can only be created by worker sessions", nil)
	}

	reportID := s.newID()
	verificationFact, cleanupEvidence, err := s.verifyDone(ctx, reportID, session, input.State)
	if err != nil {
		return domain.ReportRecord{}, err
	}
	now := s.now().UTC()
	var availableAt time.Time
	var settlementDeadline time.Time
	switch input.State {
	case domain.ReportDone:
		settlementDeadline = now.Add(domain.ReportSettlementWindow)
		availableAt = settlementDeadline
	case domain.ReportNeedsInput, domain.ReportStuck:
		availableAt = now
	default:
		availableAt = now.Add(domain.ReportBatchFallback)
	}
	rec := domain.ReportRecord{
		ID: reportID, SessionID: session.ID, ProjectID: session.ProjectID,
		State: input.State, Note: input.Note, Message: input.Message, Outputs: input.Outputs,
		CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: availableAt,
		SettlementDeadline: settlementDeadline, RepeatCount: 1, Verification: verificationFact,
	}
	created, err := s.store.CreateReportWithContractOutcome(ctx, rec, contractOutcomeForReport(session, input.State, verificationFact))
	if err != nil {
		if cleanupEvidence != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			err = errors.Join(err, cleanupEvidence(cleanupCtx))
			cancel()
		}
		return created, err
	}
	if created.Verification != nil && verificationFact != nil {
		created.Verification.OutputTail = verificationFact.OutputTail
	}
	if s.onCreated != nil {
		s.onCreated(created)
	}
	// Synchronous on purpose: ao report --done is often the agent's last tool
	// call, so a goroutine would race the turn's completion and could drop the
	// artifact or put it on the next turn.
	s.recordArtifacts(ctx, created)
	return created, nil
}

func (s *Service) verifyDone(ctx context.Context, reportID string, session domain.SessionRecord, state domain.ReportState) (*domain.ReportVerification, func(context.Context) error, error) {
	if state != domain.ReportDone || s.verifier == nil {
		return nil, nil, nil
	}
	project, ok, err := s.store.GetProject(ctx, string(session.ProjectID))
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("report: project %s not found", session.ProjectID)
	}
	if !hasVerificationCommands(project.Config.Verify) {
		return nil, nil, nil
	}
	if s.evidenceArtifacts == nil {
		return nil, nil, errors.New("report: verification artifact registrar is required")
	}

	startedAt := s.now().UTC()
	result, runErr := s.verifier.Run(ctx, verification.Request{
		ProjectID: session.ProjectID, WorkspacePath: session.Metadata.WorkspacePath,
		Commands: project.Config.Verify, Env: project.Config.Env,
	})
	if ctx.Err() != nil {
		return nil, nil, errors.Join(runErr, ctx.Err())
	}
	if result.StartedAt.IsZero() {
		result.StartedAt = startedAt
	}
	if result.CompletedAt.IsZero() {
		result.CompletedAt = s.now().UTC()
	}
	verificationState := domain.ReportVerificationFailed
	if result.Passed && runErr == nil {
		verificationState = domain.ReportVerificationPassed
	}
	outputTail := ""
	if verificationState == domain.ReportVerificationFailed {
		outputTail = result.OutputTail
		if outputTail == "" && runErr != nil {
			outputTail = runErr.Error()
		}
	}
	reference, path, err := s.writeVerificationEvidence(session, reportID, verificationState, result, runErr)
	if err != nil {
		return nil, nil, err
	}
	if err := s.evidenceArtifacts.Register(ctx, session.ID, reference); err != nil {
		return nil, nil, errors.Join(err, removeEvidence(path))
	}
	cleanup := func(cleanupCtx context.Context) error {
		return errors.Join(s.evidenceArtifacts.Unregister(cleanupCtx, session.ID, reference), removeEvidence(path))
	}
	return &domain.ReportVerification{
		State: verificationState, Evidence: reference, StartedAt: result.StartedAt,
		CompletedAt: result.CompletedAt, OutputTail: outputTail,
	}, cleanup, nil
}

func hasVerificationCommands(commands []string) bool {
	for _, step := range commands {
		_, command, _ := domain.ParseTimedScriptCommand(step)
		if strings.TrimSpace(command) != "" {
			return true
		}
	}
	return false
}

func (s *Service) writeVerificationEvidence(session domain.SessionRecord, reportID string, state domain.ReportVerificationState, result verification.Result, runErr error) (string, string, error) {
	if reportID == "" || filepath.Base(reportID) != reportID || strings.Contains(reportID, `\`) {
		return "", "", errors.New("report: invalid verification report id")
	}
	artifactDir := strings.TrimSpace(session.Metadata.ArtifactDir)
	if artifactDir == "" {
		artifactDir = sessionartifacts.Dir(s.dataDir, session.ID)
	}
	if artifactDir == "" {
		return "", "", errors.New("report: verification artifact directory is required")
	}
	reference := filepath.ToSlash(filepath.Join("verification", reportID+".txt"))
	path := filepath.Join(artifactDir, filepath.FromSlash(reference))
	var evidence strings.Builder
	fmt.Fprintf(&evidence, "Verification: %s\nStarted: %s\nCompleted: %s\n", state, result.StartedAt.Format(time.RFC3339Nano), result.CompletedAt.Format(time.RFC3339Nano))
	if result.FailedStep > 0 {
		fmt.Fprintf(&evidence, "Failed step: %d\nTimed out: %t\n", result.FailedStep, result.TimedOut)
	}
	if runErr != nil {
		fmt.Fprintf(&evidence, "Error: %v\n", runErr)
	}
	if result.OutputTail != "" {
		evidence.WriteString("\nOutput tail:\n")
		evidence.WriteString(result.OutputTail)
		if !strings.HasSuffix(result.OutputTail, "\n") {
			evidence.WriteByte('\n')
		}
	}
	if err := writeEvidenceFile(path, evidence.String()); err != nil {
		return "", "", err
	}
	return reference, path, nil
}

func writeEvidenceFile(path, contents string) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create verification evidence directory: %w", err)
	}
	file, err := os.CreateTemp(dir, ".verification-*.tmp")
	if err != nil {
		return fmt.Errorf("create verification evidence: %w", err)
	}
	temporary := file.Name()
	defer func() {
		_ = file.Close()
		_ = os.Remove(temporary)
	}()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("protect verification evidence: %w", err)
	}
	if _, err := file.WriteString(contents); err != nil {
		return fmt.Errorf("write verification evidence: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close verification evidence: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("publish verification evidence: %w", err)
	}
	return nil
}

func removeEvidence(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Service) recordArtifacts(ctx context.Context, rec domain.ReportRecord) {
	if s.artifacts == nil {
		return
	}
	for _, output := range rec.Outputs {
		if output.Kind == domain.ReportOutputArtifact {
			s.artifacts.RecordReportedArtifact(ctx, rec.SessionID, output.Reference)
		}
	}
}

func contractOutcomeForReport(session domain.SessionRecord, state domain.ReportState, verificationFact *domain.ReportVerification) domain.SessionContractOutcome {
	if session.Goal == "" || session.TimeboxDeadlineAt == nil || session.ContractOutcome != domain.SessionContractOutcomeNone {
		return domain.SessionContractOutcomeNone
	}
	switch state {
	case domain.ReportDone:
		if verificationFact != nil && verificationFact.State != domain.ReportVerificationPassed {
			return domain.SessionContractOutcomeNone
		}
		return domain.SessionContractOutcomeMet
	case domain.ReportNeedsInput, domain.ReportStuck:
		return domain.SessionContractOutcomeBlocked
	default:
		return domain.SessionContractOutcomeNone
	}
}
