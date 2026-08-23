// THROWAWAY SPIKE (AO-82). Not for merge: it shells out to a real `ssh` and
// needs sshd reachable at localhost with key auth. It exists to answer one
// question with evidence rather than reasoning — does the EXISTING proxy, with
// zero changes, carry both a request and a WebSocket upgrade through an
// `ssh -L` tunnel? Run with: npx vitest run src/main/remote-proxy.ssh-spike
import { afterAll, beforeAll, expect, it } from "vitest";
import { spawn, type ChildProcess } from "node:child_process";
import { createServer, type Server } from "node:http";
import { createServer as createNetServer, connect as netConnect, type AddressInfo } from "node:net";
import { startRemoteProxy, type ActiveProxy } from "./remote-proxy";

let daemon: Server;
let ssh: ChildProcess;
let proxy: ActiveProxy;
let fwdPort: number;
// http.Server does not track sockets it has handed to an "upgrade" listener, so
// closeAllConnections() does not reach them and daemon.close() would hang.
const farSideSockets: import("node:net").Socket[] = [];
const seen: { url: string; auth?: string; origin?: string; upgrade?: string }[] = [];

// OpenSSH 10.2 rejects `-L 0:...` and `-L 127.0.0.1:0:...` ("Bad local
// forwarding specification"), so the caller must pick the port. Bind, read the
// port, release: between release and ssh's bind the port is anyone's. This
// race is the finding, not an artifact of the test.
async function freePort(): Promise<number> {
	const probe = createNetServer();
	await new Promise<void>((r) => probe.listen(0, "127.0.0.1", r));
	const port = (probe.address() as AddressInfo).port;
	await new Promise<void>((r) => probe.close(() => r()));
	return port;
}

beforeAll(async () => {
	// A stand-in for the remote AO daemon, bound to loopback on "the far side".
	daemon = createServer((req, res) => {
		seen.push({ url: req.url!, auth: req.headers.authorization, origin: req.headers.origin });
		res.writeHead(200, { "content-type": "application/json" });
		res.end('{"status":"ok"}');
	});
	daemon.on("upgrade", (req, socket) => {
		farSideSockets.push(socket);
		seen.push({ url: req.url!, auth: req.headers.authorization, upgrade: String(req.headers.upgrade) });
		socket.write("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n");
		socket.write("far-side-hello");
	});
	await new Promise<void>((r) => daemon.listen(0, "127.0.0.1", r));
	const daemonPort = (daemon.address() as AddressInfo).port;

	fwdPort = await freePort();
	ssh = spawn(
		"ssh",
		[
			"-N",
			"-o", "BatchMode=yes",
			"-o", "ExitOnForwardFailure=yes",
			"-o", "ConnectTimeout=6",
			"-L", `127.0.0.1:${fwdPort}:127.0.0.1:${daemonPort}`,
			"localhost",
		],
		{ stdio: ["ignore", "ignore", "pipe"] },
	);
	// Wait for the forward to actually accept, rather than sleeping and hoping.
	for (let i = 0; i < 40; i++) {
		const up = await new Promise<boolean>((resolve) => {
			const s = netConnect(fwdPort, "127.0.0.1", () => (s.destroy(), resolve(true)));
			s.on("error", () => resolve(false));
		});
		if (up) break;
		await new Promise((r) => setTimeout(r, 100));
	}

	// The seam, untouched: the host's saved url is simply the local forward.
	proxy = await startRemoteProxy({ label: "ssh box", url: `http://127.0.0.1:${fwdPort}`, password: "far-side-pw" });
}, 30_000);

afterAll(async () => {
	ssh?.kill();
	await proxy?.close();
	for (const s of farSideSockets) s.destroy();
	daemon?.closeAllConnections();
	await new Promise<void>((r) => daemon?.close(() => r()));
});

it("carries a credentialled request through the tunnel with no proxy change", async () => {
	const res = await fetch(`${proxy.base}/api/v1/sessions`, { headers: { origin: "app://renderer" } });
	expect(res.status).toBe(200);
	expect(await res.json()).toEqual({ status: "ok" });

	const hit = seen.find((s) => s.url === "/api/v1/sessions")!;
	expect(hit.auth).toBe("Bearer far-side-pw"); // credential injected in main, over ssh
	expect(hit.origin).toBeUndefined(); // renderer origin still stripped
});

it("carries a WebSocket upgrade through the tunnel (terminals/SSE path)", async () => {
	const url = new URL(proxy.base);
	const chunks: Buffer[] = [];
	await new Promise<void>((resolve, reject) => {
		const socket = netConnect(Number(url.port), url.hostname, () => {
			socket.write(
				`GET ${url.pathname}/mux HTTP/1.1\r\nHost: ${url.host}\r\n` +
					"Upgrade: websocket\r\nConnection: Upgrade\r\n\r\n",
			);
		});
		socket.on("data", (d) => {
			chunks.push(d);
			if (Buffer.concat(chunks).includes("far-side-hello")) (socket.destroy(), resolve());
		});
		socket.on("error", reject);
		setTimeout(() => reject(new Error("no upgrade response")), 8_000);
	});
	const upgraded = seen.find((s) => s.url === "/mux")!;
	expect(upgraded.upgrade).toBe("websocket");
	expect(upgraded.auth).toBe("Bearer far-side-pw");
});
