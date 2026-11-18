package verification

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	tamperAnalysisTimeout = 10 * time.Second
	tamperNamesLimit      = 1 << 20
	tamperPatchLimit      = 16 << 20
)

var (
	gitObjectIDPattern   = regexp.MustCompile(`^[0-9a-fA-F]{40}(?:[0-9a-fA-F]{24})?$`)
	errTamperOutputLimit = errors.New("verification: pull request diff exceeds tamper analysis limit")
)

var testCasePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^\s*func\s+(?:Test|Benchmark|Example)\w*\s*\(`),
	regexp.MustCompile(`^\s*(?:async\s+)?def\s+test_\w*\s*\(`),
	regexp.MustCompile(`^\s*(?:it|test)(?:\.(?:each|only))?\s*\(`),
	regexp.MustCompile(`^\s*#\s*\[\s*test\s*\]`),
	regexp.MustCompile(`^\s*@Test\b`),
	regexp.MustCompile(`^\s*it\s+["'(]`),
}

var skipMarkers = []string{"t.Skip(", "t.Skipf(", "t.SkipNow(", ".skip(", "xit(", "@pytest.mark.skip", "#[ignore]"}

// AssessPullRequestTampering computes the deterministic advisory signals for
// one exact local base...head range. It never consults the mutable worktree.
func AssessPullRequestTampering(ctx context.Context, workspacePath, baseSHA, headSHA string, assessedAt time.Time) (domain.PRTamperAssessment, error) {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return domain.PRTamperAssessment{}, errors.New("verification: workspace path is required for tamper analysis")
	}
	if !gitObjectIDPattern.MatchString(baseSHA) || !gitObjectIDPattern.MatchString(headSHA) {
		return domain.PRTamperAssessment{}, errors.New("verification: full base and head commit ids are required for tamper analysis")
	}
	analysisCtx, cancel := context.WithTimeout(ctx, tamperAnalysisTimeout)
	defer cancel()
	revision := baseSHA + "..." + headSHA
	changed, err := tamperGitOutput(analysisCtx, workspacePath, tamperNamesLimit,
		"diff", "--no-ext-diff", "--no-textconv", "--name-only", "-z", revision, "--")
	if err != nil {
		return domain.PRTamperAssessment{}, err
	}
	removed, err := tamperGitOutput(analysisCtx, workspacePath, tamperNamesLimit,
		"diff", "--no-ext-diff", "--no-textconv", "--name-status", "--find-renames", "--diff-filter=DR", "-z", revision, "--")
	if err != nil {
		return domain.PRTamperAssessment{}, err
	}
	patch, err := tamperGitOutput(analysisCtx, workspacePath, tamperPatchLimit,
		"diff", "--no-ext-diff", "--no-textconv", "--unified=0", revision, "--")
	if err != nil {
		return domain.PRTamperAssessment{}, err
	}
	return assessPullRequestDiff(headSHA, assessedAt, nullSeparatedPaths(changed), removedTestPaths(removed), patch), nil
}

func tamperGitOutput(ctx context.Context, workspacePath string, limit int, args ...string) ([]byte, error) {
	cmdArgs := append([]string{"-C", workspacePath}, args...)
	cmd := aoprocess.CommandContext(ctx, "git", cmdArgs...)
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat")
	stdout := &tamperBoundedBuffer{limit: limit}
	stderr := &tamperBoundedBuffer{limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if stdout.exceeded {
		return nil, errTamperOutputLimit
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, errors.Join(err, ctxErr)
		}
		return nil, fmt.Errorf("verification: inspect pull request diff: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

type tamperBoundedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *tamperBoundedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.Len()
	if len(p) <= remaining {
		return b.Buffer.Write(p)
	}
	if remaining > 0 {
		_, _ = b.Buffer.Write(p[:remaining])
	}
	b.exceeded = true
	return len(p), nil
}

func nullSeparatedPaths(output []byte) []string {
	output = bytes.TrimSuffix(output, []byte{0})
	if len(output) == 0 {
		return nil
	}
	parts := bytes.Split(output, []byte{0})
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) > 0 {
			paths = append(paths, filepath.ToSlash(string(part)))
		}
	}
	return paths
}

func removedTestPaths(output []byte) []string {
	parts := bytes.Split(bytes.TrimSuffix(output, []byte{0}), []byte{0})
	removed := make([]string, 0, len(parts)/2)
	for i := 0; i < len(parts); {
		status := string(parts[i])
		i++
		switch {
		case status == "D" && i < len(parts):
			path := filepath.ToSlash(string(parts[i]))
			i++
			if isTestFile(path) {
				removed = append(removed, path)
			}
		case strings.HasPrefix(status, "R") && i+1 < len(parts):
			oldPath := filepath.ToSlash(string(parts[i]))
			newPath := filepath.ToSlash(string(parts[i+1]))
			i += 2
			if isDiscoveredTestFile(oldPath) && !isDiscoveredTestFile(newPath) {
				removed = append(removed, oldPath)
			}
		default:
			// Ignore a truncated or unexpected record. Git is invoked with a
			// deletion/rename-only filter, so supported output is handled above.
			return removed
		}
	}
	return removed
}

func assessPullRequestDiff(headSHA string, assessedAt time.Time, changedPaths, deletedPaths []string, patch []byte) domain.PRTamperAssessment {
	assessment := domain.PRTamperAssessment{HeadSHA: headSHA, AssessedAt: assessedAt}
	for _, path := range changedPaths {
		if isCIWorkflow(path) {
			assessment.CIWorkflowEdited = true
		}
	}
	for _, path := range deletedPaths {
		if isTestFile(path) {
			assessment.TestFilesDeleted++
		}
	}

	var oldPath, currentPath string
	for _, raw := range bytes.Split(patch, []byte{'\n'}) {
		line := string(raw)
		switch {
		case strings.HasPrefix(line, "--- "):
			oldPath = diffHeaderPath(strings.TrimPrefix(line, "--- "))
			continue
		case strings.HasPrefix(line, "+++ "):
			currentPath = diffHeaderPath(strings.TrimPrefix(line, "+++ "))
			if currentPath == "" {
				currentPath = oldPath
			}
			continue
		}
		if !isTestFile(currentPath) || len(line) < 2 {
			continue
		}
		content := line[1:]
		switch line[0] {
		case '+':
			if isTestCaseDeclaration(content) {
				assessment.TestCasesAdded++
			}
			for _, marker := range skipMarkers {
				if strings.Contains(content, marker) {
					assessment.SkipMarkersAdded++
					break
				}
			}
		case '-':
			if isTestCaseDeclaration(content) {
				assessment.TestCasesRemoved++
			}
		}
	}
	return assessment
}

func diffHeaderPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "/dev/null" || value == "" {
		return ""
	}
	if cut := strings.IndexByte(value, '\t'); cut >= 0 {
		value = value[:cut]
	}
	value = strings.TrimPrefix(value, "a/")
	value = strings.TrimPrefix(value, "b/")
	return filepath.ToSlash(value)
}

func isTestCaseDeclaration(line string) bool {
	for _, pattern := range testCasePatterns {
		if pattern.MatchString(line) {
			return true
		}
	}
	return false
}

func isTestFile(path string) bool {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	name := filepath.Base(path)
	if strings.Contains("/"+path+"/", "/test/") || strings.Contains("/"+path+"/", "/tests/") || strings.Contains("/"+path+"/", "/__tests__/") {
		return true
	}
	if strings.HasSuffix(name, "_test.go") || strings.HasPrefix(name, "test_") || strings.Contains(name, ".test.") || strings.Contains(name, ".spec.") {
		return true
	}
	return strings.HasSuffix(name, "_test.py") || strings.HasSuffix(name, "_spec.rb")
}

// isDiscoveredTestFile is stricter than isTestFile for rename comparisons.
// A helper in a tests directory may still be test-related, but moving a file
// away from a language runner's discovery name removes the suite just as
// surely as moving it out of that directory.
func isDiscoveredTestFile(path string) bool {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	name := filepath.Base(path)
	ext := filepath.Ext(name)
	switch ext {
	case ".go":
		return strings.HasSuffix(name, "_test.go")
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs":
		return strings.Contains(name, ".test.") || strings.Contains(name, ".spec.") || strings.Contains("/"+path+"/", "/__tests__/")
	case ".py":
		return strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test.py")
	case ".rb":
		return strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_spec.rb")
	case ".java", ".kt", ".kts":
		stem := strings.TrimSuffix(name, ext)
		return strings.HasSuffix(stem, "test") || strings.HasSuffix(stem, "tests") || strings.HasSuffix(stem, "testcase")
	case ".rs":
		return strings.Contains("/"+path+"/", "/tests/")
	default:
		return isTestFile(path)
	}
}

func isCIWorkflow(path string) bool {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	return strings.HasPrefix(path, ".github/workflows/") ||
		strings.HasPrefix(path, ".gitlab/ci/") ||
		strings.HasPrefix(path, ".circleci/") ||
		path == ".gitlab-ci.yml" || path == "azure-pipelines.yml"
}
