import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const {
  baseUrlForMock,
  getApiBaseUrlMock,
  hasTrustedApiBaseUrlMock,
  subscribeApiBaseUrlMock,
  unsubscribeBaseUrlMock,
} = vi.hoisted(() => ({
  baseUrlForMock: vi.fn(),
  getApiBaseUrlMock: vi.fn(() => "http://127.0.0.1:3001"),
  hasTrustedApiBaseUrlMock: vi.fn(() => true),
  subscribeApiBaseUrlMock: vi.fn(),
  unsubscribeBaseUrlMock: vi.fn(),
}));

vi.mock("./api-client", () => ({
  getApiBaseUrl: getApiBaseUrlMock,
  hasTrustedApiBaseUrl: hasTrustedApiBaseUrlMock,
  subscribeApiBaseUrl: subscribeApiBaseUrlMock,
}));
vi.mock("./host-clients", () => ({ baseUrlFor: baseUrlForMock }));

import {
  getWorkspaceFileConnectionState,
  subscribeWorkspaceFileChanges,
} from "./workspace-file-events";

let baseUrlListener: (() => void) | undefined;

class EventSourceStub {
  static instances: EventSourceStub[] = [];
  static throwNext = false;
  url: string;
  closed = false;
  readyState = 0;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  listeners = new Map<string, Set<() => void>>();

  constructor(url: string) {
    if (EventSourceStub.throwNext) {
      EventSourceStub.throwNext = false;
      throw new Error("connection setup failed");
    }
    this.url = url;
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

function fakeQueryClient() {
  return { invalidateQueries: vi.fn() } as unknown as Parameters<
    typeof subscribeWorkspaceFileChanges
  >[1];
}

beforeEach(() => {
  EventSourceStub.instances = [];
  EventSourceStub.throwNext = false;
  baseUrlListener = undefined;
  getApiBaseUrlMock.mockReset().mockReturnValue("http://127.0.0.1:3001");
  baseUrlForMock
    .mockReset()
    .mockImplementation((host: string) =>
      host === "local" ? getApiBaseUrlMock() : host,
    );
  hasTrustedApiBaseUrlMock.mockReset().mockReturnValue(true);
  subscribeApiBaseUrlMock
    .mockReset()
    .mockImplementation((listener: () => void) => {
      baseUrlListener = listener;
      return unsubscribeBaseUrlMock;
    });
  unsubscribeBaseUrlMock.mockReset();
  (globalThis as unknown as { EventSource: unknown }).EventSource =
    EventSourceStub;
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  delete (globalThis as unknown as { EventSource?: unknown }).EventSource;
});

describe("subscribeWorkspaceFileChanges", () => {
  it("shares one daemon stream until the final Files view unmounts", () => {
    getApiBaseUrlMock.mockReturnValue("http://127.0.0.1:62220/proxy-token/");
    const queryClient = fakeQueryClient();
    const unsubscribeRail = subscribeWorkspaceFileChanges(
      { host: "local", id: "session/a" },
      queryClient,
    );
    const unsubscribeMaximized = subscribeWorkspaceFileChanges(
      { host: "local", id: "session/a" },
      queryClient,
    );

    expect(EventSourceStub.instances).toHaveLength(1);
    expect(EventSourceStub.instances[0].url).toBe(
      "http://127.0.0.1:62220/proxy-token/api/v1/sessions/session%2Fa/workspace/events",
    );

    unsubscribeRail();
    expect(EventSourceStub.instances[0].closed).toBe(false);
    unsubscribeMaximized();
    expect(EventSourceStub.instances[0].closed).toBe(true);
    expect(unsubscribeBaseUrlMock).toHaveBeenCalledTimes(1);
  });

  it("coalesces filesystem events and invalidates the list plus visible details", () => {
    vi.useFakeTimers();
    const queryClient = fakeQueryClient();
    const unsubscribe = subscribeWorkspaceFileChanges(
      { host: "local", id: "sess-1" },
      queryClient,
    );
    const source = EventSourceStub.instances[0];

    source.dispatch("workspace_changed");
    source.dispatch("workspace_changed");
    vi.advanceTimersByTime(149);
    expect(queryClient.invalidateQueries).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);

    expect(queryClient.invalidateQueries).toHaveBeenCalledTimes(3);
    expect(queryClient.invalidateQueries).toHaveBeenCalledWith({
      queryKey: ["session-workspace-files", "local:sess-1"],
    });
    expect(queryClient.invalidateQueries).toHaveBeenCalledWith({
      queryKey: ["session-workspace-file", "local:sess-1"],
    });
    expect(queryClient.invalidateQueries).toHaveBeenCalledWith({
      queryKey: ["session-workspace-tree", "local:sess-1"],
    });
    unsubscribe();
  });

  it("isolates invalidation for equal session ids on different hosts", () => {
    vi.useFakeTimers();
    const firstClient = fakeQueryClient();
    const secondClient = fakeQueryClient();
    const unsubscribeFirst = subscribeWorkspaceFileChanges(
      { host: "http://node-a", id: "same" },
      firstClient,
    );
    const unsubscribeSecond = subscribeWorkspaceFileChanges(
      { host: "http://node-b", id: "same" },
      secondClient,
    );

    EventSourceStub.instances[0].dispatch("workspace_changed");
    vi.advanceTimersByTime(150);

    expect(firstClient.invalidateQueries).toHaveBeenCalledWith({
      queryKey: ["session-workspace-files", "http%3A%2F%2Fnode-a:same"],
    });
    expect(secondClient.invalidateQueries).not.toHaveBeenCalled();
    unsubscribeFirst();
    unsubscribeSecond();
  });

  it("keeps one retry pending when another connect trigger arrives", () => {
    vi.useFakeTimers();
    vi.spyOn(Math, "random").mockReturnValue(0.5);
    EventSourceStub.throwNext = true;
    const unsubscribe = subscribeWorkspaceFileChanges(
      { host: "local", id: "sess-retry" },
      fakeQueryClient(),
    );

    expect(EventSourceStub.instances).toHaveLength(0);
    baseUrlListener?.();
    expect(EventSourceStub.instances).toHaveLength(0);

    vi.advanceTimersByTime(4_999);
    expect(EventSourceStub.instances).toHaveLength(0);
    vi.advanceTimersByTime(1);
    expect(EventSourceStub.instances).toHaveLength(1);
    unsubscribe();
  });

  it("reports degraded after three completed connection failures", () => {
    vi.useFakeTimers();
    vi.spyOn(Math, "random").mockReturnValue(0.5);
    const session = { host: "local", id: "sess-degraded" };
    const unsubscribe = subscribeWorkspaceFileChanges(
      session,
      fakeQueryClient(),
    );

    for (let failure = 0; failure < 3; failure += 1) {
      const source = EventSourceStub.instances.at(-1)!;
      source.readyState = 2;
      source.onerror?.();
      if (failure < 2) vi.advanceTimersByTime(5_000);
    }

    expect(getWorkspaceFileConnectionState(session)).toBe("degraded");
    unsubscribe();
  });

  it("degrades after repeated native reconnect failures and recovers on open", () => {
    const session = { host: "local", id: "sess-native-retry" };
    const unsubscribe = subscribeWorkspaceFileChanges(
      session,
      fakeQueryClient(),
    );
    const source = EventSourceStub.instances[0];

    source.onopen?.();
    expect(getWorkspaceFileConnectionState(session)).toBe("connected");

    source.readyState = 0;
    for (let failure = 0; failure < 3; failure += 1) {
      source.onerror?.();
      expect(getWorkspaceFileConnectionState(session)).toBe(
        failure < 2 ? "connecting" : "degraded",
      );
    }

    source.onopen?.();
    expect(getWorkspaceFileConnectionState(session)).toBe("connected");
    unsubscribe();
  });

  it("uses degraded polling when EventSource is unavailable", () => {
    delete (globalThis as unknown as { EventSource?: unknown }).EventSource;

    const session = { host: "local", id: "sess-no-eventsource" };
    const unsubscribe = subscribeWorkspaceFileChanges(
      session,
      fakeQueryClient(),
    );

    expect(getWorkspaceFileConnectionState(session)).toBe("degraded");
    unsubscribe();
  });
});
