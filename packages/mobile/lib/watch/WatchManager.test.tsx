import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { HostSnapshot } from "../otherHosts";

const state = vi.hoisted(() => ({
	appState: "active", syncAt: 1_800_000_000_000, hosts: [] as HostSnapshot[],
	listeners: new Set<(state: string) => void>(),
	publish: vi.fn(async (_json: string) => {}), ready: vi.fn(async () => {}),
}));
vi.mock("react-native", () => ({ AppState: {
	get currentState() { return state.appState; },
	addEventListener: (_: string, listener: (state: string) => void) => {
		state.listeners.add(listener); return { remove: () => state.listeners.delete(listener) };
	},
} }));
vi.mock("expo-crypto", () => ({ randomUUID: () => "token" }));
vi.mock("../chat/api", () => ({ getConversationPage: vi.fn(), resolveApproval: vi.fn(), resolveInput: vi.fn(), sendConversationMessage: vi.fn() }));
vi.mock("../store", () => ({ useApp: () => ({
	configResolved: true, hostStates: state.hosts, currentHostId: "alpha",
	getLastSyncAt: () => state.syncAt, configForHost: () => null, refreshAll: vi.fn(),
}) }));
vi.mock("./bridge", () => ({ watchBridge: {
	publishSnapshot: state.publish, setReady: state.ready, isRequestActive: async () => true,
	addListener: () => ({ remove: () => {} }),
} }));

import { WatchManager } from "./WatchManager";
import type { WatchSnapshot } from "./snapshot";

let renderer: ReactTestRenderer | undefined;
const lastSnapshot = (): WatchSnapshot => JSON.parse(state.publish.mock.calls.at(-1)![0]);
beforeEach(() => {
	vi.useFakeTimers(); vi.setSystemTime(1_800_000_000_000);
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.clearAllMocks(); state.listeners.clear(); state.appState = "active"; state.syncAt = Date.now();
	state.hosts = ["alpha", "beta"].map<HostSnapshot>((hostId) => ({
		hostId, name: hostId, config: { hostId, host: hostId, httpPort: "3011", muxPort: "3011", password: "secret" }, connection: "open", error: null,
		lastSyncAt: Date.now() - 60_000, sessions: [],
		appearance: { symbol: "server" }, endpoints: [], loading: false, errorStatus: null,
		projects: [], projectsKnown: true, orchestrators: [], orchestratorId: null, stats: {}, notificationsUnread: 0,
	}));
});
afterEach(async () => {
	await act(async () => { renderer?.unmount(); }); renderer = undefined;
	vi.useRealTimers(); vi.unstubAllGlobals();
});

it("publishes all hosts using the primary poll clock even when its rows are memoized", async () => {
	await act(async () => { renderer = create(<WatchManager />); });
	expect(lastSnapshot().hosts.map((host) => [host.hostId, host.capturedAt])).toEqual([
		["alpha", state.syncAt], ["beta", state.hosts[1].lastSyncAt],
	]);
	state.syncAt += 20_000;
	await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
	expect(lastSnapshot().hosts[0].capturedAt).toBe(state.syncAt);
	expect(lastSnapshot().hosts[1].capturedAt).toBe(state.hosts[1].lastSyncAt);
	await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
	expect(lastSnapshot().hosts[0].capturedAt).toBe(state.syncAt);
});

it("does not publish in the background and removes listeners and timers on unmount", async () => {
	await act(async () => { renderer = create(<WatchManager />); });
	state.appState = "background";
	await act(async () => { for (const listener of state.listeners) listener("background"); });
	await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
	expect(state.publish).toHaveBeenCalledTimes(1);
	expect(state.ready).toHaveBeenLastCalledWith(false);
	await act(async () => { renderer!.unmount(); }); renderer = undefined;
	expect(state.listeners.size).toBe(0);
	expect(vi.getTimerCount()).toBe(0);
});
