import { QueryClient } from "@tanstack/react-query";
import { expect, it, vi } from "vitest";
import { forgetRemoteHost, persistRemoteCache, syncRemoteCache } from "./remote-chat-cache";

function seeded() {
	const client = new QueryClient();
	for (const hostId of ["box-a", "box-b"]) {
		client.setQueryData(["remote-conversation", hostId, "session-1"], { pages: [], pageParams: [] });
		client.setQueryData(["remote-workspaces", hostId, "sessions"], []);
		client.setQueryData(["remote-workspaces", hostId, "projects"], []);
		client.setQueryData(["session-coordination", hostId, "session-1"], { events: [], hasMore: false });
	}
	client.setQueryData(["session-coordination", "", "session-1"], { events: [], hasMore: false });
	client.setQueryData(["remote-conversation-refresh-error", "box-b", "session-1"], true);
	return client;
}

// IndexedDB is absent here; the real app proves the disk path.
it("prunes a removed account host while keeping locally paired and local history", async () => {
	const client = seeded();
	await syncRemoteCache(client, ["box-a"]);
	expect(client.getQueryData(["remote-conversation", "box-a", "session-1"])).toBeDefined();
	expect(client.getQueryData(["remote-workspaces", "box-a", "sessions"])).toBeDefined();
	expect(client.getQueryData(["remote-conversation", "box-b", "session-1"])).toBeUndefined();
	expect(client.getQueryData(["remote-workspaces", "box-b", "projects"])).toBeUndefined();
	expect(client.getQueryData(["session-coordination", "box-b", "session-1"])).toBeUndefined();
	expect(client.getQueryData(["session-coordination", "box-a", "session-1"])).toBeDefined();
	expect(client.getQueryData(["session-coordination", "", "session-1"])).toBeDefined();
	// Only kept queries are the cache's to prune.
	expect(client.getQueryData(["remote-conversation-refresh-error", "box-b", "session-1"])).toBe(true);
});

it("drops everything kept for a host that refused this device", async () => {
	const client = seeded();
	await forgetRemoteHost(client, "box-b");
	expect(client.getQueryData(["remote-conversation", "box-b", "session-1"])).toBeUndefined();
	expect(client.getQueryData(["remote-workspaces", "box-b", "sessions"])).toBeUndefined();
	expect(client.getQueryData(["session-coordination", "box-b", "session-1"])).toBeUndefined();
	expect(client.getQueryData(["session-coordination", "box-a", "session-1"])).toBeDefined();
	expect(client.getQueryData(["remote-conversation", "box-a", "session-1"])).toBeDefined();
});

it("cancels forgotten-host coordination so a late response cannot repopulate it", async () => {
	const client = seeded();
	let finish!: (value: unknown) => void;
	let signal!: AbortSignal;
	const fetch = client.fetchQuery({
		queryKey: ["session-coordination", "box-b", "session-1"],
		queryFn: (context) => {
			signal = context.signal;
			return new Promise((resolve) => { finish = resolve; });
		},
	}).catch(() => undefined);
	await forgetRemoteHost(client, "box-b");
	expect(signal.aborted).toBe(true);
	finish({ events: [{ summary: "Private account history" }], hasMore: false });
	await fetch;
	expect(client.getQueryData(["session-coordination", "box-b", "session-1"])).toBeUndefined();
});

it("does not add coordination history to the persistent remote cache", async () => {
	vi.useFakeTimers();
	const open = vi.fn();
	vi.stubGlobal("indexedDB", { open });
	const client = new QueryClient();
	const stop = persistRemoteCache(client);
	try {
		client.setQueryData(["session-coordination", "box-a", "session-1"], { events: [], hasMore: false });
		await vi.advanceTimersByTimeAsync(1_100);
		expect(open).not.toHaveBeenCalled();
	} finally {
		stop(); client.clear(); vi.unstubAllGlobals(); vi.useRealTimers();
	}
});
