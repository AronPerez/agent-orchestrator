import { fireEvent, render, screen, within } from "@testing-library/react";
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
const calls = [
	call({ id: "a", ...cloud, outcome: "Pinned in go.mod", outcomeAt: "2026-11-18T11:01:00Z" }),
	call({ id: "b", ...nango, outcome: "Migrated to vault", outcomeAt: "2026-11-18T11:05:00Z", changedAfterClose: true }),
	call({ id: "c", ...nango, choice: "other", chosenLabel: undefined, outcome: "Renamed the flag", outcomeAt: "2026-11-18T11:03:00Z" }),
	call({ id: "d", projectId: "shop", projectName: "Shop", outcome: "Merged the release", outcomeAt: "2026-11-18T11:09:00Z" }),
];
const entriesOf = (list: DecisionCall[]) => decisionArchiveEntries(buildDecisionBoard(list));
const entries = entriesOf(calls);

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

	it("keeps the selection on the same decision when a newer one arrives, and on its place only once it is gone", async () => {
		const onOpen = vi.fn();
		const modal = (list: DecisionCall[]) => <TooltipProvider><DecisionArchiveModal open entries={entriesOf(list)} onOpen={onOpen} onOpenChange={vi.fn()} /></TooltipProvider>;
		const selected = () => within(grid()).getAllByRole("gridcell").filter((cell) => cell.getAttribute("aria-selected") === "true").map((cell) => cell.textContent?.match(/Question \w/)?.[0]);
		const view = render(modal(calls));
		await userEvent.keyboard("{ArrowDown}");
		expect(selected()).toEqual(["Question b"]);
		// Another decision finishes while the archive is open and takes the first place.
		const newer = call({ id: "e", projectId: "shop", projectName: "Shop", outcome: "Just finished", outcomeAt: "2026-11-18T11:30:00Z" });
		view.rerender(modal([...calls, newer]));
		expect(titles()).toEqual(["Question e", "Question d", "Question b", "Question c", "Question a"]);
		expect(selected()).toEqual(["Question b"]);
		await userEvent.keyboard("{Enter}");
		expect(onOpen).toHaveBeenLastCalledWith("b");
		// The selected one leaves the archive: the selection stays where it was.
		view.rerender(modal([...calls, newer].filter((item) => item.id !== "b")));
		expect(selected()).toEqual(["Question c"]);
	});

	it("holds the card highlighted from the start, and after a new search or a widened scope, when a newer one arrives", async () => {
		const modal = (list: DecisionCall[]) => <TooltipProvider><DecisionArchiveModal open entries={entriesOf(list)} scope={{ project: "cloud", stream: "h_a:nango" }} onOpen={onOpen} onOpenChange={vi.fn()} /></TooltipProvider>;
		const onOpen = vi.fn();
		const selected = () => within(grid()).getAllByRole("gridcell").filter((cell) => cell.getAttribute("aria-selected") === "true").map((cell) => within(cell).getByRole("button").textContent?.match(/Question \w/)?.[0]);
		const view = render(modal(calls));
		// Nothing was chosen: the first card is the one Enter opens.
		expect(selected()).toEqual(["Question b"]);
		const newerInStream = call({ id: "e", ...nango, outcome: "Just finished", outcomeAt: "2026-11-18T11:30:00Z" });
		view.rerender(modal([...calls, newerInStream]));
		expect(titles()).toEqual(["Question e", "Question b", "Question c"]);
		expect(selected()).toEqual(["Question b"]);
		await userEvent.keyboard("{Enter}");
		expect(onOpen).toHaveBeenLastCalledWith("b");

		// A new search starts at its first result, and holds that one.
		await userEvent.type(field(), "renamed");
		expect(selected()).toEqual(["Question c"]);
		const newerMatch = call({ id: "f", ...nango, outcome: "Renamed again", outcomeAt: "2026-11-18T11:40:00Z" });
		view.rerender(modal([...calls, newerInStream, newerMatch]));
		expect(titles()).toEqual(["Question f", "Question c"]);
		expect(selected()).toEqual(["Question c"]);
		await userEvent.clear(field());

		// So does widening past the stream.
		await userEvent.click(screen.getByRole("button", { name: /Show every done decision$/ }));
		expect(selected()).toEqual(["Question f"]);
		const newest = call({ id: "g", projectId: "shop", projectName: "Shop", outcome: "Newest of all", outcomeAt: "2026-11-18T11:50:00Z" });
		view.rerender(modal([...calls, newerInStream, newerMatch, newest]));
		expect(titles()[0]).toBe("Question g");
		expect(selected()).toEqual(["Question f"]);
		await userEvent.keyboard("{Enter}");
		expect(onOpen).toHaveBeenLastCalledWith("f");
	});

	it("keeps the stream's chip, and the way to widen it, when that stream's last done decision leaves", async () => {
		const modal = (list: DecisionCall[]) => <TooltipProvider><DecisionArchiveModal open entries={entriesOf(list)} scope={{ project: "cloud", stream: "h_a:nango" }} onOpen={vi.fn()} onOpenChange={vi.fn()} /></TooltipProvider>;
		const view = render(modal(calls));
		expect(titles()).toEqual(["Question b", "Question c"]);
		// Both age out of the board while the archive is open.
		view.rerender(modal(calls.filter((item) => item.streamSessionId !== "nango")));
		expect(within(grid()).queryAllByRole("gridcell")).toHaveLength(0);
		expect(screen.getByRole("status")).toHaveTextContent("No done decisions match");
		await userEvent.click(screen.getByRole("button", { name: "Showing skyvern-cloud: Nango. Show every done decision" }));
		expect(titles()).toEqual(["Question d", "Question a"]);
		expect(screen.queryByRole("button", { name: /Show every done decision$/ })).not.toBeInTheDocument();
	});

	it("is not narrowed to a stream that has no done decision to name it by", () => {
		renderModal({ scope: { project: "cloud", stream: "h_a:gone" } });
		expect(titles()).toEqual(["Question d", "Question b", "Question c", "Question a"]);
		expect(screen.queryByRole("button", { name: /Show every done decision$/ })).not.toBeInTheDocument();
	});

	it("leaves Shift with an arrow, Home or End to the search field's text", () => {
		renderModal();
		const selected = () => within(grid()).getAllByRole("gridcell").findIndex((cell) => cell.getAttribute("aria-selected") === "true");
		// fireEvent returns false when the key was taken.
		for (const key of ["ArrowLeft", "ArrowRight", "ArrowDown", "Home", "End"]) {
			expect(fireEvent.keyDown(field(), { key, shiftKey: true }), key).toBe(true);
		}
		expect(selected()).toBe(0);
		expect(fireEvent.keyDown(field(), { key: "ArrowDown" })).toBe(false);
		expect(selected()).toBe(1);
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
