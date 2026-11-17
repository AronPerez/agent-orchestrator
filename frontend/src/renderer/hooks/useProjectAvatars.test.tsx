import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { onlineManager, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const { localGet, remoteConnect } = vi.hoisted(() => ({ localGet: vi.fn(), remoteConnect: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: localGet }, reportingFetch: (input: Request) => fetch(input), apiErrorMessage: () => "unavailable" }));
vi.mock("../lib/bridge", () => ({ aoBridge: { remotes: { connect: remoteConnect, disconnect: vi.fn() } } }));

import { connectHost, connectedHosts, disconnectHost } from "../lib/host-clients";
import { sessionUiKey } from "../lib/hosts";
import { forgetRemoteHost, syncRemoteCache } from "../lib/remote-chat-cache";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSummary } from "../types/workspace";
import { useProjectAvatars } from "./useProjectAvatars";

const avatar = (owner: string) => `https://avatars.githubusercontent.com/${owner}?s=64`;
const imageData = (url: string) => `data:image/png;base64,${btoa(url)}`;
const project = (hostId?: string): WorkspaceSummary => ({ id: "same", name: "Project", path: "/repo", sessions: [], hostId });
const clients: QueryClient[] = [];
let imageFails = false;
let corsBlocked = false;
const imageLoads: Array<{ url: string; crossOrigin: string }> = [];
let finishImage: (() => void) | undefined;

function setup(workspaces: WorkspaceSummary[]) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false, networkMode: "always" } } });
	clients.push(client);
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
	return { client, ...renderHook(() => useProjectAvatars(workspaces), { wrapper }) };
}

beforeEach(() => {
	window.localStorage.clear();
	useUiStore.setState({ projectAvatars: true, remoteHosts: true });
	imageFails = false;
	corsBlocked = false;
	imageLoads.length = 0;
	finishImage = undefined;
	localGet.mockReset().mockResolvedValue({ data: { status: "ok", project: { repo: "https://github.com/local/repo" } } });
	remoteConnect.mockImplementation(async (url: string) => {
		const hostId = new URL(url).hostname;
		return { hostId, label: hostId, url, base: `http://127.0.0.1:4000/${hostId}` };
	});
	vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
		const owner = new URL(request.url).pathname.split("/")[1];
		return Response.json({ status: "ok", project: { repo: `https://github.com/${owner}/repo` } });
	}));
	vi.stubGlobal("Image", class {
		onload: (() => void) | null = null;
		onerror: (() => void) | null = null;
		crossOrigin = "";
		url = "";
		set src(url: string) {
			this.url = url;
			if (!url) return;
			imageLoads.push({ url, crossOrigin: this.crossOrigin });
			queueMicrotask(() => imageFails || (corsBlocked && this.crossOrigin === "anonymous") ? this.onerror?.() : this.onload?.());
		}
	});
	let drawn = "";
	vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({ drawImage: (image: { url: string }) => { drawn = image.url; } } as unknown as CanvasRenderingContext2D);
	vi.spyOn(HTMLCanvasElement.prototype, "toDataURL").mockImplementation(() => imageData(drawn));
});

afterEach(async () => {
	cleanup();
	for (const client of clients.splice(0)) client.clear();
	onlineManager.setOnline(true);
	for (const hostId of connectedHosts()) await disconnectHost(hostId);
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

it("keeps equal project IDs on local, host A and host B separate through their owning clients", async () => {
	await connectHost("http://box-a:3001");
	await connectHost("http://box-b:3001");
	const { result } = setup([project(), project("box-a"), project("box-b")]);
	await waitFor(() => {
		expect(result.current.get("same")).toBe(imageData(avatar("local")));
		expect(result.current.get(sessionUiKey("same", "box-a"))).toBe(imageData(avatar("box-a")));
		expect(result.current.get(sessionUiKey("same", "box-b"))).toBe(imageData(avatar("box-b")));
	});
	expect(localGet).toHaveBeenCalledTimes(1);
	expect(vi.mocked(fetch).mock.calls.map(([request]) => (request as Request).url).sort()).toEqual([
		"http://127.0.0.1:4000/box-a/api/v1/projects/same",
		"http://127.0.0.1:4000/box-b/api/v1/projects/same",
	]);
	// Remote pixels and owner URLs must never enter the local first-paint cache.
	expect(window.localStorage.getItem("ao.projectAvatarCache")).not.toContain("box-a");
	expect(window.localStorage.getItem("ao.projectAvatarUrls")).toBe(JSON.stringify({ same: avatar("local") }));
});

it("uses the owning host's canonical avatar after a repository transfer", async () => {
	await connectHost("http://box-a:3001");
	vi.mocked(fetch).mockResolvedValue(Response.json({ status: "ok", project: {
		repo: "git@github.com:old/repo.git", canonicalRepo: "current/repo", canonicalOwnerAvatarUrl: "https://avatars.githubusercontent.com/u/42?v=4",
	} }));
	const { result } = setup([project("box-a")]);
	await waitFor(() => expect(result.current.get(sessionUiKey("same", "box-a"))).toBe(imageData("https://avatars.githubusercontent.com/u/42?v=4&s=64")));
	expect(localGet).not.toHaveBeenCalled();
});

it.each([404, 426, 502])("falls back for a remote project returning HTTP %i without reading the local daemon", async (status) => {
	await connectHost("http://box-a:3001");
	vi.mocked(fetch).mockResolvedValue(Response.json({ error: "unavailable" }, { status }));
	const { client, result } = setup([project("box-a")]);
	await waitFor(() => expect(vi.mocked(fetch)).toHaveBeenCalledOnce());
	await waitFor(() => expect(client.isFetching()).toBe(0));
	expect(result.current.size).toBe(0);
	expect(localGet).not.toHaveBeenCalled();
});

it("does not fetch an offline host and loads its avatar after reconnecting", async () => {
	const { result } = setup([project("box-a")]);
	expect(result.current.size).toBe(0);
	expect(fetch).not.toHaveBeenCalled();
	expect(localGet).not.toHaveBeenCalled();
	await act(async () => { await connectHost("http://box-a:3001"); });
	await waitFor(() => expect(result.current.get(sessionUiKey("same", "box-a"))).toBe(imageData(avatar("box-a"))));
});

it("falls back when a remote avatar image cannot be loaded", async () => {
	await connectHost("http://box-a:3001");
	imageFails = true;
	const { client, result } = setup([project("box-a")]);
	await waitFor(() => expect(fetch).toHaveBeenCalledOnce());
	await waitFor(() => expect(client.isFetching()).toBe(0));
	expect(result.current.size).toBe(0);
});

it("cancels a removed account host's pending image without retaining its pixels", async () => {
	await connectHost("http://box-a:3001");
	vi.stubGlobal("Image", class {
		onload: (() => void) | null = null;
		onerror: (() => void) | null = null;
		set src(_url: string) { finishImage = () => this.onload?.(); }
	});
	const { client, result } = setup([project("box-a")]);
	await waitFor(() => expect(finishImage).toBeDefined());
	await act(async () => {
		await disconnectHost("box-a");
		await syncRemoteCache(client, []);
		finishImage?.();
	});
	expect(result.current.size).toBe(0);
	expect(client.getQueryCache().findAll({ queryKey: ["project-avatar", "box-a"] })).toHaveLength(0);
	expect(client.getQueryData(["project", "box-a", "same"])).toBeUndefined();
	await forgetRemoteHost(client, "box-a");
});

it("keeps the local cached first paint and respects the avatar setting", () => {
	window.localStorage.setItem("ao.projectAvatarUrls", JSON.stringify({ same: avatar("local") }));
	window.localStorage.setItem("ao.projectAvatarCache", JSON.stringify({ [avatar("local")]: imageData(avatar("local")) }));
	const { result, rerender } = setup([project()]);
	expect(result.current.get("same")).toBe(imageData(avatar("local")));
	act(() => useUiStore.setState({ projectAvatars: false }));
	rerender();
	expect(result.current.size).toBe(0);
});

it("still saves local first-paint URLs while an unrelated remote host is offline", async () => {
	setup([project(), project("offline")]);
	await waitFor(() => expect(window.localStorage.getItem("ao.projectAvatarUrls")).toBe(JSON.stringify({ same: avatar("local") })));
});

it("uses child repositories from remote workspace details when the summary has none", async () => {
	await connectHost("http://box-a:3001");
	vi.mocked(fetch).mockResolvedValue(Response.json({ status: "ok", project: {
		kind: "workspace", workspaceRepos: [{ repo: "https://github.com/workspace-owner/child" }],
	} }));
	const { result } = setup([{ ...project("box-a"), kind: "workspace" }]);
	await waitFor(() => expect(result.current.get(sessionUiKey("same", "box-a"))).toBe(imageData(avatar("workspace-owner"))));
	expect(localGet).not.toHaveBeenCalled();
});

it("displays an avatar that loads normally but cannot be loaded anonymously for a canvas", async () => {
	await connectHost("http://box-a:3001");
	corsBlocked = true;
	const { result } = setup([project("box-a")]);
	await waitFor(() => expect(result.current.get(sessionUiKey("same", "box-a"))).toBe(avatar("box-a")));
	expect(imageLoads).toEqual([
		{ url: avatar("box-a"), crossOrigin: "anonymous" },
		{ url: avatar("box-a"), crossOrigin: "" },
	]);
});

it.each(["network", "host", "setting"])("retries a failed image after %s recovery instead of retaining a successful null", async (recovery) => {
	await connectHost("http://box-a:3001");
	imageFails = true;
	const { client, result } = setup([project("box-a")]);
	const key = ["project-avatar", "box-a", "same", avatar("box-a")];
	await waitFor(() => expect(client.getQueryState(key)?.fetchStatus).toBe("idle"));
	expect(client.getQueryState(key)?.status).toBe("error");
	expect(result.current.size).toBe(0);
	imageFails = false;
	if (recovery === "network") {
		act(() => onlineManager.setOnline(false));
		act(() => onlineManager.setOnline(true));
	} else if (recovery === "host") {
		await act(async () => { await disconnectHost("box-a"); });
		await act(async () => { await connectHost("http://box-a:3001"); });
	} else {
		act(() => useUiStore.setState({ projectAvatars: false }));
		act(() => useUiStore.setState({ projectAvatars: true }));
	}
	await waitFor(() => expect(result.current.get(sessionUiKey("same", "box-a"))).toBe(imageData(avatar("box-a"))));
});

it("does not reload every avatar's metadata when session polling invalidates a host's project queries", async () => {
	await connectHost("http://box-a:3001");
	const { client, result } = setup(["one", "two", "three"].map((id) => ({ ...project("box-a"), id })));
	await waitFor(() => expect(result.current.size).toBe(3));
	expect(fetch).toHaveBeenCalledTimes(3);
	await act(async () => {
		await client.invalidateQueries({ queryKey: ["project", "box-a"] });
		await client.invalidateQueries({ queryKey: ["project", "box-a"] });
	});
	expect(fetch).toHaveBeenCalledTimes(3);
});
