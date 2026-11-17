// Package verification runs project-owned verification commands in a selected
// workspace. A Runner serializes commands per project, so normal done checks
// and future scratch-worktree checks share one resource boundary.
package verification

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const defaultOutputTailBytes = 64 * 1024

// Request is one run of a project's configured verification commands. The
// workspace is supplied by the caller so the same runner can verify a worker's
// worktree or a scratch merged-branch worktree.
type Request struct {
	ProjectID     domain.ProjectID
	WorkspacePath string
	Commands      []string
	Env           map[string]string
	// Output receives the same bounded, redacted tail returned in Result. It is
	// optional; callers that persist evidence can write it to an artifact file.
	Output io.Writer
}

// Result is the observed outcome of a verification run. Passed is false when a
// command failed, timed out, or the request context was cancelled.
type Result struct {
	Passed      bool
	FailedStep  int
	TimedOut    bool
	OutputTail  string
	StartedAt   time.Time
	CompletedAt time.Time
}

// StepError reports the first verification command that did not complete
// successfully. It never includes the command text, which may itself contain a
// configured secret.
type StepError struct {
	Step     int
	TimedOut bool
	Timeout  time.Duration
	Cause    error
}

func (e *StepError) Error() string {
	if e.TimedOut {
		return fmt.Sprintf("verification step %d timed out after %s", e.Step, e.Timeout)
	}
	return fmt.Sprintf("verification step %d failed: %v", e.Step, e.Cause)
}

func (e *StepError) Unwrap() error { return e.Cause }

type stepExecutor func(context.Context, string, string, map[string]string, io.Writer) error

// Runner executes verification commands. Keep one daemon-owned Runner so its
// per-project gate is shared by all verification callers.
type Runner struct {
	mu       sync.Mutex
	projects map[domain.ProjectID]*projectGate

	execute     stepExecutor
	timeoutUnit time.Duration
	tailBytes   int
	now         func() time.Time
}

type projectGate struct {
	token chan struct{}
	refs  int
}

// NewRunner constructs the shared host runner.
func NewRunner() *Runner {
	return &Runner{
		projects: make(map[domain.ProjectID]*projectGate),
		execute:  runWorkspaceStep, timeoutUnit: time.Minute,
		tailBytes: defaultOutputTailBytes, now: time.Now,
	}
}

// Run executes every non-empty command in order and stops on the first
// failure. Each step has the # ao-timeout limit carried in the command, or the
// default project-script limit when it has no marker.
func (r *Runner) Run(ctx context.Context, request Request) (result Result, runErr error) {
	steps := verificationSteps(request.Commands)
	if request.ProjectID == "" {
		return result, errors.New("verification project id is required")
	}
	if len(steps) > 0 && strings.TrimSpace(request.WorkspacePath) == "" {
		return result, errors.New("verification workspace path is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.ensureDefaults()
	result.StartedAt = r.now().UTC()
	defer func() {
		result.CompletedAt = r.now().UTC()
	}()

	release, err := r.acquire(ctx, request.ProjectID)
	if err != nil {
		return result, err
	}
	defer release()

	// Keep enough lookbehind for a configured value that starts just before the
	// final evidence window. Redact first and crop second, so a secret cannot
	// leak only because the raw tail retained a suffix of it.
	tail := &tailWriter{max: r.tailBytes + longestConfiguredValue(request.Env)}
	env := agentlaunch.MergeEnv(request.Env, map[string]string{
		"AO_PROJECT_ID":    string(request.ProjectID),
		"AO_WORKTREE_PATH": request.WorkspacePath,
	})
	result.Passed = true
	for index, step := range steps {
		command, minutes := domain.ScriptStepMinutes(step)
		command = strings.TrimSpace(command)
		if minutes == 0 {
			minutes = domain.DefaultScriptStepTimeoutMinutes
		}
		timeout := time.Duration(minutes) * r.timeoutUnit
		_, _ = fmt.Fprintf(tail, "Verification step %d of %d\n", index+1, len(steps))
		stepCtx, cancel := context.WithTimeout(ctx, timeout)
		err := r.execute(stepCtx, request.WorkspacePath, command, env, tail)
		timedOut := err != nil && ctx.Err() == nil && errors.Is(stepCtx.Err(), context.DeadlineExceeded)
		if timedOut {
			err = errors.Join(err, stepCtx.Err())
		}
		cancel()
		if err == nil {
			continue
		}
		result.Passed = false
		result.FailedStep = index + 1
		result.TimedOut = timedOut
		runErr = &StepError{
			Step: index + 1, TimedOut: timedOut, Timeout: timeout,
			Cause: agentlaunch.RedactError(err, request.Env),
		}
		break
	}

	result.OutputTail = boundedTail(agentlaunch.RedactValues(tail.String(), request.Env), r.tailBytes)
	if request.Output != nil && result.OutputTail != "" {
		if _, err := io.WriteString(request.Output, result.OutputTail); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("write verification output: %w", err))
			result.Passed = false
		}
	}
	return result, runErr
}

func (r *Runner) ensureDefaults() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.projects == nil {
		r.projects = make(map[domain.ProjectID]*projectGate)
	}
	if r.execute == nil {
		r.execute = runWorkspaceStep
	}
	if r.timeoutUnit <= 0 {
		r.timeoutUnit = time.Minute
	}
	if r.tailBytes <= 0 {
		r.tailBytes = defaultOutputTailBytes
	}
	if r.now == nil {
		r.now = time.Now
	}
}

func (r *Runner) acquire(ctx context.Context, projectID domain.ProjectID) (func(), error) {
	r.mu.Lock()
	gate := r.projects[projectID]
	if gate == nil {
		gate = &projectGate{token: make(chan struct{}, 1)}
		gate.token <- struct{}{}
		r.projects[projectID] = gate
	}
	gate.refs++
	r.mu.Unlock()

	select {
	case <-ctx.Done():
		r.dropGateRef(projectID, gate)
		return nil, ctx.Err()
	case <-gate.token:
		return func() {
			gate.token <- struct{}{}
			r.dropGateRef(projectID, gate)
		}, nil
	}
}

func (r *Runner) dropGateRef(projectID domain.ProjectID, gate *projectGate) {
	r.mu.Lock()
	defer r.mu.Unlock()
	gate.refs--
	if gate.refs == 0 && r.projects[projectID] == gate {
		delete(r.projects, projectID)
	}
}

func verificationSteps(commands []string) []string {
	steps := make([]string, 0, len(commands))
	for _, step := range commands {
		_, command, _ := domain.ParseTimedScriptCommand(step)
		if strings.TrimSpace(command) == "" {
			continue
		}
		steps = append(steps, step)
	}
	return steps
}

type tailWriter struct {
	mu   sync.Mutex
	data []byte
	max  int
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	w.data = append(w.data, p...)
	if len(w.data) > w.max {
		w.data = append([]byte(nil), w.data[len(w.data)-w.max:]...)
	}
	return n, nil
}

func (w *tailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.data)
}

func boundedTail(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	return value[len(value)-maxBytes:]
}

func longestConfiguredValue(env map[string]string) int {
	longest := 0
	for _, value := range env {
		if len(value) > longest {
			longest = len(value)
		}
	}
	return longest
}
