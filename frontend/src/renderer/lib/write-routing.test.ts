import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { createElement, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useTerminateSession } from "../hooks/useTerminateSession";
import { setApiBaseUrl } from "./api-client";
import { forgetHost, registerHostBase } from "./host-clients";

vi.mock("./telemetry", () => ({
	captureRendererEvent: vi.fn().mockResolvedValue(undefined),
}));

const REMOTE = "http://192.0.2.1:3011";
const servers: Server[] = [];

async function recordingDaemon() {
	const requests: string[] = [];
	const server = createServer((request, response) => {
		requests.push(request.url ?? "");
		response.writeHead(200, { "content-type": "application/json" });
		response.end("{}");
	});
	servers.push(server);
	await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
	return {
		base: `http://127.0.0.1:${(server.address() as AddressInfo).port}`,
		requests,
	};
}

function wrapper({ children }: { children: ReactNode }) {
	return createElement(QueryClientProvider, { client: new QueryClient() }, children);
}

beforeEach(() => {
	forgetHost(REMOTE);
});

afterEach(async () => {
	await Promise.all(servers.splice(0).map((server) => new Promise<void>((resolve) => server.close(() => resolve()))));
});

describe("write routing", () => {
	it("sends a remote kill only to that host's proxy", async () => {
		const local = await recordingDaemon();
		const remote = await recordingDaemon();
		setApiBaseUrl(local.base);
		registerHostBase(REMOTE, `${remote.base}/proxy-token`);
		const { result } = renderHook(() => useTerminateSession(), { wrapper });

		await act(async () => {
			await result.current.mutateAsync({ host: REMOTE, id: "same-id" });
		});

		expect(remote.requests).toEqual(["/proxy-token/api/v1/sessions/same-id/kill"]);
		// Output cannot prove the safety property: only an empty local request log
		// proves the same-named local session was never touched.
		expect(local.requests).toEqual([]);
	});

	it("finishes a successful kill without waiting for the workspace refetch", async () => {
		const remote = await recordingDaemon();
		registerHostBase(REMOTE, `${remote.base}/proxy-token`);
		const queryClient = new QueryClient();
		let finishRefetch = () => {};
		const refetch = new Promise<void>((resolve) => {
			finishRefetch = resolve;
		});
		const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockReturnValue(refetch);
		const { result } = renderHook(() => useTerminateSession(), {
			wrapper: ({ children }) => createElement(QueryClientProvider, { client: queryClient }, children),
		});

		act(() => result.current.mutate({ host: REMOTE, id: "same-id" }));

		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(invalidate).toHaveBeenCalledWith(
			{ queryKey: ["workspaces"] },
			{ cancelRefetch: false },
		);
		finishRefetch();
	});

	it("refuses a write to a host that is not connected", async () => {
		const local = await recordingDaemon();
		setApiBaseUrl(local.base);
		const { result } = renderHook(() => useTerminateSession(), { wrapper });

		await expect(
			act(async () => {
				await result.current.mutateAsync({ host: REMOTE, id: "same-id" });
			}),
		).rejects.toThrow(/not connected/);
		expect(local.requests).toEqual([]);
	});
});
