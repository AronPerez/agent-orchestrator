import { beforeEach, describe, expect, it, vi } from "vitest";
import { reportHostConnect, reportHostQueryFailed, reportHostStreamState } from "./host-telemetry";
import { captureRendererEvent } from "./telemetry";

vi.mock("./telemetry", () => ({ captureRendererEvent: vi.fn() }));

const capture = vi.mocked(captureRendererEvent);
const REMOTE = "http://192.0.2.1:3011";

beforeEach(() => {
	capture.mockClear();
});

describe("host telemetry", () => {
	it("tags the local daemon and a remote host apart", () => {
		reportHostConnect("local", "probe", "online", 12);
		reportHostStreamState(REMOTE, "connected", 0);

		expect(capture).toHaveBeenNthCalledWith(1, "ao.renderer.host_connect", expect.objectContaining({ host_kind: "local" }));
		expect(capture).toHaveBeenNthCalledWith(
			2,
			"ao.renderer.host_stream_state",
			expect.objectContaining({ host_kind: "remote", state: "connected", reconnect_count: 0 }),
		);
	});

	it("rounds the probe duration and carries the health verbatim", () => {
		reportHostConnect(REMOTE, "add", "not-a-daemon", 411.7);

		expect(capture).toHaveBeenCalledWith(
			"ao.renderer.host_connect",
			expect.objectContaining({ source: "add", result: "not-a-daemon", duration_ms: 412 }),
		);
	});

	// A saved host that is simply switched off fails its refetch every 15s
	// forever. The per-name daily ceiling is shared, so without this one dead
	// host spends the whole budget and the next host to break reports nothing.
	it("collapses a repeat failure of the same host and status inside the window", () => {
		reportHostQueryFailed(REMOTE, 502, 1_000);
		reportHostQueryFailed(REMOTE, 502, 60_000);

		expect(capture).toHaveBeenCalledTimes(1);
	});

	it("still reports a different status, a different host, and the same failure later", () => {
		reportHostQueryFailed(REMOTE, 401, 1_000);
		reportHostQueryFailed(REMOTE, 503, 1_000);
		reportHostQueryFailed("local", 401, 1_000);
		reportHostQueryFailed(REMOTE, 401, 1_000 + 6 * 60_000);

		expect(capture).toHaveBeenCalledTimes(4);
	});
});
