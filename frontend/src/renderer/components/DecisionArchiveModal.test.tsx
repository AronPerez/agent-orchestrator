import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { decisionArchiveEntries } from "../lib/decision-archive";
import { buildDecisionBoard } from "../lib/decision-board";
import type { DecisionCall } from "../lib/decision-inbox";
import { DecisionArchiveModal, type DecisionArchiveModalProps } from "./DecisionArchiveModal";
import { TooltipProvider } from "./ui/tooltip";

function call(overrides: Partial<DecisionCall> & { id: string }): DecisionCall {
	return {
		link: `ao://decisions/${overrides.id}`,
		state: "done",
		stateLabel: "Done",
		title: `Question ${overrides.id}`,
		context: "",
		options: [],
		links: [],
		hostId: "h_a",
		machineLabel: "mac-mini",
		ownerSessionId: "w-1",
		ownerSessionName: "worker 1",
		raisedAt: "2026-11-18T09:00:00Z",
		retired: false,
		chosenLabel: "Ship",
		...overrides,
	};
}
const cloud = { projectId: "cloud", projectName: "skyvern-cloud" };
const nango = { ...cloud, streamSessionId: "nango", streamName: "Nango" };
const entries = decisionArchiveEntries(buildDecisionBoard([
	call({ id: "a", ...cloud, outcome: "Pinned in go.mod", outcomeAt: "2026-11-18T11:01:00Z" }),
	call({ id: "b", ...nango, outcome: "Migrated to vault", outcomeAt: "2026-11-18T11:05:00Z", changedAfterClose: true }),
	call({ id: "c", ...nango, choice: "other", chosenLabel: undefined, outcome: "Renamed the flag", outcomeAt: "2026-11-18T11:03:00Z" }),
	call({ id: "d", projectId: "shop", projectName: "Shop", outcome: "Merged the release", outcomeAt: "2026-11-18T11:09:00Z" }),
]));

function renderModal(props: Partial<DecisionArchiveModalProps> = {}) {
	const onOpen = vi.fn();
	const onOpenChange = vi.fn();
	render(<TooltipProvider><DecisionArchiveModal open entries={entries} onOpen={onOpen} onOpenChange={onOpenChange} {...props} /></TooltipProvider>);
	return { onOpen, onOpenChange };
}
const grid = () => screen.getByRole("grid", { name: "Done decisions" });
const titles = () => within(grid()).getAllByRole("gridcell").map((cell) => within(cell).getByRole("button").textContent?.match(/Question \w/)?.[0]);
const field = () => screen.getByRole("combobox", { name: "Search done decisions" });

describe("DecisionArchiveModal", () => {
	it("shows every done decision newest first, with its pick, outcome and place", () => {
		renderModal();
		expect(screen.getByRole("dialog", { name: "Done decisions" })).toBeInTheDocument();
		expect(titles()).toEqual(["Question d", "Question b", "Question c", "Question a"]);
		const card = within(grid()).getByRole("button", { name: /^Question b/ });
		expect(card).toHaveTextContent("Ship");
		expect(card).toHaveTextContent("Migrated to vault");
		expect(card).toHaveTextContent("skyvern-cloud · Nango");
		expect(within(card).getByText("Changed after it closed")).toBeInTheDocument();
		expect(within(grid()).getByRole("button", { name: /^Question c/ })).toHaveTextContent("Something else");
		expect(within(grid()).getByRole("button", { name: /^Question a/ })).toHaveTextContent("skyvern-cloud · Project-wide");
		expect(screen.getByText("Last 7 days")).toBeInTheDocument();
	});

	it("starts narrowed to the stream it was opened from, and one chip widens it to every stream", async () => {
		renderModal({ scope: { project: "cloud", stream: "h_a:nango" } });
		expect(titles()).toEqual(["Question b", "Question c"]);
		await userEvent.click(screen.getByRole("button", { name: "Showing skyvern-cloud: Nango. Show every done decision" }));
		expect(titles()).toEqual(["Question d", "Question b", "Question c", "Question a"]);
		expect(screen.queryByRole("button", { name: /Show every done decision$/ })).not.toBeInTheDocument();
		expect(field()).toHaveFocus();
	});

	it("searches what the cards show, and says when nothing matches", async () => {
		renderModal();
		expect(field()).toHaveFocus();
		await userEvent.keyboard("vault");
		expect(titles()).toEqual(["Question b"]);
		await userEvent.clear(field());
		await userEvent.keyboard("shop merged");
		expect(titles()).toEqual(["Question d"]);
		await userEvent.clear(field());
		await userEvent.keyboard("zzz");
		expect(within(grid()).queryAllByRole("gridcell")).toHaveLength(0);
		expect(screen.getByRole("status")).toHaveTextContent("No done decisions match.");
	});

	it("keeps focus in the search while the arrows move one selection, and Enter opens it", async () => {
		const { onOpen, onOpenChange } = renderModal();
		const selected = () => within(grid()).getAllByRole("gridcell").filter((cell) => cell.getAttribute("aria-selected") === "true").map((cell) => cell.textContent?.match(/Question \w/)?.[0]);
		expect(selected()).toEqual(["Question d"]);
		expect(field()).toHaveAttribute("aria-activedescendant", within(grid()).getAllByRole("gridcell")[0].id);
		await userEvent.keyboard("{ArrowDown}{ArrowDown}");
		expect(selected()).toEqual(["Question c"]);
		expect(field()).toHaveFocus();
		await userEvent.keyboard("{ArrowUp}{End}");
		expect(selected()).toEqual(["Question a"]);
		await userEvent.keyboard("{Home}{ArrowDown}{Enter}");
		expect(onOpenChange).toHaveBeenLastCalledWith(false);
		expect(onOpen).toHaveBeenCalledWith("b");
	});

	it("opens a card on click and closes from its button", async () => {
		const { onOpen, onOpenChange } = renderModal();
		await userEvent.click(within(grid()).getByRole("button", { name: /^Question a/ }));
		expect(onOpen).toHaveBeenCalledWith("a");
		expect(onOpenChange).toHaveBeenLastCalledWith(false);
		onOpenChange.mockClear();
		await userEvent.click(screen.getByRole("button", { name: "Close" }));
		expect(onOpenChange).toHaveBeenLastCalledWith(false);
	});

	it("draws nothing while closed", () => {
		renderModal({ open: false });
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	});
});
