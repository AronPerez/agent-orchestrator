import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { SettingsProvider, SettingsPane } from "./SettingsDialog";
import { TooltipProvider } from "./ui/tooltip";

const mocks = vi.hoisted(() => ({
	connected: ["box-a"], listeners: new Set<() => void>(), get: vi.fn(), put: vi.fn(), localGet: vi.fn(),
}));
vi.mock("../lib/host-clients", () => ({
	connectedHosts: () => mocks.connected,
	subscribeConnectedHosts: (listener: () => void) => { mocks.listeners.add(listener); return () => mocks.listeners.delete(listener); },
	labelForHost: () => "Box A",
	clientForHost: (host: string) => {
		if (!mocks.connected.includes(host)) throw new Error("Host is offline");
		return { GET: mocks.get, PUT: mocks.put };
	},
}));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: mocks.localGet }, apiErrorMessage: (error: { message: string }) => error.message,
}));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	useCloudProjectsQuery: () => ({ data: [] }),
	workspaceQueryOptions: { queryKey: ["workspaces"], queryFn: async () => [] },
	workspaceQueryKeyForHost: (host: string) => ["remote-workspaces", host],
}));
vi.mock("../hooks/useCloudGate", () => ({ useCloudGate: () => ({ cloudEnabled: false }) }));
vi.mock("../hooks/useCloudOffering", () => ({ useCloudOffering: () => ({ hasCoder: false, selfServeBilling: false }) }));
vi.mock("./ProjectSettingsForm", () => ({ ProjectSettingsForm: () => <div>General form</div> }));
vi.mock("./GlobalSettingsForm", () => ({ GlobalSettingsForm: () => null }));

const project = { id: "shared", name: "Remote", kind: "single_repo", config: { postCreate: ["echo old"], env: { TOKEN: "old" } } };
const sections = ["Scripts", "Environment"] as const;
type Section = typeof sections[number];
function connect(connected: boolean) {
	act(() => { mocks.connected = connected ? ["box-a"] : []; for (const listener of mocks.listeners) listener(); });
}
function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((done) => { resolve = done; });
	return { promise, resolve };
}
async function setup(section: Section) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	useUiStore.getState().openProjectSettings("shared", "box-a");
	render(<QueryClientProvider client={client}><TooltipProvider><SettingsProvider><SettingsPane /></SettingsProvider></TooltipProvider></QueryClientProvider>);
	await userEvent.click(await screen.findByRole("button", { name: section }));
	const label = section === "Scripts" ? "Step 1" : "Value 1";
	fireEvent.change(await screen.findByLabelText(label), { target: { value: "keep my draft" } });
	return { client, label };
}
beforeEach(() => {
	vi.resetAllMocks();
	mocks.connected = ["box-a"];
	mocks.listeners.clear();
	mocks.get.mockResolvedValue({ data: { status: "ok", project } });
	mocks.put.mockResolvedValue({ data: {} });
	useUiStore.setState({ settingsModal: null, remoteHosts: true });
});

it.each(sections)("%s keeps dirty drafts when close or navigation is attempted offline", async (section) => {
	const { label } = await setup(section);
	connect(false);
	await userEvent.click(screen.getByRole("button", { name: "Close settings" }));
	expect(useUiStore.getState().settingsModal).not.toBeNull();
	await userEvent.click(screen.getByRole("button", { name: "General" }));
	expect(screen.getByRole("button", { name: section })).toHaveAttribute("data-active", "true");
	expect(mocks.put).not.toHaveBeenCalled();
	connect(true);
	expect(await screen.findByLabelText(label)).toHaveValue("keep my draft");
	// Finishing the save can now honor the requested navigation.
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(screen.getByRole("button", { name: "General" })).toHaveAttribute("data-active", "true"));
	expect(mocks.put).toHaveBeenCalled();
	expect(mocks.localGet).not.toHaveBeenCalled();
});

it.each(sections)("%s does not complete a pending close when an in-flight save disconnects and fails", async (section) => {
	const pending = deferred<{ data?: object; error?: { message: string } }>();
	mocks.put.mockReturnValueOnce(pending.promise);
	const { label } = await setup(section);
	await userEvent.click(screen.getByRole("button", { name: "Close settings" }));
	await waitFor(() => expect(mocks.put).toHaveBeenCalledOnce());
	connect(false);
	expect(useUiStore.getState().settingsModal).not.toBeNull();
	await act(async () => pending.resolve({ error: { message: "Connection lost" } }));
	expect(useUiStore.getState().settingsModal).not.toBeNull();
	connect(true);
	expect(await screen.findByLabelText(label)).toHaveValue("keep my draft");
	await userEvent.click(screen.getByRole("button", { name: "Close settings" }));
	await waitFor(() => expect(useUiStore.getState().settingsModal).toBeNull());
});

it.each(sections)("%s preserves dirty metadata when the project refresh fails", async (section) => {
	const { client, label } = await setup(section);
	mocks.get.mockResolvedValue({ error: { message: "Refresh unavailable" } });
	await act(async () => { await client.invalidateQueries({ queryKey: ["project", "box-a", "shared"] }); });
	await userEvent.click(screen.getByRole("button", { name: "General" }));
	await userEvent.click(screen.getByRole("button", { name: "Close settings" }));
	expect(useUiStore.getState().settingsModal).not.toBeNull();
	expect(screen.getByRole("button", { name: section })).toHaveAttribute("data-active", "true");
	expect(screen.getByLabelText(label)).toHaveValue("keep my draft");
	expect(mocks.put).not.toHaveBeenCalled();
});
