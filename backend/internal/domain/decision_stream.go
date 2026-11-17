package domain

// DecisionStream names one work stream: a manager session on one machine.
type DecisionStream struct {
	Origin    string
	SessionID SessionID
	Name      string
}

// DeriveDecisionStream finds the highest manager below the project's
// orchestrator on the raising session's spawn path.
func DeriveDecisionStream(raiser SessionID, sessions []SessionRecord) (SessionRecord, bool) {
	byID := make(map[SessionID]SessionRecord, len(sessions))
	hasChild := make(map[SessionID]bool)
	for _, session := range sessions {
		byID[session.ID] = session
		if session.ParentSessionID != "" && session.ParentSessionID != session.ID {
			hasChild[session.ParentSessionID] = true
		}
	}
	current, ok := byID[raiser]
	if !ok {
		return SessionRecord{}, false
	}
	visited := make(map[SessionID]bool)
	var stream SessionRecord
	var found bool
	for !visited[current.ID] && current.Kind != KindOrchestrator {
		visited[current.ID] = true
		if hasChild[current.ID] {
			stream, found = current, true
		}
		if current.ParentSessionID == "" {
			break
		}
		parent, exists := byID[current.ParentSessionID]
		if !exists {
			break
		}
		current = parent
	}
	return stream, found
}

// AssignDecisionStreams applies the newest known name to each stream and
// joins a manager's early calls to the stream later calls established.
func AssignDecisionStreams(calls []DecisionCall, streams []DecisionStream) []DecisionCall {
	type streamKey struct {
		origin  string
		session SessionID
	}
	byKey := make(map[streamKey]DecisionStream, len(streams))
	for _, stream := range streams {
		byKey[streamKey{origin: stream.Origin, session: stream.SessionID}] = stream
	}
	result := append([]DecisionCall(nil), calls...)
	for i := range result {
		call := &result[i]
		streamID := call.Raised.StreamSessionID
		if streamID == "" {
			streamID = call.Raised.OwnerSessionID
		}
		stream, ok := byKey[streamKey{origin: call.Origin, session: streamID}]
		if !ok {
			continue
		}
		call.Raised.StreamSessionID = stream.SessionID
		call.Raised.StreamName = stream.Name
	}
	return result
}
