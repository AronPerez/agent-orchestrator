import { beforeEach, describe, expect, it, vi } from "vitest";

const getMock = vi.hoisted(() => vi.fn());
const clientForMock = vi.hoisted(() => vi.fn());
const globalGetMock = vi.hoisted(() => vi.fn());

vi.mock("../lib/host-clients", () => ({
	clientFor: (host: string) => {
		clientForMock(host);
		return { GET: (...args: unknown[]) => getMock(...args) };
	},
}));

// Regressing this hook to the global apiClient would silently bill every host's
// usage against the local daemon, so the global client is stubbed too and the
// test asserts it stays untouched.
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: (...args: unknown[]) => globalGetMock(...args) },
}));

import { fetchSessionUsage, sessionUsageDetailQueryKey } from "./useSessionUsage";

const REMOTE = { host: "https://box.lan:3001", id: "s1" };

describe("session usage detail", () => {
	beforeEach(() => {
		getMock.mockReset().mockResolvedValue({ data: {} });
		clientForMock.mockReset();
		globalGetMock.mockReset().mockResolvedValue({ data: {} });
	});

	it("queries the session's own host, never the global client", async () => {
		await fetchSessionUsage(REMOTE);

		expect(clientForMock).toHaveBeenCalledWith(REMOTE.host);
		expect(getMock).toHaveBeenCalledWith("/api/v1/usage/sessions/{sessionId}", {
			params: { path: { sessionId: REMOTE.id } },
		});
		expect(globalGetMock).not.toHaveBeenCalled();
	});

	it("keys the cache by ref, so same-id sessions on two hosts do not collide", () => {
		expect(sessionUsageDetailQueryKey({ host: "local", id: "s1" })).not.toEqual(
			sessionUsageDetailQueryKey(REMOTE),
		);
	});
});
