package session

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
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
	result := ProjectOverlapResult{ProjectID: "demo", Overlaps: []ProjectOverlap{{SessionA: "demo-a", SessionB: "demo-b", Paths: []string{"shared.go"}}}}

	if err := svc.reconcileProjectOverlapNotices(context.Background(), result); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if len(store.notices) != 1 || !store.hasNotice("demo-b", "demo-a") {
		t.Fatalf("notices = %+v, want only delivered demo-b -> demo-a", store.notices)
	}
	if err := svc.reconcileProjectOverlapNotices(context.Background(), result); err != nil {
		t.Fatalf("retry reconcile: %v", err)
	}
	if len(nudger.calls) != 3 || nudger.calls[2].sessionID != "demo-a" {
		t.Fatalf("nudge calls = %+v, want only demo-a retried", nudger.calls)
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
		svc.RunProjectOverlapNoticeRadar(ctx, make(chan struct{}))
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
	sessionChanges := make(chan struct{}, 1)
	go func() {
		svc.RunProjectOverlapNoticeRadar(ctx, sessionChanges)
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
	sessionChanges <- struct{}{}
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
		errors:   map[domain.SessionID][]error{"demo-a": {errors.New("composer busy after admission"), nil}},
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
		t.Fatalf("retry calls=%+v notices=%+v, want failed direction sent again", nudger.calls, store.notices)
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
	notices      map[overlapNoticeKey]domain.SessionOverlapNotice
	upsertErrors []error
	upsertCalls  int
	listNoticeCh chan struct{}
}

func newMemoryOverlapNoticeStore() *memoryOverlapNoticeStore {
	return &memoryOverlapNoticeStore{fakeStore: newFakeStore(), notices: map[overlapNoticeKey]domain.SessionOverlapNotice{}}
}

func (s *memoryOverlapNoticeStore) ListProjectSessionOverlapNotices(_ context.Context, projectID domain.ProjectID) ([]domain.SessionOverlapNotice, error) {
	if s.listNoticeCh != nil {
		select {
		case s.listNoticeCh <- struct{}{}:
		default:
		}
	}
	result := make([]domain.SessionOverlapNotice, 0, len(s.notices))
	for _, notice := range s.notices {
		if session, ok := s.sessions[notice.SessionID]; ok && session.ProjectID == projectID {
			result = append(result, notice)
		}
	}
	return result, nil
}

func (s *memoryOverlapNoticeStore) UpsertSessionOverlapNotice(_ context.Context, notice domain.SessionOverlapNotice) error {
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
	delete(s.notices, overlapNoticeKey{sessionID: sessionID, peerSessionID: peerSessionID})
	return nil
}

func (s *memoryOverlapNoticeStore) hasNotice(sessionID, peerSessionID domain.SessionID) bool {
	_, ok := s.notices[overlapNoticeKey{sessionID: sessionID, peerSessionID: peerSessionID}]
	return ok
}

type overlapNudgeCall struct {
	sessionID domain.SessionID
	message   string
}

type recordingOverlapNudger struct {
	calls    []overlapNudgeCall
	outcomes map[domain.SessionID][]sessionguard.Outcome
	errors   map[domain.SessionID][]error
	callCh   chan overlapNudgeCall
}

func (n *recordingOverlapNudger) NudgeCoordination(_ context.Context, sessionID domain.SessionID, message string) (sessionguard.Outcome, error) {
	call := overlapNudgeCall{sessionID: sessionID, message: message}
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

func overlapSession(id domain.SessionID, workspace string) domain.SessionRecord {
	return domain.SessionRecord{
		ID:        id,
		ProjectID: "demo",
		Kind:      domain.KindWorker,
		Metadata:  domain.SessionMetadata{WorkspacePath: workspace},
		Activity:  domain.Activity{State: domain.ActivityActive},
	}
}
