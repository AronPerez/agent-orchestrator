import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { aoBridge } from "../lib/bridge";
import { SidebarRemoteHosts } from "./SidebarRemoteHosts";

const switchToHost = vi.fn();
let mockHosts = [
	{ id: "local", label: "This Mac", url: null as string | null, status: "local" },
	{ id: "http://192.0.2.1:3011", label: "workbox", url: "http://192.0.2.1:3011", status: "online" },
];

vi.mock("../lib/active-host", () => ({
	activeHost: () => null,
	switchToHost: (...args: unknown[]) => switchToHost(...args),
}));
vi.mock("../hooks/useRemoteHosts", () => ({
	LOCAL_HOST_ID: "local",
	useRemoteHosts: () => ({ hosts: mockHosts, refresh: vi.fn() }),
}));

beforeEach(() => {
	vi.restoreAllMocks();
	switchToHost.mockClear();
	mockHosts = [
		{ id: "local", label: "This Mac", url: null, status: "local" },
		{ id: "http://192.0.2.1:3011", label: "workbox", url: "http://192.0.2.1:3011", status: "online" },
	];
});

describe("SidebarRemoteHosts", () => {
	it("lists the host's projects read-only on expand, fetching lazily", async () => {
		const request = vi.spyOn(aoBridge.remotes, "request").mockResolvedValue({
			status: 200,
			body: { projects: [{ id: "skyvern-cloud", name: "skyvern-cloud" }] },
		});
		render(<SidebarRemoteHosts />);
		expect(request).not.toHaveBeenCalled(); // nothing fetched while collapsed
		await userEvent.click(screen.getByRole("button", { name: /workbox/ }));
		expect(await screen.findByText("skyvern-cloud")).toBeInTheDocument();
		expect(request).toHaveBeenCalledWith("http://192.0.2.1:3011", { method: "GET", path: "/api/v1/projects" });
		// Read-only: a peeked project row is not a link into local routes.
		expect(screen.getByText("skyvern-cloud").closest("a")).toBeNull();
	});

	it("switches to the host from its Open action", async () => {
		vi.spyOn(aoBridge.remotes, "request").mockResolvedValue({ status: 200, body: { projects: [] } });
		render(<SidebarRemoteHosts />);
		await userEvent.click(screen.getByRole("button", { name: /workbox/ }));
		await userEvent.click(await screen.findByRole("button", { name: /open workbox/i }));
		expect(switchToHost).toHaveBeenCalledWith("http://192.0.2.1:3011");
	});

	it("shows the failure inline when a host stops answering", async () => {
		vi.spyOn(aoBridge.remotes, "request").mockResolvedValue({ status: 0, body: null });
		render(<SidebarRemoteHosts />);
		await userEvent.click(screen.getByRole("button", { name: /workbox/ }));
		expect(await screen.findByText(/unreachable/i)).toBeInTheDocument();
	});

	it("repeats the daemon's own refusal instead of calling it unreachable", async () => {
		vi.spyOn(aoBridge.remotes, "request").mockResolvedValue({
			status: 401,
			body: { error: "unauthorized", code: "UNAUTHORIZED", message: "connection password rejected" },
		});
		render(<SidebarRemoteHosts />);
		await userEvent.click(screen.getByRole("button", { name: /workbox/ }));
		expect(await screen.findByText("connection password rejected")).toBeInTheDocument();
		expect(screen.queryByText(/unreachable/i)).not.toBeInTheDocument();
	});

	it("says so when a reachable host has no projects", async () => {
		vi.spyOn(aoBridge.remotes, "request").mockResolvedValue({ status: 200, body: { projects: [] } });
		render(<SidebarRemoteHosts />);
		await userEvent.click(screen.getByRole("button", { name: /workbox/ }));
		expect(await screen.findByText(/no projects/i)).toBeInTheDocument();
	});

	it("renders nothing when no remote host is saved", () => {
		mockHosts = [{ id: "local", label: "This Mac", url: null, status: "local" }];
		const { container } = render(<SidebarRemoteHosts />);
		expect(container).toBeEmptyDOMElement();
	});
});
