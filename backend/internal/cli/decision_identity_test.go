package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// identityDaemon fakes the local daemon's identity routes.
type identityDaemon struct {
	repairBodies []map[string]any
	repair       string
}

func (d *identityDaemon) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/internal/"):
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/decision-identity":
			repair := ""
			if d.repair != "" {
				repair = `,"repair":{"state":"` + d.repair + `","origin":"h_x","fromSeq":2,"newHostId":"h_y","entries":3,"stagedAt":"2026-11-20T00:00:00Z","updatedAt":"2026-11-20T00:00:00Z"}`
			}
			_, _ = io.WriteString(w, `{"hostId":"h_x","machineLabel":"mac-mini"`+repair+`}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/decision-identity/repair":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			d.repairBodies = append(d.repairBodies, body)
			staged := ""
			if body["stage"] == true {
				staged = `,"staged":{"state":"staged","origin":"h_x","fromSeq":2,"newHostId":"h_y","entries":3,"stagedAt":"2026-11-20T00:00:00Z","updatedAt":"2026-11-20T00:00:00Z"}`
			}
			_, _ = io.WriteString(w, `{"origin":"h_x","fromSeq":2,"forkIds":["dent_2","dent_3","dent_4"],"callIds":["dec_b","dec_c"],`+
				`"links":[{"hostId":"h_b","label":"hub"}],"peersAsked":2,"peersFailed":1`+staged+`}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestDecisionIdentityRepairStagesAfterTheNameIsTyped(t *testing.T) {
	d := &identityDaemon{}
	deps := decisionCLIServer(t, d.handler(t))
	deps.In = strings.NewReader("MAC-MINI\nn\n")
	out, _, err := executeCLI(t, deps, "decision", "identity", "repair")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"shares the decision identity h_x with a copy",
		"3 of this computer's entries, from sequence 2 on, are re-issued under the new identity (calls dec_b, dec_c)",
		"Its links are removed: hub. Add them again afterwards with: ao decision link add",
		"Phones paired with this computer",
		"1 of 2 linked machines did not answer",
		"Type this computer's name (mac-mini) to confirm:",
		"Repair staged.",
		"Restart Orchestrator now? [y/N]",
		"Quit and reopen Orchestrator to finish.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if len(d.repairBodies) != 2 || d.repairBodies[1]["stage"] != true || d.repairBodies[1]["fromSeq"] != float64(2) || len(d.repairBodies[1]["forkIds"].([]any)) != 3 {
		t.Fatalf("repair bodies = %v", d.repairBodies)
	}
}

func TestDecisionIdentityRepairNeedsConfirmation(t *testing.T) {
	d := &identityDaemon{}
	deps := decisionCLIServer(t, d.handler(t))
	deps.In = strings.NewReader("other-box\n")
	_, _, err := executeCLI(t, deps, "decision", "identity", "repair")
	if err == nil || !strings.Contains(err.Error(), "not confirmed; nothing changed") {
		t.Fatalf("err = %v", err)
	}
	if len(d.repairBodies) != 1 || d.repairBodies[0]["stage"] != nil {
		t.Fatalf("a repair was staged without confirmation: %v", d.repairBodies)
	}
}

func TestDecisionIdentityRepairYesSkipsPrompts(t *testing.T) {
	d := &identityDaemon{}
	deps := decisionCLIServer(t, d.handler(t))
	out, _, err := executeCLI(t, deps, "decision", "identity", "repair", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "to confirm") || strings.Contains(out, "Restart Orchestrator now") || !strings.Contains(out, "Quit and reopen Orchestrator to finish.") {
		t.Fatalf("output:\n%s", out)
	}
	if len(d.repairBodies) != 2 || d.repairBodies[1]["stage"] != true {
		t.Fatalf("repair bodies = %v", d.repairBodies)
	}
}

func TestDecisionIdentityRepairRefusedFromSession(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "w-1")
	deps := decisionCLIServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"kind":"forbidden","code":"DECISION_PERSON_ONLY","message":"Only the person can repair this machine's decision identity. A session cannot.","requestId":"r"}`)
	}))
	_, _, err := executeCLI(t, deps, "decision", "identity", "repair", "--yes")
	if err == nil || !strings.Contains(err.Error(), "Only the person can repair") {
		t.Fatalf("err = %v", err)
	}
}

func TestDecisionIdentityStatusText(t *testing.T) {
	for state, want := range map[string]string{
		"":                  "Decision identity h_x (mac-mini).\n",
		"identity_switched": "A repair giving up h_x is staged (identity switched). Quit and reopen Orchestrator to finish it",
		"done":              "Repaired: gave up h_x; 3 entries re-issued. Add this computer's links again",
	} {
		d := &identityDaemon{repair: state}
		out, _, err := executeCLI(t, decisionCLIServer(t, d.handler(t)), "decision", "identity", "status")
		if err != nil || !strings.Contains(out, want) {
			t.Errorf("state %q: %v\n%s", state, err, out)
		}
	}
}
