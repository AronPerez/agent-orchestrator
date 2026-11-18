package httpd

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

const (
	desktopRelaySurfaceHeader = "X-AO-Client-Surface"
	desktopRelayMarkerContext = "ao-desktop-relay-v1"
)

// requestSurfaceMiddleware tags each request with the client it came from, so
// product telemetry emitted further down (a sent message, a spawn, a merge) can
// say desktop, mobile or CLI. It is classification only and never rejects.
func requestSurfaceMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := exactAllowedOrigins(allowedOrigins)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			surface := requestSurface(r, allowed)
			next.ServeHTTP(w, r.WithContext(telemetrymeta.WithSurface(r.Context(), surface)))
		})
	}
}

// requestSurface classifies from transport facts. A phone authenticates on the
// LAN listener (directly or through a managed tunnel) and sets its install-id
// header on every request. The desktop's loopback remote proxy replaces the
// client-surface header with a marker bound to the accepted bearer credential.
// The local renderer presents an exact renderer origin; everything else on
// loopback is a native client such as the CLI.
func requestSurface(r *http.Request, allowedOrigins map[string]struct{}) domain.RequestSurface {
	if r.Header.Get(InstallIDHeader) != "" {
		return domain.RequestSurfaceMobile
	}
	if authenticatedLANBearer(r) {
		if validDesktopRelayMarker(r.Header.Get(desktopRelaySurfaceHeader), bearerToken(r)) {
			return domain.RequestSurfaceDesktop
		}
		return domain.RequestSurfaceMobile
	}
	if _, ok := allowedOrigins[r.Header.Get("Origin")]; ok {
		return domain.RequestSurfaceDesktop
	}
	return domain.RequestSurfaceCLI
}

func validDesktopRelayMarker(marker, token string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(marker)
	if err != nil || token == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte(desktopRelayMarkerContext))
	return hmac.Equal(decoded, mac.Sum(nil))
}
