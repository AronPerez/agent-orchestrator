package cdc

import "testing"

// The decision_updated wire name is written by the decision_calls triggers and
// matched by the desktop event transport; both sides must agree exactly.
func TestDecisionUpdatedEventType(t *testing.T) {
	if EventDecisionUpdated != "decision_updated" {
		t.Fatalf("EventDecisionUpdated = %q, want decision_updated", EventDecisionUpdated)
	}
}

// The decision_sync_updated wire name is written by the decision_sync_peers and
// decision_sync_conflicts triggers and matched by the desktop event transport.
func TestDecisionSyncUpdatedEventType(t *testing.T) {
	if EventDecisionSyncUpdated != "decision_sync_updated" {
		t.Fatalf("EventDecisionSyncUpdated = %q, want decision_sync_updated", EventDecisionSyncUpdated)
	}
}
