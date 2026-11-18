package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
	"github.com/aoagents/agent-orchestrator/backend/internal/workspacewatch"
)

func TestProjectOverlapsReturnsSortedActivePairsAndHonorsIgnores(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	terminated := newWorkspaceRepo(t)
	preparation := newWorkspaceRepo(t)

	for _, root := range []string{first, second, terminated, preparation} {
		writeWorkspaceFile(t, root, "README.md", "changed\n")
	}
	writeWorkspaceFile(t, first, "z-only.txt", "first\n")
	writeWorkspaceFile(t, second, "a-only.txt", "second\n")
	writeWorkspaceFile(t, first, "package-lock.json", "first lock\n")
	writeWorkspaceFile(t, second, "package-lock.json", "second lock\n")
	writeWorkspaceFile(t, first, "frontend/src/api/schema.ts", "first generated\n")
	writeWorkspaceFile(t, second, "frontend/src/api/schema.ts", "second generated\n")

	store := newFakeStore()
	store.projects["demo"] = domain.ProjectRecord{
		ID: "demo",
		Config: domain.ProjectConfig{CollisionRadar: domain.CollisionRadarConfig{
			IgnorePaths: []string{"frontend/src/api/schema.ts"},
		}},
	}
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	terminatedSession := overlapSession("demo-ended", terminated)
	terminatedSession.IsTerminated = true
	store.sessions[terminatedSession.ID] = terminatedSession
	preparationSession := overlapSession("demo-preparation", preparation)
	preparationSession.IsTaskPreparation = true
	store.sessions[preparationSession.ID] = preparationSession

	got, err := NewWithDeps(Deps{Store: store}).ProjectOverlaps(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ProjectOverlaps: %v", err)
	}
	if got.ProjectID != "demo" {
		t.Fatalf("project id = %q, want demo", got.ProjectID)
	}
	if len(got.Overlaps) != 1 {
		t.Fatalf("overlaps = %+v, want one active pair", got.Overlaps)
	}
	overlap := got.Overlaps[0]
	if overlap.SessionA != "demo-a" || overlap.SessionB != "demo-b" {
		t.Fatalf("pair = %s/%s, want demo-a/demo-b", overlap.SessionA, overlap.SessionB)
	}
	if !slices.Equal(overlap.Paths, []string{"README.md"}) {
		t.Fatalf("paths = %v, want only README.md", overlap.Paths)
	}
	if got.TruncatedSessionIDs == nil || len(got.TruncatedSessionIDs) != 0 {
		t.Fatalf("truncated sessions = %#v, want a non-nil empty list", got.TruncatedSessionIDs)
	}
}

func TestProjectOverlapsCountsRenameSourceAsChanged(t *testing.T) {
	manifests := map[domain.SessionID]WorkspaceManifest{
		"demo-a": {SessionID: "demo-a", Files: []WorkspaceFileSummary{{Path: "new-name.go", PreviousPath: "old-name.go", Status: WorkspaceFileRenamed}}},
		"demo-b": {SessionID: "demo-b", Files: []WorkspaceFileSummary{{Path: "old-name.go", Status: WorkspaceFileDeleted}}},
	}

	got := projectOverlapsFromManifests("demo", manifests, nil)
	if len(got.Overlaps) != 1 || !slices.Equal(got.Overlaps[0].Paths, []string{"old-name.go"}) {
		t.Fatalf("overlaps = %+v, want old-name.go rename collision", got.Overlaps)
	}
}

func TestProjectOverlapsRefreshesCachedManifestsBeforeReading(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	writeWorkspaceFile(t, first, "shared.go", "first\n")

	store := newFakeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	svc := NewWithDeps(Deps{Store: store})

	initial, err := svc.ProjectOverlaps(context.Background(), "demo")
	if err != nil {
		t.Fatalf("initial ProjectOverlaps: %v", err)
	}
	if len(initial.Overlaps) != 0 {
		t.Fatalf("initial overlaps = %+v, want none", initial.Overlaps)
	}

	// The manifest cache is still inside its TTL. The explicit overlap read must
	// nevertheless observe this edit even when no workspace watcher is open.
	writeWorkspaceFile(t, second, "shared.go", "second\n")
	got, err := svc.ProjectOverlaps(context.Background(), "demo")
	if err != nil {
		t.Fatalf("refreshed ProjectOverlaps: %v", err)
	}
	if len(got.Overlaps) != 1 || !slices.Equal(got.Overlaps[0].Paths, []string{"shared.go"}) {
		t.Fatalf("overlaps = %+v, want shared.go", got.Overlaps)
	}
}

func TestProjectOverlapsWaitsForWatcherInvalidatedManifest(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	writeWorkspaceFile(t, first, "shared.go", "first\n")

	store := newFakeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	svc := NewWithDeps(Deps{Store: store})
	if _, err := svc.ProjectOverlaps(context.Background(), "demo"); err != nil {
		t.Fatalf("initial ProjectOverlaps: %v", err)
	}

	var pending func()
	svc.runBackground = func(work func()) { pending = work }
	writeWorkspaceFile(t, second, "shared.go", "second\n")
	svc.InvalidateWorkspaceCache("demo-b")
	if pending == nil {
		t.Fatal("watcher invalidation did not schedule a refresh")
	}

	got, err := svc.ProjectOverlaps(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ProjectOverlaps: %v", err)
	}
	if len(got.Overlaps) != 1 || !slices.Equal(got.Overlaps[0].Paths, []string{"shared.go"}) {
		t.Fatalf("overlaps = %+v, want fresh shared.go overlap", got.Overlaps)
	}
}

func TestProjectOverlapsSkipsScratchProjects(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	writeWorkspaceFile(t, first, "README.md", "first\n")
	writeWorkspaceFile(t, second, "README.md", "second\n")

	store := newFakeStore()
	store.projects["scratch"] = domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch}
	store.sessions["scratch-a"] = domain.SessionRecord{ID: "scratch-a", ProjectID: "scratch", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: first}}
	store.sessions["scratch-b"] = domain.SessionRecord{ID: "scratch-b", ProjectID: "scratch", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: second}}

	got, err := NewWithDeps(Deps{Store: store}).ProjectOverlaps(context.Background(), "scratch")
	if err != nil {
		t.Fatalf("ProjectOverlaps: %v", err)
	}
	if len(got.Overlaps) != 0 {
		t.Fatalf("scratch overlaps = %+v, want none", got.Overlaps)
	}
}

func TestProjectOverlapsSkipsUnadoptedImports(t *testing.T) {
	active := newWorkspaceRepo(t)
	pendingImport := newWorkspaceRepo(t)
	writeWorkspaceFile(t, active, "shared.go", "active\n")
	writeWorkspaceFile(t, pendingImport, "shared.go", "pending import\n")

	store := newFakeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-active"] = overlapSession("demo-active", active)
	pending := overlapSession("demo-pending", pendingImport)
	pending.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "copied-history"}
	store.sessions[pending.ID] = pending

	svc := NewWithDeps(Deps{Store: store})
	overlaps, err := svc.ProjectOverlaps(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ProjectOverlaps: %v", err)
	}
	if len(overlaps.Overlaps) != 0 {
		t.Fatalf("overlaps = %+v, want pending import excluded", overlaps.Overlaps)
	}
	watchPaths, err := svc.ProjectOverlapWatchPaths(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ProjectOverlapWatchPaths: %v", err)
	}
	if !slices.Equal(watchPaths, []string{active}) {
		t.Fatalf("watch paths = %v, want only adopted session %q", watchPaths, active)
	}
}

func TestProjectOverlapsReportsUnresolvedBasesAndUnavailableSessions(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	writeWorkspaceFile(t, first, "shared.go", "first\n")
	writeWorkspaceFile(t, second, "shared.go", "second\n")
	missing := t.TempDir()
	if err := os.Remove(missing); err != nil {
		t.Fatalf("remove missing workspace fixture: %v", err)
	}

	store := newFakeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo", Config: domain.ProjectConfig{DefaultBranch: "missing-default"}}
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	store.sessions["demo-missing"] = overlapSession("demo-missing", missing)

	got, err := NewWithDeps(Deps{Store: store}).ProjectOverlaps(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ProjectOverlaps: %v", err)
	}
	if len(got.Overlaps) != 1 {
		t.Fatalf("overlaps = %+v, want healthy pair", got.Overlaps)
	}
	if !slices.Equal(got.UnresolvedBaseSessionIDs, []domain.SessionID{"demo-a", "demo-b"}) {
		t.Fatalf("unresolved bases = %v", got.UnresolvedBaseSessionIDs)
	}
	if !slices.Equal(got.UnavailableSessionIDs, []domain.SessionID{"demo-missing"}) {
		t.Fatalf("unavailable sessions = %v", got.UnavailableSessionIDs)
	}
}

func TestProjectOverlapsReportsTruncationAndConfiguredBasenameIgnores(t *testing.T) {
	manifests := map[domain.SessionID]WorkspaceManifest{
		"demo-b": {
			SessionID: "demo-b",
			Truncated: true,
			Files: []WorkspaceFileSummary{
				{Path: "frontend/generated.go"},
				{Path: "frontend/package-lock.json"},
				{Path: " edge.txt "},
			},
		},
		"demo-a": {
			SessionID:         "demo-a",
			CompareIncomplete: true,
			Files: []WorkspaceFileSummary{
				{Path: "backend/generated.go"},
				{Path: "frontend/package-lock.json"},
				{Path: " edge.txt "},
			},
		},
	}

	got := projectOverlapsFromManifests("demo", manifests, []string{"generated.go"})
	if len(got.Overlaps) != 1 || !slices.Equal(got.Overlaps[0].Paths, []string{" edge.txt "}) {
		t.Fatalf("overlaps = %+v, want only the whitespace-bearing path", got.Overlaps)
	}
	if !slices.Equal(got.TruncatedSessionIDs, []domain.SessionID{"demo-b"}) {
		t.Fatalf("truncated sessions = %v, want demo-b", got.TruncatedSessionIDs)
	}
	if !slices.Equal(got.UnresolvedBaseSessionIDs, []domain.SessionID{"demo-a"}) {
		t.Fatalf("unresolved bases = %v, want demo-a", got.UnresolvedBaseSessionIDs)
	}
}

func TestProjectOverlapsRejectsUnknownProject(t *testing.T) {
	_, err := NewWithDeps(Deps{Store: newFakeStore()}).ProjectOverlaps(context.Background(), "missing")
	var apiError *apierr.Error
	if !errors.As(err, &apiError) || apiError.Kind != apierr.KindNotFound {
		t.Fatalf("error = %v, want not found", err)
	}
}

func TestProjectOverlapWatchPathsReturnsSortedActiveWorkspaceRoots(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	child := t.TempDir()
	ended := t.TempDir()
	preparation := t.TempDir()

	store := newFakeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	endedSession := overlapSession("demo-ended", ended)
	endedSession.IsTerminated = true
	store.sessions[endedSession.ID] = endedSession
	preparationSession := overlapSession("demo-preparation", preparation)
	preparationSession.IsTaskPreparation = true
	store.sessions[preparationSession.ID] = preparationSession
	store.worktrees["demo-a"] = []domain.SessionWorktreeRecord{
		{SessionID: "demo-a", WorktreePath: child},
		{SessionID: "demo-a", WorktreePath: first},
	}

	got, err := NewWithDeps(Deps{Store: store}).ProjectOverlapWatchPaths(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ProjectOverlapWatchPaths: %v", err)
	}
	want := []string{child, first, second}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("watch paths = %v, want %v", got, want)
	}
}

func TestProjectOverlapWatchPathsSkipsScratchAndUnavailableSessions(t *testing.T) {
	missing := t.TempDir()
	if err := os.Remove(missing); err != nil {
		t.Fatalf("remove workspace fixture: %v", err)
	}

	store := newFakeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-missing"] = overlapSession("demo-missing", missing)
	store.projects["scratch"] = domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch}
	store.sessions["scratch-a"] = domain.SessionRecord{
		ID: "scratch-a", ProjectID: "scratch", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{WorkspacePath: t.TempDir()},
	}

	svc := NewWithDeps(Deps{Store: store})
	got, err := svc.ProjectOverlapWatchPaths(context.Background(), "demo")
	if err != nil {
		t.Fatalf("unavailable ProjectOverlapWatchPaths: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("unavailable watch paths = %#v, want non-nil empty list", got)
	}

	got, err = svc.ProjectOverlapWatchPaths(context.Background(), "scratch")
	if err != nil {
		t.Fatalf("scratch ProjectOverlapWatchPaths: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("scratch watch paths = %#v, want non-nil empty list", got)
	}
}

func TestReconcileProjectOverlapNoticesSendsEachSideOnceAndRearmsAfterClear(t *testing.T) {
	store := newMemoryOverlapNoticeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = domain.SessionRecord{ID: "demo-a", ProjectID: "demo", DisplayName: "Alpha"}
	store.sessions["demo-b"] = domain.SessionRecord{ID: "demo-b", ProjectID: "demo", DisplayName: "Beta"}
	nudger := &recordingOverlapNudger{}
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger, Clock: func() time.Time { return now }})

	overlap := ProjectOverlapResult{
		ProjectID: "demo",
		Overlaps:  []ProjectOverlap{{SessionA: "demo-a", SessionB: "demo-b", Paths: []string{"README.md", "frontend/App.tsx"}}},
	}
	if err := svc.reconcileProjectOverlapNotices(context.Background(), overlap); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if len(nudger.calls) != 2 {
		t.Fatalf("nudge calls = %+v, want one per session", nudger.calls)
	}
	if nudger.calls[0].sessionID != "demo-a" || !strings.Contains(nudger.calls[0].message, "session Beta (demo-b) also changed these files") ||
		!strings.Contains(nudger.calls[0].message, "- README.md") || !strings.Contains(nudger.calls[0].message, "- frontend/App.tsx") {
		t.Fatalf("first nudge = %+v", nudger.calls[0])
	}
	if nudger.calls[1].sessionID != "demo-b" || !strings.Contains(nudger.calls[1].message, "session Alpha (demo-a) also changed these files") {
		t.Fatalf("second nudge = %+v", nudger.calls[1])
	}
	if len(store.notices) != 2 {
		t.Fatalf("persisted notices = %+v, want two directed rows", store.notices)
	}

	// A changed path set is still the same active pair, so it must not create
	// edit-by-edit noise while the collision remains present.
	overlap.Overlaps[0].Paths = []string{"backend/main.go"}
	if err := svc.reconcileProjectOverlapNotices(context.Background(), overlap); err != nil {
		t.Fatalf("repeat reconcile: %v", err)
	}
	if len(nudger.calls) != 2 {
		t.Fatalf("repeat nudge calls = %d, want 2", len(nudger.calls))
	}

	if err := svc.reconcileProjectOverlapNotices(context.Background(), ProjectOverlapResult{ProjectID: "demo"}); err != nil {
		t.Fatalf("clear reconcile: %v", err)
	}
	if len(store.notices) != 0 {
		t.Fatalf("notices after clear = %+v, want none", store.notices)
	}
	if err := svc.reconcileProjectOverlapNotices(context.Background(), overlap); err != nil {
		t.Fatalf("rearmed reconcile: %v", err)
	}
	if len(nudger.calls) != 4 {
		t.Fatalf("rearmed nudge calls = %d, want 4", len(nudger.calls))
	}
}

func TestReconcileProjectOverlapNoticesKeepsDedupeAcrossIncompleteRead(t *testing.T) {
	tests := map[string]ProjectOverlapResult{
		"unavailable":     {ProjectID: "demo", UnavailableSessionIDs: []domain.SessionID{"demo-b"}},
		"truncated":       {ProjectID: "demo", TruncatedSessionIDs: []domain.SessionID{"demo-b"}},
		"unresolved base": {ProjectID: "demo", UnresolvedBaseSessionIDs: []domain.SessionID{"demo-b"}},
	}
	for name, incomplete := range tests {
		t.Run(name, func(t *testing.T) {
			store := newMemoryOverlapNoticeStore()
			store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
			store.sessions["demo-a"] = domain.SessionRecord{ID: "demo-a", ProjectID: "demo"}
			store.sessions["demo-b"] = domain.SessionRecord{ID: "demo-b", ProjectID: "demo"}
			nudger := &recordingOverlapNudger{}
			svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
			overlap := ProjectOverlapResult{
				ProjectID: "demo",
				Overlaps:  []ProjectOverlap{{SessionA: "demo-a", SessionB: "demo-b", Paths: []string{"shared.go"}}},
			}

			if err := svc.reconcileProjectOverlapNotices(context.Background(), overlap); err != nil {
				t.Fatalf("initial reconcile: %v", err)
			}
			if err := svc.reconcileProjectOverlapNotices(context.Background(), incomplete); err != nil {
				t.Fatalf("incomplete reconcile: %v", err)
			}
			if len(store.notices) != 2 {
				t.Fatalf("notices after incomplete read = %+v, want dedupe rows preserved", store.notices)
			}
			if err := svc.reconcileProjectOverlapNotices(context.Background(), overlap); err != nil {
				t.Fatalf("recovered reconcile: %v", err)
			}
			if len(nudger.calls) != 2 {
				t.Fatalf("nudge calls after recovery = %d, want no duplicate notices", len(nudger.calls))
			}
		})
	}
}

func TestReconcileProjectOverlapNoticesRetriesOnlySuppressedRecipient(t *testing.T) {
	store := newMemoryOverlapNoticeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = domain.SessionRecord{ID: "demo-a", ProjectID: "demo"}
	store.sessions["demo-b"] = domain.SessionRecord{ID: "demo-b", ProjectID: "demo"}
	nudger := &recordingOverlapNudger{outcomes: map[domain.SessionID][]sessionguard.Outcome{
		"demo-a": {sessionguard.SuppressedBusy, sessionguard.Sent},
		"demo-b": {sessionguard.Sent},
	}}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	result := ProjectOverlapResult{ProjectID: "demo", Overlaps: []ProjectOverlap{{SessionA: "demo-a", SessionB: "demo-b", Paths: []string{"a.go"}}}}

	if err := svc.reconcileProjectOverlapNotices(context.Background(), result); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if len(store.notices) != 1 || !store.hasNotice("demo-b", "demo-a") {
		t.Fatalf("notices = %+v, want only delivered demo-b -> demo-a", store.notices)
	}
	changed := ProjectOverlapResult{ProjectID: "demo", Overlaps: []ProjectOverlap{{SessionA: "demo-a", SessionB: "demo-b", Paths: []string{"b.go"}}}}
	if err := svc.reconcileProjectOverlapNotices(context.Background(), changed); err != nil {
		t.Fatalf("retry reconcile: %v", err)
	}
	if len(nudger.calls) != 3 || nudger.calls[2].sessionID != "demo-a" {
		t.Fatalf("nudge calls = %+v, want only demo-a retried", nudger.calls)
	}
	if !strings.Contains(nudger.calls[0].message, "- a.go") || !strings.Contains(nudger.calls[2].message, "- b.go") || strings.Contains(nudger.calls[2].message, "- a.go") {
		t.Fatalf("suppressed retry payloads = %q then %q, want current overlap path", nudger.calls[0].message, nudger.calls[2].message)
	}
	if nudger.calls[0].deliveryID == nudger.calls[2].deliveryID || nudger.calls[2].confirmOnly {
		t.Fatalf("suppressed retry calls = %+v, want a fresh send attempt", nudger.calls)
	}
	if len(store.notices) != 2 {
		t.Fatalf("notices after retry = %+v, want both directions", store.notices)
	}
}

func TestReconcileProjectOverlapNoticesContinuesAfterOneRecipientErrors(t *testing.T) {
	store := newMemoryOverlapNoticeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = domain.SessionRecord{ID: "demo-a", ProjectID: "demo"}
	store.sessions["demo-b"] = domain.SessionRecord{ID: "demo-b", ProjectID: "demo"}
	nudger := &recordingOverlapNudger{
		outcomes: map[domain.SessionID][]sessionguard.Outcome{"demo-a": {sessionguard.SuppressedUnknown}, "demo-b": {sessionguard.Sent}},
		errors:   map[domain.SessionID][]error{"demo-a": {errors.New("guard read failed")}},
	}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	result := ProjectOverlapResult{ProjectID: "demo", Overlaps: []ProjectOverlap{{SessionA: "demo-a", SessionB: "demo-b", Paths: []string{"shared.go"}}}}

	if err := svc.reconcileProjectOverlapNotices(context.Background(), result); err == nil || !strings.Contains(err.Error(), "guard read failed") {
		t.Fatalf("reconcile error = %v, want guard failure", err)
	}
	if len(nudger.calls) != 2 || !store.hasNotice("demo-b", "demo-a") {
		t.Fatalf("calls=%+v notices=%+v, want independent demo-b delivery", nudger.calls, store.notices)
	}
}

func TestReconcileActiveProjectOverlapNoticesDetectsAndRetriesWithoutReader(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	writeWorkspaceFile(t, first, "shared.go", "first\n")
	writeWorkspaceFile(t, second, "shared.go", "second\n")
	store := newMemoryOverlapNoticeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	nudger := &recordingOverlapNudger{outcomes: map[domain.SessionID][]sessionguard.Outcome{
		"demo-a": {sessionguard.SuppressedBusy, sessionguard.Sent},
		"demo-b": {sessionguard.Sent},
	}}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})

	if err := svc.ReconcileActiveProjectOverlapNotices(context.Background()); err != nil {
		t.Fatalf("first headless reconcile: %v", err)
	}
	if len(nudger.calls) != 2 || len(store.notices) != 1 {
		t.Fatalf("first sweep calls=%+v notices=%+v, want one suppressed direction", nudger.calls, store.notices)
	}
	if err := svc.ReconcileActiveProjectOverlapNotices(context.Background()); err != nil {
		t.Fatalf("retry headless reconcile: %v", err)
	}
	if len(nudger.calls) != 3 || len(store.notices) != 2 {
		t.Fatalf("retry sweep calls=%+v notices=%+v, want suppressed direction retried without another read or edit", nudger.calls, store.notices)
	}
}

func TestRunProjectOverlapNoticeRadarDetectsWorkspaceChangeWithoutReader(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	writeWorkspaceFile(t, first, "shared.go", "first\n")
	store := newMemoryOverlapNoticeStore()
	store.listNoticeCh = make(chan struct{}, 1)
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	nudger := &recordingOverlapNudger{callCh: make(chan overlapNudgeCall, 2)}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.RunProjectOverlapNoticeRadar(ctx)
		close(done)
	}()

	select {
	case <-store.listNoticeCh:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("daemon-owned radar did not finish its initial scan")
	}
	writeWorkspaceFile(t, second, "shared.go", "second\n")
	for range 2 {
		select {
		case <-nudger.callCh:
		case <-time.After(3 * time.Second):
			cancel()
			<-done
			t.Fatal("daemon-owned radar did not deliver after a workspace event")
		}
	}
	cancel()
	<-done
	if len(store.notices) != 2 {
		t.Fatalf("notices = %+v, want both directions", store.notices)
	}
}

func TestRunProjectOverlapNoticeRadarRetriesSuppressedRecipientAfterSessionChange(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	writeWorkspaceFile(t, first, "shared.go", "first\n")
	writeWorkspaceFile(t, second, "shared.go", "second\n")
	store := newMemoryOverlapNoticeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	nudger := &recordingOverlapNudger{
		callCh: make(chan overlapNudgeCall, 3),
		outcomes: map[domain.SessionID][]sessionguard.Outcome{
			"demo-a": {sessionguard.SuppressedBusy, sessionguard.Sent},
			"demo-b": {sessionguard.Sent},
		},
	}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.RunProjectOverlapNoticeRadar(ctx)
		close(done)
	}()

	for range 2 {
		select {
		case <-nudger.callCh:
		case <-time.After(3 * time.Second):
			cancel()
			<-done
			t.Fatal("daemon-owned radar did not finish its initial delivery")
		}
	}
	svc.SignalProjectOverlapRadar("demo")
	select {
	case call := <-nudger.callCh:
		if call.sessionID != "demo-a" {
			t.Fatalf("retried session = %s, want only suppressed demo-a", call.sessionID)
		}
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("session change did not retry the suppressed recipient")
	}
	cancel()
	<-done
	if len(store.notices) != 2 {
		t.Fatalf("notices = %+v, want both directions after activity retry", store.notices)
	}
}

func TestRunProjectOverlapNoticeRadarIgnoresSessionSignalWithUnchangedTopology(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	store := newMemoryOverlapNoticeStore()
	store.listNoticeCh = make(chan struct{}, 2)
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: &recordingOverlapNudger{}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.RunProjectOverlapNoticeRadar(ctx)
		close(done)
	}()
	select {
	case <-store.listNoticeCh:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("radar did not finish its initial scan")
	}

	for range 20 {
		svc.SignalProjectOverlapRadarSession("demo")
	}
	select {
	case <-store.listNoticeCh:
		cancel()
		<-done
		t.Fatal("unchanged session topology rebuilt and rescanned the radar")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	<-done
}

func TestRunProjectOverlapNoticeRadarClearsRowsWhenProjectDropsBelowTwoSessions(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	writeWorkspaceFile(t, first, "shared.go", "first\n")
	writeWorkspaceFile(t, second, "shared.go", "second\n")
	store := newMemoryOverlapNoticeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	nudger := &recordingOverlapNudger{callCh: make(chan overlapNudgeCall, 2)}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.RunProjectOverlapNoticeRadar(ctx)
		close(done)
	}()
	for range 2 {
		select {
		case <-nudger.callCh:
		case <-time.After(3 * time.Second):
			cancel()
			<-done
			t.Fatal("radar did not persist the initial directed notices")
		}
	}

	terminated := store.sessions["demo-b"]
	terminated.IsTerminated = true
	store.sessions["demo-b"] = terminated
	svc.SignalProjectOverlapRadarSession("demo")
	deadline := time.Now().Add(3 * time.Second)
	for store.hasNotice("demo-a", "demo-b") || store.hasNotice("demo-b", "demo-a") {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("delivery rows remained after the project dropped below two eligible sessions")
		}
		time.Sleep(time.Millisecond)
	}

	restored := store.sessions["demo-b"]
	restored.IsTerminated = false
	store.sessions["demo-b"] = restored
	svc.SignalProjectOverlapRadarSession("demo")
	for range 2 {
		select {
		case <-nudger.callCh:
		case <-time.After(3 * time.Second):
			cancel()
			<-done
			t.Fatal("restored collision was suppressed by stale delivery rows")
		}
	}
	cancel()
	<-done
}

func TestRunProjectOverlapNoticeRadarClearsInactiveRowsOnStartup(t *testing.T) {
	store := newMemoryOverlapNoticeStore()
	store.listNoticeCh = make(chan struct{}, 4)
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = overlapSession("demo-a", t.TempDir())
	store.sessions["demo-b"] = domain.SessionRecord{ID: "demo-b", ProjectID: "demo", IsTerminated: true}
	store.deleteErrors = []error{errors.New("disk full")}
	store.notices[overlapNoticeKey{sessionID: "demo-a", peerSessionID: "demo-b"}] = domain.SessionOverlapNotice{
		SessionID: "demo-a", PeerSessionID: "demo-b",
	}
	store.notices[overlapNoticeKey{sessionID: "demo-b", peerSessionID: "demo-a"}] = domain.SessionOverlapNotice{
		SessionID: "demo-b", PeerSessionID: "demo-a",
	}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: &recordingOverlapNudger{}})
	svc.overlapNoticeRetryDelay = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.RunProjectOverlapNoticeRadar(ctx)
		close(done)
	}()

	for range 2 {
		select {
		case <-store.listNoticeCh:
		case <-time.After(3 * time.Second):
			cancel()
			<-done
			t.Fatal("startup did not retry stale-notice cleanup for an inactive project")
		}
	}
	cancel()
	<-done
	if len(store.notices) != 0 {
		t.Fatalf("startup notices = %+v, want inactive rows removed", store.notices)
	}
}

func TestProjectOverlapRadarRefreshProjectsAppliesEveryObservedTopologyChange(t *testing.T) {
	got := projectOverlapRadarRefreshProjects(
		map[domain.ProjectID]bool{"alpha": false, "beta": true},
		[]projectOverlapRadarSignal{{projectID: "alpha"}},
	)
	if got["alpha"] || !got["beta"] {
		t.Fatalf("refresh projects = %+v, want only beta after alpha's event observed beta's topology change", got)
	}
}

func TestRunProjectOverlapNoticeRadarRetriesDefiniteRefusalWithoutAnotherEvent(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	writeWorkspaceFile(t, first, "shared.go", "first\n")
	writeWorkspaceFile(t, second, "shared.go", "second\n")
	store := newMemoryOverlapNoticeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	nudger := &recordingOverlapNudger{
		callCh:   make(chan overlapNudgeCall, 3),
		outcomes: map[domain.SessionID][]sessionguard.Outcome{"demo-a": {sessionguard.Sent, sessionguard.Sent}},
		errors: map[domain.SessionID][]error{
			"demo-a": {fmt.Errorf("mode-aware send: %w", sessionmanager.ErrComposerBusy), nil},
		},
	}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	svc.overlapNoticeRetryDelay = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.RunProjectOverlapNoticeRadar(ctx)
		close(done)
	}()

	var calls []overlapNudgeCall
	for range 3 {
		select {
		case call := <-nudger.callCh:
			calls = append(calls, call)
		case <-time.After(3 * time.Second):
			cancel()
			<-done
			t.Fatal("definitely unsent notice was not retried by the bounded timer")
		}
	}
	cancel()
	<-done
	if calls[2].sessionID != "demo-a" {
		t.Fatalf("retried session = %s, want refused demo-a", calls[2].sessionID)
	}
	if calls[2].confirmOnly {
		t.Fatalf("definite refusal retry = %+v, want a new send rather than receipt confirmation", calls[2])
	}
	if calls[0].deliveryID == calls[2].deliveryID {
		t.Fatalf("definite refusal reused attempt id %q, want a fresh delivery", calls[0].deliveryID)
	}
}

func TestRunProjectOverlapNoticeRadarReconcilesConfigOnlyChange(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	writeWorkspaceFile(t, first, "shared.go", "first\n")
	writeWorkspaceFile(t, second, "shared.go", "second\n")
	store := newMemoryOverlapNoticeStore()
	store.listNoticeCh = make(chan struct{}, 1)
	store.projects["demo"] = domain.ProjectRecord{ID: "demo", Config: domain.ProjectConfig{CollisionRadar: domain.CollisionRadarConfig{IgnorePaths: []string{"shared.go"}}}}
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	nudger := &recordingOverlapNudger{callCh: make(chan overlapNudgeCall, 2)}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.RunProjectOverlapNoticeRadar(ctx)
		close(done)
	}()

	select {
	case <-store.listNoticeCh:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("radar did not finish the ignored-overlap scan")
	}
	project := store.projects["demo"]
	project.Config.CollisionRadar.IgnorePaths = nil
	store.projects["demo"] = project
	svc.SignalProjectOverlapRadar("demo")
	for range 2 {
		select {
		case <-nudger.callCh:
		case <-time.After(3 * time.Second):
			cancel()
			<-done
			t.Fatal("config-only overlap change did not deliver notices")
		}
	}
	cancel()
	<-done
}

func TestRunProjectOverlapNoticeRadarRetainsConfigSignalAfterTopologyFailure(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	writeWorkspaceFile(t, first, "shared.go", "first\n")
	writeWorkspaceFile(t, second, "shared.go", "second\n")
	store := newMemoryOverlapNoticeStore()
	store.listNoticeCh = make(chan struct{}, 1)
	store.projects["demo"] = domain.ProjectRecord{ID: "demo", Config: domain.ProjectConfig{CollisionRadar: domain.CollisionRadarConfig{IgnorePaths: []string{"shared.go"}}}}
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	nudger := &recordingOverlapNudger{callCh: make(chan overlapNudgeCall, 2)}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	svc.overlapNoticeRetryDelay = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.RunProjectOverlapNoticeRadar(ctx)
		close(done)
	}()

	select {
	case <-store.listNoticeCh:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("radar did not finish the ignored-overlap scan")
	}
	project := store.projects["demo"]
	project.Config.CollisionRadar.IgnorePaths = nil
	store.projects["demo"] = project
	store.failNextListAll(errors.New("transient topology read"))
	svc.SignalProjectOverlapRadar("demo")
	for range 2 {
		select {
		case <-nudger.callCh:
		case <-time.After(3 * time.Second):
			cancel()
			<-done
			t.Fatal("config-only refresh was lost after a transient topology read failure")
		}
	}
	cancel()
	<-done
}

func TestRunProjectOverlapNoticeRadarRecoversRestoredWorkspace(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	writeWorkspaceFile(t, first, "shared.go", "first\n")
	writeWorkspaceFile(t, second, "shared.go", "second\n")
	parked := second + "-parked"
	if err := os.Rename(second, parked); err != nil {
		t.Fatalf("park workspace: %v", err)
	}
	store := newMemoryOverlapNoticeStore()
	store.listNoticeCh = make(chan struct{}, 1)
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	nudger := &recordingOverlapNudger{callCh: make(chan overlapNudgeCall, 2)}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	svc.overlapNoticeRetryDelay = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.RunProjectOverlapNoticeRadar(ctx)
		close(done)
	}()

	select {
	case <-store.listNoticeCh:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("radar did not finish its missing-workspace scan")
	}
	if err := os.Rename(parked, second); err != nil {
		cancel()
		<-done
		t.Fatalf("restore workspace: %v", err)
	}
	for range 2 {
		select {
		case <-nudger.callCh:
		case <-time.After(3 * time.Second):
			cancel()
			<-done
			t.Fatal("restored workspace was not discovered by bounded catch-up")
		}
	}
	cancel()
	<-done
}

func TestRunProjectOverlapNoticeRadarRetriesWatcherSetup(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	writeWorkspaceFile(t, first, "shared.go", "first\n")
	store := newMemoryOverlapNoticeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	nudger := &recordingOverlapNudger{callCh: make(chan overlapNudgeCall, 2)}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	svc.overlapNoticeRetryDelay = 10 * time.Millisecond
	watchCalls := make(chan int, 2)
	var watchAttempt int
	svc.overlapWatch = func(ctx context.Context, paths ...string) (workspacewatch.Subscription, error) {
		watchAttempt++
		watchCalls <- watchAttempt
		if watchAttempt == 1 {
			return workspacewatch.Subscription{}, errors.New("watch descriptors exhausted")
		}
		return workspacewatch.WatchWithStatus(ctx, paths...)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.RunProjectOverlapNoticeRadar(ctx)
		close(done)
	}()

	select {
	case attempt := <-watchCalls:
		if attempt != 1 {
			t.Fatalf("first watch attempt = %d, want 1", attempt)
		}
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("radar did not attempt watcher setup")
	}
	writeWorkspaceFile(t, second, "shared.go", "second\n")
	select {
	case attempt := <-watchCalls:
		if attempt != 2 {
			t.Fatalf("retried watch attempt = %d, want 2", attempt)
		}
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("radar did not retry failed watcher setup")
	}
	for range 2 {
		select {
		case <-nudger.callCh:
		case <-time.After(3 * time.Second):
			cancel()
			<-done
			t.Fatal("watcher recovery did not reconcile the new overlap")
		}
	}
	cancel()
	<-done
}

func TestRunProjectOverlapNoticeRadarScopesWorkspaceEventsToProject(t *testing.T) {
	store := newMemoryOverlapNoticeStore()
	store.listProjectCh = make(chan domain.ProjectID, 4)
	nudger := &recordingOverlapNudger{callCh: make(chan overlapNudgeCall, 2)}
	for _, projectID := range []domain.ProjectID{"alpha", "beta"} {
		first := newWorkspaceRepo(t)
		second := newWorkspaceRepo(t)
		writeWorkspaceFile(t, first, "shared.go", "first\n")
		store.projects[string(projectID)] = domain.ProjectRecord{ID: string(projectID)}
		store.sessions[domain.SessionID(string(projectID)+"-a")] = projectOverlapSession(domain.SessionID(string(projectID)+"-a"), projectID, first)
		store.sessions[domain.SessionID(string(projectID)+"-b")] = projectOverlapSession(domain.SessionID(string(projectID)+"-b"), projectID, second)
	}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.RunProjectOverlapNoticeRadar(ctx)
		close(done)
	}()

	seen := map[domain.ProjectID]bool{}
	for len(seen) < 2 {
		select {
		case projectID := <-store.listProjectCh:
			seen[projectID] = true
		case <-time.After(3 * time.Second):
			cancel()
			<-done
			t.Fatal("radar did not initialize both project workers")
		}
	}
	betaReads := store.listCalls("beta")
	alphaSecond := store.sessions["alpha-b"].Metadata.WorkspacePath
	writeWorkspaceFile(t, alphaSecond, "shared.go", "second\n")
	for range 2 {
		select {
		case call := <-nudger.callCh:
			if !strings.HasPrefix(string(call.sessionID), "alpha-") {
				t.Fatalf("workspace event delivered to unrelated project session %s", call.sessionID)
			}
		case <-time.After(3 * time.Second):
			cancel()
			<-done
			t.Fatal("alpha workspace event did not deliver")
		}
	}
	cancel()
	<-done
	if got := store.listCalls("beta"); got != betaReads {
		t.Fatalf("beta reconciles after alpha edit = %d, want unchanged %d", got, betaReads)
	}
}

func TestRunProjectOverlapNoticeRadarIsolatesSlowProjects(t *testing.T) {
	store := newMemoryOverlapNoticeStore()
	for _, projectID := range []domain.ProjectID{"a-slow", "z-fast"} {
		first := newWorkspaceRepo(t)
		second := newWorkspaceRepo(t)
		writeWorkspaceFile(t, first, "shared.go", "first\n")
		writeWorkspaceFile(t, second, "shared.go", "second\n")
		store.projects[string(projectID)] = domain.ProjectRecord{ID: string(projectID)}
		store.sessions[domain.SessionID(string(projectID)+"-a")] = projectOverlapSession(domain.SessionID(string(projectID)+"-a"), projectID, first)
		store.sessions[domain.SessionID(string(projectID)+"-b")] = projectOverlapSession(domain.SessionID(string(projectID)+"-b"), projectID, second)
	}
	nudger := &isolatingOverlapNudger{slowStarted: make(chan struct{}), fastSent: make(chan struct{}, 1)}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.RunProjectOverlapNoticeRadar(ctx)
		close(done)
	}()

	select {
	case <-nudger.slowStarted:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("slow project never entered delivery")
	}
	select {
	case <-nudger.fastSent:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("slow project blocked collision delivery for another project")
	}
	cancel()
	<-done
}

func TestReconcileCurrentProjectOverlapNoticesSerializesConcurrentReads(t *testing.T) {
	first := newWorkspaceRepo(t)
	second := newWorkspaceRepo(t)
	writeWorkspaceFile(t, first, "shared.go", "first\n")
	writeWorkspaceFile(t, second, "shared.go", "second\n")
	store := newMemoryOverlapNoticeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = overlapSession("demo-a", first)
	store.sessions["demo-b"] = overlapSession("demo-b", second)
	nudger := &recordingOverlapNudger{}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			errs <- svc.ReconcileCurrentProjectOverlapNotices(context.Background(), "demo")
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent current-state reconcile: %v", err)
		}
	}
	if len(nudger.calls) != 2 || len(store.notices) != 2 {
		t.Fatalf("concurrent reads calls=%+v notices=%+v, want one current delivery per direction", nudger.calls, store.notices)
	}
}

func TestReconcileProjectOverlapNoticesDoesNotRecordSentWithError(t *testing.T) {
	store := newMemoryOverlapNoticeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = domain.SessionRecord{ID: "demo-a", ProjectID: "demo"}
	store.sessions["demo-b"] = domain.SessionRecord{ID: "demo-b", ProjectID: "demo"}
	nudger := &recordingOverlapNudger{
		outcomes: map[domain.SessionID][]sessionguard.Outcome{"demo-a": {sessionguard.Sent, sessionguard.Sent}},
		errors: map[domain.SessionID][]error{"demo-a": {
			fmt.Errorf("%w: prompt submission was not observed", sessionmanager.ErrSemanticAcceptanceUnsupported), nil,
		}},
	}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	result := ProjectOverlapResult{ProjectID: "demo", Overlaps: []ProjectOverlap{{SessionA: "demo-a", SessionB: "demo-b", Paths: []string{"shared.go"}}}}

	if err := svc.reconcileProjectOverlapNotices(context.Background(), result); err == nil {
		t.Fatal("first reconcile unexpectedly ignored send error")
	}
	if store.hasNotice("demo-a", "demo-b") {
		t.Fatal("sent-with-error response was recorded as delivered")
	}
	if err := svc.reconcileProjectOverlapNotices(context.Background(), result); err != nil {
		t.Fatalf("retry reconcile: %v", err)
	}
	if !store.hasNotice("demo-a", "demo-b") || len(nudger.calls) != 3 {
		t.Fatalf("retry calls=%+v notices=%+v, want failed direction confirmed", nudger.calls, store.notices)
	}
	if nudger.calls[0].deliveryID == "" || nudger.calls[0].deliveryID != nudger.calls[2].deliveryID {
		t.Fatalf("ambiguous retry delivery ids = %q then %q, want one stable identity", nudger.calls[0].deliveryID, nudger.calls[2].deliveryID)
	}
	if nudger.calls[0].confirmOnly || !nudger.calls[2].confirmOnly {
		t.Fatalf("ambiguous retry calls=%+v, want initial send followed by confirmation only", nudger.calls)
	}
}

func TestReconcileProjectOverlapNoticesRetriesSemanticPreflightWithFreshAttempt(t *testing.T) {
	store := newMemoryOverlapNoticeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = domain.SessionRecord{ID: "demo-a", ProjectID: "demo"}
	store.sessions["demo-b"] = domain.SessionRecord{ID: "demo-b", ProjectID: "demo"}
	nudger := &recordingOverlapNudger{
		outcomes: map[domain.SessionID][]sessionguard.Outcome{"demo-a": {sessionguard.Sent, sessionguard.Sent}},
		errors: map[domain.SessionID][]error{"demo-a": {
			fmt.Errorf("mode-aware send: %w", sessionmanager.ErrSemanticDeliveryNotAttempted), nil,
		}},
	}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	result := ProjectOverlapResult{ProjectID: "demo", Overlaps: []ProjectOverlap{{SessionA: "demo-a", SessionB: "demo-b", Paths: []string{"shared.go"}}}}

	if err := svc.reconcileProjectOverlapNotices(context.Background(), result); !errors.Is(err, sessionmanager.ErrSemanticDeliveryNotAttempted) {
		t.Fatalf("preflight reconcile error = %v, want delivery-not-attempted", err)
	}
	if err := svc.reconcileProjectOverlapNotices(context.Background(), result); err != nil {
		t.Fatalf("supported retry reconcile: %v", err)
	}
	if len(nudger.calls) != 3 || nudger.calls[2].sessionID != "demo-a" || nudger.calls[2].confirmOnly {
		t.Fatalf("preflight retry calls = %+v, want a fresh send for demo-a", nudger.calls)
	}
	if nudger.calls[0].deliveryID == nudger.calls[2].deliveryID {
		t.Fatalf("preflight retry reused attempt id %q, want a fresh delivery", nudger.calls[0].deliveryID)
	}
}

func TestReconcileProjectOverlapNoticesConfirmsOriginalAmbiguousPayload(t *testing.T) {
	store := newMemoryOverlapNoticeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = domain.SessionRecord{ID: "demo-a", ProjectID: "demo"}
	store.sessions["demo-b"] = domain.SessionRecord{ID: "demo-b", ProjectID: "demo", DisplayName: "Original peer"}
	nudger := &recordingOverlapNudger{
		errors: map[domain.SessionID][]error{"demo-a": {errors.New("delivery unconfirmed"), nil}},
	}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	first := ProjectOverlapResult{ProjectID: "demo", Overlaps: []ProjectOverlap{{SessionA: "demo-a", SessionB: "demo-b", Paths: []string{"shared.go"}}}}

	if err := svc.reconcileProjectOverlapNotices(context.Background(), first); err == nil {
		t.Fatal("first reconcile unexpectedly ignored the ambiguous delivery")
	}
	rec := store.sessions["demo-b"]
	rec.DisplayName = "Renamed peer"
	store.sessions["demo-b"] = rec
	changed := ProjectOverlapResult{ProjectID: "demo", Overlaps: []ProjectOverlap{{SessionA: "demo-a", SessionB: "demo-b", Paths: []string{"renamed.go"}}}}
	if err := svc.reconcileProjectOverlapNotices(context.Background(), changed); err != nil {
		t.Fatalf("confirm changed overlap: %v", err)
	}
	if len(nudger.calls) != 3 || !nudger.calls[2].confirmOnly {
		t.Fatalf("calls = %+v, want one confirmation for the ambiguous direction", nudger.calls)
	}
	if nudger.calls[2].message != nudger.calls[0].message {
		t.Fatalf("confirmation payload changed:\nfirst: %q\nconfirm: %q", nudger.calls[0].message, nudger.calls[2].message)
	}
}

func TestReconcileProjectOverlapNoticesKeepsAmbiguousAttemptAcrossInconclusiveConfirmation(t *testing.T) {
	tests := []struct {
		name           string
		confirmOutcome sessionguard.Outcome
		confirmErr     error
	}{
		{name: "suppressed", confirmOutcome: sessionguard.SuppressedAwaitingUser},
		{name: "failed", confirmOutcome: sessionguard.SuppressedUnknown, confirmErr: errors.New("read receipt")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemoryOverlapNoticeStore()
			store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
			store.sessions["demo-a"] = domain.SessionRecord{ID: "demo-a", ProjectID: "demo"}
			store.sessions["demo-b"] = domain.SessionRecord{ID: "demo-b", ProjectID: "demo"}
			nudger := &recordingOverlapNudger{
				outcomes: map[domain.SessionID][]sessionguard.Outcome{"demo-a": {sessionguard.Sent, tc.confirmOutcome, sessionguard.Sent}},
				errors:   map[domain.SessionID][]error{"demo-a": {errors.New("delivery unconfirmed"), tc.confirmErr, nil}},
			}
			svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
			first := ProjectOverlapResult{ProjectID: "demo", Overlaps: []ProjectOverlap{{SessionA: "demo-a", SessionB: "demo-b", Paths: []string{"shared.go"}}}}
			changed := ProjectOverlapResult{ProjectID: "demo", Overlaps: []ProjectOverlap{{SessionA: "demo-a", SessionB: "demo-b", Paths: []string{"renamed.go"}}}}

			if err := svc.reconcileProjectOverlapNotices(context.Background(), first); err == nil {
				t.Fatal("first reconcile unexpectedly ignored the ambiguous delivery")
			}
			if err := svc.reconcileProjectOverlapNotices(context.Background(), changed); (err != nil) != (tc.confirmErr != nil) {
				t.Fatalf("inconclusive confirmation error = %v, want error=%t", err, tc.confirmErr != nil)
			}
			if err := svc.reconcileProjectOverlapNotices(context.Background(), changed); err != nil {
				t.Fatalf("recovered confirmation: %v", err)
			}
			if len(nudger.calls) != 4 {
				t.Fatalf("calls = %+v, want initial sends and two confirmations", nudger.calls)
			}
			for _, call := range nudger.calls[2:] {
				if !call.confirmOnly || call.deliveryID != nudger.calls[0].deliveryID || call.message != nudger.calls[0].message {
					t.Fatalf("confirmation lost the original attempt: first=%+v confirm=%+v", nudger.calls[0], call)
				}
			}
			if !store.hasNotice("demo-a", "demo-b") {
				t.Fatal("recovered confirmation was not recorded")
			}
		})
	}
}

func TestReconcileProjectOverlapNoticesRetriesAfterAmbiguousChatAttemptIsRejected(t *testing.T) {
	store := newMemoryOverlapNoticeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = domain.SessionRecord{ID: "demo-a", ProjectID: "demo"}
	store.sessions["demo-b"] = domain.SessionRecord{ID: "demo-b", ProjectID: "demo"}
	nudger := &recordingOverlapNudger{
		errors: map[domain.SessionID][]error{"demo-a": {
			errors.New("delivery unconfirmed"), sessionmanager.ErrSemanticDeliveryRejected, nil,
		}},
	}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	first := ProjectOverlapResult{ProjectID: "demo", Overlaps: []ProjectOverlap{{SessionA: "demo-a", SessionB: "demo-b", Paths: []string{"shared.go"}}}}
	changed := ProjectOverlapResult{ProjectID: "demo", Overlaps: []ProjectOverlap{{SessionA: "demo-a", SessionB: "demo-b", Paths: []string{"renamed.go"}}}}

	if err := svc.reconcileProjectOverlapNotices(context.Background(), first); err == nil {
		t.Fatal("first reconcile unexpectedly ignored the ambiguous delivery")
	}
	if err := svc.reconcileProjectOverlapNotices(context.Background(), changed); !errors.Is(err, sessionmanager.ErrSemanticDeliveryRejected) {
		t.Fatalf("confirmation error = %v, want semantic rejection", err)
	}
	if err := svc.reconcileProjectOverlapNotices(context.Background(), changed); err != nil {
		t.Fatalf("fresh retry after rejection: %v", err)
	}
	if len(nudger.calls) != 4 {
		t.Fatalf("calls = %+v, want initial sends, confirmation, and fresh retry", nudger.calls)
	}
	if !nudger.calls[2].confirmOnly || nudger.calls[2].deliveryID != nudger.calls[0].deliveryID || nudger.calls[2].message != nudger.calls[0].message {
		t.Fatalf("rejection did not confirm the original attempt: first=%+v confirm=%+v", nudger.calls[0], nudger.calls[2])
	}
	if nudger.calls[3].confirmOnly || nudger.calls[3].deliveryID == nudger.calls[0].deliveryID || !strings.Contains(nudger.calls[3].message, "renamed.go") {
		t.Fatalf("rejection did not start a fresh current delivery: first=%+v retry=%+v", nudger.calls[0], nudger.calls[3])
	}
	if !store.hasNotice("demo-a", "demo-b") {
		t.Fatal("fresh retry after rejection was not recorded")
	}
}

func TestReconcileProjectOverlapNoticesRetiresUnconfirmedAttemptAfterCleanScan(t *testing.T) {
	store := newMemoryOverlapNoticeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = domain.SessionRecord{ID: "demo-a", ProjectID: "demo"}
	store.sessions["demo-b"] = domain.SessionRecord{ID: "demo-b", ProjectID: "demo"}
	nudger := &recordingOverlapNudger{
		outcomes: map[domain.SessionID][]sessionguard.Outcome{"demo-a": {sessionguard.Sent, sessionguard.Sent}},
		errors:   map[domain.SessionID][]error{"demo-a": {errors.New("delivery unconfirmed"), nil}},
	}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	collision := ProjectOverlapResult{ProjectID: "demo", Overlaps: []ProjectOverlap{{SessionA: "demo-a", SessionB: "demo-b", Paths: []string{"shared.go"}}}}

	if err := svc.reconcileProjectOverlapNotices(context.Background(), collision); err == nil {
		t.Fatal("first reconcile unexpectedly ignored the unconfirmed write")
	}
	firstAttempt := nudger.calls[0].deliveryID
	if firstAttempt == "" {
		t.Fatal("first reconcile did not allocate a delivery id")
	}
	if err := svc.reconcileProjectOverlapNotices(context.Background(), ProjectOverlapResult{ProjectID: "demo"}); err != nil {
		t.Fatalf("clean reconcile: %v", err)
	}
	if err := svc.reconcileProjectOverlapNotices(context.Background(), collision); err != nil {
		t.Fatalf("new collision reconcile: %v", err)
	}
	if got := nudger.calls[2].deliveryID; got == firstAttempt {
		t.Fatalf("new collision reused cleared unconfirmed attempt %q", got)
	}
	if nudger.calls[2].confirmOnly {
		t.Fatalf("new collision confirmed retired attempt instead of starting a new delivery: %+v", nudger.calls[2])
	}
}

func TestReconcileProjectOverlapNoticesDedupesSuccessfulSendWhilePersistenceRetries(t *testing.T) {
	store := newMemoryOverlapNoticeStore()
	store.projects["demo"] = domain.ProjectRecord{ID: "demo"}
	store.sessions["demo-a"] = domain.SessionRecord{ID: "demo-a", ProjectID: "demo"}
	store.sessions["demo-b"] = domain.SessionRecord{ID: "demo-b", ProjectID: "demo"}
	store.upsertErrors = []error{errors.New("disk full")}
	nudger := &recordingOverlapNudger{}
	svc := NewWithDeps(Deps{Store: store, OverlapNudger: nudger})
	result := ProjectOverlapResult{ProjectID: "demo", Overlaps: []ProjectOverlap{{SessionA: "demo-a", SessionB: "demo-b", Paths: []string{"shared.go"}}}}

	if err := svc.reconcileProjectOverlapNotices(context.Background(), result); err == nil {
		t.Fatal("first reconcile unexpectedly ignored persistence error")
	}
	if len(nudger.calls) != 2 {
		t.Fatalf("first nudge calls = %d, want 2", len(nudger.calls))
	}
	if err := svc.reconcileProjectOverlapNotices(context.Background(), result); err != nil {
		t.Fatalf("persistence retry: %v", err)
	}
	if len(nudger.calls) != 2 || len(store.notices) != 2 || store.upsertCalls != 3 {
		t.Fatalf("retry calls=%d notices=%+v upserts=%d, want no resend and one persistence retry", len(nudger.calls), store.notices, store.upsertCalls)
	}
}

func TestFormatProjectOverlapNoticeBoundsPaths(t *testing.T) {
	paths := make([]string, 23)
	for i := range paths {
		paths[i] = "path-" + string(rune('a'+i)) + ".go"
	}
	message := formatProjectOverlapNotice("Peer", "demo-b", paths)
	if !strings.Contains(message, "- path-a.go") || !strings.Contains(message, "- path-t.go") ||
		strings.Contains(message, "path-u.go") || !strings.Contains(message, "+ 3 more files") {
		t.Fatalf("bounded message:\n%s", message)
	}
}

type overlapNoticeKey struct {
	sessionID     domain.SessionID
	peerSessionID domain.SessionID
}

type memoryOverlapNoticeStore struct {
	*fakeStore
	mu            sync.Mutex
	notices       map[overlapNoticeKey]domain.SessionOverlapNotice
	upsertErrors  []error
	deleteErrors  []error
	listAllErrors []error
	upsertCalls   int
	listNoticeCh  chan struct{}
	listProjectCh chan domain.ProjectID
	listCounts    map[domain.ProjectID]int
}

func (s *memoryOverlapNoticeStore) ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	s.mu.Lock()
	if len(s.listAllErrors) > 0 {
		err := s.listAllErrors[0]
		s.listAllErrors = s.listAllErrors[1:]
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
	} else {
		s.mu.Unlock()
	}
	return s.fakeStore.ListAllSessions(ctx)
}

func (s *memoryOverlapNoticeStore) failNextListAll(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listAllErrors = append(s.listAllErrors, err)
}

func newMemoryOverlapNoticeStore() *memoryOverlapNoticeStore {
	return &memoryOverlapNoticeStore{
		fakeStore:  newFakeStore(),
		notices:    map[overlapNoticeKey]domain.SessionOverlapNotice{},
		listCounts: map[domain.ProjectID]int{},
	}
}

func (s *memoryOverlapNoticeStore) ListProjectSessionOverlapNotices(_ context.Context, projectID domain.ProjectID) ([]domain.SessionOverlapNotice, error) {
	s.mu.Lock()
	s.listCounts[projectID]++
	if s.listNoticeCh != nil {
		select {
		case s.listNoticeCh <- struct{}{}:
		default:
		}
	}
	if s.listProjectCh != nil {
		select {
		case s.listProjectCh <- projectID:
		default:
		}
	}
	result := make([]domain.SessionOverlapNotice, 0, len(s.notices))
	for _, notice := range s.notices {
		if session, ok := s.sessions[notice.SessionID]; ok && session.ProjectID == projectID {
			result = append(result, notice)
		}
	}
	s.mu.Unlock()
	return result, nil
}

func (s *memoryOverlapNoticeStore) UpsertSessionOverlapNotice(_ context.Context, notice domain.SessionOverlapNotice) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upsertCalls++
	if len(s.upsertErrors) > 0 {
		err := s.upsertErrors[0]
		s.upsertErrors = s.upsertErrors[1:]
		if err != nil {
			return err
		}
	}
	s.notices[overlapNoticeKey{sessionID: notice.SessionID, peerSessionID: notice.PeerSessionID}] = notice
	return nil
}

func (s *memoryOverlapNoticeStore) DeleteSessionOverlapNotice(_ context.Context, sessionID, peerSessionID domain.SessionID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.deleteErrors) > 0 {
		err := s.deleteErrors[0]
		s.deleteErrors = s.deleteErrors[1:]
		if err != nil {
			return err
		}
	}
	delete(s.notices, overlapNoticeKey{sessionID: sessionID, peerSessionID: peerSessionID})
	return nil
}

func (s *memoryOverlapNoticeStore) hasNotice(sessionID, peerSessionID domain.SessionID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.notices[overlapNoticeKey{sessionID: sessionID, peerSessionID: peerSessionID}]
	return ok
}

func (s *memoryOverlapNoticeStore) listCalls(projectID domain.ProjectID) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listCounts[projectID]
}

type overlapNudgeCall struct {
	sessionID   domain.SessionID
	message     string
	deliveryID  string
	confirmOnly bool
}

type recordingOverlapNudger struct {
	calls    []overlapNudgeCall
	outcomes map[domain.SessionID][]sessionguard.Outcome
	errors   map[domain.SessionID][]error
	callCh   chan overlapNudgeCall
}

func (n *recordingOverlapNudger) NudgeCoordination(_ context.Context, sessionID domain.SessionID, message, deliveryID string) (sessionguard.Outcome, error) {
	return n.record(sessionID, message, deliveryID, false)
}

func (n *recordingOverlapNudger) ConfirmCoordination(_ context.Context, sessionID domain.SessionID, message, deliveryID string) (sessionguard.Outcome, error) {
	return n.record(sessionID, message, deliveryID, true)
}

func (n *recordingOverlapNudger) record(sessionID domain.SessionID, message, deliveryID string, confirmOnly bool) (sessionguard.Outcome, error) {
	call := overlapNudgeCall{sessionID: sessionID, message: message, deliveryID: deliveryID, confirmOnly: confirmOnly}
	n.calls = append(n.calls, call)
	if n.callCh != nil {
		n.callCh <- call
	}
	var err error
	if errs := n.errors[sessionID]; len(errs) > 0 {
		n.errors[sessionID] = errs[1:]
		err = errs[0]
	}
	if outcomes := n.outcomes[sessionID]; len(outcomes) > 0 {
		n.outcomes[sessionID] = outcomes[1:]
		return outcomes[0], err
	}
	return sessionguard.Sent, err
}

type isolatingOverlapNudger struct {
	slowOnce    sync.Once
	slowStarted chan struct{}
	fastSent    chan struct{}
}

func (n *isolatingOverlapNudger) NudgeCoordination(ctx context.Context, sessionID domain.SessionID, _, _ string) (sessionguard.Outcome, error) {
	if strings.HasPrefix(string(sessionID), "a-slow-") {
		n.slowOnce.Do(func() { close(n.slowStarted) })
		<-ctx.Done()
		return sessionguard.Sent, ctx.Err()
	}
	select {
	case n.fastSent <- struct{}{}:
	default:
	}
	return sessionguard.Sent, nil
}

func (n *isolatingOverlapNudger) ConfirmCoordination(ctx context.Context, sessionID domain.SessionID, message, deliveryID string) (sessionguard.Outcome, error) {
	return n.NudgeCoordination(ctx, sessionID, message, deliveryID)
}

func overlapSession(id domain.SessionID, workspace string) domain.SessionRecord {
	return projectOverlapSession(id, "demo", workspace)
}

func projectOverlapSession(id domain.SessionID, projectID domain.ProjectID, workspace string) domain.SessionRecord {
	return domain.SessionRecord{
		ID:        id,
		ProjectID: projectID,
		Kind:      domain.KindWorker,
		Metadata:  domain.SessionMetadata{WorkspacePath: workspace},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}
}
