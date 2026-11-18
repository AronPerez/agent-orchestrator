package controllers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// MobileAPIVersion is the contract version the phone negotiates against. Bump
// it when a change to the mobile-facing surface is not backward compatible.
const MobileAPIVersion = 1

// CoordinationCapabilityAutomationAuthored means the receiving daemon keeps
// cross-host agent messages classified as automation without resolving a
// foreign session ID.
const CoordinationCapabilityAutomationAuthored = "automation-authored"

// CoordinationCapabilityHostQualifiedParent means a spawned session's parent
// can be qualified with the source daemon's stable host ID. Older daemons must
// never receive the host-local session ID by itself because it can collide.
const CoordinationCapabilityHostQualifiedParent = "host-qualified-parent"

// IdentityController serves the unauthenticated host-identity probe.
//
// The phone races several endpoints, and a private address is not an identity:
// 192.168.1.42 exists on most networks. The probe lets the phone confirm which
// machine answered before it presents a credential. Everything it returns is
// therefore non-secret by construction — an opaque host id and a version.
type IdentityController struct {
	HostID string
}

// Register mounts the identity route on the supplied router.
func (c *IdentityController) Register(r chi.Router) {
	r.Get("/identity", c.identity)
	r.Get("/coordination/capabilities", c.coordinationCapabilities)
}

func (c *IdentityController) identity(w http.ResponseWriter, r *http.Request) {
	if c.HostID == "" {
		apispec.NotImplemented(w, r, "GET", "/api/v1/identity")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, IdentityResponse{
		HostID:     c.HostID,
		APIVersion: MobileAPIVersion,
	})
}

func (c *IdentityController) coordinationCapabilities(w http.ResponseWriter, _ *http.Request) {
	envelope.WriteJSON(w, http.StatusOK, CoordinationCapabilitiesResponse{
		Capabilities: []string{
			CoordinationCapabilityAutomationAuthored,
			CoordinationCapabilityHostQualifiedParent,
		},
	})
}
