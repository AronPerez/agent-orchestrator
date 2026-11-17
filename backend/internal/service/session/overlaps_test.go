package session

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
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
			SessionID: "demo-a",
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
}

func TestProjectOverlapsRejectsUnknownProject(t *testing.T) {
	_, err := NewWithDeps(Deps{Store: newFakeStore()}).ProjectOverlaps(context.Background(), "missing")
	var apiError *apierr.Error
	if !errors.As(err, &apiError) || apiError.Kind != apierr.KindNotFound {
		t.Fatalf("error = %v, want not found", err)
	}
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
