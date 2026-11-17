package domain

import "time"

// SessionOverlapNotice records that one session was already warned about an
// active file overlap with a peer. It is delivery bookkeeping, not session
// status; removing the row re-arms a later overlap between the same pair.
type SessionOverlapNotice struct {
	SessionID     SessionID
	PeerSessionID SessionID
	NotifiedAt    time.Time
}
