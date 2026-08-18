package browser

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

type fakeProjects struct {
	result project.GetResult
	err    error
}

func (f fakeProjects) Get(_ context.Context, _ domain.ProjectID) (project.GetResult, error) {
	return f.result, f.err
}

// optedIn builds a project reader whose project has the persistent-profile
// opt-in set to want.
func optedIn(id string, want bool) fakeProjects {
	return fakeProjects{result: project.GetResult{Project: &project.Project{
		ID:     domain.ProjectID(id),
		Config: &domain.ProjectConfig{BrowserPersistentProfile: want},
	}}}
}

type fakeSessions struct {
	session domain.Session
	err     error
}

func (f fakeSessions) Get(_ context.Context, _ domain.SessionID) (domain.Session, error) {
	return f.session, f.err
}

type fakeRuntime struct {
	action     string
	profileKey string
}

func (f *fakeRuntime) Status() browserruntime.Status {
	return browserruntime.Status{Connected: true}
}

func (f *fakeRuntime) Execute(
	_ context.Context,
	_ domain.SessionID,
	action string,
	_ map[string]interface{},
	profileKey string,
) (browserruntime.Result, error) {
	f.action = action
	f.profileKey = profileKey
	return browserruntime.Result{RequestID: "r1"}, nil
}

func TestServiceRequiresOwningCapabilityAndLiveSession(t *testing.T) {
	authority := NewAuthority()
	token, verifier, err := authority.Issue("s1")
	if err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{}
	service := New(fakeSessions{session: domain.Session{SessionRecord: domain.SessionRecord{
		ID:       "s1",
		Metadata: domain.SessionMetadata{BrowserCapabilityVerifier: verifier},
	}}}, nil, runtime, authority)

	if _, err := service.Status(context.Background(), "s1", "wrong"); apiErrorCode(err) != "BROWSER_CAPABILITY_INVALID" {
		t.Fatalf("wrong capability error = %v", err)
	}
	if _, err := service.Status(context.Background(), "s1", token); err != nil {
		t.Fatalf("valid capability: %v", err)
	}
	if _, action, err := service.Execute(context.Background(), "s1", token, " SNAPSHOT ", nil); err != nil || action != "snapshot" || runtime.action != "snapshot" {
		t.Fatalf("execute action=%q runtime=%q err=%v", action, runtime.action, err)
	}
	if _, action, err := service.Execute(context.Background(), "s1", token, "dblclick", nil); err != nil || action != "dblclick" || runtime.action != "dblclick" {
		t.Fatalf("expanded action=%q runtime=%q err=%v", action, runtime.action, err)
	}
	if _, action, err := service.Execute(context.Background(), "s1", token, "DEVTOOLS-OPEN", nil); err != nil || action != "devtools-open" || runtime.action != "devtools-open" {
		t.Fatalf("devtools action=%q runtime=%q err=%v", action, runtime.action, err)
	}
	for _, action := range []string{"devtools-toggle", "devtools-focus"} {
		if _, _, err := service.Execute(context.Background(), "s1", token, action, nil); apiErrorCode(err) != "BROWSER_ACTION_UNSUPPORTED" {
			t.Fatalf("agent-facing %s error = %v", action, err)
		}
	}
	if _, _, err := service.Execute(context.Background(), "s1", token, "agent-browser-run", nil); apiErrorCode(err) != "BROWSER_ACTION_UNSUPPORTED" {
		t.Fatalf("removed nested action error = %v", err)
	}
	if _, _, err := service.Execute(context.Background(), "s1", token, "eval", nil); apiErrorCode(err) != "BROWSER_ACTION_UNSUPPORTED" {
		t.Fatalf("unsupported action error = %v", err)
	}

	terminated := New(
		fakeSessions{session: domain.Session{SessionRecord: domain.SessionRecord{ID: "s1", IsTerminated: true}}},
		nil,
		runtime,
		authority,
	)
	if _, err := terminated.Status(context.Background(), "s1", token); apiErrorCode(err) != "SESSION_TERMINATED" {
		t.Fatalf("terminated error = %v", err)
	}
}

func TestAuthorityUsesLaunchScopedSessionSecrets(t *testing.T) {
	authority := NewAuthority()
	firstToken, firstVerifier, err := authority.Issue("s1")
	if err != nil {
		t.Fatal(err)
	}
	secondToken, secondVerifier, err := authority.Issue("s1")
	if err != nil {
		t.Fatal(err)
	}
	otherToken, _, err := authority.Issue("s2")
	if err != nil {
		t.Fatal(err)
	}
	if firstToken == "" || firstToken == secondToken || firstToken == otherToken || firstVerifier == secondVerifier {
		t.Fatal("issued capabilities are not random and launch-scoped")
	}
}

func TestAuthorityValidatesDurableVerifierAcrossDaemonReplacement(t *testing.T) {
	first := NewAuthority()
	token, verifier, err := first.Issue("s1")
	if err != nil {
		t.Fatal(err)
	}
	replacement := NewAuthority()
	if !replacement.Valid("s1", token, verifier) {
		t.Fatal("replacement daemon rejected the surviving worker capability")
	}
	if replacement.Valid("s2", token, verifier) || replacement.Valid("s1", verifier, verifier) {
		t.Fatal("verifier authorized a different session or worked as a bearer token")
	}
}

func apiErrorCode(err error) string {
	var target *apierr.Error
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}

// The daemon is the only side that knows a session's project, so it is the only
// side that can decide this. Every path that cannot produce a confident "yes"
// must fall back to the isolated default: failing closed here costs a re-login,
// while failing open shares a cookie jar nobody asked to share.
func TestServiceResolvesProfileKeyFromProjectConfig(t *testing.T) {
	sessionIn := func(projectID domain.ProjectID) fakeSessions {
		return fakeSessions{session: domain.Session{SessionRecord: domain.SessionRecord{
			ID: "s1", ProjectID: projectID,
		}}}
	}

	for _, tc := range []struct {
		name     string
		sessions fakeSessions
		projects projectReader
		want     string
	}{
		{
			name: "opted in yields the project id", sessions: sessionIn("proj-alpha"),
			projects: optedIn("proj-alpha", true), want: "proj-alpha",
		},
		{
			name:     "a different project yields ITS OWN id, never a shared one",
			sessions: sessionIn("proj-beta"), projects: optedIn("proj-beta", true), want: "proj-beta",
		},
		{
			name: "not opted in stays ephemeral", sessions: sessionIn("proj-alpha"),
			projects: optedIn("proj-alpha", false), want: "",
		},
		{
			name: "no config at all stays ephemeral", sessions: sessionIn("proj-alpha"),
			projects: fakeProjects{result: project.GetResult{Project: &project.Project{ID: "proj-alpha"}}},
			want:     "",
		},
		{
			name: "a degraded project stays ephemeral", sessions: sessionIn("proj-alpha"),
			projects: fakeProjects{result: project.GetResult{Degraded: &project.Degraded{ID: "proj-alpha"}}},
			want:     "",
		},
		{
			name: "an unreadable project stays ephemeral", sessions: sessionIn("proj-alpha"),
			projects: fakeProjects{err: errors.New("boom")}, want: "",
		},
		{
			name: "a session with no project stays ephemeral", sessions: sessionIn(""),
			projects: optedIn("proj-alpha", true), want: "",
		},
		{
			name: "no project reader wired stays ephemeral", sessions: sessionIn("proj-alpha"),
			projects: nil, want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authority := NewAuthority()
			token, verifier, err := authority.Issue("s1")
			if err != nil {
				t.Fatal(err)
			}
			sessions := tc.sessions
			sessions.session.Metadata.BrowserCapabilityVerifier = verifier
			runtime := &fakeRuntime{}
			service := New(sessions, tc.projects, runtime, authority)

			if _, _, err := service.Execute(context.Background(), "s1", token, "snapshot", nil); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if runtime.profileKey != tc.want {
				t.Fatalf("profileKey = %q, want %q", runtime.profileKey, tc.want)
			}

			// Status must report the same answer the command path acts on, or the
			// user is told one thing while another is true.
			status, err := service.Status(context.Background(), "s1", token)
			if err != nil {
				t.Fatalf("status: %v", err)
			}
			if status.PersistentProfile != (tc.want != "") {
				t.Fatalf("status.PersistentProfile = %v, want %v", status.PersistentProfile, tc.want != "")
			}
		})
	}
}
