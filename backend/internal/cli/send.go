package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

type sendOptions struct {
	host            string
	session         string
	message         string
	steer           bool
	clientMessageID string
	recoverOnly     bool
}

// sendAPIRequest mirrors the daemon's SendSessionMessageRequest body for
// POST /api/v1/sessions/{id}/send. The CLI keeps its own copy so it need not
// import httpd.
type sendAPIRequest struct {
	UserAuthored    bool   `json:"userAuthored,omitempty"`
	SenderSessionID string `json:"senderSessionId,omitempty"`
	Message         string `json:"message"`
}

// The steering DTOs mirror the daemon conversation API without coupling the
// thin CLI to the HTTP controller package.
type conversationMessageAPIRequest struct {
	Text            string `json:"text"`
	ClientMessageID string `json:"clientMessageId"`
	SenderSessionID string `json:"senderSessionId,omitempty"`
	RecoverOnly     bool   `json:"recoverOnly,omitempty"`
}

type steerOrSendAPIResponse struct {
	Outcome        string `json:"outcome"`
	TurnID         string `json:"turnId"`
	ProviderTurnID string `json:"providerTurnId"`
	ActivityID     string `json:"activityId"`
	State          string `json:"state"`
	Duplicate      bool   `json:"duplicate"`
}

func newSendCommand(ctx *commandContext) *cobra.Command {
	var opts sendOptions
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send a message to a running agent session",
		Long: "Send a message to a running agent session.\n\n" +
			"This messages agents, not shell terminals. To type a command into one of\n" +
			"this session's terminal tabs, use `ao pane run <tab> <command>` or\n" +
			"`ao pane send <tab> <text>`; `ao pane` lists the terminal tab keys.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.sendMessage(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.host, "host", "", "Saved remote host label or host ID (cannot be combined with --url or AO_URL)")
	cmd.Flags().StringVar(&opts.session, "session", "", "Session id (required)")
	cmd.Flags().StringVar(&opts.message, "message", "", "Message body (required unless --recover-only)")
	cmd.Flags().BoolVar(&opts.steer, "steer", false, "Steer an active Chat turn, or start the next turn when idle")
	cmd.Flags().StringVar(&opts.clientMessageID, "client-message-id", "", "Stable steering delivery handle")
	cmd.Flags().BoolVar(&opts.recoverOnly, "recover-only", false, "Recover a steering result without contacting the provider")
	return cmd
}

func (c *commandContext) sendMessage(ctx context.Context, opts sendOptions) error {
	if opts.recoverOnly && !opts.steer {
		return usageError{errors.New("usage: --recover-only requires --steer")}
	}
	if strings.TrimSpace(opts.clientMessageID) != "" && !opts.steer {
		return usageError{errors.New("usage: --client-message-id requires --steer")}
	}
	if opts.recoverOnly && strings.TrimSpace(opts.clientMessageID) == "" {
		return usageError{errors.New("usage: --recover-only requires --client-message-id")}
	}
	if !opts.recoverOnly && strings.TrimSpace(opts.message) == "" {
		return usageError{errors.New("usage: --message is required")}
	}
	senderSessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	userAuthored := senderSessionID == ""
	message := opts.message
	if !opts.recoverOnly && senderSessionID != "" {
		message = "[from " + senderSessionID + "] " + message
	}
	session := strings.TrimSpace(opts.session)
	if session == "" {
		return usageError{errors.New("usage: --session is required")}
	}
	if err := c.selectSavedHostIfSet(ctx, opts.host); err != nil {
		return err
	}
	if c.remote != nil {
		// Session IDs are host-local. Keep the readable source prefix above, but
		// do not let the destination resolve a same-named local session as sender.
		senderSessionID = ""
	}

	// PathEscape: session ids are already "-"/digit safe, but may later come
	// from sanitized issue refs; keep the URL well-formed regardless.
	path := "sessions/" + url.PathEscape(session)
	if !opts.steer {
		if err := c.postJSON(ctx, path+"/send", sendAPIRequest{Message: message, SenderSessionID: senderSessionID, UserAuthored: userAuthored}, nil); err != nil {
			return err
		}
		if suffix := c.resolvedBySuffix(); suffix != "" {
			_, _ = fmt.Fprintf(c.deps.Out, "Message accepted%s; agent action is not confirmed.\n", suffix)
		}
		return nil
	}
	return c.steerMessage(ctx, path, message, senderSessionID, strings.TrimSpace(opts.clientMessageID), opts.recoverOnly)
}

func (c *commandContext) steerMessage(
	ctx context.Context,
	sessionPath, message, senderSessionID, clientMessageID string,
	recoverOnly bool,
) error {
	if clientMessageID == "" {
		clientMessageID = uuid.NewString()
	}
	var result steerOrSendAPIResponse
	err := c.postJSON(ctx, sessionPath+"/conversation/steer-or-send", conversationMessageAPIRequest{
		Text: message, ClientMessageID: clientMessageID, SenderSessionID: senderSessionID, RecoverOnly: recoverOnly,
	}, &result)
	if err != nil {
		var responseErr apiResponseError
		hasResponseErr := errors.As(err, &responseErr)
		if hasResponseErr && responseErr.ErrorBody.Code == "CHAT_STEER_UNCERTAIN" {
			if recoverOnly {
				return fmt.Errorf("%w; delivery handle %s remains unresolved and was not resent", err, clientMessageID)
			}
			return fmt.Errorf(
				"%w; recover this delivery without resending it: ao send%s --session %s --steer --recover-only --client-message-id %s",
				err, c.savedHostRetryArgs(), strings.TrimPrefix(sessionPath, "sessions/"), clientMessageID)
		}
		if errors.Is(err, errDaemonUnavailable) || !hasResponseErr || responseErr.StatusCode >= 500 {
			return fmt.Errorf(
				"%w; outcome is unknown for delivery handle %s; retry safely with --client-message-id %s",
				err, clientMessageID, clientMessageID)
		}
		return err
	}

	if result.Outcome == "steered" {
		if recoverOnly {
			_, _ = fmt.Fprintf(c.deps.Out,
				"Recovered steering receipt%s for turn %s with delivery handle %s; agent action is not confirmed.\n",
				c.resolvedBySuffix(), result.ProviderTurnID, clientMessageID)
			return nil
		}
		_, _ = fmt.Fprintf(c.deps.Out,
			"Steering accepted%s by provider for active turn %s with delivery handle %s; agent action is not confirmed.\n",
			c.resolvedBySuffix(), result.ProviderTurnID, clientMessageID)
		return nil
	}
	if result.Duplicate {
		_, _ = fmt.Fprintf(c.deps.Out, "Message was already accepted%s; delivery state is unchanged.\n", c.resolvedBySuffix())
		return nil
	}
	_, _ = fmt.Fprintf(c.deps.Out,
		"No active turn to steer. Message accepted%s as a normal Chat turn in %s state with delivery handle %s; provider delivery is not confirmed.\n",
		c.resolvedBySuffix(), result.State, clientMessageID)
	return nil
}
