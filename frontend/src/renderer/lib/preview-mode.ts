// Which runtime the renderer is running in. The same bundle ships three ways:
// inside Electron (window.ao present), as a browser preview with mock fixtures,
// and as the daemon-served web build.

// The daemon served this bundle from the origin the browser loaded it from
// (backend/internal/httpd/webui), so REST, SSE and the terminal mux are all
// same-origin — no base URL to configure, no CORS allowlist to keep in sync.
export const isDaemonServedWeb = import.meta.env.VITE_AO_WEB === "1";

// A real daemon is reachable without an Electron bridge: either it served this
// page, or a base URL was configured at build time (the LAN Vite dev server).
export const hasBrowserDaemon = isDaemonServedWeb || import.meta.env.VITE_AO_API_BASE_URL != null;

// Browser preview shows mock fixtures by default, but defers to a real daemon
// whenever one is reachable.
export const usesPreviewWorkspaceData = import.meta.env.VITE_NO_ELECTRON === "1" && !hasBrowserDaemon;
