import { createServer as createHttpServer, type Server as HttpServer } from "node:http";
import net from "node:net";
import { afterEach, describe, expect, it, vi } from "vitest";
import { connectBrowserRuntime, upgradeDial, type BrowserRuntimeLinkHandle } from "./browser-runtime-link";

const handles: BrowserRuntimeLinkHandle[] = [];
const servers: (net.Server | HttpServer)[] = [];

afterEach(async () => {
	handles.splice(0).forEach((handle) => handle.dispose());
	await Promise.all(
		servers.splice(0).map(
			(server) =>
				new Promise<void>((resolve) => {
					server.close(() => resolve());
				}),
		),
	);
});

describe("browser runtime link", () => {
	it("handshakes and correlates a command result", async () => {
		const execute = vi.fn(async () => ({ text: "button Save [ref=e1]" }));
		let serverSocket: net.Socket | null = null;
		let inbound = "";
		const messages: unknown[] = [];
		const server = net.createServer((socket) => {
			serverSocket = socket;
			socket.on("data", (chunk) => {
				inbound += chunk.toString("utf8");
				for (;;) {
					const newline = inbound.indexOf("\n");
					if (newline < 0) return;
					messages.push(JSON.parse(inbound.slice(0, newline)));
					inbound = inbound.slice(newline + 1);
				}
			});
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const address = server.address() as net.AddressInfo;
		const handle = connectBrowserRuntime({ host: address.address, port: address.port }, { execute });
		handles.push(handle);
		await vi.waitFor(() => expect(handle.connected).toBe(true));
		await vi.waitFor(() => expect(messages).toContainEqual({ type: "hello", version: 2 }));

		serverSocket!.write(
			`${JSON.stringify({ type: "command", requestId: "r1", sessionId: "s1", action: "snapshot", args: {} })}\n`,
		);

		await vi.waitFor(() =>
			expect(execute).toHaveBeenCalledWith(
				expect.objectContaining({ requestId: "r1" }),
				expect.any(AbortSignal),
			),
		);
		await vi.waitFor(() =>
			expect(messages).toContainEqual({
				type: "result",
				requestId: "r1",
				ok: true,
				result: { text: "button Save [ref=e1]" },
			}),
		);
	});

	it("returns structured command errors", async () => {
		let serverSocket: net.Socket | null = null;
		let inbound = "";
		const messages: unknown[] = [];
		const server = net.createServer((socket) => {
			serverSocket = socket;
			socket.on("data", (chunk) => {
				inbound += chunk.toString("utf8");
				const lines = inbound.split("\n");
				inbound = lines.pop() ?? "";
				for (const line of lines) if (line) messages.push(JSON.parse(line));
			});
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const address = server.address() as net.AddressInfo;
		const handle = connectBrowserRuntime(
			{ host: address.address, port: address.port },
			{
				execute: async () => {
					throw { code: "STALE_REFERENCE", message: "snapshot again" };
				},
			},
		);
		handles.push(handle);
		await vi.waitFor(() => expect(handle.connected).toBe(true));
		serverSocket!.write(`${JSON.stringify({ type: "command", requestId: "r2", sessionId: "s1", action: "click" })}\n`);
		await vi.waitFor(() =>
			expect(messages).toContainEqual({
				type: "result",
				requestId: "r2",
				ok: false,
				error: { code: "STALE_REFERENCE", message: "snapshot again" },
			}),
		);
	});

	it("preserves UTF-8 code points split across socket chunks", async () => {
		let serverSocket: net.Socket | null = null;
		const execute = vi.fn(async () => ({}));
		const server = net.createServer((socket) => {
			serverSocket = socket;
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const address = server.address() as net.AddressInfo;
		const handle = connectBrowserRuntime({ host: address.address, port: address.port }, { execute });
		handles.push(handle);
		await vi.waitFor(() => expect(handle.connected).toBe(true));

		const frame = Buffer.from(
			`${JSON.stringify({
				type: "command",
				requestId: "utf8",
				sessionId: "s1",
				action: "fill",
				args: { text: "café 🎉" },
			})}\n`,
			"utf8",
		);
		const emojiStart = frame.indexOf(Buffer.from("🎉", "utf8"));
		serverSocket!.write(frame.subarray(0, emojiStart + 1));
		serverSocket!.write(frame.subarray(emojiStart + 1));

		await vi.waitFor(() =>
			expect(execute).toHaveBeenCalledWith(
				expect.objectContaining({ args: { text: "café 🎉" } }),
				expect.any(AbortSignal),
			),
		);
		serverSocket!.destroy();
	});

	it("queues per session and cancels work from a closed connection", async () => {
		let serverSocket: net.Socket | null = null;
		const messages: Array<Record<string, unknown>> = [];
		const executed: string[] = [];
		const server = net.createServer((socket) => {
			serverSocket = socket;
			let inbound = "";
			socket.on("data", (chunk) => {
				inbound += chunk.toString("utf8");
				const lines = inbound.split("\n");
				inbound = lines.pop() ?? "";
				for (const line of lines) if (line) messages.push(JSON.parse(line));
			});
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const address = server.address() as net.AddressInfo;
		const handle = connectBrowserRuntime(
			{ host: address.address, port: address.port },
			{
				execute: async (command, signal) => {
					executed.push(command.requestId);
					if (command.requestId === "blocked") {
						await new Promise<void>((_, reject) => {
							signal.addEventListener("abort", () => reject(new Error("aborted")), { once: true });
						});
					}
					return { requestId: command.requestId };
				},
			},
		);
		handles.push(handle);
		await vi.waitFor(() => expect(handle.connected).toBe(true));

		serverSocket!.write(
			[
				{ type: "command", requestId: "blocked", sessionId: "s1", action: "wait" },
				{ type: "command", requestId: "queued", sessionId: "s1", action: "click" },
				{ type: "command", requestId: "independent", sessionId: "s2", action: "snapshot" },
			]
				.map((message) => JSON.stringify(message))
				.join("\n") + "\n",
		);
		await vi.waitFor(() => expect(executed).toContain("independent"));
		expect(executed).not.toContain("queued");

		serverSocket!.destroy();
		await vi.waitFor(() => expect(handle.connected).toBe(false));
		await vi.waitFor(() => expect(handle.connected).toBe(true));
		expect(executed).not.toContain("queued");
		expect(messages.some((message) => message.requestId === "blocked" && message.type === "result")).toBe(false);
	});
});

describe("upgradeDial", () => {
	it("dials through an HTTP upgrade and hands over head bytes", async () => {
		const server = createHttpServer();
		const upgraded: net.Socket[] = [];
		server.on("upgrade", (req, socket) => {
			upgraded.push(socket);
			expect(req.url).toBe("/browser-runtime");
			expect(req.headers.upgrade).toBe("ao-browser-runtime");
			socket.write(
				"HTTP/1.1 101 Switching Protocols\r\nUpgrade: ao-browser-runtime\r\nConnection: Upgrade\r\n\r\n" +
					'{"type":"command","requestId":"r1","sessionId":"s1","action":"open"}\n',
			);
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const port = (server.address() as net.AddressInfo).port;

		const seen: string[] = [];
		const handle = connectBrowserRuntime(null, {
			dial: upgradeDial(`http://127.0.0.1:${port}`),
			execute: async (command) => {
				seen.push(command.requestId);
				return { ok: true };
			},
		});
		handles.push(handle);
		await vi.waitFor(() => expect(seen).toEqual(["r1"]));
		// An upgraded socket is detached from the server, so close() alone hangs.
		upgraded.forEach((socket) => socket.destroy());
	});

	it("rejects a non-101 response and the link retries", async () => {
		const server = createHttpServer((_req, res) => {
			res.writeHead(401);
			res.end();
		});
		servers.push(server);
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const port = (server.address() as net.AddressInfo).port;
		const logs: string[] = [];
		const handle = connectBrowserRuntime(null, {
			dial: upgradeDial(`http://127.0.0.1:${port}`),
			execute: async () => ({}),
			log: (message) => logs.push(message),
		});
		handles.push(handle);
		await vi.waitFor(() => expect(logs.some((entry) => entry.includes("dial failed"))).toBe(true));
		expect(handle.connected).toBe(false);
	});
});
