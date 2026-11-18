package lifecycle

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
)

type sessionArtifactRowReader interface {
	ListSessionArtifacts(context.Context, domain.SessionID) ([]domain.SessionArtifactRecord, error)
}

type sessionDoneReportReader interface {
	ListLatestDoneReportsForSessions(context.Context, []domain.SessionID) (map[domain.SessionID]domain.SessionDoneReport, error)
}

// ReconcileSessionOutputType recomputes a session's durable OutputType from
// its live PR list and artifact directory contents, and persists it when it
// has changed. This is the sole writer of the session_output_type column;
// every reader (the API, Kanban derivation) trusts that column instead of
// rescanning on every read.
//
// A session row created before artifact_dir existed carries it as ” (the
// migration's default), even though session_manager always tells the agent
// to write into the deterministic dataDir/artifacts/<id> path regardless of
// what is stored. Backfilling that path here — the moment any reconcile call
// touches the row — means every session, not just ones that happen to
// restore, gets a correct, persisted ArtifactDir on its next poll tick.
//
// The write goes through UpdateSessionArtifactOutput, not UpdateSession: this
// method reads the session once, and callers now include the Get/List API
// read path, so an ordinary UI read can run concurrently with termination or
// another lifecycle write. A read-modify-write UpdateSession here would
// persist the stale is_terminated/activity/runtime-identity/preview-state
// captured at read time, potentially resurrecting a session that terminated
// in between. UpdateSessionArtifactOutput only ever names artifact_dir and
// session_output_type, so it cannot touch those other columns regardless of
// how stale the read was.
//
// Reconciles are serialized on reconcileMu, a lock separate from the reducer's
// mu: the scan-then-write below is not atomic, so two overlapping reconciles
// (the poller and a PR claim, say) could otherwise persist an older scan over
// a newer one and lose a combined pr_artifact. The filesystem walk therefore
// never stalls lifecycle mutations. A concurrent mutate cannot revert this
// write, because UpdateSession's SET list does not name artifact_dir or
// session_output_type, so its full-row write leaves them as written here.
func (m *Manager) ReconcileSessionOutputType(ctx context.Context, id domain.SessionID) error {
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return err
	}
	artifactDir := rec.Metadata.ArtifactDir
	backfilled := false
	if artifactDir == "" {
		if dir := sessionartifacts.Dir(m.dataDir, id); dir != "" {
			artifactDir = dir
			backfilled = true
		}
	}
	prs, err := m.store.ListPRsBySession(ctx, id)
	if err != nil {
		return err
	}
	var artifacts []domain.SessionArtifactFile
	if rows, ok := m.store.(sessionArtifactRowReader); ok {
		stored, err := rows.ListSessionArtifacts(ctx, id)
		if err != nil {
			return err
		}
		observation, err := sessionartifacts.Observe(ctx, artifactDir, sessionartifacts.RegisteredPaths(stored))
		if err != nil {
			return err
		}
		artifacts = observation.Apply(stored)
	} else {
		artifacts, err = sessionartifacts.List(ctx, artifactDir)
		if err != nil {
			return err
		}
	}
	includePlanApproval := false
	if rec.RequestedOutput == domain.RequestedOutputArtifact {
		if reports, ok := m.store.(sessionDoneReportReader); ok {
			latest, err := reports.ListLatestDoneReportsForSessions(ctx, []domain.SessionID{id})
			if err != nil {
				return err
			}
			if report, found := latest[id]; found {
				includePlanApproval = !report.Superseded
			}
		}
	}
	next := sessionartifacts.DeriveOutputType(len(prs), sessionartifacts.DeliverableCount(artifacts, rec.PlanFirstState == domain.PlanFirstPending, includePlanApproval))
	if next == rec.OutputType && !backfilled {
		return nil
	}
	_, err = m.store.UpdateSessionArtifactOutput(ctx, id, artifactDir, next)
	return err
}
