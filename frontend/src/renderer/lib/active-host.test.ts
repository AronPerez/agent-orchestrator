import { beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "./bridge";
import { getApiBaseUrl, setApiBaseUrl } from "./api-client";
import { activeHost, applyDaemonBaseUrl, initHosts } from "./active-host";
import { baseUrlFor, forgetHost } from "./host-clients";
import { LOCAL_HOST } from "./hosts";

const WORKBOX = "http://192.0.2.1:3011";
const MINI = "http://192.0.2.9:3011";

beforeEach(() => {
	localStorage.clear();
	forgetHost(WORKBOX);
	forgetHost(MINI);
	setApiBaseUrl(null);
	vi.restoreAllMocks();
});

describe("multi-host boot", () => {
	it("ignores the legacy selection and connects every saved host", async () => {
		localStorage.setItem("ao.active-host-url", WORKBOX);
		vi.spyOn(aoBridge.remotes, "list").mockResolvedValue([
			{ label: "workbox", url: WORKBOX },
			{ label: "mini", url: MINI },
		]);
		const connect = vi.spyOn(aoBridge.remotes, "connect").mockImplementation(async (url) => ({
			label: url === WORKBOX ? "workbox" : "mini",
			url,
			base: url === WORKBOX ? "http://127.0.0.1:9001/one" : "http://127.0.0.1:9002/two",
		}));

		await initHosts();

		expect(connect).toHaveBeenCalledTimes(2);
		expect(activeHost()).toBeNull();
		expect(baseUrlFor(WORKBOX)).toBe("http://127.0.0.1:9001/one");
		expect(baseUrlFor(MINI)).toBe("http://127.0.0.1:9002/two");
	});

	it("keeps connecting other saved hosts when one is unavailable", async () => {
		vi.spyOn(aoBridge.remotes, "list").mockResolvedValue([
			{ label: "workbox", url: WORKBOX },
			{ label: "mini", url: MINI },
		]);
		vi.spyOn(aoBridge.remotes, "connect").mockImplementation(async (url) => {
			if (url === WORKBOX) throw new Error("offline");
			return { label: "mini", url, base: "http://127.0.0.1:9002/two" };
		});

		await initHosts();

		expect(baseUrlFor(WORKBOX)).toBeNull();
		expect(baseUrlFor(MINI)).toBe("http://127.0.0.1:9002/two");
	});
});

describe("applyDaemonBaseUrl", () => {
	it("updates the local host and ignores every remote host", () => {
		setApiBaseUrl("http://127.0.0.1:3001");
		applyDaemonBaseUrl(WORKBOX, "http://127.0.0.1:9999/proxy-token");
		expect(getApiBaseUrl()).toBe("http://127.0.0.1:3001");

		applyDaemonBaseUrl(LOCAL_HOST, "http://127.0.0.1:3037");
		expect(getApiBaseUrl()).toBe("http://127.0.0.1:3037");
	});
});
