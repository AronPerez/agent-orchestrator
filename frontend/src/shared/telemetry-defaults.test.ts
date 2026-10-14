import { describe, expect, it } from "vitest";
import { DEFAULT_POSTHOG_PROJECT_KEY } from "./posthog-config";
import { DEFAULT_SENTRY_DSN } from "./sentry-config";

// Fork policy: no outbound telemetry unless a build or user opts in explicitly.
// An upstream sync that restores a baked-in key or DSN must fail here.
describe("fork telemetry defaults", () => {
	it("ships no default PostHog key or Sentry DSN", () => {
		expect(DEFAULT_POSTHOG_PROJECT_KEY).toBe("");
		expect(DEFAULT_SENTRY_DSN).toBe("");
	});
});
