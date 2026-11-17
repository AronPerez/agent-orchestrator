package controllers

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

func isAgentCoordinationRequest(ctx context.Context) bool {
	return requestscope.CredentialFrom(ctx).HasScope(mobilebridge.DeviceScopeAgentCoordination)
}
