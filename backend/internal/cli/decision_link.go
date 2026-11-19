package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type decisionPeerAPI struct {
	HostID        string     `json:"hostId"`
	Label         string     `json:"label"`
	URL           string     `json:"url,omitempty"`
	Direction     string     `json:"direction"`
	State         string     `json:"state"`
	LastSuccessAt *time.Time `json:"lastSuccessAt,omitempty"`
	LastAttemptAt *time.Time `json:"lastAttemptAt,omitempty"`
	LastErrorCode string     `json:"lastErrorCode,omitempty"`
	InSync        bool       `json:"inSync"`
	ClockAheadMs  int64      `json:"clockAheadMs"`
}

type decisionPeersAPIResponse struct {
	Peers []decisionPeerAPI `json:"peers"`
}

type decisionLinkAPIRequest struct {
	URL            string `json:"url"`
	ExpectedHostID string `json:"expectedHostId,omitempty"`
	Label          string `json:"label,omitempty"`
	Credential     string `json:"credential"`
}

type decisionLinkAPIResponse struct {
	Peer     decisionPeerAPI `json:"peer"`
	Warnings []struct {
		CallID string `json:"callId"`
		Field  string `json:"field"`
	} `json:"warnings"`
}

type decisionSyncAPIRequest struct {
	HostID string `json:"hostId,omitempty"`
}

type decisionSyncResultAPI struct {
	HostID    string `json:"hostId"`
	Label     string `json:"label"`
	Pulled    int    `json:"pulled"`
	Pushed    int    `json:"pushed"`
	InSync    bool   `json:"inSync"`
	ErrorCode string `json:"errorCode,omitempty"`
}

type decisionSyncAPIResponse struct {
	Results []decisionSyncResultAPI `json:"results"`
}

// sealedPasswordError explains why --host cannot link a machine whose saved
// password only the desktop app can unseal. It is shown to the person as is.
type sealedPasswordError struct{}

func (sealedPasswordError) Error() string {
	return "That machine's password is stored by the desktop app. Turn on Sync decisions for it in Settings › Remote hosts, or give its address with --password-stdin."
}

// refuseDecisionLinkRemote keeps link and sync on this computer's daemon: it
// is the daemon that holds the links and talks to the other machines.
func (c *commandContext) refuseDecisionLinkRemote(command string) error {
	if c.remote == nil {
		return nil
	}
	return usageError{fmt.Errorf("`%s` manages this computer's links and must use its own daemon; give the other machine's address as an argument instead of --url/AO_URL", command)}
}

func newDecisionLinkCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Link this computer with your other machines so they share one decision ledger",
	}
	cmd.AddCommand(newDecisionLinkAddCommand(ctx), newDecisionLinkListCommand(ctx), newDecisionLinkRemoveCommand(ctx))
	return cmd
}

func newDecisionLinkAddCommand(ctx *commandContext) *cobra.Command {
	var host, label string
	var passwordStdin bool
	cmd := &cobra.Command{
		Use:   "add [<address>]",
		Short: "Link a machine you own (its remote-access address and password, or a saved host)",
		Long: "Link a machine you own. Give its remote-access address and its password on standard input " +
			"(--password-stdin) or in AO_TOKEN, or name a host saved in the desktop app with --host. " +
			"The password is used once to obtain a sync-only credential and is not stored. " +
			"A linked machine can write any decision, including answers: link only machines you own.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ctx.refuseDecisionLinkRemote("ao decision link add"); err != nil {
				return err
			}
			req := decisionLinkAPIRequest{Label: strings.TrimSpace(label)}
			switch {
			case host != "" && len(args) == 1:
				return usageError{errors.New("usage: give either an address or --host, not both")}
			case host != "":
				entry, err := findSavedHost(host)
				if err != nil {
					return err
				}
				if strings.TrimSpace(entry.Password) == "" {
					return usageError{sealedPasswordError{}}
				}
				req.URL, req.ExpectedHostID, req.Credential = entry.URL, entry.HostID, entry.Password
				if req.Label == "" {
					req.Label = entry.Label
				}
			case len(args) == 1:
				credential, err := linkCredential(cmd.InOrStdin(), passwordStdin)
				if err != nil {
					return err
				}
				req.URL, req.Credential = args[0], credential
			default:
				return usageError{errors.New("usage: ao decision link add <address> --password-stdin, or ao decision link add --host <saved machine>")}
			}
			var linked decisionLinkAPIResponse
			if err := ctx.postJSON(cmd.Context(), "decision-peers", req, &linked); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if _, err := fmt.Fprintf(out, "Linked %s (%s).\n", peerName(linked.Peer.Label, linked.Peer.HostID), linked.Peer.HostID); err != nil {
				return err
			}
			for _, w := range linked.Warnings {
				if _, err := fmt.Fprintf(out, "Warning: %s may hold a credential in its %s; it is now copied to that machine.\n", w.CallID, w.Field); err != nil {
					return err
				}
			}
			var synced decisionSyncAPIResponse
			if err := ctx.postJSON(cmd.Context(), "decision-peers/sync", decisionSyncAPIRequest{HostID: linked.Peer.HostID}, &synced); err != nil {
				_, _ = fmt.Fprintf(out, "First sync did not run: %v\n", err)
				return nil
			}
			for _, r := range synced.Results {
				if _, err := fmt.Fprintf(out, "First sync: %s\n", syncResultText(r)); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "A machine saved in the desktop app, by label or host id")
	cmd.Flags().StringVar(&label, "label", "", "Name to show for the machine (default: its saved label or address)")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "Read the machine's password from standard input")
	return cmd
}

// linkCredential reads the other machine's password from stdin or AO_TOKEN.
// It is never accepted as a flag, which would leave it in shell history.
func linkCredential(in io.Reader, fromStdin bool) (string, error) {
	if fromStdin {
		line, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		if credential := strings.TrimSpace(line); credential != "" {
			return credential, nil
		}
		return "", usageError{errors.New("--password-stdin read an empty password")}
	}
	if credential := strings.TrimSpace(os.Getenv("AO_TOKEN")); credential != "" {
		return credential, nil
	}
	return "", usageError{errors.New("give the machine's password with --password-stdin or AO_TOKEN")}
}

func findSavedHost(name string) (remoteEntry, error) {
	entries, err := loadRemoteEntries()
	if err != nil {
		return remoteEntry{}, err
	}
	for _, entry := range entries {
		if entry.CleanupOnly {
			continue
		}
		if entry.HostID == name || strings.EqualFold(entry.Label, name) {
			return entry, nil
		}
	}
	return remoteEntry{}, usageError{fmt.Errorf("no machine named %q is saved in the desktop app", name)}
}

func newDecisionLinkListCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List linked machines and how fresh each is",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ctx.refuseDecisionLinkRemote("ao decision link ls"); err != nil {
				return err
			}
			var peers decisionPeersAPIResponse
			if err := ctx.getJSON(cmd.Context(), "decision-peers", &peers); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), peers)
			}
			return writeDecisionPeers(cmd.OutOrStdout(), peers.Peers, ctx.now())
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func writeDecisionPeers(w io.Writer, peers []decisionPeerAPI, now time.Time) error {
	if len(peers) == 0 {
		_, err := fmt.Fprintln(w, "Decisions on this computer only. Link a machine with: ao decision link add <address> --password-stdin")
		return err
	}
	for _, p := range peers {
		direction := "linked"
		if p.Direction == "inbound" {
			direction = "links to this computer"
		}
		last := "never synced"
		if p.LastSuccessAt != nil {
			last = "last synced " + sessionAge(now, *p.LastSuccessAt)
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", peerName(p.Label, p.HostID), direction, peerStateText(p), last); err != nil {
			return err
		}
	}
	return nil
}

func peerStateText(p decisionPeerAPI) string {
	switch p.State {
	case "linked_in_sync":
		return "in sync"
	case "linked_behind":
		return "behind"
	case "unreachable":
		return "not answering"
	case "revoked":
		return "no longer accepts this computer"
	case "incompatible":
		return "needs a newer Orchestrator"
	case "identity_conflict":
		return "shares an identity with another machine"
	case "inbound":
		return "syncs when it connects"
	}
	return p.State
}

func newDecisionLinkRemoveCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <machine>",
		Short: "Unlink a machine; decisions stay on both machines",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ctx.refuseDecisionLinkRemote("ao decision link rm"); err != nil {
				return err
			}
			peer, err := ctx.resolveDecisionPeer(cmd, args[0])
			if err != nil {
				return err
			}
			if err := ctx.deleteJSON(cmd.Context(), "decision-peers/"+url.PathEscape(peer.HostID), nil); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Unlinked %s. Its decisions stay on both machines.\n", peerName(peer.Label, peer.HostID))
			return err
		},
	}
}

func newDecisionSyncCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "sync [<machine>]",
		Short: "Sync decisions with linked machines now",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ctx.refuseDecisionLinkRemote("ao decision sync"); err != nil {
				return err
			}
			var req decisionSyncAPIRequest
			if len(args) == 1 {
				peer, err := ctx.resolveDecisionPeer(cmd, args[0])
				if err != nil {
					return err
				}
				req.HostID = peer.HostID
			}
			var synced decisionSyncAPIResponse
			if err := ctx.postJSON(cmd.Context(), "decision-peers/sync", req, &synced); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(synced.Results) == 0 {
				_, err := fmt.Fprintln(out, "No linked machines.")
				return err
			}
			for _, r := range synced.Results {
				if _, err := fmt.Fprintf(out, "%s: %s\n", peerName(r.Label, r.HostID), syncResultText(r)); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func syncResultText(r decisionSyncResultAPI) string {
	if r.ErrorCode != "" {
		return peerStateText(decisionPeerAPI{State: map[string]string{
			"unreachable": "unreachable", "revoked": "revoked", "unauthorized": "revoked",
			"incompatible": "incompatible", "identity_changed": "identity_changed", "server_error": "unreachable",
		}[r.ErrorCode]}) + " (" + r.ErrorCode + ")"
	}
	text := fmt.Sprintf("pulled %d, pushed %d", r.Pulled, r.Pushed)
	if r.InSync {
		return text + ", in sync"
	}
	return text + ", not yet in sync"
}

// resolveDecisionPeer finds a linked machine by label or host id.
func (c *commandContext) resolveDecisionPeer(cmd *cobra.Command, name string) (decisionPeerAPI, error) {
	var peers decisionPeersAPIResponse
	if err := c.getJSON(cmd.Context(), "decision-peers", &peers); err != nil {
		return decisionPeerAPI{}, err
	}
	var matches []decisionPeerAPI
	for _, p := range peers.Peers {
		if p.HostID == name || strings.EqualFold(p.Label, name) {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return decisionPeerAPI{}, usageError{fmt.Errorf("no linked machine is named %q; see ao decision link ls", name)}
	default:
		return decisionPeerAPI{}, usageError{fmt.Errorf("more than one linked machine is named %q; use its host id", name)}
	}
}

func peerName(label, hostID string) string {
	if strings.TrimSpace(label) != "" {
		return label
	}
	return hostID
}

func (c *commandContext) now() time.Time {
	if c.deps.Now != nil {
		return c.deps.Now()
	}
	return time.Now()
}
