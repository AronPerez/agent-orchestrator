package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type sessionOverlapsOptions struct {
	project string
	json    bool
}

type projectOverlapDTO struct {
	SessionA string   `json:"sessionA"`
	SessionB string   `json:"sessionB"`
	Paths    []string `json:"paths"`
}

type projectOverlapsResponse struct {
	ProjectID                string              `json:"projectId"`
	Overlaps                 []projectOverlapDTO `json:"overlaps"`
	TruncatedSessionIDs      []string            `json:"truncatedSessionIds"`
	UnresolvedBaseSessionIDs []string            `json:"unresolvedBaseSessionIds"`
	UnavailableSessionIDs    []string            `json:"unavailableSessionIds"`
}

func newSessionOverlapsCommand(ctx *commandContext) *cobra.Command {
	var opts sessionOverlapsOptions
	cmd := &cobra.Command{
		Use:   "overlaps",
		Short: "Show active sessions changing the same files",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.listProjectOverlaps(cmd.Context(), cmd, opts)
		},
	}
	addSessionProjectFlag(cmd.Flags(), &opts.project, "Project id to inspect")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output as JSON")
	return cmd
}

func (c *commandContext) listProjectOverlaps(ctx context.Context, cmd *cobra.Command, opts sessionOverlapsOptions) error {
	project, err := c.resolveSpawnProject(ctx, opts.project)
	if err != nil {
		return err
	}
	var response projectOverlapsResponse
	if err := c.doJSON(ctx, http.MethodGet, "projects/"+url.PathEscape(project.ID)+"/overlaps", nil, &response); err != nil {
		return err
	}
	if response.Overlaps == nil {
		response.Overlaps = []projectOverlapDTO{}
	}
	if response.TruncatedSessionIDs == nil {
		response.TruncatedSessionIDs = []string{}
	}
	if response.UnresolvedBaseSessionIDs == nil {
		response.UnresolvedBaseSessionIDs = []string{}
	}
	if response.UnavailableSessionIDs == nil {
		response.UnavailableSessionIDs = []string{}
	}
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), response)
	}
	return writeProjectOverlaps(cmd, response)
}

func writeProjectOverlaps(cmd *cobra.Command, response projectOverlapsResponse) error {
	out := cmd.OutOrStdout()
	if len(response.Overlaps) == 0 {
		if _, err := fmt.Fprintf(out, "No overlapping changed files in project %s.\n", response.ProjectID); err != nil {
			return err
		}
	} else {
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(tw, "SESSION A\tSESSION B\tSHARED PATHS"); err != nil {
			return err
		}
		for _, overlap := range response.Overlaps {
			paths := make([]string, len(overlap.Paths))
			for i, path := range overlap.Paths {
				quoted := strconv.QuoteToGraphic(path)
				paths[i] = quoted[1 : len(quoted)-1]
			}
			if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\n", overlap.SessionA, overlap.SessionB, strings.Join(paths, ", ")); err != nil {
				return err
			}
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if len(response.TruncatedSessionIDs) > 0 {
		if _, err := fmt.Fprintf(out, "Warning: overlap results may be incomplete; truncated manifests: %s\n", strings.Join(response.TruncatedSessionIDs, ", ")); err != nil {
			return err
		}
	}
	if len(response.UnresolvedBaseSessionIDs) > 0 {
		if _, err := fmt.Fprintf(out, "Warning: committed overlaps may be incomplete; unresolved comparison bases: %s\n", strings.Join(response.UnresolvedBaseSessionIDs, ", ")); err != nil {
			return err
		}
	}
	if len(response.UnavailableSessionIDs) > 0 {
		if _, err := fmt.Fprintf(out, "Warning: workspaces could not be read: %s\n", strings.Join(response.UnavailableSessionIDs, ", ")); err != nil {
			return err
		}
	}
	return nil
}
