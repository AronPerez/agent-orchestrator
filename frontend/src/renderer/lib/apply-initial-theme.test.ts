import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "./bridge";
import { themeStorageKey } from "./theme";

const { isMacPlatform } = vi.hoisted(() => ({ isMacPlatform: vi.fn(() => false) }));
vi.mock("./platform", async (importOriginal) => ({
	...await importOriginal<typeof import("./platform")>(),
	isMacPlatform,
}));

describe("apply-initial-theme", () => {
	beforeEach(() => {
		isMacPlatform.mockReturnValue(false);
	});

	afterEach(() => {
		window.localStorage.clear();
		delete document.documentElement.dataset.theme;
		delete document.documentElement.dataset.aoPlatform;
		vi.restoreAllMocks();
		vi.resetModules();
	});

	it("applies native macOS font and chrome tokens when there is no preload marker", async () => {
		isMacPlatform.mockReturnValue(true);

		await import("./apply-initial-theme");

		expect(document.documentElement.dataset.aoPlatform).toBe("darwin");
	});

	it.each(["darwin", "win32", "linux"])("preserves the %s platform supplied by native preload", async (platform) => {
		isMacPlatform.mockReturnValue(true);
		document.documentElement.dataset.aoPlatform = platform;

		await import("./apply-initial-theme");

		expect(document.documentElement.dataset.aoPlatform).toBe(platform);
	});

	it("keeps non-macOS browser font defaults", async () => {
		await import("./apply-initial-theme");

		expect(document.documentElement.dataset.aoPlatform).toBeUndefined();
	});

	it("sends the stored theme preference to main before the shell mounts", async () => {
		// The vibrancy material follows nativeTheme, so the startup loader needs
		// main to know the app theme before the shell's own theme effect runs.
		window.localStorage.setItem(themeStorageKey, "light");
		const setTheme = vi.spyOn(aoBridge.theme, "set");

		await import("./apply-initial-theme");

		expect(document.documentElement.dataset.theme).toBe("light");
		expect(setTheme).toHaveBeenCalledWith("light");
	});
});
