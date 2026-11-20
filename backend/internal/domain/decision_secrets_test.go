package domain

import (
	"errors"
	"strings"
	"testing"
)

// Samples are assembled at run time so no credential-shaped literal sits in
// the source for secret scanners to flag.
func rep(s string, n int) string { return strings.Repeat(s, n) }

func secretSamples() []struct{ name, hit, miss string } {
	return []struct{ name, hit, miss string }{
		{"api key", "use " + "sk" + "-" + rep("a1B2", 6) + " here", "use " + "sk" + "-" + rep("a", 10) + " here"},
		{"github token", "token " + "gh" + "p_" + rep("A1b2", 9), "branch " + "gh" + "p_short"},
		{"github oauth", "gh" + "o_" + rep("Z9y8", 9), "gh" + "x_" + rep("Z9y8", 9)},
		{"github pat", "github" + "_pat_" + rep("Ab1_", 6), "github" + "_pat_" + "tiny"},
		{"aws key", "key AK" + "IA" + rep("ABCD", 4) + " used", "AK" + "IA" + "ABCD lower"},
		{"slack token", "xo" + "xb-" + rep("12345", 3), "xo" + "xz-" + rep("12345", 3)},
		{"private key", "-----BEGIN RSA " + "PRIVATE KEY-----\nMII", "-----BEGIN PUBLIC KEY-----"},
		{"ao device token", "ao" + "d_" + rep("0f", 32), "ao" + "d_" + rep("0f", 4)},
		{"ao scoped token", "ao" + "c_" + rep("9a", 32), "aoc_session"},
		{"bearer", "Authorization: Bear" + "er " + rep("abcDEF123", 3), "Bearer of bad news"},
		{"jwt", "ey" + "J" + rep("hbGciOi", 2) + "." + "ey" + "J" + rep("zdWIiOi", 2) + "." + rep("SflKxwRJ", 2), "eyJ.short.token"},
	}
}

func TestFindDecisionSecretPatterns(t *testing.T) {
	for _, tc := range secretSamples() {
		if label, found := FindDecisionSecret(tc.hit); !found || label == "" {
			t.Errorf("%s: %q not found", tc.name, tc.hit)
		}
		if label, found := FindDecisionSecret(tc.miss); found {
			t.Errorf("%s: near miss %q flagged as %s", tc.name, tc.miss, label)
		}
	}
	if _, found := FindDecisionSecret("Use the staging key from the vault, not a pasted one."); found {
		t.Fatal("plain prose flagged")
	}
}

func assertSecretRule(t *testing.T, err error) {
	t.Helper()
	var rule *DecisionRuleError
	if !errors.As(err, &rule) || rule.Rule != "secret_like" {
		t.Fatalf("err = %v, want rule secret_like", err)
	}
	if !strings.Contains(rule.Message, "Calls are copied to every linked machine and kept for good. Describe it instead of pasting it.") {
		t.Fatalf("message = %q", rule.Message)
	}
}

func secret() string { return "gh" + "p_" + rep("A1b2", 9) }

func TestRaiseRefusesSecretInContext(t *testing.T) {
	p := validDecisionRaise()
	p.Context = "Deploy with " + secret()
	assertSecretRule(t, ValidateDecisionRaise(p))
}

func TestRaiseRefusesSecretInOptionConsequence(t *testing.T) {
	p := validDecisionRaise()
	p.Options[1].Consequence = "Rotate " + secret()
	assertSecretRule(t, ValidateDecisionRaise(p))
}

func TestRaiseRefusesSecretInLink(t *testing.T) {
	p := validDecisionRaise()
	p.Links = []DecisionLink{{Label: "run", URL: "https://example.com/?t=" + secret()}}
	assertSecretRule(t, ValidateDecisionRaise(p))
}

func TestAnswerNoteRefusesSecret(t *testing.T) {
	assertSecretRule(t, ValidateDecisionAnswer(2, DecisionAnsweredPayload{Choice: DecisionChoiceOption, Option: 1, Note: "use " + secret()}))
	assertSecretRule(t, ValidateDecisionNote("dismissed: "+secret()))
}

func TestOutcomeRefusesSecret(t *testing.T) {
	assertSecretRule(t, ValidateDecisionOutcome(DecisionOutcomePayload{Text: "set " + secret()}))
}

func TestDecideRefusesSecret(t *testing.T) {
	p := DecisionDecidedPayload{What: "Use " + secret(), Why: "w", Undo: "u", Kind: "design", MachineLabel: "m", OwnerSessionID: "w-1", OwnerSessionName: "w-1"}
	assertSecretRule(t, ValidateDecisionDecided(p))
	assertSecretRule(t, ValidateDecisionOverrule("do "+secret()))
}

func TestValidateDecisionSyncEntryAcceptsSecretShapedPayload(t *testing.T) {
	e := validSyncEntry()
	e.Payload = []byte(`{"note":"` + secret() + `"}`)
	if err := ValidateDecisionSyncEntry(e); err != nil {
		t.Fatalf("received entries are never checked for business rules: %v", err)
	}
}
