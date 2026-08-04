import AsyncStorage from "@react-native-async-storage/async-storage";
import { useCallback, useEffect, useState } from "react";
import { secureDeleteItem, secureGetItem, secureSetItem } from "./secure-store";

// The user points the app at their AO daemon (over Tailscale/LAN). We store the
// host + API port; HTTP and WS URLs are derived from them. The Go daemon serves
// both the REST API and the terminal mux (`/mux`) on the same port, so muxPort is
// kept only for back-compat and no longer used to build the mux URL.
export type ServerConfig = {
	host: string; // e.g. "100.101.102.103" or "my-pc.tail1234.ts.net"
	// Port of the daemon's LAN mobile bridge (REST API + /mux), default 3011.
	// NOT 3001 — that is the desktop/CLI daemon, which binds loopback only and
	// can never be reached from a phone.
	httpPort: string;
	muxPort: string; // legacy separate mux port - unused against the Go daemon
	secure?: boolean; // use https/wss instead of http/ws (TLS / Tailscale funnel)
	password: string; // daemon connection password for Authorization header
	label?: string; // display name for the node; defaults to the host
};

/** A saved node, as persisted. Never holds the password — see PW_PREFIX. */
export type ServerEntry = {
	id: string;
	label: string;
	host: string;
	httpPort: string;
	muxPort: string;
	secure?: boolean;
};

export const DEFAULT_CONFIG: ServerConfig = {
	host: "",
	httpPort: "3011",
	muxPort: "14801",
	secure: false,
	password: "",
};

export function authHeaders(cfg: ServerConfig): Record<string, string> {
	return cfg.password ? { Authorization: `Bearer ${cfg.password}` } : {};
}

// Strip a pasted scheme (http://, ws://, …) and trailing slashes so we never
// build a double-scheme URL like "http://https://host".
function cleanHost(host: string): string {
	return host
		.trim()
		.replace(/^[a-z][a-z0-9+.-]*:\/\//i, "")
		.replace(/\/+$/, "");
}

// Non-secret host/port/TLS config lives in AsyncStorage (plaintext app sandbox).
// The user runs one `ao` per node, so this is a list plus the id of the active
// one, not a single server.
const SERVERS_KEY = "ao.servers";
const ACTIVE_KEY = "ao.activeServerId";
// Pre-multi-node builds stored one server here (and, older still, its password
// inside the same blob). Read once at startup and converted — see migrateLegacy.
const LEGACY_KEY = "ao.serverConfig";
const LEGACY_PW_KEY = "ao.serverPassword";
// The connection password is the Bearer secret for REST and /mux — it authorizes
// terminal input, spawn/kill, PR actions, etc. It must NEVER touch AsyncStorage;
// it lives only in the device keystore (iOS Keychain / Android Keystore), one
// entry per node. Dot-separated, not "ao.serverPassword:<id>": SecureStore keys
// may only contain alphanumerics, ".", "-" and "_", and reject a colon outright.
const PW_PREFIX = "ao.serverPassword.";

function pwKey(id: string): string {
	return `${PW_PREFIX}${id}`;
}

function newId(): string {
	return `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 8)}`;
}

type Servers = { servers: ServerEntry[]; activeId: string | null };

// Storage is the only input here that we don't write ourselves in the same
// version, so a half-written or hand-edited value must not throw: saveConfig
// reads before it writes, and an exception there would leave the user unable to
// pair at all, with nothing to do about it.
function parseJson<T>(raw: string | null): T | null {
	if (!raw) return null;
	try {
		return JSON.parse(raw) as T;
	} catch {
		return null;
	}
}

// Every read and write funnels through here, so the legacy conversion happens
// exactly once, lazily, on whatever call touches storage first.
async function readServers(): Promise<Servers> {
	const servers = parseJson<ServerEntry[]>(await AsyncStorage.getItem(SERVERS_KEY));
	if (!Array.isArray(servers)) return migrateLegacy();
	const stored = await AsyncStorage.getItem(ACTIVE_KEY);
	// A dangling active id (its node was removed by an older build, or the write
	// was interrupted) would leave the app paired-but-not-connected with no way
	// back, so fall back to the first node.
	const activeId = stored && servers.some((s) => s.id === stored) ? stored : (servers[0]?.id ?? null);
	return { servers, activeId };
}

// Convert an already-paired phone to the list format: one entry, labelled by its
// host, with the password moved to the per-id keystore entry. The user does
// nothing and notices nothing.
async function migrateLegacy(): Promise<Servers> {
	const parsed = parseJson<Partial<ServerConfig>>(await AsyncStorage.getItem(LEGACY_KEY));
	if (!parsed) return { servers: [], activeId: null };
	const entry: ServerEntry = {
		id: newId(),
		label: cleanHost(parsed.host ?? ""),
		host: parsed.host ?? DEFAULT_CONFIG.host,
		httpPort: parsed.httpPort ?? DEFAULT_CONFIG.httpPort,
		muxPort: parsed.muxPort ?? DEFAULT_CONFIG.muxPort,
		secure: parsed.secure ?? DEFAULT_CONFIG.secure,
	};
	// Older builds than that persisted the password inside the blob itself. Take
	// it from there when present — dropping the legacy blob below is what finally
	// removes the plaintext copy from disk.
	const password = parsed.password ?? (await secureGetItem(LEGACY_PW_KEY));
	if (password) await secureSetItem(pwKey(entry.id), password);
	await secureDeleteItem(LEGACY_PW_KEY);
	await writeServers([entry], entry.id);
	await AsyncStorage.removeItem(LEGACY_KEY);
	return { servers: [entry], activeId: entry.id };
}

async function writeServers(servers: ServerEntry[], activeId: string | null): Promise<void> {
	await AsyncStorage.setItem(SERVERS_KEY, JSON.stringify(servers));
	if (activeId) await AsyncStorage.setItem(ACTIVE_KEY, activeId);
	else await AsyncStorage.removeItem(ACTIVE_KEY);
}

function configOf(entry: ServerEntry, password: string): ServerConfig {
	return {
		host: entry.host,
		httpPort: entry.httpPort,
		muxPort: entry.muxPort || DEFAULT_CONFIG.muxPort,
		secure: entry.secure,
		password,
		label: entry.label,
	};
}

/** The active node, or DEFAULT_CONFIG when nothing is paired. */
export async function loadConfig(): Promise<ServerConfig> {
	try {
		const { servers, activeId } = await readServers();
		const entry = servers.find((s) => s.id === activeId);
		if (!entry) return DEFAULT_CONFIG;
		return configOf(entry, (await secureGetItem(pwKey(entry.id))) ?? "");
	} catch {
		return DEFAULT_CONFIG;
	}
}

/** Every saved node, plus which one is active. */
export async function listServers(): Promise<Servers> {
	try {
		return await readServers();
	} catch {
		return { servers: [], activeId: null };
	}
}

/**
 * Upsert by `host:port`, then make it active. The upsert is what lets pairing a
 * second node *add* one instead of overwriting the first, without the pairing
 * screens needing to know that nodes are a list at all — and re-pairing a node
 * you already have (a password change, say) still edits it in place rather than
 * leaving a duplicate row behind.
 */
export async function saveConfig(cfg: ServerConfig): Promise<void> {
	const { servers } = await readServers();
	const host = cleanHost(cfg.host);
	const existing = servers.find((s) => cleanHost(s.host) === host && s.httpPort === cfg.httpPort);
	const id = existing?.id ?? newId();
	// QR pairing carries no name, so an unnamed node labels itself by its host —
	// and re-pairing keeps whatever the user renamed it to.
	const entry: ServerEntry = {
		id,
		label: cfg.label?.trim() || existing?.label || host,
		host: cfg.host,
		httpPort: cfg.httpPort,
		muxPort: cfg.muxPort,
		secure: cfg.secure,
	};
	await writeServers(
		existing ? servers.map((s) => (s.id === id ? entry : s)) : [...servers, entry],
		id,
	);
	if (cfg.password) await secureSetItem(pwKey(id), cfg.password);
	else await secureDeleteItem(pwKey(id));
}

/** Make `id` the active node. Unknown ids are ignored. */
export async function switchServer(id: string): Promise<void> {
	const { servers } = await readServers();
	if (!servers.some((s) => s.id === id)) return;
	await AsyncStorage.setItem(ACTIVE_KEY, id);
}

/**
 * Forget one node. Both storage tiers must be cleared: dropping only the
 * AsyncStorage entry would leave its connection password behind in the device
 * keystore, so a later re-pair to the same host would silently resurrect it.
 * Removing the active node promotes the next one, so a user with several nodes
 * lands on another instead of an unpaired app.
 */
export async function removeServer(id: string): Promise<void> {
	const { servers, activeId } = await readServers();
	const rest = servers.filter((s) => s.id !== id);
	if (rest.length === servers.length) return;
	await writeServers(rest, activeId === id ? (rest[0]?.id ?? null) : activeId);
	await secureDeleteItem(pwKey(id));
}

export async function renameServer(id: string, label: string): Promise<void> {
	const { servers, activeId } = await readServers();
	if (!servers.some((s) => s.id === id)) return;
	await writeServers(
		servers.map((s) => (s.id === id ? { ...s, label: label.trim() || cleanHost(s.host) } : s)),
		activeId,
	);
}

/** Forget the *active* node — the storage half of "Disconnect & forget server". */
export async function clearConfig(): Promise<void> {
	const { activeId } = await readServers();
	if (activeId) await removeServer(activeId);
}

export function httpBase(cfg: ServerConfig): string {
	return `${cfg.secure ? "https" : "http"}://${cleanHost(cfg.host)}:${cfg.httpPort}`;
}

export function muxUrl(cfg: ServerConfig): string {
	// The Go daemon serves the terminal mux at /mux on the same HTTP port as the
	// REST API (not a separate mux port).
	return `${cfg.secure ? "wss" : "ws"}://${cleanHost(cfg.host)}:${cfg.httpPort}/mux`;
}

export function isConfigured(cfg: ServerConfig): boolean {
	return cleanHost(cfg.host).length > 0;
}

// Small reactive hook so screens re-render when the config changes.
export function useServerConfig() {
	const [config, setConfig] = useState<ServerConfig | null>(null);

	const reload = useCallback(async () => {
		setConfig(await loadConfig());
	}, []);

	useEffect(() => {
		reload();
	}, [reload]);

	const update = useCallback(async (cfg: ServerConfig) => {
		await saveConfig(cfg);
		setConfig(cfg);
	}, []);

	return { config, update, reload };
}
