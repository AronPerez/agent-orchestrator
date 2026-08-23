import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "./ui-store";

beforeEach(() => {
	window.localStorage.clear();
	vi.resetModules();
});

// A fresh module sees exactly what a booting renderer sees: only storage.
async function bootStore() {
	return (await import("./ui-store")).useUiStore;
}

describe("remoteHosts flag", () => {
	it("is off until the user turns it on", async () => {
		expect((await bootStore()).getState().remoteHosts).toBe(false);
	});

	it("persists the switch so the choice survives a restart", () => {
		useUiStore.getState().setRemoteHosts(true);
		expect(useUiStore.getState().remoteHosts).toBe(true);
		expect(window.localStorage.getItem("ao.remoteHosts")).toBe("true");
		useUiStore.getState().setRemoteHosts(false);
	});

	it("reads a stored choice back at startup", async () => {
		window.localStorage.setItem("ao.remoteHosts", "true");
		expect((await bootStore()).getState().remoteHosts).toBe(true);
	});
});
