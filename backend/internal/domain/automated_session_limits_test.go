package domain

import "testing"

func TestAutomatedSessionLimitsWithDefaults(t *testing.T) {
	got := (AutomatedSessionLimits{}).WithDefaults()
	if got.MaxConcurrent != DefaultAutomatedSessionMaxConcurrent {
		t.Fatalf("MaxConcurrent = %d, want %d", got.MaxConcurrent, DefaultAutomatedSessionMaxConcurrent)
	}
	if got.MaxStartsPerPass != DefaultAutomatedSessionMaxStartsPerPass {
		t.Fatalf("MaxStartsPerPass = %d, want %d", got.MaxStartsPerPass, DefaultAutomatedSessionMaxStartsPerPass)
	}

	set := AutomatedSessionLimits{MaxConcurrent: 7, MaxStartsPerPass: 4}.WithDefaults()
	if set.MaxConcurrent != 7 || set.MaxStartsPerPass != 4 {
		t.Fatalf("WithDefaults overwrote configured limits: %#v", set)
	}
}

func TestAutomatedSessionLimitsValidate(t *testing.T) {
	tests := []struct {
		name    string
		limits  AutomatedSessionLimits
		wantErr bool
	}{
		{name: "zero uses defaults"},
		{name: "configured", limits: AutomatedSessionLimits{MaxConcurrent: 8, MaxStartsPerPass: 3}},
		{name: "negative concurrent", limits: AutomatedSessionLimits{MaxConcurrent: -1}, wantErr: true},
		{name: "negative starts", limits: AutomatedSessionLimits{MaxStartsPerPass: -1}, wantErr: true},
		{name: "concurrent too high", limits: AutomatedSessionLimits{MaxConcurrent: MaxAutomatedSessionLimit + 1}, wantErr: true},
		{name: "starts too high", limits: AutomatedSessionLimits{MaxStartsPerPass: MaxAutomatedSessionLimit + 1}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.limits.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestAutomatedSessionLimitsRemainingStarts(t *testing.T) {
	limits := AutomatedSessionLimits{MaxConcurrent: 5, MaxStartsPerPass: 3}
	tests := []struct {
		name      string
		active    int
		attempted int
		want      int
	}{
		{name: "empty", want: 3},
		{name: "concurrent bound", active: 4, want: 1},
		{name: "pass bound", active: 1, attempted: 2, want: 1},
		{name: "concurrent full", active: 5, want: 0},
		{name: "concurrent over cap", active: 7, want: 0},
		{name: "pass full", attempted: 3, want: 0},
		{name: "attempt consumes concurrent slot for this pass", active: 4, attempted: 1, want: 0},
		{name: "negative counters normalize to zero", active: -1, attempted: -2, want: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := limits.RemainingStarts(tt.active, tt.attempted); got != tt.want {
				t.Fatalf("RemainingStarts(%d, %d) = %d, want %d", tt.active, tt.attempted, got, tt.want)
			}
		})
	}
}
