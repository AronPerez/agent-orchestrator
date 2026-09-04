package github

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SCMCloser = (*Provider)(nil)

// ClosePullRequest closes one GitHub pull request without merging it. It
// performs exactly one HTTP request.
func (p *Provider) ClosePullRequest(ctx context.Context, ref ports.SCMPRRef) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("github scm: close provider is not configured")
	}
	if ref.Number <= 0 || strings.TrimSpace(ref.Repo.Owner) == "" || strings.TrimSpace(ref.Repo.Name) == "" {
		return fmt.Errorf("github scm: invalid pull request reference")
	}

	payload := struct {
		State string `json:"state"`
	}{State: "closed"}
	resp, err := p.client.doREST(ctx, http.MethodPatch,
		repoPath(ref.Repo.Owner, ref.Repo.Name, "pulls", strconv.Itoa(ref.Number)),
		nil, payload)
	if err != nil {
		switch resp.StatusCode {
		case http.StatusNotFound:
			return fmt.Errorf("%w: %w", ports.ErrSCMNotFound, err)
		case http.StatusMethodNotAllowed, http.StatusUnprocessableEntity:
			return fmt.Errorf("%w: %w", ports.ErrSCMNotMergeable, err)
		default:
			return err
		}
	}
	return nil
}
