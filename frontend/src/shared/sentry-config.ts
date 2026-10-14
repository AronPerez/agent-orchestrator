// Baked default Sentry DSN for the desktop surfaces (renderer, Electron main,
// and the Go daemon spawned by the app), mirroring how DEFAULT_POSTHOG_PROJECT_KEY
// is committed in posthog-config.ts. A client DSN is a write-only ingest
// endpoint, so it is safe to ship in the client the same way the PostHog project
// key already is; volume is bounded by the project's Sentry-side spike protection
// and rate limit, not by anything secret here.
//
// Overridable per environment: the renderer reads VITE_AO_SENTRY_DSN, the main
// process and daemon read AO_SENTRY_DSN, and both fall back to this constant.
// Mobile has its own project and constant (see packages/mobile), because the
// Expo bundle cannot reach frontend/src.
// Fork policy: this fork sends no outbound telemetry by default (develop
// 09d842346). The upstream baked-in default is cleared, so nothing is sent
// unless a build or user explicitly supplies one via the override below.
export const DEFAULT_SENTRY_DSN = "";
