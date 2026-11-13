import { beforeEach, describe, expect, it, vi } from "vitest";

const ipc = vi.hoisted(() => ({ invoke: vi.fn(async () => undefined), send: vi.fn(), on: vi.fn(), off: vi.fn(), removeListener: vi.fn() }));
const bridge = vi.hoisted(() => ({ exposeInMainWorld: vi.fn(), executeInMainWorld: vi.fn() }));
vi.mock("electron", () => ({ ipcRenderer: ipc, contextBridge: bridge, webUtils: { getPathForFile: vi.fn() } }));

async function load() {
	await import("../preload");
	return bridge.exposeInMainWorld.mock.calls.find(([name]) => name === "ao")![1] as import("../preload").AoBridge;
}

beforeEach(() => { vi.resetModules(); vi.clearAllMocks(); });

describe("preload browser runtime contract", () => {
	it("uses distinct global and session IPC requests", async () => {
		const api = await load();
		await api.browser.getRuntimeState();
		await api.browser.reconnectRuntime();
		await api.browser.getSessionRuntimeState("same-id", "host-a");
		await api.browser.attachRuntime("same-id", "host-a", true);
		expect(ipc.invoke.mock.calls).toContainEqual(["browser:runtime:state"]);
		expect(ipc.invoke.mock.calls).toContainEqual(["browser:runtime:reconnect"]);
		expect(ipc.invoke.mock.calls).toContainEqual(["browser:getSessionRuntimeState", "same-id", "host-a"]);
		expect(ipc.invoke.mock.calls).toContainEqual(["browser:runtime:attach", "same-id", "host-a", true]);
	});

	it("keeps event payloads and listener cleanup on separate channels", async () => {
		const api = await load();
		// Electron's off() is fluent; unsubscribe must still return void.
		ipc.off.mockReturnValue(ipc);
		const global = vi.fn(), session = vi.fn();
		const stopGlobal = api.browser.onRuntimeState(global);
		const stopSession = api.browser.onSessionRuntimeStateChanged(session);
		const health = { connected: true, connecting: false, rejected: false };
		const ownership = { hostId: "host-a", sessionId: "same-id", connected: true, owned: false };
		const globalListener = ipc.on.mock.calls.find(([channel]) => channel === "browser:runtimeState")![1];
		const sessionListener = ipc.on.mock.calls.find(([channel]) => channel === "browser:sessionRuntimeStateChanged")![1];
		globalListener({}, health);
		expect(global).toHaveBeenCalledExactlyOnceWith(health);
		expect(session).not.toHaveBeenCalled();
		sessionListener({}, ownership);
		expect(session).toHaveBeenCalledExactlyOnceWith(ownership);
		expect(stopGlobal()).toBeUndefined();
		expect(stopSession()).toBeUndefined();
		expect(ipc.off).toHaveBeenCalledWith("browser:runtimeState", globalListener);
		expect(ipc.off).toHaveBeenCalledWith("browser:sessionRuntimeStateChanged", sessionListener);
	});
});
