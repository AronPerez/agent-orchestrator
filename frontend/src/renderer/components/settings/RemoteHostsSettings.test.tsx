import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useUiStore } from "../../stores/ui-store";

const saved = vi.hoisted(() => ({ entries: [] as Array<{ hostId: string; label: string; url: string }> }));
const account = vi.hoisted(() => ({ status: "unauthenticated" as "authenticated" | "unauthenticated" | "loading" }));
const cloud = vi.hoisted(() => ({ saveAccountRemoteHost: vi.fn(), deleteAccountRemoteHost: vi.fn() }));
const remotes = vi.hoisted(() => ({
	list: vi.fn(async () => saved.entries),
	add: vi.fn(async ({ label, url }: { label: string; url: string }) => {
		saved.entries = [{ hostId: "box-a", label, url }];
		return "online" as "online" | "incompatible";
	}),
	connect: vi.fn(async (url: string) => ({ hostId: "box-a", label: "Box A", url, base: "http://127.0.0.1:4000" })),
	disconnect: vi.fn(async () => undefined),
	remove: vi.fn(async () => undefined),
	issueAccountToken: vi.fn(async () => "account-token"),
	update: vi.fn(async (_oldUrl: string, changes: { label?: string; url?: string }) => {
		saved.entries = saved.entries.map((host) => ({ ...host, label: changes.label ?? host.label, url: changes.url ?? host.url }));
		return "online" as const;
	}),
}));
vi.mock("../../lib/bridge", () => ({ aoBridge: { remotes } }));
vi.mock("../../lib/cloud-session", () => ({ useCloudSession: () => ({ status: account.status, signIn: vi.fn() }) }));
vi.mock("../../lib/account-remote-hosts", () => cloud);
vi.mock("../../hooks/useSettings", () => ({ useSettings: () => ({ settings: { cloudControlPlaneUrl: "https://cloud.example" } }) }));

import { RemoteHostsSettings } from "./RemoteHostsSettings";

const renderSettings = () => render(<QueryClientProvider client={new QueryClient()}><RemoteHostsSettings /></QueryClientProvider>);

beforeEach(() => {
	account.status = "unauthenticated";
	useUiStore.setState({ developerMode: false, remoteHosts: false });
});

afterEach(() => {
	saved.entries = [];
	useUiStore.setState({ remoteHosts: false });
	remotes.connect.mockClear();
	remotes.list.mockClear();
	remotes.add.mockClear();
	remotes.update.mockClear();
	remotes.remove.mockClear();
	remotes.issueAccountToken.mockClear();
	cloud.saveAccountRemoteHost.mockClear();
	cloud.deleteAccountRemoteHost.mockClear();
});

it.each(["unauthenticated", "loading"] as const)("exposes local hosts and the toggle with Developer mode off and Cloud %s", async (status) => {
	account.status = status;
	saved.entries = [{ hostId: "box-a", label: "Box A", url: "http://box-a:3001" }];
	renderSettings();
	expect(await screen.findByRole("button", { name: "Edit Box A" })).toBeVisible();
	expect(screen.queryByRole("button", { name: "Sign in to AO Cloud" })).toBeNull();
	const toggle = screen.getByRole("switch", { name: "Connect to remote hosts" });
	fireEvent.click(toggle);
	expect(toggle).toHaveAttribute("aria-checked", "true");
	fireEvent.click(toggle);
	expect(toggle).toHaveAttribute("aria-checked", "false");
	expect(screen.getByRole("textbox", { name: "Name" })).toBeEnabled();
});

it("ignores a signed-in host list that arrives after sign-out", async () => {
	account.status = "authenticated";
	let finish!: (hosts: typeof saved.entries) => void;
	remotes.list.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
	const view = renderSettings();
	account.status = "unauthenticated";
	view.rerender(<QueryClientProvider client={new QueryClient()}><RemoteHostsSettings /></QueryClientProvider>);
	await screen.findByText("No hosts yet. Add one below.");
	await act(async () => finish([{ hostId: "cloud-box", label: "Account host", url: "https://cloud-box.example" }]));
	expect(screen.queryByText("Account host")).not.toBeInTheDocument();
});

it("does not connect a newly paired host after Remote hosts is turned off", async () => {
	useUiStore.setState({ remoteHosts: true });
	let finishAdd: ((health: "online") => void) | undefined;
	remotes.add.mockImplementationOnce(() => new Promise((resolve) => { finishAdd = resolve; }));
	renderSettings();
	fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Box A" } });
	fireEvent.change(screen.getByRole("textbox", { name: "Address" }), { target: { value: "http://box-a:3001" } });
	fireEvent.change(screen.getByLabelText("Connection password"), { target: { value: "secret123" } });
	const submit = screen.getByRole("button", { name: "Add host" });
	fireEvent.click(submit);
	expect(submit).toBeDisabled();
	act(() => useUiStore.setState({ remoteHosts: false }));
	await act(async () => finishAdd?.("online"));
	await waitFor(() => expect(screen.getByRole("textbox", { name: "Name" })).toHaveValue(""));
	expect(remotes.connect).not.toHaveBeenCalled();
});

it("pairs a host and shows it in the saved-host list", async () => {
	renderSettings();
	expect(screen.getByRole("switch", { name: "Connect to remote hosts" })).toHaveAttribute("aria-checked", "false");
	fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Box A" } });
	fireEvent.change(screen.getByRole("textbox", { name: "Address" }), { target: { value: "http://box-a:3001" } });
	fireEvent.change(screen.getByLabelText("Connection password"), { target: { value: "secret123" } });
	fireEvent.click(screen.getByRole("button", { name: "Add host" }));
	await waitFor(() => expect(screen.getByText("Box A")).toBeVisible());
	expect(screen.getByRole("switch", { name: "Connect to remote hosts" })).toHaveAttribute("aria-checked", "true");
	expect(remotes.add).toHaveBeenCalledWith({ label: "Box A", url: "http://box-a:3001", password: "secret123" });
	expect(screen.getByLabelText("Connection password")).toHaveAttribute("type", "password");
	expect(remotes.issueAccountToken).not.toHaveBeenCalled();
	expect(cloud.saveAccountRemoteHost).not.toHaveBeenCalled();
});

it("syncs a paired host only when signed in to AO Cloud", async () => {
	account.status = "authenticated";
	renderSettings();
	fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Box A" } });
	fireEvent.change(screen.getByRole("textbox", { name: "Address" }), { target: { value: "http://box-a:3001" } });
	fireEvent.change(screen.getByLabelText("Connection password"), { target: { value: "secret123" } });
	fireEvent.click(screen.getByRole("button", { name: "Add host" }));
	await waitFor(() => expect(cloud.saveAccountRemoteHost).toHaveBeenCalledWith("https://cloud.example", {
		hostId: "box-a", label: "Box A", url: "http://box-a:3001", token: "account-token",
	}));
	expect(remotes.issueAccountToken).toHaveBeenCalledWith("http://box-a:3001");
});

it("explains an incompatible host without adding it", async () => {
	remotes.add.mockResolvedValueOnce("incompatible");
	renderSettings();
	fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Box A" } });
	fireEvent.change(screen.getByRole("textbox", { name: "Address" }), { target: { value: "http://box-a:3001" } });
	fireEvent.change(screen.getByLabelText("Connection password"), { target: { value: "secret123" } });
	fireEvent.click(screen.getByRole("button", { name: "Add host" }));
	await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Update AO"));
	expect(saved.entries).toEqual([]);
});

it("explains how to re-pair a saved host without an identity", async () => {
	saved.entries = [{ hostId: "", label: "Old Box", url: "http://old-box:3001" }];
	renderSettings();
	await screen.findByText(/Old Box/);
	expect(screen.getByText(/edit this host and enter its connection password/i)).toBeVisible();
	fireEvent.click(screen.getByRole("button", { name: "Edit Old Box" }));
	expect(screen.getByLabelText("Connection password")).toBeRequired();
	fireEvent.change(screen.getByLabelText("Connection password"), { target: { value: "new-secret" } });
	fireEvent.click(screen.getByRole("button", { name: "Save host" }));
	await waitFor(() => expect(remotes.add).toHaveBeenCalledWith({
		label: "Old Box",
		url: "http://old-box:3001",
		password: "new-secret",
	}));
	expect(remotes.update).not.toHaveBeenCalled();
});

it("edits a saved host without reading or replacing its password", async () => {
	saved.entries = [{ hostId: "box-a", label: "Box A", url: "http://box-a:3001" }];
	renderSettings();
	await screen.findByRole("button", { name: "Edit Box A" });
	fireEvent.click(screen.getByRole("button", { name: "Edit Box A" }));
	expect(screen.getByLabelText("Connection password")).toHaveValue("");
	expect(screen.getByLabelText("Connection password")).not.toBeRequired();
	fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "Office" } });
	fireEvent.change(screen.getByRole("textbox", { name: "Address" }), { target: { value: "http://office:3011" } });
	fireEvent.click(screen.getByRole("button", { name: "Save host" }));
	await waitFor(() => expect(remotes.update).toHaveBeenCalledWith("http://box-a:3001", {
		label: "Office",
		url: "http://office:3011",
	}));
	expect(await screen.findByText("Office")).toBeVisible();
	expect(cloud.saveAccountRemoteHost).not.toHaveBeenCalled();
});

it("requires confirmation before forgetting a host", async () => {
	saved.entries = [{ hostId: "box-a", label: "Box A", url: "http://box-a:3001" }];
	remotes.remove.mockImplementationOnce(async () => { saved.entries = []; });
	renderSettings();
	fireEvent.click(await screen.findByRole("button", { name: "Remove Box A" }));
	expect(remotes.remove).not.toHaveBeenCalled();
	expect(screen.getByText(/Sessions keep running on that host/i)).toBeVisible();
	fireEvent.click(screen.getByRole("button", { name: "Remove" }));
	await waitFor(() => expect(remotes.remove).toHaveBeenCalledWith("http://box-a:3001"));
	expect(await screen.findByText("No hosts yet. Add one below.")).toBeVisible();
	expect(cloud.deleteAccountRemoteHost).not.toHaveBeenCalled();
});
