package verification

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestRunnerRunsVerifyStepsInOrderWithProjectContext(t *testing.T) {
	var calls []string
	runner := newTestRunner(func(_ context.Context, workspace, command string, env stepEnvironment, output io.Writer) error {
		calls = append(calls, workspace+"\x00"+command+"\x00"+env.Value("TOKEN")+"\x00"+env.Value("AO_PROJECT_ID")+"\x00"+env.Value("AO_WORKTREE_PATH"))
		_, _ = io.WriteString(output, "output from "+command+"\n")
		return nil
	})
	var evidence bytes.Buffer
	result, err := runner.Run(context.Background(), Request{
		ProjectID: "demo", WorkspacePath: "/work/demo", Commands: []string{" first ", "", "second"},
		Env: map[string]string{"TOKEN": "safe"}, Output: &evidence,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.FailedStep != 0 || result.TimedOut || result.StartedAt.IsZero() || result.CompletedAt.Before(result.StartedAt) {
		t.Fatalf("result = %+v", result)
	}
	want := []string{
		"/work/demo\x00first\x00safe\x00demo\x00/work/demo",
		"/work/demo\x00second\x00safe\x00demo\x00/work/demo",
	}
	if len(calls) != len(want) || calls[0] != want[0] || calls[1] != want[1] {
		t.Fatalf("calls = %q, want %q", calls, want)
	}
	for _, text := range []string{"Verification step 1 of 2", "output from first", "Verification step 2 of 2", "output from second"} {
		if !strings.Contains(result.OutputTail, text) || !strings.Contains(evidence.String(), text) {
			t.Fatalf("output missing %q:\nresult=%q\nevidence=%q", text, result.OutputTail, evidence.String())
		}
	}
}

func TestRunnerFailureStopsLaterStepsAndRedactsEvidence(t *testing.T) {
	sentinel := errors.New("exit status 7")
	var calls []string
	runner := newTestRunner(func(_ context.Context, _ string, command string, _ stepEnvironment, output io.Writer) error {
		calls = append(calls, command)
		_, _ = io.WriteString(output, strings.Repeat("noise", 100)+" secret-value\n")
		if command == "fail" {
			return sentinel
		}
		return nil
	})
	runner.tailBytes = 96
	var evidence bytes.Buffer
	result, err := runner.Run(context.Background(), Request{
		ProjectID: "demo", WorkspacePath: "/work/demo", Commands: []string{"pass", "fail", "never"},
		Env: map[string]string{"TOKEN": "secret-value"}, Output: &evidence,
	})
	var stepErr *StepError
	if !errors.As(err, &stepErr) || !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want StepError wrapping sentinel", err)
	}
	if result.Passed || result.FailedStep != 2 || result.TimedOut || stepErr.Step != 2 {
		t.Fatalf("result = %+v, stepErr = %+v", result, stepErr)
	}
	if len(calls) != 2 || calls[0] != "pass" || calls[1] != "fail" {
		t.Fatalf("calls = %q", calls)
	}
	if strings.Contains(result.OutputTail, "secret-value") || strings.Contains(evidence.String(), "secret-value") {
		t.Fatalf("secret leaked: result=%q evidence=%q", result.OutputTail, evidence.String())
	}
	if !strings.Contains(result.OutputTail, "[REDACTED]") || len(result.OutputTail) > runner.tailBytes {
		t.Fatalf("bounded redacted tail = %q (%d bytes)", result.OutputTail, len(result.OutputTail))
	}
}

func TestRunnerRedactsASecretAcrossTheTailBoundary(t *testing.T) {
	const secret = "secret-value"
	runner := newTestRunner(func(_ context.Context, _ string, _ string, _ stepEnvironment, output io.Writer) error {
		_, _ = io.WriteString(output, "prefix-"+secret+"!")
		return nil
	})
	runner.tailBytes = 8
	result, err := runner.Run(context.Background(), Request{
		ProjectID: "demo", WorkspacePath: "/work/demo", Commands: []string{"verify"},
		Env: map[string]string{"TOKEN": secret},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.OutputTail, "value") {
		t.Fatalf("secret suffix leaked from the tail boundary: %q", result.OutputTail)
	}
}

func TestRunnerRedactsRepeatedSecretsBeforeBoundingTheTail(t *testing.T) {
	const secret = "0123456789abcdef0123456789ABCDEF"
	payload := strings.Repeat(secret, 2100) + strings.Repeat("x", 15)
	runner := newTestRunner(func(_ context.Context, _ string, _ string, _ stepEnvironment, output io.Writer) error {
		_, _ = io.WriteString(output, payload)
		return nil
	})
	result, err := runner.Run(context.Background(), Request{
		ProjectID: "demo", WorkspacePath: "/work/demo", Commands: []string{"verify"},
		Env: map[string]string{"TOKEN": secret},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.OutputTail, secret[15:]) {
		t.Fatalf("secret suffix leaked after repeated replacements: %q", result.OutputTail[:min(len(result.OutputTail), 80)])
	}
}

func TestRunnerRedactsInheritedCredentialEnvironment(t *testing.T) {
	const secret = "inherited-verification-secret"
	t.Setenv("VERIFY_REVIEW_TOKEN", secret)
	runner := newTestRunner(func(_ context.Context, _ string, _ string, _ stepEnvironment, output io.Writer) error {
		_, _ = io.WriteString(output, os.Getenv("VERIFY_REVIEW_TOKEN"))
		return nil
	})
	result, err := runner.Run(context.Background(), Request{
		ProjectID: "demo", WorkspacePath: "/work/demo", Commands: []string{"verify"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.OutputTail, secret) || !strings.Contains(result.OutputTail, "[REDACTED]") {
		t.Fatalf("inherited credential was not redacted: %q", result.OutputTail)
	}
}

func TestRunnerRedactsInheritedCredentialURL(t *testing.T) {
	const databaseURL = "postgres://verify-user:private-password@database.example/verify"
	t.Setenv("DATABASE_URL", databaseURL)
	runner := newTestRunner(func(_ context.Context, _ string, _ string, _ stepEnvironment, output io.Writer) error {
		_, _ = io.WriteString(output, os.Getenv("DATABASE_URL"))
		return nil
	})
	result, err := runner.Run(context.Background(), Request{
		ProjectID: "demo", WorkspacePath: "/work/demo", Commands: []string{"verify"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.OutputTail, "private-password") || !strings.Contains(result.OutputTail, "[REDACTED]") {
		t.Fatalf("credential URL was not redacted: %q", result.OutputTail)
	}
}

func TestRunnerUsesOneEnvironmentSnapshotForEveryStep(t *testing.T) {
	const initial = "initial-verification-token"
	const rotated = "rotated-verification-token"
	t.Setenv("VERIFY_ROTATING_TOKEN", initial)
	runner := newTestRunner(func(_ context.Context, _ string, command string, env stepEnvironment, output io.Writer) error {
		if command == "rotate" {
			return os.Setenv("VERIFY_ROTATING_TOKEN", rotated)
		}
		_, _ = io.WriteString(output, env.Value("VERIFY_ROTATING_TOKEN"))
		return nil
	})
	result, err := runner.Run(context.Background(), Request{
		ProjectID: "demo", WorkspacePath: "/work/demo", Commands: []string{"rotate", "print"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.OutputTail, rotated) {
		t.Fatalf("second step observed environment mutation after the run began: %q", result.OutputTail)
	}
}

func TestRedactingWriterMatchesASecretSplitAcrossWrites(t *testing.T) {
	var output bytes.Buffer
	redactor := newRedactingWriter(&output, map[string]string{"TOKEN": "split-secret"})
	if _, err := redactor.Write([]byte("before split-")); err != nil {
		t.Fatal(err)
	}
	if _, err := redactor.Write([]byte("secret after")); err != nil {
		t.Fatal(err)
	}
	if err := redactor.Finalize(); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "before [REDACTED] after"; got != want {
		t.Fatalf("redacted output = %q, want %q", got, want)
	}
}

func TestRedactingWriterWaitsForOverlappingLongerSecret(t *testing.T) {
	var output bytes.Buffer
	redactor := newRedactingWriter(&output, map[string]string{
		"PREFIX": "sk-",
		"TOKEN":  "sk-abcdef123456",
	})
	if _, err := redactor.Write([]byte("sk-")); err != nil {
		t.Fatal(err)
	}
	if _, err := redactor.Write([]byte("abcdef123456")); err != nil {
		t.Fatal(err)
	}
	if err := redactor.Finalize(); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "[REDACTED]"; got != want {
		t.Fatalf("overlapping redaction = %q, want %q", got, want)
	}
}

type countingWriter struct {
	calls int
	bytes int
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.calls++
	w.bytes += len(p)
	return len(p), nil
}

func TestRedactingWriterBatchesLargeSafeOutput(t *testing.T) {
	var output countingWriter
	redactor := newRedactingWriter(&output, map[string]string{"TOKEN": "not-in-the-output"})
	payload := bytes.Repeat([]byte("x"), 1<<20)
	if _, err := redactor.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := redactor.Finalize(); err != nil {
		t.Fatal(err)
	}
	if output.bytes != len(payload) || output.calls > 10 {
		t.Fatalf("large safe output used %d writes for %d bytes", output.calls, output.bytes)
	}
}

func TestRunnerAppliesEachStepTimeout(t *testing.T) {
	runner := newTestRunner(func(ctx context.Context, _ string, _ string, _ stepEnvironment, _ io.Writer) error {
		<-ctx.Done()
		return ctx.Err()
	})
	runner.timeoutUnit = 10 * time.Millisecond
	result, err := runner.Run(context.Background(), Request{
		ProjectID: "demo", WorkspacePath: "/work/demo", Commands: []string{"# ao-timeout: 1m\nslow"},
	})
	var stepErr *StepError
	if !errors.As(err, &stepErr) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want timed-out StepError", err)
	}
	if result.Passed || !result.TimedOut || result.FailedStep != 1 || stepErr.Timeout != 10*time.Millisecond {
		t.Fatalf("result = %+v, stepErr = %+v", result, stepErr)
	}
}

func TestRunnerSerializesOneProjectButNotDifferentProjects(t *testing.T) {
	entered := make(chan domain.ProjectID, 3)
	releaseSame := make(chan struct{})
	releaseOther := make(chan struct{})
	var active atomic.Int32
	var maxActive atomic.Int32
	runner := newTestRunner(func(ctx context.Context, _ string, _ string, env stepEnvironment, _ io.Writer) error {
		n := active.Add(1)
		for {
			seen := maxActive.Load()
			if n <= seen || maxActive.CompareAndSwap(seen, n) {
				break
			}
		}
		projectID := domain.ProjectID(env.Value("AO_PROJECT_ID"))
		entered <- projectID
		release := releaseSame
		if projectID == "other" {
			release = releaseOther
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		active.Add(-1)
		return ctx.Err()
	})

	type outcome struct{ err error }
	run := func(project domain.ProjectID) <-chan outcome {
		ch := make(chan outcome, 1)
		go func() {
			_, err := runner.Run(context.Background(), Request{ProjectID: project, WorkspacePath: "/work", Commands: []string{"verify"}})
			ch <- outcome{err: err}
		}()
		return ch
	}
	first := run("same")
	if got := <-entered; got != "same" {
		t.Fatalf("first project = %q", got)
	}
	second := run("same")
	other := run("other")
	if got := <-entered; got != "other" {
		t.Fatalf("concurrent project = %q, want other", got)
	}
	select {
	case got := <-entered:
		t.Fatalf("same project entered concurrently: %q", got)
	case <-time.After(25 * time.Millisecond):
	}
	if maxActive.Load() != 2 {
		t.Fatalf("max active = %d, want two different projects", maxActive.Load())
	}
	releaseOther <- struct{}{}
	if err := (<-other).err; err != nil {
		t.Fatal(err)
	}
	releaseSame <- struct{}{}
	if err := (<-first).err; err != nil {
		t.Fatal(err)
	}
	if got := <-entered; got != "same" {
		t.Fatalf("queued project = %q", got)
	}
	releaseSame <- struct{}{}
	if err := (<-second).err; err != nil {
		t.Fatal(err)
	}
}

func TestRunnerCanCancelWhileWaitingForProject(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	runner := newTestRunner(func(context.Context, string, string, stepEnvironment, io.Writer) error {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return nil
	})
	var first sync.WaitGroup
	first.Add(1)
	go func() {
		defer first.Done()
		_, _ = runner.Run(context.Background(), Request{ProjectID: "demo", WorkspacePath: "/work", Commands: []string{"verify"}})
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := runner.Run(ctx, Request{ProjectID: "demo", WorkspacePath: "/work", Commands: []string{"verify"}}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting run err = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("executor calls = %d, want only the active run", calls.Load())
	}
	close(release)
	first.Wait()
}

func TestRunnerValidatesRequestBeforeExecution(t *testing.T) {
	runner := newTestRunner(func(context.Context, string, string, stepEnvironment, io.Writer) error {
		t.Fatal("executor should not run")
		return nil
	})
	for _, request := range []Request{
		{WorkspacePath: "/work", Commands: []string{"verify"}},
		{ProjectID: "demo", Commands: []string{"verify"}},
	} {
		if _, err := runner.Run(context.Background(), request); err == nil {
			t.Fatalf("request %+v should fail", request)
		}
	}
}

func newTestRunner(execute stepExecutor) *Runner {
	return &Runner{
		execute: execute, timeoutUnit: time.Minute, tailBytes: defaultOutputTailBytes,
		projects: make(map[domain.ProjectID]*projectGate), now: time.Now,
	}
}
