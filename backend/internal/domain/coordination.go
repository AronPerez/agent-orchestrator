package domain

import "time"

// CoordinationEvent is recorded evidence, not proof that a recipient acted on it.
type CoordinationEvent struct {
	ID              string    `json:"id"`
	Kind            string    `json:"kind" enum:"message,report"`
	SourceSessionID string    `json:"sourceSessionId"`
	TargetSessionID string    `json:"targetSessionId,omitempty"`
	Summary         string    `json:"summary"`
	State           string    `json:"state"`
	ReportState     string    `json:"reportState,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
}
