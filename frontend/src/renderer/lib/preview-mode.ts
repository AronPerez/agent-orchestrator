// Browser preview shows mock fixtures by default, but defers to a real daemon
// when an explicit API base URL is configured (LAN-exposed via VITE_AO_API_BASE_URL).
export const usesPreviewWorkspaceData =
	import.meta.env.VITE_NO_ELECTRON === "1" && import.meta.env.VITE_AO_API_BASE_URL == null;
