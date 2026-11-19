package domain

import (
	"errors"
	"testing"
	"time"
)

func decisionNotification() NotificationRecord {
	return NotificationRecord{
		ID:                   "n_1",
		Type:                 NotificationDecisionNeeded,
		Title:                "Decision needed · ao",
		Body:                 "Ship it?",
		Status:               NotificationUnread,
		CreatedAt:            time.Unix(100, 0),
		DecisionCallID:       "dec_1",
		DecisionOriginHostID: "h_origin",
		SourceKey:            "decision:dec_1",
	}
}

func TestNotificationDecisionNeededIsValidAndNeedsResolution(t *testing.T) {
	if !NotificationDecisionNeeded.Valid() {
		t.Fatal("decision_needed must be a valid notification type")
	}
	if !NotificationDecisionNeeded.NeedsResolution() {
		t.Fatal("decision_needed stays open until the call leaves Needs you")
	}
	if string(NotificationDecisionNeeded) != "decision_needed" {
		t.Fatalf("wire value = %q", NotificationDecisionNeeded)
	}
}

func TestDecisionNotificationRequiresCallAndOriginButNotSession(t *testing.T) {
	if err := decisionNotification().Validate(); err != nil {
		t.Fatalf("sessionless decision notification rejected: %v", err)
	}
	noCall := decisionNotification()
	noCall.DecisionCallID = ""
	if err := noCall.Validate(); !errors.Is(err, ErrInvalidNotificationRecord) {
		t.Fatalf("missing call id err = %v", err)
	}
	noOrigin := decisionNotification()
	noOrigin.DecisionOriginHostID = ""
	if err := noOrigin.Validate(); !errors.Is(err, ErrInvalidNotificationRecord) {
		t.Fatalf("missing origin err = %v", err)
	}
	noTitle := decisionNotification()
	noTitle.Title = ""
	if err := noTitle.Validate(); !errors.Is(err, ErrInvalidNotificationRecord) {
		t.Fatalf("missing title err = %v", err)
	}
}

func TestSessionNotificationStillRequiresSession(t *testing.T) {
	rec := NotificationRecord{
		ID: "n_2", Type: NotificationNeedsInput, Title: "t", Status: NotificationUnread, CreatedAt: time.Unix(1, 0),
	}
	if err := rec.Validate(); !errors.Is(err, ErrInvalidNotificationRecord) {
		t.Fatalf("sessionless needs_input err = %v", err)
	}
	rec.SessionID = "s_1"
	if err := rec.Validate(); err != nil {
		t.Fatalf("needs_input with session rejected: %v", err)
	}
}

func TestNotificationRejectsAmbiguousTargets(t *testing.T) {
	withSession := decisionNotification()
	withSession.SessionID = "s_1"
	if err := withSession.Validate(); !errors.Is(err, ErrInvalidNotificationRecord) {
		t.Fatalf("decision with session err = %v", err)
	}
	withPR := decisionNotification()
	withPR.PRURL = "https://github.com/o/r/pull/1"
	if err := withPR.Validate(); !errors.Is(err, ErrInvalidNotificationRecord) {
		t.Fatalf("decision with pr err = %v", err)
	}
	sessionWithCall := NotificationRecord{
		ID: "n_3", SessionID: "s_1", Type: NotificationNeedsInput, Title: "t", Status: NotificationUnread,
		CreatedAt: time.Unix(1, 0), DecisionCallID: "dec_1",
	}
	if err := sessionWithCall.Validate(); !errors.Is(err, ErrInvalidNotificationRecord) {
		t.Fatalf("needs_input carrying a call id err = %v", err)
	}
	sessionWithOrigin := sessionWithCall
	sessionWithOrigin.DecisionCallID = ""
	sessionWithOrigin.DecisionOriginHostID = "h_origin"
	if err := sessionWithOrigin.Validate(); !errors.Is(err, ErrInvalidNotificationRecord) {
		t.Fatalf("needs_input carrying an origin err = %v", err)
	}
}
