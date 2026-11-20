import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { aoBridge } from "../../lib/bridge";
import { DecisionSyncRow } from "./DecisionSyncRow";

const host = { hostId: "h_mini", label: "mac-mini", url: "http://mini:3011" };
const peer = (state: string, direction = "outbound") => ({ hostId: "h_mini", label: "mac-mini", direction, state, inSync: state === "linked_in_sync", clockAheadMs: 0 }) as never;

describe("DecisionSyncRow", () => {
	it("toggles through the main process and shows the peer's state", async () => {
		const setDecisionSync = vi.spyOn(aoBridge.remotes, "setDecisionSync").mockResolvedValue({ ok: true, warnings: [{ callId: "dec_leaky", field: "context" }] });
		const onChanged = vi.fn();
		const { rerender } = render(<DecisionSyncRow host={host} peers={[]} onChanged={onChanged} />);
		const toggle = screen.getByRole("switch", { name: "Sync decisions · mac-mini" });
		expect(toggle).not.toBeChecked();
		await userEvent.click(toggle);
		expect(setDecisionSync).toHaveBeenCalledWith("http://mini:3011", true);
		expect(onChanged).toHaveBeenCalled();
		expect(await screen.findByText("ao://decisions/dec_leaky")).toBeInTheDocument();

		rerender(<DecisionSyncRow host={host} peers={[peer("linked_in_sync")]} onChanged={onChanged} />);
		expect(screen.getByRole("switch", { name: "Sync decisions · mac-mini" })).toBeChecked();
		expect(screen.getByText("In sync")).toBeInTheDocument();
		setDecisionSync.mockRestore();
	});

	it("is disabled without host id", () => {
		render(<DecisionSyncRow host={{ ...host, hostId: "" }} peers={[]} onChanged={vi.fn()} />);
		expect(screen.getByRole("switch")).toBeDisabled();
		expect(screen.getByText("Connect to this machine once to sync decisions with it.")).toBeInTheDocument();
	});

	it("says why when the change fails, without any detail from the daemon", async () => {
		const setDecisionSync = vi.spyOn(aoBridge.remotes, "setDecisionSync").mockResolvedValue({ ok: false, code: "DECISION_SYNC_LINK_REFUSED" });
		render(<DecisionSyncRow host={host} peers={[]} onChanged={vi.fn()} />);
		await userEvent.click(screen.getByRole("switch"));
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not change decision sync for this machine.");
		setDecisionSync.mockRestore();
	});
});
