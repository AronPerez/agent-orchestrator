// Fork policy: this fork sends no outbound telemetry by default (develop
// 09d842346). The upstream baked-in default is cleared, so nothing is sent
// unless a build or user explicitly supplies one via the override below.
export const DEFAULT_POSTHOG_PROJECT_KEY = "";
export const DEFAULT_POSTHOG_HOST = "https://us.i.posthog.com";
