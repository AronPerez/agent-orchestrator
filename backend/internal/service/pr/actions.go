package pr

import "context"

// ActionManager is the controller-facing contract for guarded PR mutations.
type ActionManager interface {
	Merge(ctx context.Context, request MergeRequest) (MergeResult, error)
	Close(ctx context.Context, prID string) (CloseResult, error)
	ResolveComments(ctx context.Context, prID string, commentIDs []string) (ResolveResult, error)
}

// MergeRequest identifies the tracked PR and pins the mutation to the exact
// head the user saw when they clicked Merge.
type MergeRequest struct {
	PRID            string
	PRURL           string
	ExpectedHeadSHA string
}

// MergeResult is the successful outcome of a PR merge.
type MergeResult struct {
	PRNumber       int
	Method         string
	MergeCommitSHA string
}

// CloseResult is the successful outcome of closing a PR without merging.
type CloseResult struct {
	PRNumber int
}

// ResolveResult is the successful outcome of a resolve-comments operation.
type ResolveResult struct {
	Resolved int
}
