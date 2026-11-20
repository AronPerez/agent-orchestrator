import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DecisionSyncStatus } from "../lib/decision-sync";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));
vi.mock("../lib/host-clients", () => ({ clientForSessionHost: () => ({ GET: getMock }) }));

import { useUiStore } from "../stores/ui-store";
import { DecisionSyncFooter } from "./DecisionSyncFooter";

const now = new Date("2026-11-19T12:00:00Z").getTime();
const ago = (ms: number) => new Date(now - ms).toISOString();

function renderFooter(status: Partial<DecisionSyncStatus>) {
	getMock.mockResolvedValue({ data: { hostId: "h_me", machineLabel: "laptop", peers: [], origins: [], conflicts: [], ...status }, error: undefined });
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}><DecisionSyncFooter now={now} /></QueryClientProvider>);
}

const peer = (p: Partial<DecisionSyncStatus["peers"][number]>): DecisionSyncStatus["peers"][number] =>
	({ hostId: "h_x", label: "x", direction: "outbound", inSync: true, clockAheadMs: 0, state: "linked_in_sync", ...p });

describe("DecisionSyncFooter", () => {
	beforeEach(() => getMock.mockReset());

	it("says this computer only and links to the remote hosts settings", async () => {
		const openGlobalSettings = vi.fn();
		useUiStore.setState({ openGlobalSettings });
		renderFooter({});
		expect(await screen.findByText("Decisions on this computer only")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Link machines in Settings › Remote hosts" }));
		expect(openGlobalSettings).toHaveBeenCalledWith("remoteHosts");
	});

	it("puts problems first in warning text and keeps freshness muted", async () => {
		renderFooter({
			peers: [
				peer({ hostId: "h_a", label: "mac-mini", lastSuccessAt: ago(30_000) }),
				peer({ hostId: "h_b", label: "vm-2", state: "revoked" }),
			],
		});
		const footer = await screen.findByTestId("decision-sync-footer");
		expect(footer.textContent).toBe("vm-2 no longer accepts this computer. Turn Sync decisions off and on again. · In sync with mac-mini");
		expect(screen.getByText(/no longer accepts/).className).toContain("text-status-in-review");
		expect(screen.getByText("In sync with mac-mini").className).not.toContain("text-status");
	});

	it("falls back to the plain local line when the daemon has no sync status", async () => {
		getMock.mockResolvedValue({ data: { calls: [] }, error: undefined });
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={client}><DecisionSyncFooter now={now} /></QueryClientProvider>);
		expect(await screen.findByText("Decisions on this computer")).toBeInTheDocument();
	});
});
