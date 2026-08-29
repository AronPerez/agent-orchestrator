import { beforeEach, describe, expect, it, vi } from "vitest";

const { apiGetMock, clientForMock, hostGetMock } = vi.hoisted(() => ({
  apiGetMock: vi.fn(),
  clientForMock: vi.fn(),
  hostGetMock: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
  apiClient: { GET: apiGetMock },
  apiErrorMessage: (error: unknown, fallback: string) =>
    error instanceof Error ? error.message : fallback,
}));
vi.mock("../lib/host-clients", () => ({
  clientFor: clientForMock,
}));

import {
  sessionWorkspaceFileQueryKey,
  sessionWorkspaceFileQueryOptions,
  sessionWorkspaceFilesQueryKey,
  workspaceFilesRefetchInterval,
} from "./useSessionWorkspaceFiles";

beforeEach(() => {
  apiGetMock.mockReset();
  hostGetMock
    .mockReset()
    .mockResolvedValue({ data: { sessionId: "s1", path: "src/a.ts" } });
  clientForMock.mockReset().mockImplementation((host: string) => ({
    GET: (url: string, options: unknown) => hostGetMock(host, url, options),
  }));
});

describe("workspace file query identity", () => {
  it("keys summary and detail queries by the complete session Ref", () => {
    expect(
      sessionWorkspaceFilesQueryKey({ host: "http://node-a", id: "s1" }),
    ).toEqual(["session-workspace-files", "http%3A%2F%2Fnode-a:s1"]);
    expect(
      sessionWorkspaceFileQueryKey(
        { host: "http://node-b", id: "s1" },
        "src/a.ts",
      ),
    ).toEqual(["session-workspace-file", "http%3A%2F%2Fnode-b:s1", "src/a.ts"]);
  });

  it("reads detail files through the session host client", async () => {
    const session = { host: "http://node-b", id: "s1" };
    await sessionWorkspaceFileQueryOptions(session, "src/a.ts").queryFn();

    expect(clientForMock).toHaveBeenCalledWith("http://node-b");
    expect(hostGetMock).toHaveBeenCalledWith(
      "http://node-b",
      "/api/v1/sessions/{sessionId}/workspace/file",
      { params: { path: { sessionId: "s1" }, query: { path: "src/a.ts" } } },
    );
    expect(apiGetMock).not.toHaveBeenCalled();
  });
});

describe("workspaceFilesRefetchInterval", () => {
  it("polls only while workspace SSE is degraded", () => {
    expect(workspaceFilesRefetchInterval("connecting")).toBe(false);
    expect(workspaceFilesRefetchInterval("connected")).toBe(false);
    expect(workspaceFilesRefetchInterval("degraded")).toBe(30_000);
  });
});
