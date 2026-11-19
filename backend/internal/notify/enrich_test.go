package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestEnrichReadyToMergePrioritizesPRContext(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:               domain.NotificationReadyToMerge,
		SessionID:          "sess-1",
		ProjectID:          "proj-1",
		PRURL:              "https://github.com/acme/app/pull/67",
		SessionDisplayName: "Checkout flow",
		PRNumber:           67,
		PRTitle:            "Fix checkout totals",
		CreatedAt:          time.Date(2026, 8, 5, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich ready notification: %v", err)
	}

	if want := "Fix checkout totals · PR #67"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
	if want := "PR from session Checkout flow is ready to merge. CI passed with no blocking review feedback."; rec.Body != want {
		t.Fatalf("body = %q, want %q", rec.Body, want)
	}
}

func TestEnrichReadyToMergeFallsBackWithoutPRTitle(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:      domain.NotificationReadyToMerge,
		SessionID: "sess-1",
		ProjectID: "proj-1",
		PRURL:     "https://github.com/acme/app/pull/67",
		PRNumber:  67,
		CreatedAt: time.Date(2026, 8, 5, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich ready notification: %v", err)
	}

	if want := "PR #67 is ready to merge"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
}

func TestEnrichSessionStalledUsesSessionContextWithoutPR(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type: domain.NotificationSessionStalled, SessionID: "sess-1", ProjectID: "proj-1",
		SessionDisplayName: "Checkout flow", SourceKey: "session-contract-stall:sess-1:1",
		CreatedAt: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich stalled notification: %v", err)
	}
	if rec.Title != "Checkout flow stalled" {
		t.Fatalf("title = %q", rec.Title)
	}
	if rec.Body != "Your worker stopped without reporting after one automatic nudge." || rec.PRURL != "" {
		t.Fatalf("record = %+v", rec)
	}
}

func decisionIntent() Intent {
	return Intent{
		Type:                 domain.NotificationDecisionNeeded,
		DecisionCallID:       "dec_1",
		DecisionOriginHostID: "h_origin",
		DecisionProjectName:  "agent-orchestrator",
		DecisionTitle:        "Ship the bell badge now?",
		CreatedAt:            time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC),
	}
}

func TestEnrichDecisionProjectOnly(t *testing.T) {
	t.Parallel()
	rec, err := enrich(decisionIntent())
	if err != nil {
		t.Fatalf("enrich decision: %v", err)
	}
	if rec.Title != "Decision needed · agent-orchestrator" || rec.Body != "Ship the bell badge now?" {
		t.Fatalf("copy title=%q body=%q", rec.Title, rec.Body)
	}
	if rec.SourceKey != "decision:dec_1" || rec.DecisionCallID != "dec_1" || rec.DecisionOriginHostID != "h_origin" {
		t.Fatalf("identity = %+v", rec)
	}
	if rec.SessionID != "" || rec.ProjectID != "" || rec.PRURL != "" {
		t.Fatalf("decision notification carried a local target: %+v", rec)
	}
}

func TestEnrichDecisionProjectAndStream(t *testing.T) {
	t.Parallel()
	intent := decisionIntent()
	intent.DecisionStreamName = "Bell work"
	rec, err := enrich(intent)
	if err != nil {
		t.Fatalf("enrich decision: %v", err)
	}
	if rec.Title != "Decision needed · agent-orchestrator · Bell work" {
		t.Fatalf("title = %q", rec.Title)
	}
}

func TestEnrichDecisionNoProject(t *testing.T) {
	t.Parallel()
	intent := decisionIntent()
	intent.DecisionProjectName = ""
	rec, err := enrich(intent)
	if err != nil || rec.Title != "Decision needed · No project" {
		t.Fatalf("title=%q err=%v", rec.Title, err)
	}
	intent.ProjectID = "proj-remote"
	rec, err = enrich(intent)
	if err != nil || rec.Title != "Decision needed · proj-remote" || rec.ProjectID != "" {
		t.Fatalf("project id fallback title=%q project=%q err=%v", rec.Title, rec.ProjectID, err)
	}
}

func TestEnrichDecisionNormalizesLabelsToOneLine(t *testing.T) {
	t.Parallel()
	intent := decisionIntent()
	intent.DecisionProjectName = "agent\n orchestrator\u202e"
	intent.DecisionStreamName = "bell\twork"
	rec, err := enrich(intent)
	if err != nil {
		t.Fatalf("enrich decision: %v", err)
	}
	if rec.Title != "Decision needed · agent orchestrator · bell work" {
		t.Fatalf("title = %q", rec.Title)
	}
}

func TestEnrichDecisionRejectsMissingIdentityOrTitle(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*Intent){
		"call":   func(i *Intent) { i.DecisionCallID = "" },
		"origin": func(i *Intent) { i.DecisionOriginHostID = "" },
		"title":  func(i *Intent) { i.DecisionTitle = " " },
		"long":   func(i *Intent) { i.DecisionTitle = strings.Repeat("x", domain.MaxDecisionTitle+1) },
	} {
		intent := decisionIntent()
		mutate(&intent)
		if _, err := enrich(intent); err == nil {
			t.Fatalf("%s: enrich accepted an invalid decision intent", name)
		}
	}
}

func TestEnrichDecisionCopyNeverCarriesContext(t *testing.T) {
	t.Parallel()
	// The intent has no context/options field at all; the copy is exactly the
	// recorded labels and title, so nothing else can reach a lock screen.
	rec, err := enrich(decisionIntent())
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"context", "option", "recommend"} {
		if strings.Contains(strings.ToLower(rec.Title+rec.Body), leak) {
			t.Fatalf("copy contains %q: %q / %q", leak, rec.Title, rec.Body)
		}
	}
}

func TestEnrichDecisionKeepsJoinersInValidTitles(t *testing.T) {
	t.Parallel()
	// A ZWJ emoji sequence and a Persian word written with a ZWNJ are valid
	// titles; the notification must carry them exactly.
	for _, title := range []string{"Ship the \U0001F469\u200D\U0001F4BB build?", "Publish می\u200Cخواهم copy?"} {
		intent := decisionIntent()
		intent.DecisionTitle = title
		intent.DecisionProjectName = "team\u200Dname"
		rec, err := enrich(intent)
		if err != nil {
			t.Fatalf("enrich %q: %v", title, err)
		}
		if rec.Body != title {
			t.Fatalf("body = %q, want the exact title %q", rec.Body, title)
		}
		if rec.Title != "Decision needed · team\u200Dname" {
			t.Fatalf("title = %q", rec.Title)
		}
	}
}

func TestEnrichDecisionStripsBidiControls(t *testing.T) {
	t.Parallel()
	intent := decisionIntent()
	intent.DecisionTitle = "Approve \u202Etxt.exe\u202C now?"
	intent.DecisionStreamName = "a\u2066b\u2069"
	rec, err := enrich(intent)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Body != "Approve txt.exe now?" || rec.Title != "Decision needed · agent-orchestrator · a b" {
		t.Fatalf("title=%q body=%q", rec.Title, rec.Body)
	}
}
