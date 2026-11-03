import net from "node:net";
import { randomUUID } from "node:crypto";
import type { Duplex } from "node:stream";
import WebSocket, { createWebSocketStream } from "ws";
import { StringDecoder } from "node:string_decoder";

const PROTOCOL_VERSION = 4;
const BACKOFF_INIT_MS = 200;
const BACKOFF_MAX_MS = 2_000;
const MAX_COMMAND_BYTES = 1 << 20;
const MAX_RESULT_BYTES = 8 << 20;

export type BrowserRuntimeCommand = {
	type: "command";
	requestId: string;
	sessionId: string;
	action: string;
	args?: Record<string, unknown>;
};

type BrowserRuntimeCancel = {
	type: "cancel";
	requestId: string;
};

export type BrowserRuntimeCommandError = {
	code: string;
	message: string;
};

export type BrowserRuntimeSessionState = {
	sessionId: string;
	connected: boolean;
	owned: boolean;
	error?: BrowserRuntimeCommandError;
};

type SessionStateMessage = {
	type: "session-state";
	sessionId: string;
	owned: boolean;
	revision: number;
	requestId?: string;
	error?: BrowserRuntimeCommandError;
};

export interface BrowserRuntimeLinkHandle {
	readonly runtimeId: string;
	readonly sessions: readonly string[];
	attachSession(sessionId: string, takeover: boolean): Promise<BrowserRuntimeSessionState>;
	sessionState(sessionId: string): BrowserRuntimeSessionState;
	readonly connected: boolean;
	reconnect(): void;
	dispose(): void;
}

type BrowserRuntimeLinkOptions = {
	runtimeId?: string;
	sessions?: readonly string[];
	onSessionState?: (state: BrowserRuntimeSessionState) => void;
	execute: (command: BrowserRuntimeCommand, signal: AbortSignal) => Promise<unknown>;
	token?: string;
	log?: (message: string) => void;
	onStateChange?: (connected: boolean) => void;
};

export function connectBrowserRuntime(
	address: string | net.TcpNetConnectOpts | URL,
	options: BrowserRuntimeLinkOptions,
): BrowserRuntimeLinkHandle {
	const log = options.log ?? (() => undefined);
	let disposed = false;
	let connected = false;
	const runtimeId = options.runtimeId ?? randomUUID();
	const sessions = new Set(options.sessions);
	let helloSessions = new Set<string>();
	const sessionStates = new Map<string, SessionStateMessage>();
	let runtimeError: BrowserRuntimeCommandError | undefined;
	const claims = new Map<string, { sessionId: string; resolve: (state: BrowserRuntimeSessionState) => void; timer: ReturnType<typeof setTimeout> }>();
	const sessionState = (sessionId: string): BrowserRuntimeSessionState => {
		const state = sessionStates.get(sessionId);
		return { sessionId, connected, owned: connected && state?.owned === true, error: runtimeError ?? state?.error };
	};
	const publishSessions = () => {
		for (const sessionId of sessions) options.onSessionState?.(sessionState(sessionId));
	};
	const settleClaim = (requestId: string) => {
		const pending = claims.get(requestId);
		if (!pending) return;
		clearTimeout(pending.timer);
		claims.delete(requestId);
		pending.resolve(sessionState(pending.sessionId));
	};
	let socket: Duplex | null = null;
	let retryTimer: ReturnType<typeof setTimeout> | null = null;
	let backoff = BACKOFF_INIT_MS;
	let buffer = "";
	let decoder = new StringDecoder("utf8");
	let connectionEpoch = 0;
	const commandChains = new Map<string, Promise<void>>();
	const commandControllers = new Map<string, AbortController>();
	const activeCommands = new Map<
		string,
		{ command: BrowserRuntimeCommand; target: Duplex; epoch: number }
	>();
	const setConnected = (value: boolean) => {
		if (connected === value) return;
		connected = value;
		options.onStateChange?.(value);
		if (!value) {
			for (const requestId of claims.keys()) settleClaim(requestId);
		}
		publishSessions();
	};

	const sendCancelledResultOnTarget = (command: BrowserRuntimeCommand, target: Duplex) => {
		if (target.destroyed) return;
		const frame = `${JSON.stringify({
			type: "result",
			requestId: command.requestId,
			ok: false,
			error: { code: "BROWSER_COMMAND_CANCELED", message: "Browser runtime link closed" },
		})}\n`;
		try {
			target.write(frame);
		} catch {
			// Socket already torn down; the daemon will observe disconnect.
		}
	};

	const cancelConnectionCommands = () => {
		for (const { command, target } of activeCommands.values()) {
			sendCancelledResultOnTarget(command, target);
		}
		// Drop tracking before aborting so in-flight respond() catch paths do not
		// emit a second cancellation frame for the same requestId.
		activeCommands.clear();
		for (const controller of commandControllers.values()) controller.abort();
		commandControllers.clear();
		commandChains.clear();
	};

	const clearRetry = () => {
		if (retryTimer !== null) {
			clearTimeout(retryTimer);
			retryTimer = null;
		}
	};

	const destroySocket = () => {
		if (!socket) return;
		const target = socket;
		cancelConnectionCommands();
		connectionEpoch += 1;
		target.removeAllListeners();
		// A WebSocket stream can fail asynchronously when closed during its handshake.
		target.on("error", () => undefined);
		target.destroy();
		socket = null;
	};

	const send = async (message: unknown, target: Duplex, epoch: number): Promise<void> => {
		if (socket !== target || connectionEpoch !== epoch || target.destroyed) return;
		const frame = `${JSON.stringify(message)}\n`;
		if (Buffer.byteLength(frame, "utf8") > MAX_RESULT_BYTES) {
			throw Object.assign(new Error(`Browser result exceeds ${MAX_RESULT_BYTES} bytes`), {
				code: "BROWSER_RESULT_TOO_LARGE",
			});
		}
		await new Promise<void>((resolve, reject) => {
			const onError = (error: Error) => {
				target.off("error", onError);
				reject(error);
			};
			target.once("error", onError);
			target.write(frame, (error) => {
				target.off("error", onError);
				if (error) reject(error);
				else resolve();
			});
		});
	};

	const sendCancelledResult = async (
		command: BrowserRuntimeCommand,
		target: Duplex,
		epoch: number,
	) => {
		try {
			await send(
				{
					type: "result",
					requestId: command.requestId,
					ok: false,
					error: { code: "BROWSER_COMMAND_CANCELED", message: "Browser runtime link closed" },
				},
				target,
				epoch,
			);
		} catch {
			// Socket already torn down; the daemon will observe disconnect.
		}
	};

	const respond = async (
		command: BrowserRuntimeCommand,
		target: Duplex,
		epoch: number,
		controller: AbortController,
	) => {
		try {
			controller.signal.throwIfAborted();
			const result = await options.execute(command, controller.signal);
			if (command.action === "__destroy-session") {
				sessions.delete(command.sessionId);
				sessionStates.delete(command.sessionId);
				for (const [requestId, claim] of claims) if (claim.sessionId === command.sessionId) settleClaim(requestId);
			}
			controller.signal.throwIfAborted();
			await send({ type: "result", requestId: command.requestId, ok: true, result }, target, epoch);
		} catch (error) {
			if (controller.signal.aborted) {
				// Daemon-initiated cancel frames abort one controller without clearing
				// activeCommands; connection teardown clears the map before aborting.
				if (activeCommands.has(command.requestId)) {
					await sendCancelledResult(command, target, epoch);
				}
				return;
			}
			const normalized = normalizeCommandError(error);
			try {
				await send({ type: "result", requestId: command.requestId, ok: false, error: normalized }, target, epoch);
			} catch (sendError) {
				log(`browser-runtime-link: response failed: ${String(sendError)}`);
				target.destroy();
			}
		} finally {
			activeCommands.delete(command.requestId);
			if (commandControllers.get(command.requestId) === controller) {
				commandControllers.delete(command.requestId);
			}
		}
	};

	const consumeLine = (line: string, target: Duplex, epoch: number) => {
		if (!line.trim()) return;
		let message: BrowserRuntimeCommand | BrowserRuntimeCancel | SessionStateMessage | { type: "hello-ok" | "hello-rejected" | "rejected" | "ping"; error?: BrowserRuntimeCommandError };
		try {
			message = JSON.parse(line);
		} catch {
			return;
		}
		if (!message || typeof message !== "object") return;
		if (message.type === "hello-ok") {
			runtimeError = undefined;
			setConnected(true);
			for (const sessionId of sessions) {
				if (!helloSessions.has(sessionId) && ![...claims.values()].some((claim) => claim.sessionId === sessionId)) void attachSession(sessionId, false);
			}
			backoff = BACKOFF_INIT_MS;
			return;
		}
		if (message.type === "hello-rejected" || message.type === "rejected") {
			runtimeError = isCommandError(message.error) ? message.error : { code: "BROWSER_RUNTIME_REJECTED", message: "Browser runtime registration was rejected" };
			setConnected(false);
			publishSessions();
			target.destroy();
			return;
		}
		if (message.type === "session-state") {
			if (typeof message.sessionId !== "string" || typeof message.owned !== "boolean" || !Number.isSafeInteger(message.revision) || !sessions.has(message.sessionId)) return;
			const previous = sessionStates.get(message.sessionId);
			if (!previous || message.revision >= previous.revision) {
				sessionStates.set(message.sessionId, { ...message, error: isCommandError(message.error) ? message.error : undefined });
				options.onSessionState?.(sessionState(message.sessionId));
			}
			if (message.requestId && claims.get(message.requestId)?.sessionId === message.sessionId) settleClaim(message.requestId);
			return;
		}
		if (message.type === "ping") {
			void send({ type: "pong" }, target, epoch).catch(() => target.destroy());
			return;
		}
		if (message.type === "cancel" && typeof message.requestId === "string") {
			commandControllers.get(message.requestId)?.abort();
			return;
		}
		const command = message as BrowserRuntimeCommand;
		if (
			command.type !== "command" ||
			typeof command.requestId !== "string" ||
			typeof command.sessionId !== "string" ||
			typeof command.action !== "string"
		) {
			return;
		}
		if (!command.action.startsWith("__")) {
			sessions.add(command.sessionId);
			if (!sessionStates.has(command.sessionId)) {
				sessionStates.set(command.sessionId, { type: "session-state", sessionId: command.sessionId, owned: true, revision: 0 });
				options.onSessionState?.(sessionState(command.sessionId));
			}
		}
		const controller = new AbortController();
		commandControllers.set(command.requestId, controller);
		activeCommands.set(command.requestId, { command, target, epoch });
		const previous = commandChains.get(command.sessionId) ?? Promise.resolve();
		const next = previous.then(() => respond(command, target, epoch, controller));
		commandChains.set(command.sessionId, next);
		void next.finally(() => {
			if (commandChains.get(command.sessionId) === next) commandChains.delete(command.sessionId);
		});
	};

	const consume = (chunk: Buffer, target: Duplex, epoch: number) => {
		if (socket !== target || connectionEpoch !== epoch) return;
		buffer += decoder.write(chunk);
		for (;;) {
			const newline = buffer.indexOf("\n");
			if (newline < 0) {
				if (Buffer.byteLength(buffer, "utf8") > MAX_COMMAND_BYTES) {
					log("browser-runtime-link: oversized command frame; reconnecting");
					target.destroy();
				}
				return;
			}
			const line = buffer.slice(0, newline);
			buffer = buffer.slice(newline + 1);
			if (Buffer.byteLength(line, "utf8") > MAX_COMMAND_BYTES) {
				log("browser-runtime-link: oversized command frame; reconnecting");
				target.destroy();
				return;
			}
			consumeLine(line, target, epoch);
		}
	};

	const scheduleReconnect = () => {
		if (disposed) return;
		clearRetry();
		const delay = backoff;
		backoff = Math.min(backoff * 2, BACKOFF_MAX_MS);
		retryTimer = setTimeout(connect, delay);
	};

	function connect() {
		if (disposed) return;
		destroySocket();
		sessionStates.clear();
		buffer = "";
		decoder = new StringDecoder("utf8");
		const epoch = ++connectionEpoch;
		const ws = address instanceof URL ? new WebSocket(address, { handshakeTimeout: 8_000, maxPayload: MAX_COMMAND_BYTES }) : null;
		const next = ws ? createWebSocketStream(ws, { decodeStrings: false }) : typeof address === "string" ? net.connect(address) : net.connect(address as net.TcpNetConnectOpts);
		socket = next;
		const ready = () => {
			if (disposed) {
				next.destroy();
				return;
			}
			// Both transports must acknowledge the v4 registration before accepting claims.
			helloSessions = new Set(sessions);
			void send({ type: "hello", version: PROTOCOL_VERSION, runtimeId, sessions: [...helloSessions], token: options.token }, next, epoch).catch((error) => {
				log(`browser-runtime-link: hello failed: ${String(error)}`);
				next.destroy();
			});
			log("browser-runtime-link: connected");
		};
		if (ws) ws.on("open", ready);
		else next.on("connect", ready);
		next.on("data", (chunk) => consume(chunk, next, epoch));
		next.on("error", (error) => log(`browser-runtime-link: error: ${error.message}`));
		let connectionTornDown = false;
		const tearDownConnection = () => {
			if (connectionTornDown || socket !== next || connectionEpoch !== epoch) return;
			connectionTornDown = true;
			setConnected(false);
			cancelConnectionCommands();
			socket = null;
			connectionEpoch += 1;
			if (!disposed) scheduleReconnect();
		};
		// Cancel while the socket may still accept writes; 'close' can arrive too late.
		next.on("end", tearDownConnection);
		next.on("close", tearDownConnection);
	}

	function attachSession(sessionId: string, takeover: boolean): Promise<BrowserRuntimeSessionState> {
		if (disposed) return Promise.resolve(sessionState(sessionId));
		sessions.add(sessionId);
		if (!connected || !socket) return Promise.resolve(sessionState(sessionId));
		const target = socket, epoch = connectionEpoch, requestId = randomUUID();
		const previous = sessionStates.get(sessionId);
		return new Promise((resolve) => {
			const timer = setTimeout(() => {
				if (sessionStates.get(sessionId) === previous) {
					sessionStates.set(sessionId, { type: "session-state", sessionId, owned: false, revision: previous?.revision ?? 0, error: { code: "BROWSER_RUNTIME_TIMEOUT", message: "Browser ownership request timed out" } });
					options.onSessionState?.(sessionState(sessionId));
				}
				settleClaim(requestId);
			}, 8_000);
			claims.set(requestId, { sessionId, resolve, timer });
			void send({ type: "attach-session", requestId, sessionId, takeover }, target, epoch).catch(() => target.destroy());
		});
	}

	connect();
	return {
		runtimeId,
		get sessions() { return [...sessions]; },
		sessionState,
		attachSession,

		get connected() {
			return connected;
		},
		reconnect() {
			if (disposed) return;
			setConnected(false);
			clearRetry();
			connect();
		},
		dispose() {
			disposed = true;
			setConnected(false);
			clearRetry();
			destroySocket();
		},
	};
}

function normalizeCommandError(error: unknown): BrowserRuntimeCommandError {
	if (isCommandError(error)) {
		return { code: error.code, message: error.message };
	}
	return {
		code: "BROWSER_COMMAND_FAILED",
		message: error instanceof Error ? error.message : "Browser command failed",
	};
}

function isCommandError(error: unknown): error is BrowserRuntimeCommandError {
	return Boolean(
		error &&
		typeof error === "object" &&
		typeof (error as BrowserRuntimeCommandError).code === "string" &&
		typeof (error as BrowserRuntimeCommandError).message === "string",
	);
}
