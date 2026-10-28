import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, expect, it, vi } from "vitest";
import { ProjectScriptsSettings } from "./ProjectScriptsSettings";
import { ProjectEnvironmentSettings } from "./ProjectEnvironmentSettings";
import { CuesSettings } from "./CuesDialog";
import { terminateSessionMutationKey } from "../hooks/useTerminateSession";

const mocks = vi.hoisted(() => ({
	connected: [] as string[], listeners: new Set<() => void>(),
	get: vi.fn(), put: vi.fn(), post: vi.fn(), patch: vi.fn(), delete: vi.fn(),
	localGet: vi.fn(), localPut: vi.fn(), localPost: vi.fn(), localPatch: vi.fn(), localDelete: vi.fn(),
}));
vi.mock("../lib/host-clients", () => ({
	connectedHosts: () => mocks.connected,
	subscribeConnectedHosts: (listener: () => void) => {
		mocks.listeners.add(listener);
		return () => mocks.listeners.delete(listener);
	},
	clientForHost: (hostId: string) => {
		if (!mocks.connected.includes(hostId)) throw new Error(`Host ${hostId} is not connected`);
		return Object.fromEntries((["get", "put", "post", "patch", "delete"] as const).map((method) => [
			method.toUpperCase(), (path: string, options: unknown) => mocks[method](hostId, path, options),
		]));
	},
}));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: mocks.localGet, PUT: mocks.localPut, POST: mocks.localPost, PATCH: mocks.localPatch, DELETE: mocks.localDelete },
	apiErrorMessage: (error: { message?: string }) => error.message ?? "Request failed",
}));
vi.mock("../stores/ui-store", () => ({
	useUiStore: (select: (state: unknown) => unknown) => select({ remoteHosts: true, showGlobalToast: vi.fn() }),
}));

const sections = { Scripts: ProjectScriptsSettings, Environment: ProjectEnvironmentSettings, Cues: CuesSettings };
type Section = keyof typeof sections;
const project = (host: string) => ({ id: "shared", name: host, kind: "single_repo", config: {
	postCreate: [`echo ${host}`], preRemove: ["echo cleanup"], env: { TOKEN: host }, autoReview: true,
} });
const cue = { id: "cue-shared", projectId: "shared", name: "Tests", type: "agent", prompt: "Run tests" };
function connect(...hosts: string[]) {
	act(() => { mocks.connected = hosts; for (const listener of mocks.listeners) listener(); });
}
function setup(section: Section, hostId = "box-a") {
	const Component = sections[section];
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	client.setQueryData(["project", "shared"], project("local"));
	client.setQueryData(["cues", "shared"], [{ ...cue, name: "Local cue" }]);
	const view = (host: string) => <QueryClientProvider client={client}><Component projectId="shared" hostId={host} /></QueryClientProvider>;
	const result = render(view(hostId));
	return { client, ...result, rerenderHost: (host: string) => result.rerender(view(host)) };
}
beforeEach(() => {
	vi.clearAllMocks();
	mocks.connected = [];
	mocks.listeners.clear();
	mocks.get.mockImplementation(async (host: string, path: string) => path.endsWith("/cues")
		? { data: { cues: [{ ...cue, name: host }] } }
		: { data: { status: "ok", project: project(host) } });
	mocks.put.mockResolvedValue({ data: {} });
	mocks.post.mockResolvedValue({ data: { cue, cleaned: ["session-1"], alreadyGone: [], skipped: [] } });
	mocks.patch.mockResolvedValue({ data: { cue } });
	mocks.delete.mockResolvedValue({});
});

it.each(Object.keys(sections) as Section[])("%s scopes reads and cached drafts to their host, never the local same-ID project", async (section) => {
	connect("box-a", "box-b");
	const { client, rerenderHost } = setup(section);
	const assertHost = async (host: string) => {
		if (section === "Scripts") expect(await screen.findByLabelText("Step 1")).toHaveValue(`echo ${host}`);
		else if (section === "Environment") expect(await screen.findByLabelText("Value 1")).toHaveValue(host);
		else expect(await screen.findByText(host)).toBeVisible();
	};
	await assertHost("box-a");
	rerenderHost("box-b");
	await assertHost("box-b");
	rerenderHost("box-a");
	await assertHost("box-a");
	const key = section === "Cues" ? "cues" : "project";
	expect(client.getQueryData([key, "box-a", "shared"])).toBeDefined();
	expect(client.getQueryData([key, "box-b", "shared"])).toBeDefined();
	expect(client.getQueryData(["project", "shared"])).toEqual(project("local"));
	expect(client.getQueryData(["cues", "shared"])).toEqual([{ ...cue, name: "Local cue" }]);
	expect(mocks.localGet).not.toHaveBeenCalled();
});

it.each(["Scripts", "Environment"] as const)("%s refreshes and saves config on the owning host and invalidates only that host", async (section) => {
	connect("box-a");
	const { client } = setup(section);
	const invalidate = vi.spyOn(client, "invalidateQueries");
	const input = await screen.findByLabelText(section === "Scripts" ? "Step 1" : "Value 1");
	fireEvent.change(input, { target: { value: "updated" } });
	const latest = { ...project("box-a"), name: "Renamed", config: { ...project("box-a").config, defaultBranch: "release" } };
	mocks.get.mockResolvedValue({ data: { status: "ok", project: latest } });
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(mocks.put).toHaveBeenCalledWith("box-a", "/api/v1/projects/{id}", {
		params: { path: { id: "shared" } }, body: { displayName: "Renamed", config: {
			...latest.config, ...(section === "Scripts" ? { postCreate: ["updated"] } : { env: { TOKEN: "updated" } }),
		} },
	}));
	await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ["remote-workspaces", "box-a"] }));
	expect(invalidate).toHaveBeenCalledWith({ queryKey: ["project", "box-a", "shared"] });
	expect(client.getQueryState(["project", "shared"])?.isInvalidated).toBe(false);
	expect(mocks.localGet).not.toHaveBeenCalled();
	expect(mocks.localPut).not.toHaveBeenCalled();
});

it("retries remote cleanup without clearing a same-ID local session's error", async () => {
	connect("box-a");
	const { client } = setup("Scripts");
	for (const hostId of [undefined, "box-a", "box-b"]) {
		const mutation = client.getMutationCache().build(client, {
			mutationKey: terminateSessionMutationKey, mutationFn: async () => { throw new Error("cleanup failed"); },
		});
		await expect(mutation.execute({ id: "session-1", hostId })).rejects.toThrow("cleanup failed");
	}
	await userEvent.click(await screen.findByRole("tab", { name: "Cleanup" }));
	await userEvent.click(screen.getByRole("button", { name: "Retry cleanup" }));
	await waitFor(() => expect(mocks.post).toHaveBeenCalledWith("box-a", "/api/v1/sessions/cleanup", { params: { query: { project: "shared" } } }));
	await screen.findByText("Cleaned 1; skipped 0.");
	expect(client.getMutationCache().findAll({ mutationKey: terminateSessionMutationKey }).map((m) => (m.state.variables as { hostId?: string }).hostId)).toEqual([undefined, "box-b"]);
	expect(mocks.localPost).not.toHaveBeenCalled();
});

it("creates, edits, and deletes remote agent cues with host-scoped invalidation", async () => {
	connect("box-a");
	const { client } = setup("Cues");
	const invalidate = vi.spyOn(client, "invalidateQueries");
	await userEvent.click(await screen.findByRole("button", { name: "New cue" }));
	fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Remote tests" } });
	fireEvent.change(screen.getByLabelText("Agent instruction"), { target: { value: "Run tests" } });
	await userEvent.click(screen.getByRole("button", { name: "Create" }));
	await waitFor(() => expect(mocks.post).toHaveBeenCalledWith("box-a", "/api/v1/projects/{projectId}/cues", {
		params: { path: { projectId: "shared" } }, body: { name: "Remote tests", type: "agent", prompt: "Run tests" },
	}));
	await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
	fireEvent.change(screen.getByLabelText("Agent instruction"), { target: { value: "Run lint" } });
	await userEvent.click(screen.getByRole("button", { name: "Save" }));
	await waitFor(() => expect(mocks.patch).toHaveBeenCalledWith("box-a", "/api/v1/cues/{cueId}", {
		params: { path: { cueId: "cue-shared" } }, body: { name: "box-a", type: "agent", prompt: "Run lint" },
	}));
	await userEvent.click(await screen.findByRole("button", { name: "Delete" }));
	await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Delete" }));
	await waitFor(() => expect(mocks.delete).toHaveBeenCalledWith("box-a", "/api/v1/cues/{cueId}", { params: { path: { cueId: "cue-shared" } } }));
	expect(invalidate).toHaveBeenCalledWith({ queryKey: ["cues", "box-a", "shared"] });
	expect(client.getQueryState(["cues", "shared"])?.isInvalidated).toBe(false);
	for (const method of [mocks.localGet, mocks.localPost, mocks.localPatch, mocks.localDelete]) expect(method).not.toHaveBeenCalled();
});

it.each(Object.keys(sections) as Section[])("%s is offline without requests or a crash", (section) => {
	setup(section);
	expect(screen.getByRole("alert")).toHaveTextContent("Host is offline");
	for (const method of [mocks.get, mocks.put, mocks.post, mocks.localGet]) expect(method).not.toHaveBeenCalled();
});

it.each(Object.keys(sections) as Section[])("%s preserves drafts through disconnection without saving offline", async (section) => {
	connect("box-a");
	setup(section);
	if (section === "Cues") await userEvent.click(await screen.findByRole("button", { name: "New cue" }));
	const label = section === "Scripts" ? "Step 1" : section === "Environment" ? "Value 1" : "Name";
	fireEvent.change(await screen.findByLabelText(label), { target: { value: "unsaved draft" } });
	connect();
	expect(screen.getByRole("alert")).toHaveTextContent("Host is offline");
	await act(async () => { await new Promise((resolve) => setTimeout(resolve, 800)); });
	expect(mocks.put).not.toHaveBeenCalled();
	expect(mocks.post).not.toHaveBeenCalled();
	connect("box-a");
	expect(await screen.findByLabelText(label)).toHaveValue("unsaved draft");
	if (section === "Environment") await waitFor(() => expect(mocks.put).toHaveBeenCalledOnce());
});

it.each(["Scripts", "Environment"] as const)("%s keeps a draft when a reconnect refresh fails", async (section) => {
	connect("box-a");
	const { client } = setup(section);
	const label = section === "Scripts" ? "Step 1" : "Value 1";
	fireEvent.change(await screen.findByLabelText(label), { target: { value: "keep this draft" } });
	connect();
	mocks.get.mockResolvedValue({ error: { message: "Remote unavailable" } });
	connect("box-a");
	expect(await screen.findByRole("alert")).toHaveTextContent("Remote unavailable");
	expect(screen.getByLabelText(label)).toHaveValue("keep this draft");
	expect(screen.getByLabelText(label)).toBeDisabled();
	expect(mocks.put).not.toHaveBeenCalled();
	mocks.get.mockResolvedValue({ data: { status: "ok", project: project("box-a") } });
	await act(async () => { await client.invalidateQueries({ queryKey: ["project", "box-a", "shared"] }); });
	await waitFor(() => expect(screen.getByLabelText(label)).toBeEnabled());
	expect(screen.getByLabelText(label)).toHaveValue("keep this draft");
});

it("explains the remote command policy and blocks command creation, editing, and conversion", async () => {
	connect("box-a");
	mocks.get.mockResolvedValue({ data: { cues: [{ ...cue, type: "command", command: "npm test", prompt: undefined }] } });
	setup("Cues");
	expect(await screen.findByText("Tests")).toBeVisible();
	expect(screen.getByText("Create and edit command cues on the owning desktop. You can manage agent cues here.")).toBeVisible();
	expect(screen.getByRole("button", { name: "Edit" })).toBeDisabled();
	await userEvent.click(screen.getByRole("button", { name: "New cue" }));
	expect(screen.getByLabelText("Agent instruction")).toBeVisible();
	fireEvent.keyDown(screen.getByRole("button", { name: "Type" }), { key: "ArrowDown" });
	expect(await screen.findByRole("menuitem", { name: "Command" })).toHaveAttribute("aria-disabled", "true");
	expect(mocks.post).not.toHaveBeenCalled();
	expect(mocks.patch).not.toHaveBeenCalled();
});
