import type { IpcMain, WebContents } from "electron";
import type { BrowserRuntimeLinkHandle } from "./browser-runtime-link";
import { BROWSER_RUNTIME_DISCONNECTED } from "./browser-runtime-link";
import type { SessionBrowserRuntimeState } from "./browser-view-host";

export function registerBrowserRuntimeIpc(ipc: Pick<IpcMain, "handle">, options: {
	getShellContents: () => WebContents | undefined;
	getLocalRuntime: () => BrowserRuntimeLinkHandle | null;
	getRemoteRuntime: (hostId: string) => BrowserRuntimeLinkHandle | undefined;
	reconnectLocalRuntime: () => void;
}): void {
	const requireShell = (sender: WebContents) => {
		if (!sender || sender !== options.getShellContents() || sender.isDestroyed()) throw new Error("Untrusted browser runtime request.");
	};
	const sessionRuntime = (sessionId: string, hostId?: string) => {
		if (typeof sessionId !== "string" || !sessionId || (hostId !== undefined && (typeof hostId !== "string" || !hostId))) {
			throw new Error("Invalid browser session identity.");
		}
		return hostId ? options.getRemoteRuntime(hostId) : options.getLocalRuntime();
	};
	ipc.handle("browser:runtime:state", (event) => {
		requireShell(event.sender);
		return options.getLocalRuntime()?.state ?? BROWSER_RUNTIME_DISCONNECTED;
	});
	ipc.handle("browser:runtime:reconnect", (event) => {
		requireShell(event.sender);
		options.reconnectLocalRuntime();
	});
	ipc.handle("browser:getSessionRuntimeState", (event, sessionId: string, hostId?: string): SessionBrowserRuntimeState => {
		requireShell(event.sender);
		const runtime = sessionRuntime(sessionId, hostId);
		return { hostId, ...(runtime?.sessionState(sessionId) ?? { sessionId, connected: false, owned: false }) };
	});
	ipc.handle("browser:runtime:attach", async (event, sessionId: string, hostId?: string, takeover = false): Promise<SessionBrowserRuntimeState> => {
		requireShell(event.sender);
		if (typeof takeover !== "boolean") throw new Error("Invalid browser session claim.");
		const runtime = sessionRuntime(sessionId, hostId);
		return { hostId, ...(await runtime?.attachSession(sessionId, takeover) ?? { sessionId, connected: false, owned: false }) };
	});
}
