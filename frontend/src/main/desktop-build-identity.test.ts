// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { desktopBuildIdentity } from "../../vite.build-identity";

afterEach(() => vi.unstubAllEnvs());

it("shares one invocation's desktop identity and distinguishes same-commit rebuilds", () => {
	vi.stubEnv("AO_DESKTOP_BUILD_ID", "first-build");
	const main = desktopBuildIdentity();
	expect(desktopBuildIdentity()).toBe(main);
	vi.stubEnv("AO_DESKTOP_BUILD_ID", "second-build");
	expect(desktopBuildIdentity()).not.toBe(main);
});
