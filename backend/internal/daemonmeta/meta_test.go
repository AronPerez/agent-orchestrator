package daemonmeta

import "testing"

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
