import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HostSwitcher } from "./HostSwitcher";

const switchToHost = vi.fn();
let mockActive: { label: string; url: string } | null = null;
let mockUnauthorized = false;

vi.mock("../lib/active-host", () => ({
	activeHost: () => mockActive,
	switchToHost: (...args: unknown[]) => switchToHost(...args),
}));
vi.mock("../lib/auth-gate", () => ({
	isUnauthorized: () => mockUnauthorized,
	subscribeUnauthorized: () => () => undefined,
}));
vi.mock("../hooks/useRemoteHosts", () => ({
	LOCAL_HOST_ID: "local",
	useRemoteHosts: () => ({
		hosts: [
			{ id: "local", label: "This Mac", url: null, status: "local" },
			{ id: "http://192.0.2.1:3011", label: "workbox", url: "http://192.0.2.1:3011", status: "online" },
		],
		refresh: vi.fn(),
	}),
}));

beforeEach(() => {
	mockActive = null;
	mockUnauthorized = false;
	switchToHost.mockClear();
});

// The trigger is a shadcn Select, so its role is `combobox`, not `button` — the
// plan's snippet predates that choice. Everything else it asserts holds.
describe("HostSwitcher", () => {
	it("shows This Mac when local and switches to a remote host", async () => {
		render(<HostSwitcher />);
		expect(screen.getByRole("combobox", { name: /this mac/i })).toBeInTheDocument();
		await userEvent.click(screen.getByRole("combobox", { name: /this mac/i }));
		await userEvent.click(await screen.findByRole("option", { name: /workbox/ }));
		expect(switchToHost).toHaveBeenCalledWith("http://192.0.2.1:3011");
	});

	it("names the remote host it is viewing, visibly distinct from local", () => {
		mockActive = { label: "workbox", url: "http://192.0.2.1:3011" };
		render(<HostSwitcher />);
		expect(screen.getByText(/viewing workbox/i)).toBeInTheDocument();
	});

	it("switches back to local", async () => {
		mockActive = { label: "workbox", url: "http://192.0.2.1:3011" };
		render(<HostSwitcher />);
		await userEvent.click(screen.getByRole("combobox", { name: /workbox/i }));
		await userEvent.click(await screen.findByRole("option", { name: /this mac/i }));
		expect(switchToHost).toHaveBeenCalledWith(null);
	});

	it("says the remote rejected the saved password and offers the way home", async () => {
		mockActive = { label: "workbox", url: "http://192.0.2.1:3011" };
		mockUnauthorized = true;
		render(<HostSwitcher />);
		expect(screen.getByText(/rejected the saved password/i)).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: /back to this mac/i }));
		expect(switchToHost).toHaveBeenCalledWith(null);
	});

	it("stays quiet about a 401 that came from the local daemon", () => {
		mockUnauthorized = true;
		render(<HostSwitcher />);
		expect(screen.queryByText(/rejected the saved password/i)).not.toBeInTheDocument();
	});
});
