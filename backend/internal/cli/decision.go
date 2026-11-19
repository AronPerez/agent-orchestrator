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
	Urgency   string              `json:"urgency,omitempty"`
	Kind      string              `json:"kind,omitempty"`
	// Prediction is what the session would have decided on its own.
	Prediction *decisionPredictionAPI `json:"prediction,omitempty"`
}

type decisionPredictionAPI struct {
	Option int    `json:"option"`
	Label  string `json:"label,omitempty"`
	Why    string `json:"why"`
	Undo   string `json:"undo"`
}

type decisionKindAPI struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Reserved bool   `json:"reserved"`
}

type decisionKindsAPIResponse struct {
	ProjectID string            `json:"projectId,omitempty"`
	Mode      string            `json:"mode"`
	Kinds     []decisionKindAPI `json:"kinds"`
}

type decisionObserveRowAPI struct {
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Reserved bool   `json:"reserved"`
	Calls    int    `json:"calls"`
	Answered int    `json:"answered"`
	Agreed   int    `json:"agreed"`
	Differed int    `json:"differed"`
}

type decisionObserveAPIResponse struct {
	ProjectID string                  `json:"projectId,omitempty"`
	Mode      string                  `json:"mode"`
	Rows      []decisionObserveRowAPI `json:"rows"`
}

type decisionDecideAPIRequest struct {
	Kind      string            `json:"kind"`
	What      string            `json:"what"`
	Why       string            `json:"why"`
	Undo      string            `json:"undo"`
	Verify    string            `json:"verify,omitempty"`
	Links     []decisionLinkAPI `json:"links,omitempty"`
	Reference string            `json:"reference,omitempty"`
}

type decisionOverruleAPIRequest struct {
	Note string `json:"note"`
}

type decisionDecidedAPI struct {
	Undo          string     `json:"undo"`
	Verify        string     `json:"verify,omitempty"`
	OutsidePolicy string     `json:"outsidePolicy,omitempty"`
	CarriedOut    bool       `json:"carriedOut"`
	CarriedOutAt  *time.Time `json:"carriedOutAt,omitempty"`
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
	StandingEntryID   string              `json:"standingEntryId,omitempty"`
	Outcome           string              `json:"outcome,omitempty"`
	OutcomeLink       string              `json:"outcomeLink,omitempty"`
	OutcomeAt         *time.Time          `json:"outcomeAt,omitempty"`
	Retired           bool                `json:"retired"`
	Delivery          string              `json:"delivery,omitempty"`
	DeliveredTo       string              `json:"deliveredTo,omitempty"`
	DeliveredToName   string              `json:"deliveredToName,omitempty"`
	DeliveredFallback bool                `json:"deliveredFallback,omitempty"`
	ChangedAfterClose bool                `json:"changedAfterClose,omitempty"`
	Urgency           string              `json:"urgency,omitempty"`
	Kind              string              `json:"kind,omitempty"`
	KindLabel         string              `json:"kindLabel,omitempty"`
	Reserved          bool                `json:"reserved,omitempty"`
	AutonomyMode      string              `json:"autonomyMode,omitempty"`
	// Prediction is what the session would have decided on its own.
	Prediction       *decisionPredictionAPI `json:"prediction,omitempty"`
	PredictionAgreed *bool                  `json:"predictionAgreed,omitempty"`
	Decided          *decisionDecidedAPI    `json:"decided,omitempty"`
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
	ForEntry      string    `json:"forEntry,omitempty"`
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

type decisionAnswerAPIRequest struct {
	Choice string `json:"choice"`
	Option int    `json:"option,omitempty"`
	Note   string `json:"note,omitempty"`
}

type decisionDismissAPIRequest struct {
	Note string `json:"note,omitempty"`
}

type decisionOutcomeAPIRequest struct {
	ForEntry string `json:"forEntry,omitempty"`
	Text     string `json:"text"`
	Link     string `json:"link,omitempty"`
}

type decisionRetireAPIRequest struct {
	Retired bool `json:"retired"`
}

type decisionRaiseOptions struct {
	title, context, file, reference string
	urgency, kind, why, undo        string
	options, links                  []string
	recommend, wouldDecide          int
	json                            bool
}

func newDecisionCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{Use: "decision", Short: "Raise and inspect durable decision calls"}
	cmd.AddCommand(
		newDecisionRaiseCommand(ctx),
		newDecisionSearchCommand(ctx),
		newDecisionListCommand(ctx),
		newDecisionShowCommand(ctx),
		newDecisionAnswerCommand(ctx),
		newDecisionDismissCommand(ctx),
		newDecisionOutcomeCommand(ctx),
		newDecisionWithdrawCommand(ctx),
		newDecisionRetireCommand(ctx),
		newDecisionKindsCommand(ctx),
		newDecisionObserveCommand(ctx),
		newDecisionDecideCommand(ctx),
		newDecisionOverruleCommand(ctx),
		newDecisionCarriedOutCommand(ctx),
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
	f.StringVar(&opts.urgency, "urgency", "can-wait", "blocking when all your remaining work depends on the answer, otherwise can-wait")
	f.StringVar(&opts.kind, "kind", "", "Kind of decision (see ao decision kinds)")
	f.IntVar(&opts.wouldDecide, "would-decide", 0, "Position of the option you would have picked on your own")
	f.StringVar(&opts.why, "why", "", "Why you would have picked it (with --would-decide)")
	f.StringVar(&opts.undo, "undo", "", "How the person could undo it (with --would-decide)")
	f.StringVar(&opts.file, "file", "", "Read the call JSON from a file, or - for stdin")
	f.BoolVar(&opts.json, "json", false, "Print JSON")
	return cmd
}

func decisionRaiseRequest(cmd *cobra.Command, opts decisionRaiseOptions) (decisionRaiseAPIRequest, error) {
	if cmd.Flags().Changed("file") {
		for _, name := range []string{"title", "context", "option", "recommend", "link", "ref", "urgency", "kind", "would-decide", "why", "undo"} {
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

	links, err := parseDecisionLinks(opts.links)
	if err != nil {
		return decisionRaiseAPIRequest{}, err
	}
	var urgency string
	switch strings.TrimSpace(opts.urgency) {
	case "blocking":
		urgency = "blocking"
	case "can-wait", "can_wait":
		urgency = "can_wait"
	default:
		return decisionRaiseAPIRequest{}, usageError{errors.New("usage: --urgency is blocking or can-wait")}
	}
	request := decisionRaiseAPIRequest{
		Title: opts.title, Context: opts.context, Options: options, Links: links, Reference: opts.reference,
		Urgency: urgency, Kind: strings.TrimSpace(opts.kind),
	}
	if cmd.Flags().Changed("would-decide") {
		request.Prediction = &decisionPredictionAPI{Option: opts.wouldDecide, Why: opts.why, Undo: opts.undo}
	} else if cmd.Flags().Changed("why") || cmd.Flags().Changed("undo") {
		return decisionRaiseAPIRequest{}, usageError{errors.New("usage: --why and --undo go with --would-decide")}
	}
	return request, nil
}

func decisionProjectPath(base, project string) string {
	if project = strings.TrimSpace(project); project != "" {
		return base + "?project=" + url.QueryEscape(project)
	}
	return base
}

func newDecisionObserveCommand(ctx *commandContext) *cobra.Command {
	var project string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "observe",
		Short: "Show how often sessions predicted the person's answers in observe mode",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var response decisionObserveAPIResponse
			if err := ctx.getJSON(cmd.Context(), decisionProjectPath("decisions/observe", project), &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			return writeDecisionObserve(cmd.OutOrStdout(), response)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project id (the person's shell only; a session always gets its own project)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func writeDecisionObserve(w io.Writer, report decisionObserveAPIResponse) error {
	project := report.ProjectID
	if project == "" {
		project = "(none)"
	}
	if _, err := fmt.Fprintf(w, "Project %s · autonomy mode: %s\n", project, report.Mode); err != nil {
		return err
	}
	if len(report.Rows) == 0 {
		_, err := fmt.Fprintln(w, "No calls raised in observe mode yet.")
		return err
	}
	for _, row := range report.Rows {
		name := fmt.Sprintf("%s (%s)", row.Label, row.Kind)
		var line string
		if row.Reserved {
			line = fmt.Sprintf("  %s, reserved: %s", name, plural(row.Calls, "call"))
		} else {
			line = fmt.Sprintf("  %s: %s, %d answered, %d agreed, %d differed", name, plural(row.Calls, "call"), row.Answered, row.Agreed, row.Differed)
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func parseDecisionLinks(raw []string) ([]decisionLinkAPI, error) {
	links := make([]decisionLinkAPI, len(raw))
	for i, value := range raw {
		label, target, ok := strings.Cut(value, "=")
		if !ok {
			return nil, usageError{errors.New(`usage: write each --link as "<label>=<url>"`)}
		}
		links[i] = decisionLinkAPI{Label: strings.TrimSpace(label), URL: strings.TrimSpace(target)}
	}
	return links, nil
}

func newDecisionKindsCommand(ctx *commandContext) *cobra.Command {
	var project string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "kinds",
		Short: "List decision kinds and which ones the project reserves for the person",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := decisionProjectPath("decisions/kinds", project)
			var response decisionKindsAPIResponse
			if err := ctx.getJSON(cmd.Context(), path, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			return writeDecisionKinds(cmd.OutOrStdout(), response)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project id (the person's shell only; a session always gets its own project)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

func writeDecisionKinds(w io.Writer, policy decisionKindsAPIResponse) error {
	project := policy.ProjectID
	if project == "" {
		project = "(none)"
	}
	if _, err := fmt.Fprintf(w, "Project %s · autonomy mode: %s\n", project, policy.Mode); err != nil {
		return err
	}
	for _, group := range []struct {
		heading  string
		reserved bool
	}{{"Reserved for the person:", true}, {"Not reserved:", false}} {
		if _, err := fmt.Fprintln(w, group.heading); err != nil {
			return err
		}
		for _, kind := range policy.Kinds {
			if kind.Reserved != group.reserved {
				continue
			}
			if _, err := fmt.Fprintf(w, "  %s  %s\n", kind.ID, kind.Label); err != nil {
				return err
			}
		}
	}
	return nil
}

func newDecisionSearchCommand(ctx *commandContext) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "search <words...>",
		Short: "Search decision calls",
		Args: func(_ *cobra.Command, args []string) error {
			if strings.TrimSpace(strings.Join(args, " ")) == "" {
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
	var needsYou, decided, jsonOutput bool
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List decision calls",
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if needsYou && decided {
				return usageError{errors.New("usage: choose one of --needs-you and --decided")}
			}
			scope := "mine"
			switch {
			case decided:
				scope = "decided"
			case needsYou || callerSession() == "":
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
	cmd.Flags().BoolVar(&decided, "decided", false, "List decisions made for the person that stand")
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
			_, err := decisionIDArg(args[0])
			return err
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := decisionIDArg(args[0])
			if err != nil {
				return err
			}
			var response decisionDetailAPIResponse
			if err := ctx.getJSON(cmd.Context(), "decisions/"+url.PathEscape(id), &response); err != nil {
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

func newDecisionAnswerCommand(ctx *commandContext) *cobra.Command {
	var pick int
	var other bool
	var note string
	cmd := &cobra.Command{
		Use:   "answer <id>",
		Short: "Answer a decision call",
		Args:  decisionIDArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := decisionIDArg(args[0])
			if err != nil {
				return err
			}
			pickSet := cmd.Flags().Changed("pick")
			if pickSet == other {
				return usageError{errors.New("usage: choose with --pick <n> or --other")}
			}
			request := decisionAnswerAPIRequest{Choice: "option", Option: pick, Note: note}
			if other {
				request.Choice, request.Option = "other", 0
			}
			var response decisionCallAPI
			if err := ctx.postJSON(cmd.Context(), decisionActionPath(id, "answer"), request, &response); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Answered %s: %s\n", response.ID, response.ChosenLabel)
			return err
		},
	}
	cmd.Flags().IntVar(&pick, "pick", 0, "Position of the option to choose")
	cmd.Flags().BoolVar(&other, "other", false, "Choose something outside the listed options")
	cmd.Flags().StringVar(&note, "note", "", "Optional note for the owning session")
	return cmd
}

func newDecisionDecideCommand(ctx *commandContext) *cobra.Command {
	var request decisionDecideAPIRequest
	var links []string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "decide",
		Short: "Record a decision this session made on the person's behalf (autonomy mode on)",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := ctx.refuseRemoteDecisionSession("ao decision decide"); err != nil {
				return err
			}
			parsed, err := parseDecisionLinks(links)
			if err != nil {
				return err
			}
			request.Links = parsed
			var response decisionCallAPI
			if err := ctx.postJSON(cmd.Context(), "decisions/decided", request, &response); err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), response)
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Recorded %s\n%s\n", response.ID, response.Link); err != nil {
				return err
			}
			if note := outsidePolicyNote(response.ID, response.Decided); note != "" {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), note)
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Now carry it out, then confirm: ao decision carried-out %s\nIf you do not carry it out, close it: ao decision outcome %s --text \"Not carried out: <why>\"\n", response.ID, response.ID)
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&request.Kind, "kind", "", "Kind of decision; it must not be reserved (see ao decision kinds)")
	f.StringVar(&request.What, "what", "", "What you decided, in one line")
	f.StringVar(&request.Why, "why", "", "Why you decided it")
	f.StringVar(&request.Undo, "undo", "", "How the person can undo it")
	f.StringVar(&request.Verify, "verify", "", "Optional: how to tell it worked")
	f.StringArrayVar(&links, "link", nil, `Supporting link as "<label>=<url>" (repeatable)`)
	f.StringVar(&request.Reference, "ref", "", "Issue or plan reference")
	f.BoolVar(&jsonOutput, "json", false, "Print JSON")
	return cmd
}

// outsidePolicyNote tells a session whose instructions are out of date that
// its record was kept but the decision is no longer its to make.
func outsidePolicyNote(id string, decided *decisionDecidedAPI) string {
	if decided == nil {
		return ""
	}
	var why string
	switch decided.OutsidePolicy {
	case "autonomy_not_on":
		why = "this project no longer lets sessions decide on the person's behalf"
	case "kind_reserved":
		why = "this kind of decision is now reserved for the person"
	default:
		return ""
	}
	return fmt.Sprintf("Kept, but %s.\n"+
		"If you have not acted yet, do not: close this record with ao decision outcome %s --text \"Not carried out: policy changed\" and raise a call instead.\n"+
		"If you already acted, do not close it: confirm with ao decision carried-out %s and tell the person, so they can overrule it.", why, id, id)
}

func newDecisionCarriedOutCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "carried-out <id>",
		Short: "Confirm this session carried out a decision it recorded",
		Args:  decisionIDArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ctx.refuseRemoteDecisionSession("ao decision carried-out"); err != nil {
				return err
			}
			id, err := decisionIDArg(args[0])
			if err != nil {
				return err
			}
			var response decisionCallAPI
			if err := ctx.postJSON(cmd.Context(), decisionActionPath(id, "carried-out"), struct{}{}, &response); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Confirmed %s as carried out\n", response.ID)
			return err
		},
	}
}

func newDecisionOverruleCommand(ctx *commandContext) *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use:   "overrule <id>",
		Short: "Overrule a decision made on your behalf",
		Args:  decisionIDArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := decisionIDArg(args[0])
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("note") {
				return usageError{errors.New("usage: ao decision overrule <id> --note <what to do instead>")}
			}
			var response decisionCallAPI
			if err := ctx.postJSON(cmd.Context(), decisionActionPath(id, "overrule"), decisionOverruleAPIRequest{Note: note}, &response); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Overruled %s\n", response.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "What the session should do instead")
	return cmd
}

func newDecisionDismissCommand(ctx *commandContext) *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use:   "dismiss <id>",
		Short: "Dismiss a decision call without choosing",
		Args:  decisionIDArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := decisionIDArg(args[0])
			if err != nil {
				return err
			}
			var response decisionCallAPI
			if err := ctx.postJSON(cmd.Context(), decisionActionPath(id, "dismiss"), decisionDismissAPIRequest{Note: note}, &response); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Dismissed %s\n", response.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "Optional reason for dismissing the call")
	return cmd
}

func newDecisionOutcomeCommand(ctx *commandContext) *cobra.Command {
	var text, link, forEntry string
	cmd := &cobra.Command{
		Use:   "outcome <id>",
		Short: "Record what happened after a decision",
		Args:  decisionIDArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := decisionIDArg(args[0])
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("text") {
				return usageError{errors.New("usage: --text is required")}
			}
			if err := ctx.refuseRemoteDecisionSession("ao decision outcome"); err != nil {
				return err
			}
			var response decisionCallAPI
			if err := ctx.postJSON(cmd.Context(), decisionActionPath(id, "outcome"), decisionOutcomeAPIRequest{ForEntry: forEntry, Text: text, Link: link}, &response); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Recorded the outcome of %s\n", response.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&text, "text", "", "One-line outcome")
	cmd.Flags().StringVar(&link, "link", "", "Optional supporting URL")
	cmd.Flags().StringVar(&forEntry, "for-answer", "", "Answer entry named by the delivered instruction")
	return cmd
}

func newDecisionWithdrawCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "withdraw <id>",
		Short: "Withdraw a decision call",
		Args:  decisionIDArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := decisionIDArg(args[0])
			if err != nil {
				return err
			}
			if err := ctx.refuseRemoteDecisionSession("ao decision withdraw"); err != nil {
				return err
			}
			var response decisionCallAPI
			if err := ctx.postJSON(cmd.Context(), decisionActionPath(id, "withdraw"), nil, &response); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Withdrew %s\n", response.ID)
			return err
		},
	}
}

func newDecisionRetireCommand(ctx *commandContext) *cobra.Command {
	var undo bool
	cmd := &cobra.Command{
		Use:   "retire <id>",
		Short: "Mark whether a finished decision still applies",
		Args:  decisionIDArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := decisionIDArg(args[0])
			if err != nil {
				return err
			}
			var response decisionCallAPI
			if err := ctx.postJSON(cmd.Context(), decisionActionPath(id, "retire"), decisionRetireAPIRequest{Retired: !undo}, &response); err != nil {
				return err
			}
			message := "no longer applies"
			if undo {
				message = "applies again"
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", response.ID, message)
			return err
		},
	}
	cmd.Flags().BoolVar(&undo, "undo", false, "Mark the decision as applying again")
	return cmd
}

func decisionIDArgs(_ *cobra.Command, args []string) error {
	if len(args) != 1 {
		return usageError{errors.New("usage: ao decision <command> <id>")}
	}
	_, err := decisionIDArg(args[0])
	return err
}

func decisionIDArg(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", usageError{errors.New("use a valid decision ID or ao://decisions/ link")}
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", usageError{errors.New("use a valid decision ID or ao://decisions/ link")}
	}
	if parsed.Scheme == "" {
		return raw, nil
	}
	if parsed.Scheme != "ao" || parsed.Host != "decisions" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", usageError{errors.New("use a valid decision ID or ao://decisions/ link")}
	}
	escapedID := strings.TrimPrefix(parsed.EscapedPath(), "/")
	if escapedID == "" || strings.Contains(escapedID, "/") {
		return "", usageError{errors.New("use a valid decision ID or ao://decisions/ link")}
	}
	id, err := url.PathUnescape(escapedID)
	if err != nil || strings.TrimSpace(id) == "" || strings.Contains(id, "/") {
		return "", usageError{errors.New("use a valid decision ID or ao://decisions/ link")}
	}
	return id, nil
}

func decisionActionPath(id, action string) string {
	return "decisions/" + url.PathEscape(id) + "/" + action
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
	if call.Decided != nil && !call.Decided.CarriedOut && (call.State == "decided" || call.State == "in_progress") {
		if _, err := fmt.Fprintf(w, "  Not confirmed carried out: ao decision carried-out %s\n", call.ID); err != nil {
			return err
		}
	}
	if outcomeOwed && call.State == "in_progress" {
		_, err := fmt.Fprintf(w, "  Outcome owed: ao decision outcome %s --for-answer %s --text \"<what happened>\"\n", call.ID, call.StandingEntryID)
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
		"Urgency: " + call.Urgency,
		"Kind: " + joinNamedID(call.KindLabel, call.Kind),
		fmt.Sprintf("Reserved: %t", call.Reserved),
		"Autonomy mode: " + call.AutonomyMode,
	}
	if prediction := call.Prediction; prediction != nil {
		lines = append(lines, fmt.Sprintf("Would have decided: %d. %s — %s (undo: %s)", prediction.Option, prediction.Label, prediction.Why, prediction.Undo))
	}
	if call.Decided != nil {
		lines = append(lines, "Undo: "+call.Decided.Undo, "Verify: "+call.Decided.Verify, "Outside policy: "+call.Decided.OutsidePolicy,
			fmt.Sprintf("Carried out: %t", call.Decided.CarriedOut), "Carried out at: "+formatDecisionTime(call.Decided.CarriedOutAt))
	}
	if call.PredictionAgreed != nil {
		if *call.PredictionAgreed {
			lines = append(lines, "Prediction: agreed with the answer")
		} else {
			lines = append(lines, "Prediction: differed from the answer")
		}
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
