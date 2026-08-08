package httpd

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// corsMiddleware grants cross-origin read access to the allowlisted browser
// origins only. The daemon is a no-auth loopback service, so CORS is the one
// boundary between it and hostile browser content running on the same
// machine: the allowlist must never contain "*" or the opaque "null" origin
// (every file:// page and sandboxed iframe on any website presents "null").
// The packaged Electron renderer is served from app://renderer specifically
// so it has a distinct, unforgeable origin this allowlist can name.
//
// Requests without an Origin header (the CLI, curl, health probes) pass
// through untouched. Requests bearing an Origin outside the allowlist are
// rejected with 403 before any handler runs: merely omitting CORS headers
// would hide the response but NOT the side effect — a hostile page can issue
// "simple" cross-origin POSTs (no-cors mode, text/plain body) that handlers
// would otherwise execute. Same philosophy as localControlRequest on
// /shutdown, applied to the whole surface.
func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin == "" || origin == "null" || origin == "*" {
			continue
		}
		allowed[origin] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			// The strict check runs before the no-Origin shortcut: a browser can
			// omit Origin (EventSource, <img>) while still telling us the request
			// came from another site, and that must not be a way past it.
			if requiresStrictOrigin(r) {
				w.Header().Add("Vary", "Origin, Sec-Fetch-Site")
				if !strictOriginOK(allowed, r) {
					envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "ORIGIN_FORBIDDEN",
						"Origin is not allowed to perform this request; add it to AO_ALLOWED_ORIGINS", nil)
					return
				}
			}
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			// Cache keys must split on Origin even for rejected values, or a
			// 403 could be replayed to an allowed origin.
			w.Header().Add("Vary", "Origin")
			if _, ok := allowed[origin]; !ok && !isLoopbackOrigin(origin) {
				envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "ORIGIN_FORBIDDEN",
					"Origin is not allowed to access this daemon", nil)
				return
			}

			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)

			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
				if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
					h.Set("Access-Control-Allow-Headers", reqHeaders)
				}
				h.Set("Access-Control-Max-Age", "600")
				// Chromium's Private Network Access preflight for requests
				// reaching loopback from a less-private address space.
				if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
					h.Set("Access-Control-Allow-Private-Network", "true")
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// requiresStrictOrigin reports whether r carries enough authority that
// loopback-origin trust (see isLoopbackOrigin) is too coarse for it.
//
// On the loopback listener the daemon has no credential at all: reaching the
// socket IS the authorization. Any page a browser happens to have open from any
// local dev server can reach it too, so for anything with a side effect — the
// terminal mux and every state-changing method — the Origin must be one this
// daemon actually names, not merely "something loopback-ish".
//
// The LAN listener is exempt because it is credential-gated: a request there is
// authenticated by a secret the caller had to be given (Bearer header or the
// ao.bearer.* subprotocol), so origin is not what authorizes it. That exemption
// is what keeps the native mobile client working, which pins
// Origin: http://localhost on its /mux upgrade while addressing the LAN host.
// Where a COOKIE authenticates instead, authMiddleware applies strictOriginOK
// itself — cookies ride cross-site, so there origin checking is mandatory.
func requiresStrictOrigin(r *http.Request) bool {
	if servedOverLAN(r) {
		return false
	}
	return r.URL.Path == terminalMuxPath || stateChangingMethod(r.Method)
}

// stateChangingMethod reports whether a method may have a side effect. OPTIONS
// is safe here specifically so a CORS preflight still gets its 204 and the real
// request fails with a clean 403 instead of an opaque preflight error.
func stateChangingMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// strictOriginOK reports whether r's Origin is trusted for an operation that
// runs on ambient authority. Loopback-served content is deliberately NOT
// trusted here; only three things are:
//
//   - the explicit allowlist (app://renderer, plus anything in AO_ALLOWED_ORIGINS)
//   - exact same-origin: the Origin names the very host:port the request was
//     addressed to, i.e. a page this daemon served itself
//   - the browser's own Sec-Fetch-Site attestation, where it sends one. Page
//     script cannot set it: "same-origin" means the request came from a page of
//     this origin, "none" a top-level user navigation with no initiator.
//
// A missing Origin passes only after Sec-Fetch-Site has had its say. Requests a
// browser makes with no Origin are real and must keep working — EventSource and
// <img> omit it on same-origin requests, which is precisely what the cookie
// exists to serve — but such a request from another site is labelled
// "cross-site", so the browser closes the gap the missing Origin would leave.
// Absent both headers the caller is not a browser (the CLI, curl, a native
// client) and there is no ambient authority to abuse.
//
// Explicit trust is checked before Sec-Fetch-Site: an allowlisted origin is
// legitimately cross-site (the dev renderer, the LAN web build) and the operator
// naming it outranks the browser's label.
func strictOriginOK(allowed map[string]struct{}, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin != "" {
		if _, ok := allowed[origin]; ok {
			return true
		}
		if sameOriginRequest(origin, r) {
			return true
		}
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "cross-site", "same-site":
		return false
	}
	return origin == ""
}

// sameOriginRequest reports whether origin names the same host:port the request
// was addressed to. Comparing against r.Host (not a loopback predicate) is what
// makes this exact: http://localhost:8080 is not the same origin as the daemon
// on 127.0.0.1:3001, even though both are loopback.
func sameOriginRequest(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

// isLoopbackOrigin reports whether a browser origin is content served from
// this machine's loopback (the Vite dev server / preview server on whatever
// port it picked). Such content can already reach the no-auth daemon directly,
// so granting it CORS adds no exposure — while a remote page can never bear a
// loopback origin (DNS rebinding changes resolution, not the Origin header).
//
// This is read access only. Anything with a side effect goes through
// requiresStrictOrigin / strictOriginOK instead.
func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	// RFC 6761 reserves localhost and every name below it for loopback.
	// Workspace previews use a per-session subdomain so browser-enforced CORS
	// requests (ES modules and fetch) remain isolated without being rejected by
	// the daemon's origin boundary.
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
