// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const hls = vi.hoisted(() => ({
	loadSource: vi.fn(),
	attachMedia: vi.fn(),
	destroy: vi.fn(),
	on: vi.fn(),
	isSupported: vi.fn(() => true),
}));

vi.mock("hls.js", () => ({
	default: class {
		static isSupported = hls.isSupported;
		static Events = { ERROR: "error" };
		loadSource = hls.loadSource;
		attachMedia = hls.attachMedia;
		destroy = hls.destroy;
		on = hls.on;
	},
}));
vi.mock("next/image", () => ({ default: () => null }));

import { VideoSection } from "./VideoSection";

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
	vi.clearAllMocks();
	hls.isSupported.mockReturnValue(true);
	vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("");
	Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
	container = document.createElement("div");
	document.body.append(container);
	root = createRoot(container);
	act(() => root.render(<VideoSection />));
});

afterEach(() => {
	act(() => root.unmount());
	container.remove();
	vi.restoreAllMocks();
});

async function play() {
	await act(async () => {
		container.querySelector("button")!.click();
	});
}

describe("demo video playback", () => {
	it("loads nothing before play and uses native HLS with accessible controls when supported", async () => {
		expect(container.querySelector("video")).toBeNull();
		expect(hls.loadSource).not.toHaveBeenCalled();
		vi.mocked(HTMLMediaElement.prototype.canPlayType).mockReturnValue("probably");
		await play();
		const video = container.querySelector("video")!;
		expect(video.src).toMatch(/^https:\/\/stream\.mux\.com\/[^?]+\.m3u8$/);
		expect(video.controls).toBe(true);
		expect(video.getAttribute("aria-label")).toBe("AO Demo");
		expect(hls.loadSource).not.toHaveBeenCalled();
	});

	it("uses only the HLS engine on other browsers and destroys it when leaving", async () => {
		await play();
		await vi.waitFor(() => expect(hls.attachMedia).toHaveBeenCalledWith(container.querySelector("video")));
		expect(hls.loadSource).toHaveBeenCalledWith(expect.stringMatching(/\.m3u8$/));
		act(() => root.render(null));
		expect(hls.destroy).toHaveBeenCalledOnce();
	});

	it("reports a fatal playback error rather than hiding a broken player", async () => {
		await play();
		await vi.waitFor(() => expect(hls.on).toHaveBeenCalled());
		act(() => hls.on.mock.calls[0][1]("error", { fatal: true }));
		expect(container.querySelector('[role="alert"]')?.textContent).toContain("Unable to play");
	});
});
