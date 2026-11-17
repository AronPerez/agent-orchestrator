package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type decisionOptionAPI struct {
	Label       string `json:"label"`
	Consequence string `json:"consequence"`
	Recommended bool   `json:"recommended,omitempty"`
}

type decisionLinkAPI struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type decisionRaiseAPIRequest struct {
	Title     string              `json:"title"`
	Context   string              `json:"context"`
	Options   []decisionOptionAPI `json:"options"`
	Links     []decisionLinkAPI   `json:"links,omitempty"`
	Reference string              `json:"reference,omitempty"`
}

type decisionCallAPI struct {
	ID                string              `json:"id"`
	Link              string              `json:"link"`
	State             string              `json:"state"`
	StateLabel        string              `json:"stateLabel"`
	Title             string              `json:"title"`
	Context           string              `json:"context"`
	Options           []decisionOptionAPI `json:"options"`
	Links             []decisionLinkAPI   `json:"links"`
	Reference         string              `json:"reference,omitempty"`
	ProjectID         string              `json:"projectId,omitempty"`
	ProjectName       string              `json:"projectName,omitempty"`
	StreamSessionID   string              `json:"streamSessionId,omitempty"`
	StreamName        string              `json:"streamName,omitempty"`
	HostID            string              `json:"hostId"`
	MachineLabel      string              `json:"machineLabel"`
	OwnerSessionID    string              `json:"ownerSessionId"`
	OwnerSessionName  string              `json:"ownerSessionName"`
	RaisedAt          time.Time           `json:"raisedAt"`
	Choice            string              `json:"choice,omitempty"`
	Option            int                 `json:"option,omitempty"`
	ChosenLabel       string              `json:"chosenLabel,omitempty"`
	Note              string              `json:"note,omitempty"`
	AnsweredAt        *time.Time          `json:"answeredAt,omitempty"`
	AnswerSurface     string              `json:"answerSurface,omitempty"`
	Outcome           string              `json:"outcome,omitempty"`
	OutcomeLink       string              `json:"outcomeLink,omitempty"`
	OutcomeAt         *time.Time          `json:"outcomeAt,omitempty"`
	Retired           bool                `json:"retired"`
	Delivery          string              `json:"delivery,omitempty"`
	DeliveredTo       string              `json:"deliveredTo,omitempty"`
	DeliveredToName   string              `json:"deliveredToName,omitempty"`
	DeliveredFallback bool                `json:"deliveredFallback,omitempty"`
	ChangedAfterClose bool                `json:"changedAfterClose,omitempty"`
}

type decisionEntryAPI struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	WrittenAt     time.Time `json:"writtenAt"`
	HostID        string    `json:"hostId"`
	AuthorKind    string    `json:"authorKind"`
	AuthorSurface string    `json:"authorSurface,omitempty"`
	AuthorSession string    `json:"authorSession,omitempty"`
	Choice        string    `json:"choice,omitempty"`
	Option        int       `json:"option,omitempty"`
	Note          string    `json:"note,omitempty"`
	Text          string    `json:"text,omitempty"`
	Link          string    `json:"link,omitempty"`
	Status        string    `json:"status,omitempty"`
	SessionID     string    `json:"sessionId,omitempty"`
	SessionName   string    `json:"sessionName,omitempty"`
	Fallback      bool      `json:"fallback,omitempty"`
	Retired       *bool     `json:"retired,omitempty"`
}

type decisionListAPIResponse struct {
	Calls []decisionCallAPI `json:"calls"`
}

type decisionDetailAPIResponse struct {
	Call    decisionCallAPI    `json:"call"`
	History []decisionEntryAPI `json:"history"`
}

type decisionRaiseOptions struct {
	title, context, file, reference string
	options, links                  []string
	recommend                       int
	json                            bool
}

func newDecisionCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{Use: "decision", Short: "Raise and inspect durable decision calls"}
	cmd.AddCommand(
		newDecisionRaiseCommand(ctx),
		newDecisionSearchCommand(ctx),
		newDecisionListCommand(ctx),
		newDecisionShowCommand(ctx),
	)
	return cmd
}

func newDecisionRaiseCommand(ctx *commandContext) *cobra.Command {
	var opts decisionRaiseOptions
	cmd := &cobra.Command{
		Use:   "raise",
		Short: "Raise a decision call from this session",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ctx.refuseRemoteDecisionSession("ao decision raise"); err != nil {
				return err
			}
			request, err := decisionRaiseRequest(cmd, opts)
			if err != nil {
				return err
			}
			var response decisionCallAPI
			if err := ctx.postJSON(cmd.Context(), "decisions", request, &response); err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Raised %s\n%s\n", response.ID, response.Link)
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.title, "title", "", "Question to decide")
	f.StringVar(&opts.context, "context", "", "What the person needs to know")
	f.StringArrayVar(&opts.options, "option", nil, `Choice as "<label> :: <what it leads to>" (repeatable)`)
	f.IntVar(&opts.recommend, "recommend", 0, "Position of the recommended option")
	f.StringArrayVar(&opts.links, "link", nil, `Supporting link as "<label>=<url>" (repeatable)`)
	f.StringVar(&opts.reference, "ref", "", "Issue or plan reference")
	f.StringVar(&opts.file, "file", "", "Read the call JSON from a file, or - for stdin")
	f.BoolVar(&opts.json, "json", false, "Print JSON")
	return cmd
}

func decisionRaiseRequest(cmd *cobra.Command, opts decisionRaiseOptions) (decisionRaiseAPIRequest, error) {
	if cmd.Flags().Changed("file") {
		for _, name := range []string{"title", "context", "option", "recommend", "link", "ref"} {
			if cmd.Flags().Changed(name) {
				return decisionRaiseAPIRequest{}, usageError{errors.New("usage: --file cannot be combined with the other call flags")}
			}
		}
		var (
			raw []byte
			err error
		)
		if opts.file == "-" {
			raw, err = io.ReadAll(cmd.InOrStdin())
		} else {
			raw, err = os.ReadFile(opts.file)
		}
		if err != nil {
			return decisionRaiseAPIRequest{}, err
		}
		var request decisionRaiseAPIRequest
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&request); err != nil {
			return decisionRaiseAPIRequest{}, fmt.Errorf("read decision JSON: %w", err)
		}
		if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			if err == nil {
				err = errors.New("multiple JSON values")
			}
			return decisionRaiseAPIRequest{}, fmt.Errorf("read decision JSON: %w", err)
		}
		return request, nil
	}

	options := make([]decisionOptionAPI, len(opts.options))
	for i, raw := range opts.options {
		label, consequence, ok := strings.Cut(raw, "::")
		if !ok {
			return decisionRaiseAPIRequest{}, usageError{errors.New(`usage: write each --option as "<label> :: <what it leads to>"`)}
		}
		options[i] = decisionOptionAPI{Label: strings.TrimSpace(label), Consequence: strings.TrimSpace(consequence)}
	}
	if opts.recommend < 1 || opts.recommend > len(options) {
		return decisionRaiseAPIRequest{}, usageError{errors.New("usage: --recommend takes the position of one of the options")}
	}
	options[opts.recommend-1].Recommended = true

	links := make([]decisionLinkAPI, len(opts.links))
	for i, raw := range opts.links {
		label, target, ok := strings.Cut(raw, "=")
		if !ok {
			return decisionRaiseAPIRequest{}, usageError{errors.New(`usage: write each --link as "<label>=<url>"`)}
		}
		links[i] = decisionLinkAPI{Label: strings.TrimSpace(label), URL: strings.TrimSpace(target)}
	}
	return decisionRaiseAPIRequest{
		Title: opts.title, Context: opts.context, Options: options, Links: links, Reference: opts.reference,
	}, nil
}

func newDecisionSearchCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "search <words...>",
		Short: "Search decision calls",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageError{errors.New("usage: say what to search for")}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ctx.refuseRemoteDecisionSession("ao decision search"); err != nil {
				return err
			}
			var response decisionListAPIResponse
			if err := ctx.getJSON(cmd.Context(), "decisions?q="+url.QueryEscape(strings.Join(args, " ")), &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			return writeDecisionCalls(cmd.OutOrStdout(), response.Calls, "No decisions match.", false)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func newDecisionListCommand(ctx *commandContext) *cobra.Command {
	var needsYou, jsonOutput bool
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List decision calls",
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			scope := "mine"
			if needsYou || callerSession() == "" {
				scope = "needs_you"
			}
			if scope == "mine" {
				if err := ctx.refuseRemoteDecisionSession("ao decision ls"); err != nil {
					return err
				}
			}
			var response decisionListAPIResponse
			if err := ctx.getJSON(cmd.Context(), "decisions?scope="+scope, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			return writeDecisionCalls(cmd.OutOrStdout(), response.Calls, "No calls.", scope == "mine")
		},
	}
	cmd.Flags().BoolVar(&needsYou, "needs-you", false, "List calls waiting for the person")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func (c *commandContext) refuseRemoteDecisionSession(command string) error {
	session := callerSession()
	if c.remote == nil || session == "" {
		return nil
	}
	return usageError{fmt.Errorf(
		"AO_SESSION_ID %q names a session on this machine, but %s targets the remote daemon at %s; `%s` must use the local daemon",
		session, c.remote.source, c.remote.baseURL, command,
	)}
}

func newDecisionShowCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a decision call and its history",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usageError{errors.New("usage: ao decision show <id>")}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var response decisionDetailAPIResponse
			if err := ctx.getJSON(cmd.Context(), "decisions/"+url.PathEscape(args[0]), &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			return writeDecisionDetail(cmd.OutOrStdout(), response)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func writeDecisionCalls(w io.Writer, calls []decisionCallAPI, empty string, outcomeOwed bool) error {
	if len(calls) == 0 {
		_, err := fmt.Fprintln(w, empty)
		return err
	}
	for i, call := range calls {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if err := writeDecisionCall(w, call, outcomeOwed); err != nil {
			return err
		}
	}
	return nil
}

func writeDecisionCall(w io.Writer, call decisionCallAPI, outcomeOwed bool) error {
	if _, err := fmt.Fprintf(w, "%s  %s  %s\n", call.ID, call.StateLabel, call.Title); err != nil {
		return err
	}
	metadata := nonemptyStrings(call.ProjectName, call.StreamName, call.MachineLabel)
	if !call.RaisedAt.IsZero() {
		metadata = append(metadata, call.RaisedAt.Format("2006-01-02"))
	}
	if _, err := fmt.Fprintf(w, "  %s\n", strings.Join(metadata, " · ")); err != nil {
		return err
	}
	if call.Choice != "" {
		answer := "Chose: " + call.ChosenLabel
		if call.Note != "" {
			answer += " · Note: " + call.Note
		}
		if _, err := fmt.Fprintln(w, "  "+answer); err != nil {
			return err
		}
	}
	if call.Outcome != "" {
		if _, err := fmt.Fprintln(w, "  Outcome: "+call.Outcome); err != nil {
			return err
		}
	}
	if call.Retired {
		if _, err := fmt.Fprintln(w, "  No longer applies"); err != nil {
			return err
		}
	}
	if outcomeOwed && call.State == "in_progress" {
		_, err := fmt.Fprintf(w, "  Outcome owed: ao decision outcome %s --text \"<what happened>\"\n", call.ID)
		return err
	}
	return nil
}

func writeDecisionDetail(w io.Writer, detail decisionDetailAPIResponse) error {
	call := detail.Call
	if err := writeDecisionCall(w, call, false); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Link: %s\nContext: %s\n", call.Link, call.Context); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "Options:"); err != nil {
		return err
	}
	for i, option := range call.Options {
		recommended := ""
		if option.Recommended {
			recommended = " (recommended)"
		}
		if _, err := fmt.Fprintf(w, "  %d. %s%s — %s\n", i+1, option.Label, recommended, option.Consequence); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, "Links:"); err != nil {
		return err
	}
	for _, link := range call.Links {
		if _, err := fmt.Fprintf(w, "  %s: %s\n", link.Label, link.URL); err != nil {
			return err
		}
	}
	lines := []string{
		"Reference: " + call.Reference,
		"Project: " + joinNamedID(call.ProjectName, call.ProjectID),
		"Stream: " + joinNamedID(call.StreamName, call.StreamSessionID),
		"Host: " + call.HostID,
		"Machine: " + call.MachineLabel,
		"Owner: " + joinNamedID(call.OwnerSessionName, call.OwnerSessionID),
		"Raised: " + formatDecisionTime(&call.RaisedAt),
		"Choice: " + call.Choice,
		fmt.Sprintf("Option: %d", call.Option),
		"Chosen label: " + call.ChosenLabel,
		"Note: " + call.Note,
		"Answered: " + formatDecisionTime(call.AnsweredAt),
		"Answer surface: " + call.AnswerSurface,
		"Outcome: " + call.Outcome,
		"Outcome link: " + call.OutcomeLink,
		"Outcome at: " + formatDecisionTime(call.OutcomeAt),
		fmt.Sprintf("Retired: %t", call.Retired),
		"Delivery: " + call.Delivery,
		"Delivered to: " + joinNamedID(call.DeliveredToName, call.DeliveredTo),
		fmt.Sprintf("Delivered fallback: %t", call.DeliveredFallback),
		fmt.Sprintf("Changed after close: %t", call.ChangedAfterClose),
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, "History:"); err != nil {
		return err
	}
	for _, entry := range detail.History {
		if _, err := fmt.Fprintf(w, "  %s  %s  %s%s\n", entry.WrittenAt.Format(time.RFC3339), entry.Kind, decisionEntryAuthor(entry), decisionEntrySummary(entry)); err != nil {
			return err
		}
	}
	return nil
}

func decisionEntryAuthor(entry decisionEntryAPI) string {
	switch entry.AuthorKind {
	case "person":
		if entry.AuthorSurface != "" {
			return "person (" + entry.AuthorSurface + ")"
		}
		return "person"
	case "session":
		if entry.AuthorSession != "" {
			return "session " + entry.AuthorSession
		}
	}
	return entry.AuthorKind
}

func decisionEntrySummary(entry decisionEntryAPI) string {
	parts := make([]string, 0, 4)
	if entry.Choice != "" {
		choice := entry.Choice
		if entry.Option > 0 {
			choice += fmt.Sprintf(" %d", entry.Option)
		}
		parts = append(parts, choice)
	}
	parts = append(parts, nonemptyStrings(entry.Note, entry.Text, entry.Link, entry.Status, joinNamedID(entry.SessionName, entry.SessionID))...)
	if entry.Fallback {
		parts = append(parts, "fallback")
	}
	if entry.Retired != nil {
		parts = append(parts, fmt.Sprintf("retired=%t", *entry.Retired))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  " + strings.Join(parts, " · ")
}

func nonemptyStrings(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func joinNamedID(name, id string) string {
	if name == "" {
		return id
	}
	if id == "" || id == name {
		return name
	}
	return name + " (" + id + ")"
}

func formatDecisionTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}
