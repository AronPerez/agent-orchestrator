package daemonmeta

import (
	"encoding/json"
	"testing"
)

func TestFormatBuildIdentity(t *testing.T) {
	cases := []struct {
		name     string
		revision string
		modified string
		want     string
	}{
		{"clean commit", "abc123", "false", "abc123"},
		{"dirty commit", "abc123", "true", "abc123-dirty"},
		{"no revision (unstamped build)", "", "false", ""},
		{"no revision, modified unknown", "", "", ""},
		{"revision, modified unset", "abc123", "", "abc123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatBuildIdentity(tc.revision, tc.modified); got != tc.want {
				t.Fatalf("formatBuildIdentity(%q, %q) = %q, want %q", tc.revision, tc.modified, got, tc.want)
			}
		})
	}
}

// A clean build and a dirty build of the same commit must never compare equal —
// that is the whole point of the -dirty suffix for the identity check.
func TestFormatBuildIdentity_DirtyDiffersFromClean(t *testing.T) {
	if formatBuildIdentity("abc123", "true") == formatBuildIdentity("abc123", "false") {
		t.Fatal("dirty and clean builds of the same revision must not share an identity")
	}
}

// CurrentBuild's whole job is to make "unknown" distinguishable from a real
// answer, so that is what these pin: which source wins, and that an unknown
// build carries no identity for a naive equality check to match on.
func TestCurrentBuildSourcePrecedence(t *testing.T) {
	orig := buildStamp
	t.Cleanup(func() { buildStamp = orig })

	buildStamp = "deadbeef-dirty"
	got := CurrentBuild()
	if got.Source != BuildSourceStamp || got.Identity != "deadbeef-dirty" {
		t.Fatalf("with a link-time stamp: got %+v, want stamp/deadbeef-dirty", got)
	}
	if BuildIdentity() != "deadbeef-dirty" {
		t.Errorf("BuildIdentity() = %q, want the stamp (existing callers must keep working)", BuildIdentity())
	}

	// No stamp: fall through to Go's VCS stamping, which is absent in a worktree
	// build — the case that produced the empty field this exists to fix. Either
	// outcome is legitimate depending on where the test binary was built, but the
	// invariant must hold in both.
	buildStamp = ""
	got = CurrentBuild()
	switch got.Source {
	case BuildSourceVCS:
		if got.Identity == "" {
			t.Error("source vcs must carry an identity")
		}
	case BuildSourceUnknown:
		if got.Identity != "" {
			t.Errorf("source unknown must carry NO identity, got %q", got.Identity)
		}
	default:
		t.Fatalf("unexpected source %q", got.Source)
	}
}

// The empty-field problem stated as a test: an unknown build must not serialize
// an identity, because `a.identity === b.identity` across two unknown daemons
// would otherwise be true and read as "same build".
func TestUnknownBuildSerializesWithoutIdentity(t *testing.T) {
	b, err := json.Marshal(Build{Source: BuildSourceUnknown})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{"source":"unknown"}` {
		t.Fatalf("unknown build serialized as %s, want no identity key at all", got)
	}
	known, err := json.Marshal(Build{Identity: "abc123", Source: BuildSourceStamp})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(known); got != `{"identity":"abc123","source":"stamp"}` {
		t.Fatalf("known build serialized as %s", got)
	}
}
