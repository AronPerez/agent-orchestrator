package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type decisionRepairStatusAPI struct {
	State     string    `json:"state"`
	Origin    string    `json:"origin"`
	FromSeq   int64     `json:"fromSeq"`
	NewHostID string    `json:"newHostId"`
	Entries   int       `json:"entries"`
	StagedAt  time.Time `json:"stagedAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type decisionIdentityAPI struct {
	HostID       string                   `json:"hostId"`
	MachineLabel string                   `json:"machineLabel"`
	Repair       *decisionRepairStatusAPI `json:"repair,omitempty"`
}

type decisionRepairAPIRequest struct {
	Stage   bool     `json:"stage,omitempty"`
	FromSeq int64    `json:"fromSeq,omitempty"`
	ForkIDs []string `json:"forkIds,omitempty"`
}

type decisionRepairAPIResponse struct {
	Origin  string   `json:"origin"`
	FromSeq int64    `json:"fromSeq"`
	ForkIDs []string `json:"forkIds"`
	CallIDs []string `json:"callIds"`
	Links   []struct {
		HostID string `json:"hostId"`
		Label  string `json:"label"`
	} `json:"links"`
	PeersAsked  int                      `json:"peersAsked"`
	PeersFailed int                      `json:"peersFailed"`
	Staged      *decisionRepairStatusAPI `json:"staged,omitempty"`
}

func newDecisionIdentityCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "Repair this computer's decision identity after its data directory was copied",
	}
	cmd.AddCommand(newDecisionIdentityRepairCommand(ctx), newDecisionIdentityStatusCommand(ctx))
	return cmd
}

func newDecisionIdentityRepairCommand(ctx *commandContext) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Give this computer a new identity when it shares one with a copy of itself",
		Long: "Run this on the machine that should give up the shared identity, usually the copy. " +
			"It compares this computer's history with your linked machines', shows what changes, and asks you " +
			"to type this computer's name. Orchestrator finishes the repair when it restarts: this computer gets a " +
			"new identity, its own entries since the copy are re-issued under it, and its links are removed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ctx.refuseDecisionLinkRemote("ao decision identity repair"); err != nil {
				return err
			}
			var identity decisionIdentityAPI
			if err := ctx.getJSON(cmd.Context(), "decision-identity", &identity); err != nil {
				return err
			}
			var plan decisionRepairAPIResponse
			if err := ctx.postJSON(cmd.Context(), "decision-identity/repair", decisionRepairAPIRequest{}, &plan); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if err := writeDecisionRepairPlan(out, identity, plan); err != nil {
				return err
			}
			in := bufio.NewReader(cmd.InOrStdin())
			if !yes {
				answer, err := prompt(out, in, fmt.Sprintf("Type this computer's name (%s) to confirm: ", identity.MachineLabel))
				if err != nil {
					return err
				}
				if !strings.EqualFold(answer, identity.MachineLabel) {
					return usageError{errors.New("not confirmed; nothing changed")}
				}
			}
			var staged decisionRepairAPIResponse
			if err := ctx.postJSON(cmd.Context(), "decision-identity/repair", decisionRepairAPIRequest{
				Stage: true, FromSeq: plan.FromSeq, ForkIDs: plan.ForkIDs,
			}, &staged); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(out, "Repair staged. Decision sync is off until Orchestrator restarts and finishes it."); err != nil {
				return err
			}
			if yes {
				_, err := fmt.Fprintln(out, "Quit and reopen Orchestrator to finish.")
				return err
			}
			answer, err := prompt(out, in, "Restart Orchestrator now? [y/N] ")
			if err != nil {
				return err
			}
			if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
				_, err := fmt.Fprintln(out, "Quit and reopen Orchestrator to finish.")
				return err
			}
			if _, err := ctx.stopDaemon(cmd.Context(), stopOptions{timeout: defaultStopTimeout}); err != nil {
				return fmt.Errorf("stop Orchestrator: %w; quit and reopen it to finish", err)
			}
			return ctx.runStart(cmd.Context(), cmd, startOptions{})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Do not ask to confirm or to restart (for scripts)")
	return cmd
}

func prompt(out io.Writer, in *bufio.Reader, question string) (string, error) {
	if _, err := fmt.Fprint(out, question); err != nil {
		return "", err
	}
	line, err := in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func writeDecisionRepairPlan(w io.Writer, identity decisionIdentityAPI, plan decisionRepairAPIResponse) error {
	lines := []string{
		fmt.Sprintf("This computer (%s) shares the decision identity %s with a copy of its data directory.", identity.MachineLabel, plan.Origin),
		"Repairing gives this computer a new identity; the other machine keeps " + plan.Origin + ".",
		fmt.Sprintf("  %d of this computer's entries, from sequence %d on, are re-issued under the new identity (%s).",
			len(plan.ForkIDs), plan.FromSeq, callList(plan.CallIDs)),
	}
	if len(plan.Links) > 0 {
		names := make([]string, len(plan.Links))
		for i, l := range plan.Links {
			names[i] = peerName(l.Label, l.HostID)
		}
		lines = append(lines, "  Its links are removed: "+strings.Join(names, ", ")+". Add them again afterwards with: ao decision link add")
	}
	lines = append(lines,
		"  Phones paired with this computer, and other desktops' saved entries for it, must reconnect.",
		"  Machines that link to this computer must link to it again.",
	)
	if plan.PeersFailed > 0 {
		lines = append(lines, fmt.Sprintf("  %d of %d linked machines did not answer; the fork point was found without them.", plan.PeersFailed, plan.PeersAsked))
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func callList(ids []string) string {
	switch len(ids) {
	case 0:
		return "no calls"
	case 1:
		return "call " + ids[0]
	}
	return "calls " + strings.Join(ids, ", ")
}

func newDecisionIdentityStatusCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show this computer's decision identity and where a repair stands",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ctx.refuseDecisionLinkRemote("ao decision identity status"); err != nil {
				return err
			}
			var identity decisionIdentityAPI
			if err := ctx.getJSON(cmd.Context(), "decision-identity", &identity); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), identity)
			}
			return writeDecisionIdentity(cmd.OutOrStdout(), identity)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func writeDecisionIdentity(w io.Writer, identity decisionIdentityAPI) error {
	if _, err := fmt.Fprintf(w, "Decision identity %s (%s).\n", identity.HostID, identity.MachineLabel); err != nil {
		return err
	}
	r := identity.Repair
	if r == nil {
		return nil
	}
	var text string
	if r.State == "done" {
		text = fmt.Sprintf("Repaired: gave up %s; %d entries re-issued. Add this computer's links again with: ao decision link add", r.Origin, r.Entries)
	} else {
		text = fmt.Sprintf("A repair giving up %s is staged (%s). Quit and reopen Orchestrator to finish it; decision sync is off until then.", r.Origin, strings.ReplaceAll(r.State, "_", " "))
	}
	_, err := fmt.Fprintln(w, text)
	return err
}
