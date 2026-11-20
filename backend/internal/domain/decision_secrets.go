package domain

import (
	"fmt"
	"regexp"
)

// decisionSecretPatterns are high-confidence credential shapes. A call is
// copied to every linked machine and kept for good, so text that matches is
// refused when it is written. Received entries are never checked: a looser
// rule elsewhere must not stop this machine from relaying.
var decisionSecretPatterns = []struct {
	label string
	re    *regexp.Regexp
}{
	{"an API key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`)},
	{"a GitHub token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}`)},
	{"a GitHub token", regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,}`)},
	{"an AWS access key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"a Slack token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"a private key", regexp.MustCompile(`-{5}BEGIN [A-Z0-9 ]*PRIVATE KEY-{5}`)},
	{"an Orchestrator token", regexp.MustCompile(`\bao[dc]_[0-9a-f]{64}\b`)},
	{"a bearer token", regexp.MustCompile(`\bBearer [A-Za-z0-9._~+/=-]{20,}`)},
	{"a JSON Web Token", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
}

// FindDecisionSecret reports whether text holds a credential-shaped string,
// and which kind, for the refusal message. It never returns the match.
func FindDecisionSecret(text string) (label string, found bool) {
	for _, p := range decisionSecretPatterns {
		if p.re.MatchString(text) {
			return p.label, true
		}
	}
	return "", false
}

// rejectDecisionSecrets refuses the first field that holds a credential.
func rejectDecisionSecrets(fields ...string) error {
	for _, field := range fields {
		if label, found := FindDecisionSecret(field); found {
			return decisionRule("secret_like", fmt.Sprintf(
				"This looks like it contains a credential (%s). Calls are copied to every linked machine and kept for good. Describe it instead of pasting it.", label))
		}
	}
	return nil
}

func validateRaiseSecrets(p DecisionRaisedPayload) error {
	fields := make([]string, 0, 5+2*len(p.Options)+2*len(p.Links))
	fields = append(fields, p.Title, p.Context, p.Reference)
	for _, o := range p.Options {
		fields = append(fields, o.Label, o.Consequence)
	}
	for _, l := range p.Links {
		fields = append(fields, l.Label, l.URL)
	}
	if p.Prediction != nil {
		fields = append(fields, p.Prediction.Why, p.Prediction.Undo)
	}
	return rejectDecisionSecrets(fields...)
}

func validateDecidedSecrets(p DecisionDecidedPayload) error {
	fields := make([]string, 0, 5+2*len(p.Links))
	fields = append(fields, p.What, p.Why, p.Undo, p.Verify, p.Reference)
	for _, l := range p.Links {
		fields = append(fields, l.Label, l.URL)
	}
	return rejectDecisionSecrets(fields...)
}
