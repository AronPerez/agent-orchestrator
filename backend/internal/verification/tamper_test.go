package verification

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAssessPullRequestDiffFlagsApprovedTamperSignals(t *testing.T) {
	at := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	assessment := assessPullRequestDiff(
		"head-2",
		at,
		[]string{"internal/widget_test.go", ".github/workflows/ci.yml", "tests/removed_test.py"},
		[]string{"tests/removed_test.py"},
		[]byte(`diff --git a/internal/widget_test.go b/internal/widget_test.go
--- a/internal/widget_test.go
+++ b/internal/widget_test.go
@@ -1,3 +1,7 @@
-func TestRemoved(t *testing.T) {}
-func TestAlsoRemoved(t *testing.T) {}
-func TestThirdRemoved(t *testing.T) {}
+func TestReplacement(t *testing.T) {}
+t.Skip("later")
+test.skip("later", () => {})
+xit("later", () => {})
+@pytest.mark.skip(reason="later")
+#[ignore]
`),
	)

	if !assessment.Suspected() {
		t.Fatalf("assessment = %+v, want suspected", assessment)
	}
	if assessment.HeadSHA != "head-2" || assessment.AssessedAt != at {
		t.Fatalf("assessment identity = %+v", assessment)
	}
	if assessment.TestFilesDeleted != 1 || assessment.TestCasesRemoved != 3 || assessment.TestCasesAdded != 1 || assessment.SkipMarkersAdded != 5 || !assessment.CIWorkflowEdited {
		t.Fatalf("assessment counts = %+v", assessment)
	}
	wantReasons := []string{"test_files_deleted", "test_cases_removed", "skip_markers_added", "ci_workflow_edited"}
	if !slices.Equal(assessment.Reasons(), wantReasons) {
		t.Fatalf("reasons = %q, want %q", assessment.Reasons(), wantReasons)
	}
}

func TestAssessPullRequestDiffRecognizesAllGoSkipCalls(t *testing.T) {
	assessment := assessPullRequestDiff("head-2", time.Unix(1, 0).UTC(), []string{"widget_test.go"}, nil, []byte(`diff --git a/widget_test.go b/widget_test.go
--- a/widget_test.go
+++ b/widget_test.go
@@ -1 +1,3 @@
 func TestWidget(t *testing.T) {
+t.Skipf("later: %s", reason)
+t.SkipNow()
 }
`))
	if assessment.SkipMarkersAdded != 2 {
		t.Fatalf("skip markers = %d, want 2", assessment.SkipMarkersAdded)
	}
}

func TestAssessPullRequestDiffIgnoresCleanSourceChanges(t *testing.T) {
	assessment := assessPullRequestDiff(
		"head-2",
		time.Unix(1, 0).UTC(),
		[]string{"src/contest.ts", "docs/testing.md"},
		nil,
		[]byte(`diff --git a/src/contest.ts b/src/contest.ts
--- a/src/contest.ts
+++ b/src/contest.ts
@@ -1 +1,2 @@
 export const contest = true;
+export const text = "test.skip(";
`),
	)
	if assessment.Suspected() || len(assessment.Reasons()) != 0 {
		t.Fatalf("assessment = %+v, want clean", assessment)
	}
}

func TestAssessPullRequestTamperingReadsExactGitRange(t *testing.T) {
	repo := t.TempDir()
	runTestGit(t, repo, "init")
	runTestGit(t, repo, "config", "user.name", "AO Test")
	runTestGit(t, repo, "config", "user.email", "ao@example.test")
	runTestGit(t, repo, "config", "commit.gpgsign", "false")
	writeTestFile(t, repo, "pkg/widget_test.go", "package pkg\n\nfunc TestKeep(t *testing.T) {}\nfunc TestRemove(t *testing.T) {}\n")
	writeTestFile(t, repo, "tests/delete_test.py", "def test_delete_me():\n    pass\n")
	runTestGit(t, repo, "add", ".")
	runTestGit(t, repo, "commit", "-m", "base")
	base := strings.TrimSpace(runTestGit(t, repo, "rev-parse", "HEAD"))

	writeTestFile(t, repo, "pkg/widget_test.go", "package pkg\n\nfunc TestKeep(t *testing.T) { t.Skip(\"later\") }\n")
	if err := os.Remove(filepath.Join(repo, "tests/delete_test.py")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, repo, ".github/workflows/ci.yml", "name: ci\n")
	runTestGit(t, repo, "add", "-A")
	runTestGit(t, repo, "commit", "-m", "head")
	head := strings.TrimSpace(runTestGit(t, repo, "rev-parse", "HEAD"))

	assessment, err := AssessPullRequestTampering(context.Background(), repo, base, head, time.Unix(2, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if assessment.HeadSHA != head || assessment.TestFilesDeleted != 1 || assessment.TestCasesRemoved != 3 || assessment.TestCasesAdded != 1 || assessment.SkipMarkersAdded != 1 || !assessment.CIWorkflowEdited {
		t.Fatalf("assessment = %+v", assessment)
	}
}

func TestAssessPullRequestTamperingTreatsTestRenameOutOfDiscoveryAsDeletion(t *testing.T) {
	repo := t.TempDir()
	runTestGit(t, repo, "init")
	runTestGit(t, repo, "config", "user.name", "AO Test")
	runTestGit(t, repo, "config", "user.email", "ao@example.test")
	runTestGit(t, repo, "config", "commit.gpgsign", "false")
	writeTestFile(t, repo, "widget.test.ts", "test('widget', () => expect(true).toBe(true));\n")
	runTestGit(t, repo, "add", ".")
	runTestGit(t, repo, "commit", "-m", "base")
	base := strings.TrimSpace(runTestGit(t, repo, "rev-parse", "HEAD"))
	runTestGit(t, repo, "mv", "widget.test.ts", "widget.ts")
	runTestGit(t, repo, "commit", "-m", "head")
	head := strings.TrimSpace(runTestGit(t, repo, "rev-parse", "HEAD"))

	assessment, err := AssessPullRequestTampering(context.Background(), repo, base, head, time.Unix(2, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if assessment.TestFilesDeleted != 1 {
		t.Fatalf("assessment = %+v, want renamed test counted as deleted", assessment)
	}
}

func runTestGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...) //nolint:gosec // Test owns every argument.
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func writeTestFile(t *testing.T, repo, name, contents string) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
