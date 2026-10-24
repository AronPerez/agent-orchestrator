package domain

import (
	"strings"
	"testing"
	"time"
)

func TestReportDeliveryEnvelopeRoundTrip(t *testing.T) {
	message := WrapReportDelivery("report-batch:abc_123", "Reports since your previous turn:\ncomplete")
	if got, ok := ReportDeliveryID(message); !ok || got != "report-batch:abc_123" {
		t.Fatalf("ReportDeliveryID() = %q, %v", got, ok)
	}
	for _, invalid := range []string{
		`<ao-report-delivery id="bad value">x`,
		`<ao-report-delivery id="bad\"value">x`,
		"ordinary prompt",
	} {
		if got, ok := ReportDeliveryID(invalid); ok {
			t.Fatalf("ReportDeliveryID(%q) = %q, true", invalid, got)
		}
	}
}

// Claude Code can drop the head of a large typed delivery or wrap arbitrary
// slices of it in <pasted_content> blocks. A paste-wrapped envelope is still
// complete and is acknowledged; a head-less one must not be, because the
// reports in the lost head never reached the orchestrator.
func TestReportDeliveryIDSurvivesTUIInputMangling(t *testing.T) {
	const id = "report-batch:rpt_92472097-ef55-4e90-81dd-92b5b443f178"
	message := WrapReportDelivery(id, "Reports since your previous turn:\n"+strings.Repeat("[checkpoint] worker detail\n", 200))
	wrap := func(s string) string {
		return "<pasted_content id=\"8437\">\n" + s + "\n</pasted_content id=\"8437\">\n\n\n"
	}
	footer := strings.LastIndex(message, "<ao-report-delivery-end")
	for name, observed := range map[string]string{
		"head paste wrapped":        wrap(message[:1536]) + message[1536:],
		"opening id split by paste": message[:30] + "\n\n" + wrap(message[30:]),
		"whole message pasted":      wrap(message),
	} {
		if got, ok := ReportDeliveryID(observed); !ok || got != id {
			t.Errorf("%s: ReportDeliveryID() = %q, %v", name, got, ok)
		}
		if got, ok := CoordinationDeliveryID(observed); !ok || got != id {
			t.Errorf("%s: CoordinationDeliveryID() = %q, %v", name, got, ok)
		}
	}
	for name, observed := range map[string]string{
		"head truncated":              message[1081:],
		"head truncated, tail pasted": message[1081:2000] + "\n\n" + wrap(message[2000:]),
		"footer id split by paste":    message[1081:footer+40] + "\n\n" + wrap(message[footer+40:]),
	} {
		if got, ok := ReportDeliveryID(observed); ok {
			t.Errorf("%s: head-less delivery acknowledged as %q", name, got)
		}
		// Still AO coordination, under a turn id that never acknowledges the batch.
		if got, ok := CoordinationDeliveryID(observed); !ok || got != TruncatedReportDeliveryTurnID(id) || got == id {
			t.Errorf("%s: CoordinationDeliveryID() = %q, %v", name, got, ok)
		}
	}
	for name, observed := range map[string]string{
		// A human quoting a delivery for diagnosis keeps their instruction.
		"human quote":           "Please explain this failure:\n" + WrapReportDelivery("report-batch:abc123", "worker failed"),
		"footer not at end":     message[1081:] + "\nthanks",
		"footer cut":            message[1081 : len(message)-3],
		"markup in footer id":   `tail <ao-report-delivery-end id="abc">junk"/>`,
		"empty footer id":       `tail <ao-report-delivery-end id=""/>`,
		"footer prefix overlap": `<ao-report-delivery-end id="/>`,
	} {
		if got, ok := CoordinationDeliveryID(observed); ok {
			t.Errorf("%s: classified as coordination %q", name, got)
		}
	}
}

func TestSessionDeliveryID(t *testing.T) {
	message := WrapSessionDelivery("session-send:abc123", "orchestrator direction")
	if got, ok := SessionDeliveryID(message); !ok || got != "session-send:abc123" {
		t.Fatalf("SessionDeliveryID() = %q, %v", got, ok)
	}
	if _, ok := SessionDeliveryID(WrapReportDelivery("report-batch:abc123", "report")); ok {
		t.Fatal("SessionDeliveryID() recognized a report delivery")
	}
}

func TestValidateReportContent(t *testing.T) {
	pr := "https://github.com/owner/repo/pull/42"
	tests := []struct {
		name          string
		state         ReportState
		note, message string
		outputs       []ReportOutput
		wantErr       bool
	}{
		{name: "free form", message: strings.Repeat("界", 1000)},
		{name: "output only", outputs: []ReportOutput{{Kind: ReportOutputArtifact, Reference: "opaque://anything"}, {Kind: ReportOutputPRCreated, Reference: pr}, {Kind: ReportOutputPRReviewed, Reference: pr}}},
		{name: "state only", state: ReportCheckpoint, note: "checkpoint"},
		{name: "state and outputs", state: ReportDone, note: "finished", outputs: []ReportOutput{{Kind: ReportOutputArtifact, Reference: "result"}}},
		{name: "empty", wantErr: true},
		{name: "message with output", message: "message", outputs: []ReportOutput{{Kind: ReportOutputArtifact, Reference: "result"}}, wantErr: true},
		{name: "note without state", note: "note", outputs: []ReportOutput{{Kind: ReportOutputArtifact, Reference: "result"}}, wantErr: true},
		{name: "state missing note", state: ReportDone, wantErr: true},
		{name: "unknown state", state: "working", note: "note", wantErr: true},
		{name: "long message", message: strings.Repeat("x", 1001), wantErr: true},
		{name: "long note", state: ReportStuck, note: strings.Repeat("x", 1001), wantErr: true},
		{name: "empty artifact", outputs: []ReportOutput{{Kind: ReportOutputArtifact}}, wantErr: true},
		{name: "invalid created PR", outputs: []ReportOutput{{Kind: ReportOutputPRCreated, Reference: "https://example.com/o/r/pull/1"}}, wantErr: true},
		{name: "invalid reviewed PR", outputs: []ReportOutput{{Kind: ReportOutputPRReviewed, Reference: "git://github.com/o/r/pull/1"}}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateReportContent(tc.state, tc.note, tc.message, tc.outputs)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestReportRecordValidatesSettlementDeadline(t *testing.T) {
	now := time.Date(2026, 9, 7, 1, 2, 3, 0, time.UTC)
	rec := ReportRecord{ID: "rpt_1", SessionID: "worker", ProjectID: "ao", State: ReportDone, Note: "done", CreatedAt: now, DeliveryState: ReportPending, AvailableAt: now.Add(ReportSettlementWindow), SettlementDeadline: now.Add(ReportSettlementWindow), RepeatCount: 1}
	if err := rec.Validate(); err != nil {
		t.Fatal(err)
	}
	rec.SettlementDeadline = time.Time{}
	if err := rec.Validate(); err == nil {
		t.Fatal("expected missing settlement deadline to fail")
	}
}
