import { expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({ platform: { OS: "web" }, load: vi.fn(() => null) }));
vi.mock("react-native", () => ({ Platform: state.platform }));
vi.mock("expo", () => ({ requireOptionalNativeModule: state.load }));

it.each(["web", "android", "ios"])("keeps %s usable without a Watch native module", async (platform) => {
	vi.resetModules(); state.load.mockClear(); state.platform.OS = platform;
	expect((await import("./bridge")).watchBridge).toBeNull();
	if (platform === "ios") expect(state.load).toHaveBeenCalledWith("AOWatchBridge");
	else expect(state.load).not.toHaveBeenCalled();
});
