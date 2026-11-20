package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
)

type decisionSyncStatusAPI struct {
	HostID  string `json:"hostId"`
	Origins []struct {
		HostID       string     `json:"hostId"`
		MachineLabel string     `json:"machineLabel"`
		LastHeardAt  *time.Time `json:"lastHeardAt"`
	} `json:"origins"`
}

// decisionMachines says which machine served a listing and when each other
// machine was last heard from. A nil value prints nothing extra.
type decisionMachines struct {
	hostID    string
	lastHeard map[string]time.Time
	now       time.Time
}

// decisionMachines reads the serving daemon's sync status once per command.
// Any failure leaves the listing as it was before sync existed.
func (c *commandContext) decisionMachines(cmd *cobra.Command) *decisionMachines {
	var status decisionSyncStatusAPI
	if err := c.getJSON(cmd.Context(), "decision-sync/status", &status); err != nil || status.HostID == "" {
		return nil
	}
	m := &decisionMachines{hostID: status.HostID, lastHeard: map[string]time.Time{}, now: c.now()}
	for _, o := range status.Origins {
		if o.LastHeardAt != nil {
			m.lastHeard[o.HostID] = *o.LastHeardAt
		}
	}
	return m
}

// writeOrigin marks a call raised on another machine, and for an open one
// says where its answer goes and how fresh that machine is.
func (m *decisionMachines) writeOrigin(w io.Writer, call decisionCallAPI) error {
	if m == nil || call.HostID == "" || call.HostID == m.hostID {
		return nil
	}
	machine := call.MachineLabel
	if machine == "" {
		machine = call.HostID
	}
	if _, err := fmt.Fprintf(w, "  On %s\n", machine); err != nil {
		return err
	}
	if call.State != "needs_you" && call.State != "in_progress" && call.State != "decided" {
		return nil
	}
	line := "  Answer goes to " + machine
	if at, ok := m.lastHeard[call.HostID]; ok {
		line += " · last heard " + sessionAge(m.now, at)
	}
	_, err := fmt.Fprintln(w, line)
	return err
}
