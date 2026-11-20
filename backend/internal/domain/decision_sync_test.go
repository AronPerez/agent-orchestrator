package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func validSyncEntry() DecisionEntry {
	return DecisionEntry{
		ID:            "e_1",
		Origin:        "h_a",
		OriginSeq:     1,
		Stamp:         DecisionStamp{WallMS: 1000, Counter: 0},
		CallID:        "c_1",
		Kind:          DecisionKindAnswered,
		Payload:       json.RawMessage(`{"option":1}`),
		WrittenAt:     time.Unix(1, 0).UTC(),
		AuthorKind:    DecisionAuthorPerson,
		AuthorSurface: "desktop",
	}
}

func TestValidateDecisionSyncEntryAcceptsUnknownKind(t *testing.T) {
	e := validSyncEntry()
	e.Kind = "future_kind"
	e.Payload = json.RawMessage(`{"x":1}`)
	if err := ValidateDecisionSyncEntry(e); err != nil {
		t.Fatalf("ValidateDecisionSyncEntry(unknown kind) = %v, want nil", err)
	}
}

func TestValidateDecisionSyncEntryRejectsShape(t *testing.T) {
	cases := map[string]func(*DecisionEntry){
		"empty id":           func(e *DecisionEntry) { e.ID = "" },
		"empty origin":       func(e *DecisionEntry) { e.Origin = "" },
		"empty call id":      func(e *DecisionEntry) { e.CallID = "" },
		"129-byte call id":   func(e *DecisionEntry) { e.CallID = strings.Repeat("c", 129) },
		"control in id":      func(e *DecisionEntry) { e.ID = "e\n1" },
		"kind Raised":        func(e *DecisionEntry) { e.Kind = "Raised" },
		"empty author kind":  func(e *DecisionEntry) { e.AuthorKind = "" },
		"author kind digits": func(e *DecisionEntry) { e.AuthorKind = "p1" },
		"seq 0":              func(e *DecisionEntry) { e.OriginSeq = 0 },
		"wall 0":             func(e *DecisionEntry) { e.Stamp.WallMS = 0 },
		"counter negative":   func(e *DecisionEntry) { e.Stamp.Counter = -1 },
		"payload array":      func(e *DecisionEntry) { e.Payload = json.RawMessage(`[]`) },
		"payload string":     func(e *DecisionEntry) { e.Payload = json.RawMessage(`"s"`) },
		"payload invalid":    func(e *DecisionEntry) { e.Payload = json.RawMessage(`{"a":`) },
		"payload empty":      func(e *DecisionEntry) { e.Payload = nil },
		"payload 65537 bytes": func(e *DecisionEntry) {
			pad := MaxDecisionSyncPayloadBytes + 1 - len(`{"p":""}`)
			e.Payload = json.RawMessage(`{"p":"` + strings.Repeat("x", pad) + `"}`)
		},
		"zero written at":      func(e *DecisionEntry) { e.WrittenAt = time.Time{} },
		"129-byte surface":     func(e *DecisionEntry) { e.AuthorSurface = RequestSurface(strings.Repeat("s", 129)) },
		"129-byte author sess": func(e *DecisionEntry) { e.AuthorSession = SessionID(strings.Repeat("s", 129)) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := validSyncEntry()
			mutate(&e)
			err := ValidateDecisionSyncEntry(e)
			if !errors.Is(err, ErrDecisionEntryInvalid) {
				t.Fatalf("ValidateDecisionSyncEntry = %v, want ErrDecisionEntryInvalid", err)
			}
		})
	}
}

func TestValidateDecisionSyncEntryAcceptsMaxPayload(t *testing.T) {
	e := validSyncEntry()
	pad := MaxDecisionSyncPayloadBytes - len(`{"p":""}`)
	e.Payload = json.RawMessage(`{"p":"` + strings.Repeat("x", pad) + `"}`)
	if err := ValidateDecisionSyncEntry(e); err != nil {
		t.Fatalf("payload of exactly the cap: %v", err)
	}
}

func TestValidateDecisionSyncEntryIgnoresBusinessRules(t *testing.T) {
	e := validSyncEntry()
	e.Kind = DecisionKindRaised
	payload, err := json.Marshal(DecisionRaisedPayload{
		Title:   strings.Repeat("t", 500),
		Options: []DecisionOption{{Label: "only one"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.Payload = payload
	if err := ValidateDecisionSyncEntry(e); err != nil {
		t.Fatalf("ValidateDecisionSyncEntry(business-invalid raised) = %v, want nil", err)
	}
}

func TestSameDecisionEntryComparesPayloadBytes(t *testing.T) {
	a := validSyncEntry()
	a.Payload = json.RawMessage(`{"a":1}`)
	b := a
	b.Payload = json.RawMessage(`{"a": 1}`)
	if SameDecisionEntry(a, b) {
		t.Fatal("payloads differing only in whitespace compared equal")
	}
	b.Payload = json.RawMessage(`{"a":1}`)
	if !SameDecisionEntry(a, b) {
		t.Fatal("identical entries compared different")
	}
	loc := time.FixedZone("UTC+5", 5*3600)
	b.WrittenAt = a.WrittenAt.In(loc)
	if !SameDecisionEntry(a, b) {
		t.Fatal("same instant in two zones compared different")
	}
	for name, mutate := range map[string]func(*DecisionEntry){
		"id":      func(e *DecisionEntry) { e.ID = "e_2" },
		"origin":  func(e *DecisionEntry) { e.Origin = "h_b" },
		"seq":     func(e *DecisionEntry) { e.OriginSeq = 2 },
		"wall":    func(e *DecisionEntry) { e.Stamp.WallMS++ },
		"counter": func(e *DecisionEntry) { e.Stamp.Counter++ },
		"call":    func(e *DecisionEntry) { e.CallID = "c_2" },
		"kind":    func(e *DecisionEntry) { e.Kind = DecisionKindDismissed },
		"written": func(e *DecisionEntry) { e.WrittenAt = e.WrittenAt.Add(time.Millisecond) },
		"author":  func(e *DecisionEntry) { e.AuthorKind = DecisionAuthorSession },
		"surface": func(e *DecisionEntry) { e.AuthorSurface = "cli" },
		"session": func(e *DecisionEntry) { e.AuthorSession = "w-1" },
	} {
		c := a
		mutate(&c)
		if SameDecisionEntry(a, c) {
			t.Fatalf("entries differing in %s compared equal", name)
		}
	}
}

func TestDecisionVectorCovers(t *testing.T) {
	v := DecisionVector{"a": 3, "b": 1}
	if !v.Covers(DecisionVector{"a": 2}) {
		t.Fatal("{a:3,b:1} should cover {a:2}")
	}
	if v.Covers(DecisionVector{"a": 4}) {
		t.Fatal("{a:3,b:1} should not cover {a:4}")
	}
	if v.Covers(DecisionVector{"c": 1}) {
		t.Fatal("{a:3,b:1} should not cover {c:1}")
	}
	if !(DecisionVector{}).Covers(DecisionVector{}) {
		t.Fatal("empty should cover empty")
	}
	if !(DecisionVector{}).Covers(DecisionVector{"z": 0}) {
		t.Fatal("empty should cover an origin held at 0")
	}
}
