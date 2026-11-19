package notify

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func enrich(intent Intent) (domain.NotificationRecord, error) {
	if intent.Type.TargetsDecision() {
		return enrichDecision(intent)
	}
	rec := domain.NotificationRecord{
		SessionID: intent.SessionID,
		ProjectID: intent.ProjectID,
		PRURL:     strings.TrimSpace(intent.PRURL),
		Type:      intent.Type,
		Status:    domain.NotificationUnread,
		CreatedAt: intent.CreatedAt,
		SourceKey: strings.TrimSpace(intent.SourceKey),
	}
	if !intent.Type.Valid() {
		return domain.NotificationRecord{}, domain.ErrInvalidNotificationType
	}
	if intent.Type != domain.NotificationNeedsInput && intent.Type != domain.NotificationSessionStalled && rec.PRURL == "" {
		return domain.NotificationRecord{}, domain.ErrInvalidNotificationRecord
	}
	rec.Title = titleForIntent(intent)
	rec.Body = bodyForIntent(intent)
	if err := rec.Validate(); err != nil {
		return domain.NotificationRecord{}, err
	}
	return rec, nil
}

func titleForIntent(intent Intent) string {
	switch intent.Type {
	case domain.NotificationNeedsInput:
		return fmt.Sprintf("%s needs your input", sessionLabel(intent))
	case domain.NotificationSessionStalled:
		return fmt.Sprintf("%s stalled", sessionLabel(intent))
	case domain.NotificationReadyToMerge:
		if title := strings.TrimSpace(intent.PRTitle); title != "" {
			if label := prLabel(intent); label != "PR" {
				return fmt.Sprintf("%s · %s", title, label)
			}
			return title
		}
		return fmt.Sprintf("%s is ready to merge", prLabel(intent))
	case domain.NotificationPRMerged:
		return fmt.Sprintf("%s merged", prLabel(intent))
	case domain.NotificationPRClosedUnmerged:
		return fmt.Sprintf("%s closed", prLabel(intent))
	case domain.NotificationReviewCompleted:
		return fmt.Sprintf("Review complete for %s", prLabel(intent))
	case domain.NotificationReviewChangesRequested:
		return fmt.Sprintf("Review found changes for %s", prLabel(intent))
	default:
		return "Notification"
	}
}

func bodyForIntent(intent Intent) string {
	switch intent.Type {
	case domain.NotificationNeedsInput:
		return "Your agent is waiting on you to continue."
	case domain.NotificationSessionStalled:
		return "Your worker stopped without reporting after one automatic nudge."
	case domain.NotificationReadyToMerge:
		if session := sessionLabel(intent); session != "session" {
			return fmt.Sprintf("PR from session %s is ready to merge. CI passed with no blocking review feedback.", session)
		}
		return "CI passed with no blocking review feedback."
	case domain.NotificationPRMerged:
		title := strings.TrimSpace(intent.PRTitle)
		if target := strings.TrimSpace(intent.PRTargetBranch); title != "" && target != "" {
			return fmt.Sprintf("%s is now on %s.", title, target)
		}
		if title != "" {
			return fmt.Sprintf("%s was merged.", title)
		}
		return "The pull request was merged."
	case domain.NotificationPRClosedUnmerged:
		if title := strings.TrimSpace(intent.PRTitle); title != "" {
			return fmt.Sprintf("%s was closed without merging. Reopen it if this wasn't intended.", title)
		}
		return "Closed without merging. Reopen it if this wasn't intended."
	case domain.NotificationReviewCompleted:
		return "Orchestrator finished reviewing this pull request and approved it."
	case domain.NotificationReviewChangesRequested:
		return "Orchestrator finished reviewing this pull request and found changes to address."
	default:
		return ""
	}
}

func sessionLabel(intent Intent) string {
	if v := strings.TrimSpace(intent.SessionDisplayName); v != "" {
		return v
	}
	if intent.SessionID != "" {
		return string(intent.SessionID)
	}
	return "session"
}

func prLabel(intent Intent) string {
	if intent.PRNumber > 0 {
		return fmt.Sprintf("PR #%d", intent.PRNumber)
	}
	if title := strings.TrimSpace(intent.PRTitle); title != "" {
		return "PR " + title
	}
	return "PR"
}

// enrichDecision renders the approved decision copy. The title names where the
// call lives; the body is exactly the call's title. Nothing else from the call
// (context, options, notes) is available here, so nothing else can leak into a
// banner or lock screen.
func enrichDecision(intent Intent) (domain.NotificationRecord, error) {
	callID := strings.TrimSpace(intent.DecisionCallID)
	origin := strings.TrimSpace(intent.DecisionOriginHostID)
	body := oneLine(intent.DecisionTitle)
	if callID == "" || origin == "" || body == "" || utf8.RuneCountInString(body) > domain.MaxDecisionTitle {
		return domain.NotificationRecord{}, domain.ErrInvalidNotificationRecord
	}
	project := oneLine(intent.DecisionProjectName)
	if project == "" {
		project = oneLine(string(intent.ProjectID))
	}
	if project == "" {
		project = "No project"
	}
	title := "Decision needed · " + project
	if stream := oneLine(intent.DecisionStreamName); stream != "" {
		title += " · " + stream
	}
	rec := domain.NotificationRecord{
		Type:                 intent.Type,
		Title:                title,
		Body:                 body,
		Status:               domain.NotificationUnread,
		CreatedAt:            intent.CreatedAt,
		SourceKey:            DecisionSourceKey(callID),
		DecisionCallID:       callID,
		DecisionOriginHostID: origin,
	}
	if err := rec.Validate(); err != nil {
		return domain.NotificationRecord{}, err
	}
	return rec, nil
}

// DecisionSourceKey is the durable dedupe key for one call's notification.
func DecisionSourceKey(callID string) string { return "decision:" + callID }

// oneLine keeps recorded labels to one visible line: control characters
// (newlines, tabs) and bidi controls (marks, embeddings, overrides, isolates)
// become spaces, and runs collapse. Other format characters stay: zero-width
// joiners and non-joiners are part of emoji sequences and of words in many
// scripts, so a valid title reaches the banner exactly.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || domain.IsBidiControl(r) {
			return ' '
		}
		return r
	}, s)), " ")
}
