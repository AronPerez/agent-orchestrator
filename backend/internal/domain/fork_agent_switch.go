package domain

import "time"

// AgentSwitch is the fork's legacy UI projection, not the retired switching saga.
// Execution belongs to ProviderSwitch; the retained rows preserve retry/history.
type AgentSwitch struct {
	ID                                                                                                string
	SessionID                                                                                         SessionID
	FromHarness, TargetHarness                                                                        AgentHarness
	TargetStartMode, State, AgentHandoffStatus, SourceTranscriptStatus, ErrorCode                     string
	SemanticHandoffIncluded                                                                           bool
	RequestedAt, UpdatedAt                                                                            time.Time
	IdempotencyKey, RequestFingerprint, SourceGenerationID, TargetGenerationID, TargetRuntimeHandleID string
}

// Terminal reports whether the legacy receipt no longer needs recovery.
func (s AgentSwitch) Terminal() bool { return s.State == "completed" || s.State == "failed" }
