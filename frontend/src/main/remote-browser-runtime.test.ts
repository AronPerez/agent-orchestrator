// @vitest-environment node
import { createServer, request as httpRequest, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import WebSocket, { WebSocketServer } from "ws";
import { afterEach, describe, expect, it, vi } from "vitest";
import { attachRemoteBrowserRuntime } from "./remote-browser-runtime";
import { startRemoteProxy, type ActiveProxy } from "./remote-proxy";
import { sessionUiKey } from "../renderer/lib/hosts";
import type { BrowserRuntimeCommand } from "./browser-runtime-link";

const proxies: ActiveProxy[] = [];
const servers: Server[] = [];
const sockets: WebSocketServer[] = [];
afterEach(async () => {
	await Promise.all(proxies.splice(0).map((proxy) => proxy.close()));
	for (const ws of sockets.splice(0)) { for (const client of ws.clients) client.terminate(); ws.close(); }
	await Promise.all(servers.splice(0).map((server) => new Promise<void>((resolve) => { server.closeAllConnections(); server.close(() => resolve()); })));
});

async function remote(hostId = "host-a", acceptHello = true) {
	const requests: { url?: string; auth?: string; host?: string }[] = [];
	const server = createServer((req, res) => {
		if (req.url === "/ao/api/v1/identity") res.end(JSON.stringify({ hostId, apiVersion: 1 }));
		else if (req.url === "/ao/api/v1/sessions/project-1/preview") res.end(JSON.stringify({ previewUrl: "http://localhost:5173/" }));
		else { requests.push({ url: req.url, auth: req.headers.authorization }); res.end("preview content"); }
	});
	servers.push(server);
	const ws = new WebSocketServer({ noServer: true });
	sockets.push(ws);
	server.on("upgrade", (req, socket, head) => {
		requests.push({ url: req.url, auth: req.headers.authorization, host: req.headers["x-ao-expected-host-id"] as string });
		ws.handleUpgrade(req, socket, head, (peer) => ws.emit("connection", peer, req));
	});
	const messages: Record<string, unknown>[] = [];
	let peer: WebSocket;
	ws.on("connection", (client) => {
		peer = client;
		client.on("message", (data, isBinary) => {
			// Match the Go endpoint's text-only websocket.NetConn contract.
			if (isBinary) { client.close(1003, "expected text frame"); return; }
			for (const line of data.toString().split("\n").filter(Boolean)) {
				const message = JSON.parse(line);
				messages.push(message);
				if (message.type === "hello" && acceptHello) client.send('{"type":"hello-ok"}\n');
			}
		});
	});
	await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
	const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}/ao`;
	const proxy = await startRemoteProxy({ hostId, url: base, password: "paired-secret", label: "Host" });
	proxies.push(proxy);
	return { proxy, requests, messages, send: (command: unknown) => peer!.send(`${JSON.stringify(command)}\n`), drop: () => peer!.terminate() };
}

function attach(hostId: string, proxy: ActiveProxy, execute: (command: BrowserRuntimeCommand, signal: AbortSignal) => Promise<unknown>) {
	const attached = attachRemoteBrowserRuntime(hostId, proxy, execute);
	proxies[proxies.indexOf(proxy)] = attached;
	return attached;
}

const command = { type: "command", sessionId: "project-1", requestId: "r1", action: "open", args: { url: "http://localhost:5173/page" } };

describe("remote desktop browser routing", () => {
	it("registers sessions opened while the hello acknowledgement is pending", async () => {
		const host = await remote("host-a", false);
		const link = attach("host-a", host.proxy, vi.fn(async () => ({}))).browserRuntime!;
		await vi.waitFor(() => expect(host.messages).toHaveLength(1));
		await link.attachSession("late-session", false);
		host.send({ type: "hello-ok" });
		await vi.waitFor(() => expect(host.messages.at(-1)).toMatchObject({ type: "attach-session", sessionId: "late-session", takeover: false }));
		host.send({ type: "session-state", requestId: host.messages.at(-1)!.requestId, sessionId: "late-session", owned: true, revision: 1 });
		await vi.waitFor(() => expect(link.sessionState("late-session").owned).toBe(true));
	});

	it("keeps a denied session claim separate from the connected transport and replays it on reconnect", async () => {
		const host = await remote();
		const attached = attach("host-a", host.proxy, vi.fn(async () => ({})));
		const link = attached.browserRuntime!;
		await vi.waitFor(() => expect(link.connected).toBe(true));
		const claim = link.attachSession("project-1", false);
		await vi.waitFor(() => expect(host.messages.at(-1)).toMatchObject({ type: "attach-session", sessionId: "project-1", takeover: false }));
		const requestId = host.messages.at(-1)!.requestId;
		host.send({ type: "session-state", requestId, sessionId: "project-1", owned: false, revision: 2, error: { code: "BROWSER_SESSION_BUSY", message: "Browser is controlled by another desktop." } });
		expect(await claim).toMatchObject({ connected: true, owned: false, error: { code: "BROWSER_SESSION_BUSY" } });
		expect(link.connected).toBe(true);
		const takeover = link.attachSession("project-1", true);
		await vi.waitFor(() => expect(host.messages.at(-1)).toMatchObject({ takeover: true }));
		host.send({ type: "session-state", requestId: host.messages.at(-1)!.requestId, sessionId: "project-1", owned: true, revision: 4 });
		expect(await takeover).toMatchObject({ owned: true });
		// A delayed notification must not undo the newer claim.
		host.send({ type: "session-state", sessionId: "project-1", owned: false, revision: 3 });
		host.send({ type: "ping" });
		await vi.waitFor(() => expect(host.messages.at(-1)).toEqual({ type: "pong" }));
		expect(link.sessionState("project-1").owned).toBe(true);
		const pending = link.attachSession("project-2", false);
		host.drop();
		await expect(pending).resolves.toMatchObject({ connected: false, owned: false });
		await vi.waitFor(() => expect(host.messages.filter((m) => m.type === "hello")).toHaveLength(2));
		const hellos = host.messages.filter((m) => m.type === "hello");
		expect(hellos[1]).toMatchObject({ version: 4, runtimeId: hellos[0].runtimeId, sessions: ["project-1", "project-2"] });
		expect(link.sessionState("project-1").owned).toBe(false);
	});

	it("does not let a timed-out claim overwrite a newer ownership update", async () => {
		const host = await remote();
		const link = attach("host-a", host.proxy, vi.fn(async () => ({}))).browserRuntime!;
		await vi.waitFor(() => expect(link.connected).toBe(true));
		const timers = vi.spyOn(globalThis, "setTimeout");
		try {
			const claim = link.attachSession("project-1", false);
			const timeout = timers.mock.calls.find(([, delay]) => delay === 8_000)![0] as () => void;
			host.send({ type: "session-state", sessionId: "project-1", owned: true, revision: 3 });
			await vi.waitFor(() => expect(link.sessionState("project-1").owned).toBe(true));
			timeout();
			expect(await claim).toMatchObject({ owned: true, error: undefined });
		} finally {
			timers.mockRestore();
		}
	});

	it("exposes a rejected hello and never claims that a transport is ready", async () => {
		const host = await remote();
		const attached = attach("host-a", host.proxy, vi.fn(async () => ({})));
		await vi.waitFor(() => expect(attached.browserRuntime!.connected).toBe(true));
		host.send({ type: "hello-rejected", error: { code: "BROWSER_RUNTIME_PROTOCOL_MISMATCH", message: "Update AO" } });
		await vi.waitFor(() => expect(attached.browserRuntime!.sessionState("project-1")).toMatchObject({ connected: false, error: { code: "BROWSER_RUNTIME_PROTOCOL_MISMATCH" } }));
	});

	it("authenticates the bridge, scopes the panel and tunnels loopback previews", async () => {
		const host = await remote();
		const execute = vi.fn(async (cmd: BrowserRuntimeCommand) => ({ url: cmd.args?.url, target: { url: cmd.args?.url }, text: "page text" }));
		attach("host-a", host.proxy, execute);
		await vi.waitFor(() => expect(host.messages[0]).toMatchObject({ type: "hello", version: 4, runtimeId: expect.any(String), sessions: [] }));
		expect(host.requests[0]).toEqual({ url: "/ao/browser-runtime", auth: "Bearer paired-secret", host: "host-a" });
		host.send(command);
		await vi.waitFor(() => expect(execute).toHaveBeenCalledOnce());
		const routed = execute.mock.calls[0][0];
		expect(routed.sessionId).toBe(sessionUiKey("project-1", "host-a"));
		const preview = new URL(String(routed.args?.url));
		expect(preview.hostname).toMatch(/^ao-preview-[a-f0-9]{32}\.localhost$/);
		expect(preview.href).not.toContain(new URL(host.proxy.base).pathname);
		// Node does not resolve *.localhost; send the same Host header Chromium uses.
		const body = await new Promise<string>((resolve, reject) => {
			const req = httpRequest(`http://127.0.0.1:${preview.port}${preview.pathname}`, { headers: { Host: preview.host } }, (res) => {
				let body = ""; res.on("data", (chunk) => { body += chunk; }); res.on("end", () => resolve(body));
			});
			req.on("error", reject); req.end();
		});
		expect(body).toBe("preview content");
		expect(host.requests.at(-1)).toMatchObject({ url: "/ao/api/v1/sessions/project-1/preview/app/page", auth: "Bearer paired-secret" });
		await vi.waitFor(() => expect(host.messages.at(-1)).toEqual({ type: "result", requestId: "r1", ok: true, result: { url: command.args.url, target: { url: command.args.url }, text: "page text" } }));
	});

	it("preserves public navigation and reports source URLs without rewriting page text", async () => {
		const host = await remote();
		const preview = host.proxy.previewUrl("project-1", command.args.url);
		const execute = vi.fn(async () => ({ url: preview, value: preview, target: { origin: new URL(preview).origin }, text: preview }));
		attach("host-a", host.proxy, execute);
		await vi.waitFor(() => expect(host.messages).toHaveLength(1));
		host.send(null);
		host.send({ ...command, action: "get", args: { property: "url" } });
		await vi.waitFor(() => expect(host.messages.at(-1)).toMatchObject({ result: {
			url: command.args.url, value: command.args.url, target: { origin: "http://localhost:5173" }, text: preview,
		} }));
		host.send({ ...command, requestId: "r2", args: { url: "example.com" } });
		await vi.waitFor(() => expect(execute).toHaveBeenCalledTimes(2));
		expect(execute.mock.calls[1]).toEqual([expect.objectContaining({ args: { url: "example.com" } }), expect.any(AbortSignal)]);
		expect(host.proxy.resolvePreviewUrl("project-1", preview)).toBe(command.args.url);
		host.send({ type: "ping" });
		await vi.waitFor(() => expect(host.messages).toContainEqual({ type: "pong" }));
	});

	it("can disconnect immediately during the WebSocket handshake", async () => {
		const host = await remote();
		const execute = vi.fn(async () => ({}));
		const attached = attach("host-a", host.proxy, execute);
		await attached.close();
		proxies.splice(proxies.indexOf(attached), 1);
		expect(execute).not.toHaveBeenCalled();
	});

	it("refuses a different loopback port instead of opening the selected app", async () => {
		const host = await remote();
		const execute = vi.fn(async () => ({}));
		attach("host-a", host.proxy, execute);
		await vi.waitFor(() => expect(host.messages).toHaveLength(1));
		host.send({ ...command, args: { url: "http://localhost:3001/api/v1/projects" } });
		await vi.waitFor(() => expect(host.messages.at(-1)).toMatchObject({ type: "result", ok: false, error: { code: "PREVIEW_MANAGED_REQUIRED" } }));
		expect(execute).not.toHaveBeenCalled();
	});

	it("isolates colliding sessions and cancels commands on disconnect", async () => {
		const a = await remote("host-a"), b = await remote("host-b");
		const signals: AbortSignal[] = [];
		const execute = vi.fn(async (_cmd: BrowserRuntimeCommand, signal: AbortSignal) => {
			if (_cmd.action === "__destroy-session") return {};
			signals.push(signal);
			return new Promise((resolve) => signal.addEventListener("abort", () => resolve({}), { once: true }));
		});
		attach("host-a", a.proxy, execute);
		const attachedB = attach("host-b", b.proxy, execute);
		await vi.waitFor(() => expect(a.messages.length + b.messages.length).toBe(2));
		a.send({ ...command, action: "snapshot" }); b.send({ ...command, action: "snapshot" });
		await vi.waitFor(() => expect(signals).toHaveLength(2));
		expect(new Set(execute.mock.calls.map(([cmd]) => cmd.sessionId))).toEqual(new Set([sessionUiKey("project-1", "host-a"), sessionUiKey("project-1", "host-b")]));
		a.drop();
		await vi.waitFor(() => expect(signals.filter((signal) => signal.aborted)).toHaveLength(1));
		await attachedB.close();
		proxies.splice(proxies.indexOf(attachedB), 1);
		expect(signals.every((signal) => signal.aborted)).toBe(true);
		expect(execute).toHaveBeenCalledWith(expect.objectContaining({ sessionId: sessionUiKey("project-1", "host-b"), action: "__destroy-session" }), expect.any(AbortSignal));
	});

	it.each(["/browser-runtime", "/browser-runtime?ignored=1", "/%62rowser-runtime"])("rejects page-origin runtime registration at %s before forwarding credentials", async (path) => {
		const host = await remote();
		const url = new URL(`${host.proxy.base}${path}`); url.protocol = "ws:";
		const ws = new WebSocket(url, { origin: "https://untrusted.example" });
		await new Promise<void>((resolve) => { ws.on("error", () => resolve()); });
		expect(host.requests).toEqual([]);
	});
});
