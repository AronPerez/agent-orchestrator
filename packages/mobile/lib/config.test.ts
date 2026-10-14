import { beforeEach, describe, expect, it, vi } from "vitest";

// Both storage tiers are native, so they're faked with plain maps: what matters
// here is which tier each field lands in (the password must never reach the
// AsyncStorage one) and how the list behaves across save/switch/remove.
const async_ = new Map<string, string>();
const secure = new Map<string, string>();

vi.mock("@react-native-async-storage/async-storage", () => ({
	default: {
		getItem: async (k: string) => async_.get(k) ?? null,
		setItem: async (k: string, v: string) => void async_.set(k, v),
		removeItem: async (k: string) => void async_.delete(k),
	},
}));
vi.mock("./secure-store", () => ({
	secureGetItem: async (k: string) => secure.get(k) ?? null,
	secureSetItem: async (k: string, v: string) => void secure.set(k, v),
	secureDeleteItem: async (k: string) => void secure.delete(k),
}));

const { clearConfig, listServers, loadConfig, removeServer, renameServer, saveConfig, switchServer } =
	await import("./config");

const node = (host: string, password: string, label?: string) => ({
	host,
	httpPort: "3011",
	muxPort: "14801",
	secure: false,
	password,
	...(label ? { label } : {}),
});

/** Every AsyncStorage value, as one string — for "no password on disk" checks. */
const plaintext = () => JSON.stringify([...async_.entries()]);

beforeEach(() => {
	async_.clear();
	secure.clear();
});

describe("legacy migration", () => {
	// An already-paired phone must keep working with no user action: the single
	// stored server becomes the one node in the list, and its password moves from
	// the shared keystore entry to the per-node one.
	it("converts a single stored server into a one-node list", async () => {
		async_.set("ao.serverConfig", JSON.stringify({ host: "pc.tail1234.ts.net", httpPort: "3011", muxPort: "14801" }));
		secure.set("ao.serverPassword", "hunter2");

		const cfg = await loadConfig();
		expect(cfg.host).toBe("pc.tail1234.ts.net");
		expect(cfg.password).toBe("hunter2");

		const { servers, activeId } = await listServers();
		expect(servers).toHaveLength(1);
		expect(servers[0].label).toBe("pc.tail1234.ts.net"); // labels default to the host
		expect(activeId).toBe(servers[0].id);
		expect(secure.get(`ao.serverPassword.${servers[0].id}`)).toBe("hunter2");
		// The legacy keys are consumed, so this runs once and never again.
		expect(async_.has("ao.serverConfig")).toBe(false);
		expect(secure.has("ao.serverPassword")).toBe(false);
	});

	// Builds older still kept the password inside the AsyncStorage blob. That path
	// existed before multi-node and still has to end with the plaintext copy gone.
	it("lifts a password out of the legacy blob into the keystore", async () => {
		async_.set("ao.serverConfig", JSON.stringify({ host: "100.64.0.1", httpPort: "3011", password: "in-the-blob" }));

		const cfg = await loadConfig();
		expect(cfg.password).toBe("in-the-blob");
		const { servers } = await listServers();
		expect(secure.get(`ao.serverPassword.${servers[0].id}`)).toBe("in-the-blob");
		expect(plaintext()).not.toContain("in-the-blob");
	});

	it("reports nothing paired on a fresh install", async () => {
		expect(await loadConfig()).toMatchObject({ host: "", password: "" });
		expect(await listServers()).toMatchObject({ servers: [], activeId: null });
	});

	// saveConfig reads the list before writing it, so a truncated value would
	// otherwise throw there and leave the user unable to pair at all.
	it("recovers from a corrupt list instead of blocking pairing", async () => {
		async_.set("ao.servers", "[{oh no");
		expect(await listServers()).toMatchObject({ servers: [], activeId: null });

		await saveConfig(node("a.ts.net", "pw-a"));
		expect((await loadConfig()).host).toBe("a.ts.net");
	});
});

describe("saveConfig", () => {
	// The whole point of upserting: pairing a second node adds it, so the pairing
	// screens don't have to know a list exists.
	it("adds a node per host:port and activates the newest", async () => {
		await saveConfig(node("a.ts.net", "pw-a"));
		await saveConfig(node("b.ts.net", "pw-b"));

		const { servers, activeId } = await listServers();
		expect(servers.map((s) => s.host)).toEqual(["a.ts.net", "b.ts.net"]);
		expect(servers.find((s) => s.id === activeId)?.host).toBe("b.ts.net");
		expect((await loadConfig()).password).toBe("pw-b");
	});

	// Re-pairing an existing node (new password, renamed) edits it in place —
	// otherwise every re-pair would leave a duplicate row behind.
	it("edits in place when the host:port is already saved", async () => {
		await saveConfig(node("a.ts.net", "old"));
		await saveConfig(node("b.ts.net", "pw-b"));
		await saveConfig(node("a.ts.net", "new", "Studio"));

		const { servers } = await listServers();
		expect(servers).toHaveLength(2);
		const cfg = await loadConfig();
		expect(cfg).toMatchObject({ host: "a.ts.net", password: "new", label: "Studio" });
	});

	// A pasted "http://host/" and a bare "host" are the same node, so the dedupe
	// has to compare cleaned hosts rather than raw input.
	it("treats a pasted scheme as the same node", async () => {
		await saveConfig(node("a.ts.net", "pw"));
		await saveConfig(node("http://a.ts.net/", "pw"));
		expect((await listServers()).servers).toHaveLength(1);
	});

	it("labels an unnamed node by its host and keeps the password out of AsyncStorage", async () => {
		await saveConfig(node("a.ts.net", "secret-pw"));
		const { servers } = await listServers();
		expect(servers[0].label).toBe("a.ts.net");
		expect(plaintext()).not.toContain("secret-pw");
		expect(secure.get(`ao.serverPassword.${servers[0].id}`)).toBe("secret-pw");
	});
});

describe("switchServer", () => {
	it("makes the chosen node the one loadConfig returns", async () => {
		await saveConfig(node("a.ts.net", "pw-a"));
		await saveConfig(node("b.ts.net", "pw-b"));
		const a = (await listServers()).servers[0];

		await switchServer(a.id);

		expect(await loadConfig()).toMatchObject({ host: "a.ts.net", password: "pw-a" });
		expect((await listServers()).activeId).toBe(a.id);
	});

	it("ignores an unknown id", async () => {
		await saveConfig(node("a.ts.net", "pw-a"));
		await switchServer("nope");
		expect((await loadConfig()).host).toBe("a.ts.net");
	});
});

describe("removeServer / clearConfig", () => {
	it("promotes the next node when the active one is removed", async () => {
		await saveConfig(node("a.ts.net", "pw-a"));
		await saveConfig(node("b.ts.net", "pw-b")); // active

		await clearConfig();

		const { servers, activeId } = await listServers();
		expect(servers.map((s) => s.host)).toEqual(["a.ts.net"]);
		expect(activeId).toBe(servers[0].id);
		expect((await loadConfig()).password).toBe("pw-a");
	});

	// Leaving the password in the keystore would silently resurrect it on a later
	// re-pair to the same host.
	it("deletes the removed node's password", async () => {
		await saveConfig(node("a.ts.net", "pw-a"));
		const { servers } = await listServers();
		await removeServer(servers[0].id);
		expect(secure.size).toBe(0);
		expect(await loadConfig()).toMatchObject({ host: "", password: "" });
	});

	it("leaves the active node alone when a different one is removed", async () => {
		await saveConfig(node("a.ts.net", "pw-a"));
		await saveConfig(node("b.ts.net", "pw-b")); // active
		const a = (await listServers()).servers[0];

		await removeServer(a.id);

		expect((await loadConfig()).host).toBe("b.ts.net");
	});

	it("is a no-op with nothing paired", async () => {
		await clearConfig();
		expect(await listServers()).toMatchObject({ servers: [], activeId: null });
	});
});

describe("renameServer", () => {
	it("renames a node, falling back to the host when the name is blank", async () => {
		await saveConfig(node("a.ts.net", "pw-a"));
		const id = (await listServers()).servers[0].id;

		await renameServer(id, "  Studio  ");
		expect((await listServers()).servers[0].label).toBe("Studio");

		await renameServer(id, "   ");
		expect((await listServers()).servers[0].label).toBe("a.ts.net");
	});
});
