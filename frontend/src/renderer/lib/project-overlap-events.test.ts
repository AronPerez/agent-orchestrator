import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const {
	baseUrlForHostMock,
	eventStreamBaseUrlMock,
	getApiBaseUrlMock,
	hasTrustedApiBaseUrlMock,
	probeRemoteSseMock,
	stopRemoteMock,
	subscribeApiBaseUrlMock,
	subscribeConnectedHostsMock,
} = vi.hoisted(() => ({
	baseUrlForHostMock: vi.fn(),
	eventStreamBaseUrlMock: vi.fn((baseUrl: string) => baseUrl.replace(/\/+$/, "")),
	getApiBaseUrlMock: vi.fn(() => "http://127.0.0.1:3001"),
	hasTrustedApiBaseUrlMock: vi.fn(() => true),
	probeRemoteSseMock: vi.fn(),
	stopRemoteMock: vi.fn(),
	subscribeApiBaseUrlMock: vi.fn(() => () => undefined),
	subscribeConnectedHostsMock: vi.fn(() => () => undefined),
}));

vi.mock("./api-client", () => ({
	eventStreamBaseUrl: eventStreamBaseUrlMock,
	getApiBaseUrl: getApiBaseUrlMock,
	hasTrustedApiBaseUrl: hasTrustedApiBaseUrlMock,
	subscribeApiBaseUrl: subscribeApiBaseUrlMock,
}));
vi.mock("./host-clients", () => ({
	baseUrlForHost: baseUrlForHostMock,
	subscribeConnectedHosts: subscribeConnectedHostsMock,
}));
vi.mock("./remote-sse-probe", () => ({ probeRemoteSse: probeRemoteSseMock }));
vi.mock("./sse-backoff", () => ({ computeSseRetryDelayMs: () => 5_000 }));

import { REMOTE_PROJECT_OVERLAP_POLL_MS, subscribeProjectOverlapChanges } from "./project-overlap-events";

class EventSourceStub {
	static instances: EventSourceStub[] = [];
	closed = false;
	readyState = 0;
	onerror: (() => void) | null = null;
	listeners = new Map<string, Set<() => void>>();

	constructor(readonly url: string) {
		EventSourceStub.instances.push(this);
	}
	addEventListener(type: string, listener: () => void) {
		const listeners = this.listeners.get(type) ?? new Set();
		listeners.add(listener);
		this.listeners.set(type, listeners);
	}
	dispatch(type: string) {
		for (const listener of this.listeners.get(type) ?? []) listener();
	}
	close() {
		this.closed = true;
		this.readyState = 2;
	}
}

const subscriptions: Array<() => void> = [];
function subscribe(...args: Parameters<typeof subscribeProjectOverlapChanges>) {
	const release = subscribeProjectOverlapChanges(...args);
	subscriptions.push(release);
	return release;
}

function fakeQueryClient() {
	const invalidateQueries = vi.fn(() => Promise.resolve());
	const isFetching = vi.fn(() => 0);
	return {
		client: { invalidateQueries, isFetching } as unknown as Parameters<typeof subscribeProjectOverlapChanges>[2],
		invalidateQueries,
		isFetching,
	};
}

beforeEach(() => {
	vi.useFakeTimers();
	EventSourceStub.instances = [];
	baseUrlForHostMock.mockReset();
	eventStreamBaseUrlMock.mockReset().mockImplementation((baseUrl: string) => baseUrl.replace(/\/+$/, ""));
	getApiBaseUrlMock.mockReset().mockReturnValue("http://127.0.0.1:3001");
	hasTrustedApiBaseUrlMock.mockReset().mockReturnValue(true);
	probeRemoteSseMock.mockReset().mockReturnValue(stopRemoteMock);
	stopRemoteMock.mockReset();
	subscribeApiBaseUrlMock.mockReset().mockReturnValue(() => undefined);
	subscribeConnectedHostsMock.mockReset().mockReturnValue(() => undefined);
	(globalThis as unknown as { EventSource: unknown }).EventSource = EventSourceStub;
});

afterEach(() => {
	for (const release of subscriptions.splice(0)) release();
	vi.useRealTimers();
});

describe("subscribeProjectOverlapChanges", () => {
	it("shares one local stream and invalidates only that project on ready and changes", async () => {
		const { client, invalidateQueries } = fakeQueryClient();
		const first = subscribe("demo", "one,two", client);
		const second = subscribe("demo", "one,two", client);

		expect(EventSourceStub.instances).toHaveLength(1);
		expect(EventSourceStub.instances[0].url).toBe("http://127.0.0.1:3001/api/v1/projects/demo/overlaps/events");
		EventSourceStub.instances[0].dispatch("ready");
		EventSourceStub.instances[0].dispatch("overlaps_changed");
		await vi.advanceTimersByTimeAsync(0);
		expect(invalidateQueries).toHaveBeenCalledTimes(2);
		expect(invalidateQueries).toHaveBeenLastCalledWith(
			{ queryKey: ["project-overlaps", "", "demo"] },
			{ cancelRefetch: false },
		);

		first();
		expect(EventSourceStub.instances[0].closed).toBe(false);
		second();
		expect(EventSourceStub.instances[0].closed).toBe(true);
	});

	it("opens a fresh watcher when the active-session fingerprint changes", () => {
		const { client } = fakeQueryClient();
		const first = subscribe("demo", "one,two", client);
		const second = subscribe("demo", "one,three", client);
		expect(EventSourceStub.instances).toHaveLength(2);
		first();
		second();
	});

	it("uses the authenticated remote stream transport", async () => {
		baseUrlForHostMock.mockReturnValue("https://box.example/api");
		const { client, invalidateQueries } = fakeQueryClient();
		subscribe("demo/id", "one,two", client, "box-a");

		expect(probeRemoteSseMock).toHaveBeenCalledTimes(1);
		const [url, names, onEvent, onVerified] = probeRemoteSseMock.mock.calls[0];
		expect(url).toBe("https://box.example/api/api/v1/projects/demo%2Fid/overlaps/events");
		expect(names).toEqual(["overlaps_changed"]);
		onVerified();
		onEvent();
		await vi.advanceTimersByTimeAsync(0);
		expect(invalidateQueries).toHaveBeenLastCalledWith(
			{ queryKey: ["project-overlaps", "box-a", "demo/id"] },
			{ cancelRefetch: false },
		);
	});

	it("polls while a remote stream probe is unavailable and stops after verification", async () => {
		baseUrlForHostMock.mockReturnValue("https://box.example");
		const callbacks: Array<{ fail: () => void; verify: () => void }> = [];
		probeRemoteSseMock.mockImplementation((_url, _names, _onEvent, onVerified, onFailure) => {
			callbacks.push({ fail: onFailure, verify: onVerified });
			return stopRemoteMock;
		});
		const { client, invalidateQueries } = fakeQueryClient();
		subscribe("demo", "one,two", client, "box-a");

		callbacks[0].fail();
		await vi.advanceTimersByTimeAsync(0);
		expect(invalidateQueries).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(REMOTE_PROJECT_OVERLAP_POLL_MS);
		expect(invalidateQueries.mock.calls.length).toBeGreaterThan(1);

		callbacks.at(-1)!.verify();
		await vi.advanceTimersByTimeAsync(0);
		const verifiedCount = invalidateQueries.mock.calls.length;
		await vi.advanceTimersByTimeAsync(REMOTE_PROJECT_OVERLAP_POLL_MS * 2);
		expect(invalidateQueries).toHaveBeenCalledTimes(verifiedCount);
	});

	it("queues one follow-up refresh when an overlap event arrives during the initial fetch", async () => {
		let finish!: () => void;
		const initial = new Promise<void>((resolve) => { finish = resolve; });
		const { client, invalidateQueries, isFetching } = fakeQueryClient();
		isFetching.mockReturnValueOnce(1);
		invalidateQueries.mockReturnValueOnce(initial);
		subscribe("demo", "one,two", client);

		EventSourceStub.instances[0].dispatch("overlaps_changed");
		expect(invalidateQueries).toHaveBeenCalledTimes(1);
		finish();
		await vi.advanceTimersByTimeAsync(0);
		expect(invalidateQueries).toHaveBeenCalledTimes(2);
	});

	it("retries when the remote transport fails before returning its close handle", () => {
		baseUrlForHostMock.mockReturnValue("https://box.example");
		probeRemoteSseMock.mockImplementation((_url, _names, _onEvent, _onVerified, onFailure) => {
			onFailure();
			return stopRemoteMock;
		});
		const { client } = fakeQueryClient();
		subscribe("demo", "one,two", client, "box-a");
		expect(probeRemoteSseMock).toHaveBeenCalledTimes(1);
		vi.advanceTimersByTime(5_000);
		expect(probeRemoteSseMock).toHaveBeenCalledTimes(2);
	});
});
