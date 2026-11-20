//go:build linux

package persistenthost

import "testing"

func TestParseProcStatSession(t *testing.T) {
	tests := []struct {
		name        string
		stat        string
		wantSession int
		wantAlive   bool
		wantOK      bool
	}{
		{"running", "4242 (node) S 1 4242 13142 0 -1", 13142, true, true},
		{"command with spaces and parens", "4242 (a) b (c)) R 1 4242 777 0", 777, true, true},
		{"zombie", "4242 (sleep) Z 1 4242 13142 0", 13142, false, true},
		{"truncated", "4242 (sleep) S 1", 0, false, false},
		{"no command", "4242 sleep S 1 2 3", 0, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, alive, ok := parseProcStatSession(tt.stat)
			if session != tt.wantSession || alive != tt.wantAlive || ok != tt.wantOK {
				t.Fatalf("parseProcStatSession(%q) = %d, %v, %v; want %d, %v, %v",
					tt.stat, session, alive, ok, tt.wantSession, tt.wantAlive, tt.wantOK)
			}
		})
	}
}
