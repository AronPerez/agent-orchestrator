import { beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "./bridge";
import { getApiBaseUrl, setApiBaseUrl } from "./api-client";
import { activeHost, applyDaemonBaseUrl, initActiveHost, switchToHost } from "./active-host";

beforeEach(async () => {
	localStorage.clear();
	setApiBaseUrl(null);
	vi.restoreAllMocks();
	// Module state outlives a test; reset it to local so each case starts clean.
	vi.spyOn(aoBridge.remotes, "deactivate").mockResolvedValue(undefined);
	await initActiveHost();
	vi.restoreAllMocks();
	setApiBaseUrl(null);
});

describe("active-host", () => {
	it("boots local when nothing is stored, and tears down any stale proxy", async () => {
		const deactivate = vi.spyOn(aoBridge.remotes, "deactivate").mockResolvedValue(undefined);
		await initActiveHost();
		expect(activeHost()).toBeNull();
		expect(deactivate).toHaveBeenCalled();
	});

	it("boots onto the stored host via the proxy base", async () => {
		localStorage.setItem("ao.active-host-url", "http://192.0.2.1:3011");
		vi.spyOn(aoBridge.remotes, "activate").mockResolvedValue({
			label: "workbox",
			url: "http://192.0.2.1:3011",
			base: "http://127.0.0.1:9999/tok",
		});
		await initActiveHost();
		expect(activeHost()).toEqual({
			label: "workbox",
			url: "http://192.0.2.1:3011",
		});
		expect(getApiBaseUrl()).toBe("http://127.0.0.1:9999/tok");
	});

	it("falls back to local when the stored host cannot be activated", async () => {
		localStorage.setItem("ao.active-host-url", "http://192.0.2.1:3011");
		vi.spyOn(aoBridge.remotes, "activate").mockRejectedValue(new Error("gone"));
		await initActiveHost();
		expect(activeHost()).toBeNull();
		// A host that fails to boot must not wedge every future boot.
		expect(localStorage.getItem("ao.active-host-url")).toBeNull();
	});

	it("switchToHost persists and reloads; switching to local clears", async () => {
		vi.spyOn(aoBridge.remotes, "deactivate").mockResolvedValue(undefined);
		const reload = vi.fn();
		await switchToHost("http://192.0.2.1:3011", reload);
		expect(localStorage.getItem("ao.active-host-url")).toBe("http://192.0.2.1:3011");
		expect(reload).toHaveBeenCalled();

		await switchToHost(null, reload);
		expect(localStorage.getItem("ao.active-host-url")).toBeNull();
	});

	it("gates the local daemon's base-URL stomp while a remote host is active", async () => {
		localStorage.setItem("ao.active-host-url", "http://192.0.2.1:3011");
		vi.spyOn(aoBridge.remotes, "activate").mockResolvedValue({
			label: "workbox",
			url: "http://192.0.2.1:3011",
			base: "http://127.0.0.1:9999/tok",
		});
		await initActiveHost();

		applyDaemonBaseUrl("http://127.0.0.1:3001"); // local daemon came up — must NOT stomp
		expect(getApiBaseUrl()).toBe("http://127.0.0.1:9999/tok");
	});

	it("does not let a daemon-down report clear the remote base either", async () => {
		localStorage.setItem("ao.active-host-url", "http://192.0.2.1:3011");
		vi.spyOn(aoBridge.remotes, "activate").mockResolvedValue({
			label: "workbox",
			url: "http://192.0.2.1:3011",
			base: "http://127.0.0.1:9999/tok",
		});
		await initActiveHost();

		applyDaemonBaseUrl(null);
		expect(getApiBaseUrl()).toBe("http://127.0.0.1:9999/tok");
	});

	it("passes the daemon base through when local is active", async () => {
		vi.spyOn(aoBridge.remotes, "deactivate").mockResolvedValue(undefined);
		await initActiveHost();
		applyDaemonBaseUrl("http://127.0.0.1:3001");
		expect(getApiBaseUrl()).toBe("http://127.0.0.1:3001");
	});
});
