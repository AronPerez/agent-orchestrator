package trackerref

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestResolveBareNumberUsesActualProjectOrigin(t *testing.T) {
	project := domain.ProjectRecord{
		RepoOriginURL: "ssh://git@gitlab.internal:2222/group/repo.git",
		Config: domain.ProjectConfig{TrackerIntake: domain.TrackerIntakeConfig{
			Provider: domain.TrackerProviderGitHub,
			Repo:     "override/elsewhere",
		}},
	}
	got, ok := Resolve("#7", project)
	if !ok || got != "gitlab:group/repo#7@gitlab.internal:2222" {
		t.Fatalf("Resolve(#7) = %q, %t, want actual GitLab origin identity", got, ok)
	}
}

func TestEquivalentBareNumberIsRepositoryScoped(t *testing.T) {
	project := domain.ProjectRecord{RepoOriginURL: "git@github.com:acme/demo.git"}
	if !Equivalent("18", "github:acme/demo#18", project) {
		t.Fatal("bare number did not match its project repository")
	}
	if Equivalent("18", "github:other/demo#18", project) {
		t.Fatal("bare number matched a different repository")
	}
}

func TestResolveLegacyIssueURLs(t *testing.T) {
	project := domain.ProjectRecord{}
	for raw, want := range map[domain.IssueID]domain.IssueID{
		"https://github.com/acme/demo/issues/9":              "github:acme/demo#9",
		"https://gitlab.com/group/repo/-/issues/9":           "gitlab:group/repo#9",
		"https://gitlab.internal:8443/group/repo/-/issues/9": "gitlab:group/repo#9@gitlab.internal:8443",
		"gitlab:group/repo#9@GITLAB.INTERNAL:8443":           "gitlab:group/repo#9@gitlab.internal:8443",
	} {
		got, ok := Resolve(raw, project)
		if !ok || got != want {
			t.Errorf("Resolve(%q) = %q, %t, want %q", raw, got, ok, want)
		}
	}
}
