package session

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ProjectOverlap is one pair of live sessions that changed the same paths.
type ProjectOverlap struct {
	SessionA domain.SessionID
	SessionB domain.SessionID
	Paths    []string
}

// ProjectOverlapResult is the derived collision radar for one project.
type ProjectOverlapResult struct {
	ProjectID                domain.ProjectID
	Overlaps                 []ProjectOverlap
	TruncatedSessionIDs      []domain.SessionID
	UnresolvedBaseSessionIDs []domain.SessionID
	UnavailableSessionIDs    []domain.SessionID
}

var defaultOverlapIgnoredBasenames = map[string]struct{}{
	"Cargo.lock":        {},
	"Gemfile.lock":      {},
	"bun.lock":          {},
	"bun.lockb":         {},
	"composer.lock":     {},
	"go.sum":            {},
	"package-lock.json": {},
	"pnpm-lock.yaml":    {},
	"poetry.lock":       {},
	"uv.lock":           {},
	"yarn.lock":         {},
}

// ProjectOverlaps computes overlap from fresh base-relative workspace
// manifests and publishes those snapshots into the shared manifest cache. It
// stores no derived collision status.
func (s *Service) ProjectOverlaps(ctx context.Context, projectID domain.ProjectID) (ProjectOverlapResult, error) {
	project, active, err := s.activeProjectOverlapSessions(ctx, projectID)
	if err != nil {
		return ProjectOverlapResult{}, err
	}
	if project.Kind.WithDefault() == domain.ProjectKindScratch {
		return emptyProjectOverlapResult(projectID), nil
	}

	manifests := make([]WorkspaceManifest, len(active))
	unavailable := make([]bool, len(active))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(4)
	for i := range active {
		group.Go(func() error {
			// An explicit collision-radar read is authoritative even when no Files
			// view has a filesystem watcher open. Reconcile bypasses both cache
			// layers, and also waits through a watcher-invalidated stale snapshot.
			manifest, readErr := s.ReconcileWorkspaceManifest(groupCtx, active[i].ID)
			if readErr != nil {
				if overlapWorkspaceUnavailable(readErr) {
					unavailable[i] = true
					return nil
				}
				return fmt.Errorf("read workspace manifest for %s: %w", active[i].ID, readErr)
			}
			manifests[i] = manifest
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return ProjectOverlapResult{}, err
	}
	bySession := make(map[domain.SessionID]WorkspaceManifest, len(manifests))
	result := emptyProjectOverlapResult(projectID)
	for i, manifest := range manifests {
		if unavailable[i] {
			result.UnavailableSessionIDs = append(result.UnavailableSessionIDs, active[i].ID)
			continue
		}
		bySession[manifest.SessionID] = manifest
	}
	derived := projectOverlapsFromManifests(projectID, bySession, project.Config.CollisionRadar.IgnorePaths)
	derived.UnavailableSessionIDs = result.UnavailableSessionIDs
	return derived, nil
}

// ProjectOverlapWatchPaths returns the live worktree roots whose filesystem
// changes can invalidate a project's collision radar. Missing workspaces are
// reported by ProjectOverlaps and do not prevent healthy sessions from being
// watched.
func (s *Service) ProjectOverlapWatchPaths(ctx context.Context, projectID domain.ProjectID) ([]string, error) {
	project, active, err := s.activeProjectOverlapSessions(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project.Kind.WithDefault() == domain.ProjectKindScratch {
		return []string{}, nil
	}
	seen := make(map[string]struct{})
	paths := make([]string, 0, len(active))
	for _, record := range active {
		workspacePaths, watchErr := s.WorkspaceWatchPaths(ctx, record.ID)
		if watchErr != nil {
			if overlapWorkspaceUnavailable(watchErr) {
				continue
			}
			return nil, fmt.Errorf("list overlap watch paths for %s: %w", record.ID, watchErr)
		}
		for _, workspacePath := range workspacePaths {
			workspacePath = strings.TrimSpace(workspacePath)
			if workspacePath == "" {
				continue
			}
			if _, ok := seen[workspacePath]; ok {
				continue
			}
			seen[workspacePath] = struct{}{}
			paths = append(paths, workspacePath)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func (s *Service) activeProjectOverlapSessions(ctx context.Context, projectID domain.ProjectID) (domain.ProjectRecord, []domain.SessionRecord, error) {
	project, found, err := s.store.GetProject(ctx, string(projectID))
	if err != nil {
		return domain.ProjectRecord{}, nil, fmt.Errorf("get project %s: %w", projectID, err)
	}
	if !found {
		return domain.ProjectRecord{}, nil, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	if project.Kind.WithDefault() == domain.ProjectKindScratch {
		return project, []domain.SessionRecord{}, nil
	}
	records, err := s.store.ListSessions(ctx, projectID)
	if err != nil {
		return domain.ProjectRecord{}, nil, fmt.Errorf("list project sessions: %w", err)
	}
	active := records[:0]
	for _, record := range records {
		if !eligibleProjectOverlapSession(record) {
			continue
		}
		active = append(active, record)
	}
	sort.Slice(active, func(i, j int) bool { return active[i].ID < active[j].ID })
	return project, active, nil
}

func eligibleProjectOverlapSession(record domain.SessionRecord) bool {
	return !record.IsTerminated && !record.IsTaskPreparation && !record.NeedsImportResume() && strings.TrimSpace(record.Metadata.WorkspacePath) != ""
}

func overlapWorkspaceUnavailable(err error) bool {
	if errors.Is(err, ports.ErrWorkspaceRepoUnavailable) {
		return true
	}
	var apiError *apierr.Error
	return errors.As(err, &apiError) && apiError.Code == "SESSION_WORKSPACE_NOT_FOUND"
}

func emptyProjectOverlapResult(projectID domain.ProjectID) ProjectOverlapResult {
	return ProjectOverlapResult{
		ProjectID:                projectID,
		Overlaps:                 make([]ProjectOverlap, 0),
		TruncatedSessionIDs:      make([]domain.SessionID, 0),
		UnresolvedBaseSessionIDs: make([]domain.SessionID, 0),
		UnavailableSessionIDs:    make([]domain.SessionID, 0),
	}
}

func projectOverlapsFromManifests(projectID domain.ProjectID, manifests map[domain.SessionID]WorkspaceManifest, ignored []string) ProjectOverlapResult {
	result := emptyProjectOverlapResult(projectID)
	ids := make([]domain.SessionID, 0, len(manifests))
	pathsBySession := make(map[domain.SessionID]map[string]struct{}, len(manifests))
	for id, manifest := range manifests {
		ids = append(ids, id)
		if manifest.Truncated {
			result.TruncatedSessionIDs = append(result.TruncatedSessionIDs, id)
		}
		if manifest.CompareIncomplete {
			result.UnresolvedBaseSessionIDs = append(result.UnresolvedBaseSessionIDs, id)
		}
		paths := make(map[string]struct{}, len(manifest.Files))
		for _, file := range manifest.Files {
			for _, changedPath := range []string{file.Path, file.PreviousPath} {
				changedPath = path.Clean(changedPath)
				if changedPath == "." || overlapPathIgnored(changedPath, ignored) {
					continue
				}
				paths[changedPath] = struct{}{}
			}
		}
		pathsBySession[id] = paths
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	sort.Slice(result.TruncatedSessionIDs, func(i, j int) bool { return result.TruncatedSessionIDs[i] < result.TruncatedSessionIDs[j] })
	sort.Slice(result.UnresolvedBaseSessionIDs, func(i, j int) bool { return result.UnresolvedBaseSessionIDs[i] < result.UnresolvedBaseSessionIDs[j] })
	for i, first := range ids {
		for _, second := range ids[i+1:] {
			shared := intersectOverlapPaths(pathsBySession[first], pathsBySession[second])
			if len(shared) == 0 {
				continue
			}
			result.Overlaps = append(result.Overlaps, ProjectOverlap{SessionA: first, SessionB: second, Paths: shared})
		}
	}
	return result
}

func overlapPathIgnored(changedPath string, configured []string) bool {
	if _, ignored := defaultOverlapIgnoredBasenames[path.Base(changedPath)]; ignored {
		return true
	}
	for _, ignored := range configured {
		if strings.ContainsRune(ignored, '/') {
			if changedPath == ignored {
				return true
			}
		} else if path.Base(changedPath) == ignored {
			return true
		}
	}
	return false
}

func intersectOverlapPaths(first, second map[string]struct{}) []string {
	if len(first) > len(second) {
		first, second = second, first
	}
	shared := make([]string, 0)
	for changedPath := range first {
		if _, ok := second[changedPath]; ok {
			shared = append(shared, changedPath)
		}
	}
	sort.Strings(shared)
	return shared
}
