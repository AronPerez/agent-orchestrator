import { afterEach, describe, expect, it } from "vitest";
import { createServer, type IncomingMessage, type Server } from "node:http";
import { connect as netConnect, type AddressInfo } from "node:net";
import { startRemoteProxy, type ActiveProxy } from "./remote-proxy";

type Seen = {
	url: string;
	auth: string | undefined;
	origin: string | undefined;
	body: string;
};

let upstream: Server | undefined;
let proxy: ActiveProxy | undefined;

afterEach(async () => {
	await proxy?.close();
	await new Promise<void>((resolve) => (upstream ? upstream.close(() => resolve()) : resolve()));
	upstream = undefined;
	proxy = undefined;
});

async function startUpstream(
	handler: (req: IncomingMessage, seen: Seen[]) => { status: number; body: string },
): Promise<{ port: number; seen: Seen[] }> {
	const seen: Seen[] = [];
	upstream = createServer((req, res) => {
		let body = "";
		req.on("data", (chunk) => (body += chunk));
		req.on("end", () => {
			seen.push({
				url: req.url ?? "",
				auth: req.headers.authorization,
				origin: req.headers.origin,
				body,
			});
			const out = handler(req, seen);
			res.writeHead(out.status, { "content-type": "application/json" });
			res.end(out.body);
		});
	});
	await new Promise<void>((resolve) => upstream?.listen(0, "127.0.0.1", resolve));
	return { port: (upstream.address() as AddressInfo).port, seen };
}

describe("startRemoteProxy", () => {
	it("forwards with the token stripped and the credential injected", async () => {
		const { port, seen } = await startUpstream(() => ({
			status: 200,
			body: '{"ok":true}',
		}));
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});

		const res = await fetch(`${proxy.base}/api/v1/projects`, {
			method: "POST",
			headers: { "content-type": "application/json", origin: "app://renderer" },
			body: '{"path":"/srv/repo"}',
		});

		expect(res.status).toBe(200);
		expect(await res.json()).toEqual({ ok: true });
		expect(seen).toHaveLength(1);
		expect(seen[0].url).toBe("/api/v1/projects"); // token gone
		expect(seen[0].auth).toBe("Bearer pw");
		expect(seen[0].origin).toBeUndefined(); // app://renderer never reaches the daemon
		expect(seen[0].body).toBe('{"path":"/srv/repo"}');
	});

	it("refuses a request without the token and sends nothing upstream", async () => {
		const { port, seen } = await startUpstream(() => ({
			status: 200,
			body: "{}",
		}));
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});

		const bare = new URL(proxy.base);
		const res = await fetch(`${bare.origin}/api/v1/projects`);
		expect(res.status).toBe(404);
		expect(seen).toHaveLength(0);
	});

	it("refuses a near-miss token prefix", async () => {
		const { port, seen } = await startUpstream(() => ({
			status: 200,
			body: "{}",
		}));
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});

		// A path that merely starts with the token's characters is not the token:
		// /<token>x/... must not authorize, or the token stops being a boundary.
		const bare = new URL(proxy.base);
		const res = await fetch(`${bare.origin}${bare.pathname}x/api/v1/projects`);
		expect(res.status).toBe(404);
		expect(seen).toHaveLength(0);
	});

	it("answers CORS preflight itself for the renderer origin", async () => {
		const { port, seen } = await startUpstream(() => ({
			status: 200,
			body: "{}",
		}));
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});

		const res = await fetch(`${proxy.base}/api/v1/projects`, {
			method: "OPTIONS",
			headers: {
				origin: "app://renderer",
				"access-control-request-method": "POST",
				"access-control-request-headers": "content-type",
			},
		});
		expect(res.status).toBe(204);
		expect(res.headers.get("access-control-allow-origin")).toBe("app://renderer");
		expect(res.headers.get("access-control-allow-headers")).toMatch(/content-type/i);
		expect(seen).toHaveLength(0); // preflight never leaves the machine
	});

	it("adds the renderer origin to real responses so cross-origin fetch succeeds", async () => {
		const { port } = await startUpstream(() => ({
			status: 400,
			body: '{"error":"bad"}',
		}));
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});

		const res = await fetch(`${proxy.base}/api/v1/projects`, {
			headers: { origin: "app://renderer" },
		});
		expect(res.status).toBe(400); // errors pass through untouched…
		expect(res.headers.get("access-control-allow-origin")).toBe("app://renderer"); // …but stay readable
	});

	it("returns 502 when the upstream is unreachable", async () => {
		proxy = await startRemoteProxy({
			label: "dead",
			url: "http://127.0.0.1:1",
			password: "pw",
		});
		const res = await fetch(`${proxy.base}/api/v1/projects`);
		expect(res.status).toBe(502);
	});

	it("listens on loopback only", async () => {
		const { port } = await startUpstream(() => ({ status: 200, body: "{}" }));
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});
		expect(new URL(proxy.base).hostname).toBe("127.0.0.1");
	});
});

describe("startRemoteProxy streams", () => {
	it("delivers SSE chunks as they are written, not on close", async () => {
		upstream = createServer((req, res) => {
			res.writeHead(200, { "content-type": "text/event-stream" });
			res.write("data: first\n\n");
			setTimeout(() => {
				res.write("data: second\n\n");
				res.end();
			}, 500);
		});
		await new Promise<void>((resolve) => upstream?.listen(0, "127.0.0.1", resolve));
		const port = (upstream.address() as AddressInfo).port;
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});

		const res = await fetch(`${proxy.base}/api/v1/events`);
		const reader = res.body!.getReader();
		const started = Date.now();
		const first = new TextDecoder().decode((await reader.read()).value);
		const firstArrivedAfterMs = Date.now() - started;

		expect(first).toContain("data: first");
		// The second chunk is written 500ms later; receiving the first well before
		// that proves streaming rather than buffer-until-close.
		expect(firstArrivedAfterMs).toBeLessThan(300);
		let rest = "";
		for (;;) {
			const { done, value } = await reader.read();
			if (done) break;
			rest += new TextDecoder().decode(value);
		}
		expect(rest).toContain("data: second");
	});

	it("tunnels a WebSocket upgrade with the credential injected", async () => {
		const sawAuth: Array<string | undefined> = [];
		upstream = createServer();
		upstream.on("upgrade", (req, socket) => {
			sawAuth.push(req.headers.authorization);
			socket.write("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n");
			socket.on("data", (d) => socket.write(d)); // echo frames back verbatim
			// Upgraded sockets are half-open by default and would hold close() open.
			socket.on("end", () => socket.destroy());
		});
		await new Promise<void>((resolve) => upstream?.listen(0, "127.0.0.1", resolve));
		const port = (upstream.address() as AddressInfo).port;
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});

		const proxyUrl = new URL(proxy.base);
		const received: Buffer[] = [];
		const socket = netConnect(Number(proxyUrl.port), "127.0.0.1");
		await new Promise<void>((resolve) => socket.on("connect", () => resolve()));
		socket.on("data", (d) => received.push(d));
		socket.write(
			`GET ${proxyUrl.pathname}/mux HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGVzdA==\r\nSec-WebSocket-Version: 13\r\n\r\n`,
		);
		await new Promise((resolve) => setTimeout(resolve, 200));
		socket.write("payload-bytes");
		await new Promise((resolve) => setTimeout(resolve, 200));
		socket.destroy();

		const all = Buffer.concat(received).toString();
		expect(all).toContain("101 Switching Protocols");
		expect(all).toContain("payload-bytes"); // echoed through both pipes
		expect(sawAuth).toEqual(["Bearer pw"]);
	});

	it("destroys an upgrade that carries no token", async () => {
		const sawUpgrade: string[] = [];
		upstream = createServer();
		upstream.on("upgrade", (req) => sawUpgrade.push(req.url ?? ""));
		await new Promise<void>((resolve) => upstream?.listen(0, "127.0.0.1", resolve));
		const port = (upstream.address() as AddressInfo).port;
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});

		const proxyUrl = new URL(proxy.base);
		const socket = netConnect(Number(proxyUrl.port), "127.0.0.1");
		await new Promise<void>((resolve) => socket.on("connect", () => resolve()));
		const closed = new Promise<void>((resolve) => socket.on("close", () => resolve()));
		socket.on("error", () => undefined);
		socket.write(
			"GET /mux HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGVzdA==\r\nSec-WebSocket-Version: 13\r\n\r\n",
		);
		await closed;
		expect(sawUpgrade).toEqual([]);
	});
});
