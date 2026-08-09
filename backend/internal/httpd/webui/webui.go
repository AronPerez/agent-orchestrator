// Package webui serves the browser build of the renderer from the daemon
// itself, so a machine that has only a browser can open the daemon's own
// address and get the full UI. Serving the UI and the API from one origin is
// the point: same-origin REST/SSE/WebSocket need no CORS allowlist, no Origin
// laundering proxy, and no Local Network Access prompt.
//
// The bundle is produced at build time — frontend/scripts/build-daemon.mjs runs
// `vite build` into ./bundle before `go build` — rather than committed, so the
// repo carries no hashed asset blobs. A checkout that has not run that step
// still compiles and passes tests: ./bundle then holds only .gitkeep and UI
// requests answer 503 instead of a page.
//
// Mirrors the embed precedent in internal/httpd/apispec.
package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:bundle
var bundleFS embed.FS

// daemonPrefixes are the paths the daemon answers itself. Everything else on a
// GET is the web UI: a hashed asset, or a client-side route that must resolve
// to index.html so a deep link or a reload lands on the right screen.
//
// This list is what keeps the SPA fallback from swallowing the API: an unknown
// /api/v1 path has to keep returning the locked JSON 404 envelope, not HTML.
// It is also what the LAN listener consults to decide which requests may skip
// the connection password (see httpd.NewLANManager), so no path that carries
// daemon data may ever be missing from it.
var daemonPrefixes = []string{
	"/api/",
	"/mux",
	"/internal/",
	"/shutdown",
	"/healthz",
	"/readyz",
}

// IsUIRequest reports whether r should be answered by the embedded UI rather
// than by the daemon. Only GET/HEAD qualify: the UI is static files, and a
// POST to an unknown path is a client error, not a page.
func IsUIRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	p := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
	for _, prefix := range daemonPrefixes {
		trimmed := strings.TrimSuffix(prefix, "/")
		if p == trimmed || strings.HasPrefix(p, trimmed+"/") {
			return false
		}
	}
	return true
}

// bundle is the embedded build, rooted at the bundle directory.
var bundle = func() fs.FS {
	sub, err := fs.Sub(bundleFS, "bundle")
	if err != nil {
		return fs.FS(embed.FS{}) // impossible for a literal directory
	}
	return sub
}()

// Available reports whether a UI bundle was built into this daemon.
func Available() bool { return indexHTML(bundle) != nil }

func indexHTML(assets fs.FS) []byte {
	b, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil
	}
	return b
}

// Handler serves the embedded UI for UI requests and delegates everything else
// to next — the router's JSON 404 — so API and control paths keep their locked
// error envelope. Mount it as the router's last-resort route.
func Handler(next http.Handler) http.Handler { return handlerFor(bundle, next) }

func handlerFor(assets fs.FS, next http.Handler) http.Handler {
	files := http.FileServer(http.FS(assets))
	index := indexHTML(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsUIRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy(r))
		if index == nil {
			http.Error(w, "web UI bundle was not built into this daemon", http.StatusServiceUnavailable)
			return
		}
		if name := assetName(assets, r.URL.Path); name != "" {
			// Vite fingerprints every emitted asset, so the content behind a given
			// URL never changes; index.html is the one mutable entry point.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			r = r.Clone(r.Context())
			r.URL.Path = "/" + name
			files.ServeHTTP(w, r)
			return
		}
		// The SPA shell must never be cached: an app update changes the hashed
		// asset names it points at, and a stale copy would ask this daemon for
		// files it no longer embeds.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	})
}

// assetName returns the embedded file a request maps to, or "" when the path is
// a client-side route that must fall back to index.html. index.html itself is
// excluded so it is always served through the fallback, with its no-store
// headers.
func assetName(assets fs.FS, urlPath string) string {
	name := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(urlPath, "/")), "/")
	if name == "" || name == "index.html" {
		return ""
	}
	f, err := assets.Open(name)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	if st, err := f.Stat(); err != nil || st.IsDir() {
		return ""
	}
	return name
}

// contentSecurityPolicy is sent as a response header rather than baked into a
// meta tag, because the one directive that cannot be known at build time — the
// WebSocket origin for the terminal mux — is exactly the host this request
// arrived on. The desktop build keeps its build-time meta CSP (see
// frontend/vite.renderer.config.ts); the web build ships without one.
func contentSecurityPolicy(r *http.Request) string {
	connect := "'self'"
	if origin := websocketOrigin(r); origin != "" {
		// CSP 'self' does not reliably match a ws:/wss: URL, so the mux origin is
		// named explicitly. It is the same host that served this document.
		connect += " " + origin
	}
	return strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data:",
		"font-src 'self' data:",
		"connect-src " + connect,
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'none'",
		// The workspace preview panel frames whatever dev server the agent
		// started, which is a different origin on an unpredictable port. A framed
		// document cannot reach into this one, so allowing http(s) frames costs
		// nothing here while 'none' would blank the panel.
		"frame-src http: https:",
	}, "; ")
}

// websocketOrigin derives the mux WebSocket source from the request's own host.
// The Host header is client-supplied, so a value that could break out of the
// directive it lands in is dropped rather than reflected.
func websocketOrigin(r *http.Request) string {
	host := r.Host
	if host == "" || strings.ContainsAny(host, " \t\r\n;,'\"") {
		return ""
	}
	if r.TLS != nil {
		return "wss://" + host
	}
	return "ws://" + host
}
