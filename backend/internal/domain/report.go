package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	reportDeliveryPrefix  = `<ao-report-delivery id="`
	reportDeliveryFooter  = `<ao-report-delivery-end id="`
	sessionDeliveryPrefix = `<ao-session-delivery id="`
)

// pastedContentMarkup matches the wrapper Claude Code inserts when it collapses
// bursts of typed PTY input into paste blocks. Wrappers land at arbitrary byte
// offsets (mid-word, mid-id), so they are removed before ids are matched.
var pastedContentMarkup = regexp.MustCompile(`\n*</?pasted_content(?: id="[^"\n]*")?>\n*`)

// WrapReportDelivery marks an AO-authored report batch so provider prompt
// hooks can correlate semantic acceptance with its durable delivery identity.
// The footer repeats the id so a delivery whose head a TUI dropped is still
// recognised as AO coordination (see CoordinationDeliveryID) and the sender can
// tell it from one that never arrived. It also carries a checksum of the body,
// because a TUI can cut the middle or tail out while leaving the head intact.
func WrapReportDelivery(id, message string) string {
	return fmt.Sprintf("%s%s\">\n%s\n</ao-report-delivery>\n%s%s\" sum=%q/>", reportDeliveryPrefix, id, message, reportDeliveryFooter, id, reportDeliverySum(message))
}

// reportDeliverySum ignores whitespace and paste wrappers: TUIs reflow both
// without losing content, while any dropped text changes the sum.
func reportDeliverySum(body string) string {
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(unwrapPastedContent(body)), "")))
	return hex.EncodeToString(sum[:8])
}

// ReportDeliveryID returns the durable identity from an AO report envelope.
// Only an intact envelope counts: its opening tag, its footer, and a body
// matching the footer checksum. A tail alone is not proof the whole batch
// arrived, and is indistinguishable from quoted text; a head with a cut body
// lost every report in the cut.
// Delivery ids are generated internally and deliberately restricted so a
// malformed prompt cannot manufacture markup or an unbounded correlation key.
func ReportDeliveryID(message string) (string, bool) {
	message = unwrapPastedContent(message)
	id, ok := deliveryID(message, reportDeliveryPrefix)
	if !ok || !reportDeliveryIntact(message, id) {
		return "", false
	}
	return id, true
}

// reportDeliveryIntact checks that a delivery opening with id ends with its
// footer and that the body between them matches the footer checksum.
func reportDeliveryIntact(message, id string) bool {
	message = strings.TrimSpace(message)
	i, footerID, sum := reportDeliveryFooterOf(message)
	start := len(reportDeliveryPrefix) + len(id) + len(`">`)
	if footerID != id || i < start {
		return false
	}
	body, ok := strings.CutSuffix(strings.TrimSpace(message[start:i]), "</ao-report-delivery>")
	return ok && sum == reportDeliverySum(body)
}

// reportDeliveryFooterOf parses the footer that ends message, returning its
// offset (-1 when absent), id, and body checksum.
func reportDeliveryFooterOf(message string) (int, string, string) {
	rest, ok := strings.CutSuffix(message, `"/>`)
	i := strings.LastIndex(rest, reportDeliveryFooter)
	if !ok || i < 0 {
		return -1, "", ""
	}
	id, sum, _ := strings.Cut(rest[i+len(reportDeliveryFooter):], `" sum="`)
	if !validDeliveryID(id) {
		return -1, "", ""
	}
	return i, id, sum
}

// ErrReportDeliveryTruncated reports that an awaited report envelope arrived
// without its head, so the target saw only part of the batch.
var ErrReportDeliveryTruncated = errors.New("report delivery arrived truncated")

// truncatedDeliverySuffix marks the coordination turn of a head-less delivery.
// It never equals the delivery id, so the batch is not acknowledged, but the
// sender awaiting that id can recognise it with TruncatedReportDeliveryTurnID.
const truncatedDeliverySuffix = ":truncated"

// TruncatedReportDeliveryTurnID is the coordination turn id recorded when the
// envelope for id arrives with its head dropped.
func TruncatedReportDeliveryTurnID(id string) string {
	return id + truncatedDeliverySuffix
}

// truncatedReportDeliveryID recognises an AO envelope whose head a TUI dropped:
// it ends with the id footer, and no opening tag for that id appears anywhere.
// A human quoting a whole delivery after their own text keeps the opening tag,
// so their prompt stays human.
func truncatedReportDeliveryID(message string) (string, bool) {
	message = strings.TrimSpace(unwrapPastedContent(message))
	i, id, _ := reportDeliveryFooterOf(message)
	if i < 0 || strings.Contains(message, reportDeliveryPrefix+id+`">`) {
		return "", false
	}
	return id, true
}

func unwrapPastedContent(message string) string {
	if !strings.Contains(message, "pasted_content") {
		return message
	}
	return pastedContentMarkup.ReplaceAllString(message, "")
}

func deliveryID(message, prefix string) (string, bool) {
	message = strings.TrimSpace(message)
	if !strings.HasPrefix(message, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(message, prefix)
	end := strings.Index(rest, `">`)
	if end < 0 || !validDeliveryID(rest[:end]) {
		return "", false
	}
	return rest[:end], true
}

func validDeliveryID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') &&
			(r < '0' || r > '9') && r != ':' && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

const (
	// MaxReportTextCharacters is the hard limit for free-form messages and structured notes.
	MaxReportTextCharacters = 1000
	// ReportBatchFallback is the fixed informational-report fallback deadline.
	ReportBatchFallback = time.Hour
	// ReportSettlementWindow is the fixed window opened by a done report.
	ReportSettlementWindow = 5 * time.Minute
)

// ReportState is an optional worker-originated coordination claim.
type ReportState string

// Supported report states.
const (
	ReportCheckpoint ReportState = "checkpoint"
	ReportNeedsInput ReportState = "needs_input"
	ReportStuck      ReportState = "stuck"
	ReportDone       ReportState = "done"
)

// Valid reports whether s is a supported structured state.
func (s ReportState) Valid() bool {
	switch s {
	case ReportCheckpoint, ReportNeedsInput, ReportStuck, ReportDone:
		return true
	default:
		return false
	}
}

// ReportOutputKind identifies a durable output reference attached to a report.
type ReportOutputKind string

// Supported report output kinds.
const (
	ReportOutputArtifact   ReportOutputKind = "artifact"
	ReportOutputPRCreated  ReportOutputKind = "pr_created"
	ReportOutputPRReviewed ReportOutputKind = "pr_reviewed"
)

// Valid reports whether k is a supported output kind.
func (k ReportOutputKind) Valid() bool {
	switch k {
	case ReportOutputArtifact, ReportOutputPRCreated, ReportOutputPRReviewed:
		return true
	default:
		return false
	}
}

// ReportOutput is an ordered reference to work produced by a worker.
type ReportOutput struct {
	Kind      ReportOutputKind
	Reference string
	Label     string
}

// ReportDeliveryState is the durable outbox lifecycle of a report.
type ReportDeliveryState string

// Report delivery states.
const (
	ReportPending      ReportDeliveryState = "pending"
	ReportClaimed      ReportDeliveryState = "claimed"
	ReportAcknowledged ReportDeliveryState = "acknowledged"
)

// ReportRecord is the durable report and outbox persistence shape. State and
// outputs are independent, and neither changes authoritative session state.
type ReportRecord struct {
	ID        string
	SessionID SessionID
	ProjectID ProjectID
	// SessionDisplayName is resolved at delivery time for presentation. It is
	// intentionally not persisted with the durable report row.
	SessionDisplayName string
	State              ReportState
	Note               string
	Message            string
	Outputs            []ReportOutput
	CreatedAt          time.Time
	DeliveryState      ReportDeliveryState
	AvailableAt        time.Time
	SettlementDeadline time.Time
	RepeatCount        int64
	ClaimToken         string
	ClaimedAt          time.Time
	DeliveryAttempts   int64
	AcknowledgedAt     time.Time
	LastError          string
	DeliveryBatchID    string
}

// ErrInvalidReport reports an invalid report or delivery transition input.
var ErrInvalidReport = errors.New("invalid report")

// Validate checks content, ownership, output, and delivery-state invariants.
func (r ReportRecord) Validate() error {
	if r.ID == "" || r.SessionID == "" || r.ProjectID == "" || r.CreatedAt.IsZero() || r.AvailableAt.IsZero() || r.RepeatCount < 1 {
		return ErrInvalidReport
	}
	if err := ValidateReportContent(r.State, r.Note, r.Message, r.Outputs); err != nil {
		return err
	}
	if r.State == ReportDone {
		if r.SettlementDeadline.IsZero() {
			return ErrInvalidReport
		}
	} else if !r.SettlementDeadline.IsZero() {
		return ErrInvalidReport
	}
	switch r.DeliveryState {
	case ReportPending:
		if r.ClaimToken != "" || !r.ClaimedAt.IsZero() || !r.AcknowledgedAt.IsZero() {
			return ErrInvalidReport
		}
	case ReportClaimed:
		if r.ClaimToken == "" || r.ClaimedAt.IsZero() || !r.AcknowledgedAt.IsZero() {
			return ErrInvalidReport
		}
	case ReportAcknowledged:
		if r.ClaimToken == "" || r.ClaimedAt.IsZero() || r.AcknowledgedAt.IsZero() {
			return ErrInvalidReport
		}
	default:
		return ErrInvalidReport
	}
	if r.DeliveryAttempts < 0 {
		return ErrInvalidReport
	}
	return nil
}

// ValidateReportContent checks the authoritative report payload contract.
func ValidateReportContent(state ReportState, note, message string, outputs []ReportOutput) error {
	if message != "" {
		if state != "" || note != "" || len(outputs) != 0 || strings.TrimSpace(message) == "" || utf8.RuneCountInString(message) > MaxReportTextCharacters {
			return ErrInvalidReport
		}
		return nil
	}
	if state == "" {
		if note != "" || len(outputs) == 0 {
			return ErrInvalidReport
		}
	} else if !state.Valid() || strings.TrimSpace(note) == "" || utf8.RuneCountInString(note) > MaxReportTextCharacters {
		return ErrInvalidReport
	}
	for _, output := range outputs {
		if !output.Kind.Valid() || strings.TrimSpace(output.Reference) == "" {
			return ErrInvalidReport
		}
		if output.Kind == ReportOutputPRCreated || output.Kind == ReportOutputPRReviewed {
			if _, err := ParseChangeRequestURL(output.Reference); err != nil {
				return ErrInvalidReport
			}
		}
	}
	return nil
}

// WrapSessionDelivery preserves AO session-origin direction as coordination in
// terminal prompt hooks, without labeling it as a human prompt or worker report.
func WrapSessionDelivery(id, message string) string {
	return fmt.Sprintf("<ao-session-delivery id=%q>\n%s\n</ao-session-delivery>", id, message)
}

// SessionDeliveryID returns the identity from an AO session-delivery envelope.
func SessionDeliveryID(message string) (string, bool) {
	return deliveryID(unwrapPastedContent(message), sessionDeliveryPrefix)
}

// CoordinationDeliveryID recognizes both durable reports and session sends.
// A report missing its head, body, or tail yields TruncatedReportDeliveryTurnID,
// never the delivery id itself, so it is coordination without acknowledging
// the batch.
func CoordinationDeliveryID(message string) (string, bool) {
	unwrapped := unwrapPastedContent(message)
	if id, ok := deliveryID(unwrapped, reportDeliveryPrefix); ok {
		if !reportDeliveryIntact(unwrapped, id) {
			return TruncatedReportDeliveryTurnID(id), true
		}
		return id, true
	}
	if id, ok := truncatedReportDeliveryID(message); ok {
		return TruncatedReportDeliveryTurnID(id), true
	}
	return SessionDeliveryID(message)
}
