import { describe, expect, it, vi } from "vitest";
import type { IpcMain, IpcMainInvokeEvent, WebContents } from "electron";
import { registerBrowserRuntimeIpc } from "./browser-runtime-ipc";
import type { BrowserRuntimeLinkHandle } from "./browser-runtime-link";

function fixture() {
	const sender = { isDestroyed: () => false } as WebContents;
	const runtime = (connected: boolean, owned: boolean) => ({
		state: { connected, connecting: false, rejected: false },
		sessionState: vi.fn((sessionId: string) => ({ sessionId, connected, owned })),
		attachSession: vi.fn(async (sessionId: string, _takeover: boolean) => ({ sessionId, connected, owned })),
	}) as unknown as BrowserRuntimeLinkHandle;
	const local = runtime(true, false);
	const remote = runtime(true, true);
	const handle = vi.fn<IpcMain["handle"]>();
	const reconnectLocalRuntime = vi.fn();
	const getLocalRuntime = vi.fn((): BrowserRuntimeLinkHandle | null => local);
	const getRemoteRuntime = vi.fn((hostId: string) => hostId === "host-a" ? remote : undefined);
	registerBrowserRuntimeIpc({ handle }, { getShellContents: () => sender, getLocalRuntime, getRemoteRuntime, reconnectLocalRuntime });
	const invoke = (channel: string, args: unknown[] = [], from = sender) => {
		const listener = handle.mock.calls.find(([name]) => name === channel)![1];
		return listener({ sender: from } as IpcMainInvokeEvent, ...args);
	};
	return { invoke, sender, local, remote, getLocalRuntime, getRemoteRuntime, reconnectLocalRuntime };
}

describe("browser runtime IPC boundaries", () => {
	it("keeps global connection health separate from session ownership", async () => {
		const f = fixture();
		expect(f.invoke("browser:runtime:state")).toEqual({ connected: true, connecting: false, rejected: false });
		expect(f.invoke("browser:getSessionRuntimeState", ["same-id"])).toEqual({ hostId: undefined, sessionId: "same-id", connected: true, owned: false });
		expect(f.invoke("browser:getSessionRuntimeState", ["same-id", "host-a"])).toEqual({ hostId: "host-a", sessionId: "same-id", connected: true, owned: true });
		await f.invoke("browser:runtime:attach", ["same-id", "host-a", true]);
		expect(f.remote.attachSession).toHaveBeenCalledExactlyOnceWith("same-id", true);
		expect(f.local.attachSession).not.toHaveBeenCalled();
		f.invoke("browser:runtime:reconnect");
		expect(f.reconnectLocalRuntime).toHaveBeenCalledOnce();
	});

	it("claims locally by default and reports absent hosts without a local fallback", async () => {
		const f = fixture();
		await f.invoke("browser:runtime:attach", ["same-id"]);
		expect(f.local.attachSession).toHaveBeenCalledExactlyOnceWith("same-id", false);
		expect(await f.invoke("browser:runtime:attach", ["same-id", "missing"])).toEqual({ hostId: "missing", sessionId: "same-id", connected: false, owned: false });
		f.getLocalRuntime.mockReturnValue(null);
		expect(f.invoke("browser:runtime:state")).toEqual({ connected: false, connecting: false, rejected: false });
	});

	it.each(["browser:runtime:state", "browser:runtime:reconnect", "browser:getSessionRuntimeState", "browser:runtime:attach"])("rejects a page sender for %s", async (channel) => {
		const f = fixture();
		await expect(async () => f.invoke(channel, ["same-id", "host-a"], { isDestroyed: () => false } as WebContents)).rejects.toThrow("Untrusted");
		expect(f.getLocalRuntime).not.toHaveBeenCalled();
		expect(f.getRemoteRuntime).not.toHaveBeenCalled();
	});

	it.each([["", undefined], [null, undefined], ["id", 42], ["id", ""]])("rejects malformed session/host identity %j", async (sessionId, hostId) => {
		const f = fixture();
		await expect(async () => f.invoke("browser:getSessionRuntimeState", [sessionId, hostId])).rejects.toThrow("identity");
		await expect(async () => f.invoke("browser:runtime:attach", [sessionId, hostId])).rejects.toThrow("identity");
		expect(f.getRemoteRuntime).not.toHaveBeenCalled();
	});

	it("rejects a nonboolean takeover flag", async () => {
		const f = fixture();
		await expect(f.invoke("browser:runtime:attach", ["id", "host-a", "true"])).rejects.toThrow("claim");
		expect(f.remote.attachSession).not.toHaveBeenCalled();
	});
});
