import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useDecisionFolds } from "../hooks/useDecisionFolds";
import { buildDecisionBoard } from "../lib/decision-board";
import { DECISION_FOLD_STORAGE_KEY, decisionBandFoldKey, decisionRowFoldKey } from "../lib/decision-folds";
import type { DecisionCall } from "../lib/decision-inbox";
import { DecisionsBoard as BareBoard, type DecisionsBoardProps } from "./DecisionsBoard";
import { TooltipProvider } from "./ui/tooltip";

/** The board with the page's fold state, as DecisionsView mounts it. */
function DecisionsBoard(props: Omit<DecisionsBoardProps, "folds">) {
	const folds = useDecisionFolds(props.model, props.selectedId);
	return <TooltipProvider><BareBoard {...props} folds={folds} /></TooltipProvider>;
}

const NOW = Date.parse("2026-11-18T12:00:00Z");

function call(overrides: Partial<DecisionCall> & { id: string }): DecisionCall {
	return {
		link: `ao://decisions/${overrides.id}`,
		state: "needs_you",
		stateLabel: "Needs you",
		title: `Question ${overrides.id}`,
		context: "",
		options: [],
		links: [],
		hostId: "h_a",
		machineLabel: "mac-mini",
		ownerSessionId: "w-5512",
		ownerSessionName: "worker 5512",
		raisedAt: "2026-11-18T09:00:00Z",
		retired: false,
		...overrides,
	};
}

const calls: DecisionCall[] = [
	call({ id: "need", projectId: "cloud", projectName: "skyvern-cloud", streamSessionId: "nango", streamName: "Power BI and Nango" }),
	call({ id: "wide", projectId: "cloud", projectName: "skyvern-cloud", raisedAt: "2026-11-17T12:00:00Z" }),
	call({ id: "sent", projectId: "cloud", projectName: "skyvern-cloud", state: "in_progress", chosenLabel: "Retry with backoff", delivery: "sent", deliveredToName: "worker 5498", answeredAt: "2026-11-18T10:00:00Z" }),
	call({ id: "pending", projectId: "orch", projectName: "agent-orchestrator", state: "in_progress", chosenLabel: "Rename", delivery: "pending", answeredAt: "2026-11-18T10:00:00Z" }),
	call({ id: "orphan", projectId: "orch", projectName: "agent-orchestrator", state: "in_progress", choice: "other", delivery: "no_session", answeredAt: "2026-11-18T09:30:00Z" }),
	call({ id: "done", projectId: "orch", projectName: "agent-orchestrator", state: "done", chosenLabel: "Migrate", outcome: "Migrated in PR #412", outcomeAt: "2026-11-18T11:00:00Z", changedAfterClose: true }),
];

function renderBoard(props: Partial<Parameters<typeof DecisionsBoard>[0]> = {}) {
	const onSelect = vi.fn();
	const view = render(<DecisionsBoard model={buildDecisionBoard(calls)} now={NOW} onSelect={onSelect} {...props} />);
	return { ...view, onSelect };
}

describe("DecisionsBoard", () => {
	beforeEach(() => window.localStorage.clear());

	it("heads one continuous grid with the three states, their dots and exact counts", () => {
		renderBoard();
		const header = screen.getByRole("list", { name: "Decision states" });
		expect(within(header).getAllByRole("listitem").map((cell) => cell.textContent)).toEqual([
			"Needs you2",
			"In progress3",
			"Done1Last 7 days",
		]);
	});

	it("orders project bands and stream rows, with the project's own row first and unnamed", () => {
		renderBoard();
		const bands = screen.getAllByRole("region");
		expect(bands.map((band) => band.getAttribute("aria-label"))).toEqual(["skyvern-cloud", "agent-orchestrator"]);
		const rows = within(bands[0]).getAllByRole("group");
		expect(rows.map((row) => row.getAttribute("aria-label"))).toEqual(["Project-wide", "Power BI and Nango"]);
		expect(within(bands[0]).getByRole("button", { name: /skyvern-cloud/ })).toHaveTextContent("Needs you: 2");
	});

	it("shows each card's title, who or what, and its truthful status line", () => {
		renderBoard();
		const card = (id: string) => screen.getByRole("button", { name: new RegExp(`^Question ${id}`) });
		expect(card("need")).toHaveTextContent("mac-mini · worker 5512");
		expect(card("need")).toHaveTextContent("Waiting 3h");
		expect(card("wide")).toHaveTextContent("Waiting 1d");
		expect(card("sent")).toHaveTextContent("Retry with backoff");
		expect(card("sent")).toHaveTextContent("Sent to worker 5498");
		expect(card("pending")).toHaveTextContent("Not delivered yet");
		expect(card("orphan")).toHaveTextContent("Something else");
		expect(card("orphan")).toHaveTextContent("No session to carry this out");
		expect(card("done")).toHaveTextContent("Migrated in PR #412");
		expect(within(card("done")).getByText("Changed after it closed")).toBeInTheDocument();
	});

	it("draws only the title on a card and keeps the rest for assistive tech", () => {
		renderBoard();
		const card = screen.getByRole("button", { name: /^Question need/ });
		expect(within(card).getByText("Question need")).not.toHaveClass("sr-only");
		expect(within(card).getByText("mac-mini · worker 5512")).toHaveClass("sr-only");
		expect(within(card).getByText("Waiting 3h")).toHaveClass("sr-only");
	});

	it("shows a card's details when it takes keyboard focus, not only on hover", async () => {
		renderBoard();
		// A native title tooltip never opens for the keyboard, so there is none.
		expect(screen.getByRole("button", { name: /^Question need/ })).not.toHaveAttribute("title");
		expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
		act(() => screen.getByRole("button", { name: /^Question need/ }).focus());
		const details = await screen.findByRole("tooltip");
		expect(details).toHaveTextContent("Question need");
		expect(details).toHaveTextContent("mac-mini · worker 5512");
		expect(details).toHaveTextContent("Waiting 3h");

		// Arrowing on moves the details to the next card, flags included.
		act(() => screen.getByRole("button", { name: /^Question done/ }).focus());
		const next = await screen.findByRole("tooltip");
		expect(next).toHaveTextContent("Question done");
		expect(next).toHaveTextContent("Migrated in PR #412");
		expect(next).toHaveTextContent("Changed after it closed");
		expect(next).not.toHaveTextContent("Waiting 3h");
	});

	it("still says on the card when an answer has not reached a session", () => {
		renderBoard();
		const card = (id: string) => screen.getByRole("button", { name: new RegExp(`^Question ${id}`) });
		expect(within(card("pending")).getByText("Not delivered yet")).not.toHaveClass("sr-only");
		expect(within(card("orphan")).getByText("No session to carry this out")).not.toHaveClass("sr-only");
		expect(within(card("sent")).getByText("Sent to worker 5498")).toHaveClass("sr-only");
		expect(within(card("done")).getByText("Migrated in PR #412")).toHaveClass("sr-only");
	});

	it("folds a stream to its counts and opens it again", async () => {
		renderBoard();
		const row = screen.getByRole("group", { name: "Power BI and Nango" });
		const toggle = within(row).getByRole("button", { name: /Power BI and Nango/ });
		expect(toggle).toHaveAttribute("aria-expanded", "true");
		await userEvent.click(toggle);
		expect(toggle).toHaveAttribute("aria-expanded", "false");
		expect(toggle).toHaveTextContent("Needs you: 1");
		expect(screen.queryByRole("button", { name: /^Question need/ })).not.toBeInTheDocument();
		expect(within(row).getAllByTestId("decision-fold-count").map((cell) => cell.textContent)).toEqual(["1", "", ""]);
		await userEvent.click(toggle);
		expect(screen.getByRole("button", { name: /^Question need/ })).toBeInTheDocument();
	});

	it("lets the project's own row fold too", async () => {
		renderBoard();
		const row = within(screen.getByRole("region", { name: "skyvern-cloud" })).getByRole("group", { name: "Project-wide" });
		await userEvent.click(within(row).getByRole("button", { name: /Project-wide/ }));
		expect(screen.queryByRole("button", { name: /^Question wide/ })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: /^Question need/ })).toBeInTheDocument();
	});

	it("starts a project folded when nothing in it is open, and shows its counts in the state columns", () => {
		const finished = call({ id: "fin", projectId: "shop", projectName: "Shop", state: "done", outcome: "Shipped", outcomeAt: "2026-11-18T11:00:00Z" });
		render(<DecisionsBoard model={buildDecisionBoard([...calls, finished])} now={NOW} onSelect={vi.fn()} />);
		const band = screen.getByRole("region", { name: "Shop" });
		expect(within(band).getByRole("button", { name: /Shop/ })).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByRole("button", { name: /^Question fin/ })).not.toBeInTheDocument();
		expect(within(band).getAllByTestId("decision-band-count").map((cell) => cell.textContent)).toEqual(["", "", "1"]);
		const open = screen.getByRole("region", { name: "skyvern-cloud" });
		expect(within(open).getAllByTestId("decision-band-count").map((cell) => cell.textContent)).toEqual(["2", "1", ""]);
	});

	it("saves folds and restores them on the next visit", async () => {
		const first = renderBoard();
		await userEvent.click(screen.getByRole("button", { name: /agent-orchestrator/ }));
		await userEvent.click(within(screen.getByRole("group", { name: "Power BI and Nango" })).getByRole("button", { name: /Power BI and Nango/ }));
		expect(JSON.parse(window.localStorage.getItem(DECISION_FOLD_STORAGE_KEY) ?? "{}")).toEqual({
			[decisionBandFoldKey("orch")]: true,
			[decisionRowFoldKey("cloud", "h_a:nango")]: true,
		});
		first.unmount();
		renderBoard();
		expect(screen.getByRole("button", { name: /agent-orchestrator/ })).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByRole("button", { name: /^Question need/ })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: /^Question wide/ })).toBeInTheDocument();
	});

	it("opens a folded stream when one of its calls is selected", async () => {
		const { rerender, onSelect } = renderBoard();
		await userEvent.click(within(screen.getByRole("group", { name: "Power BI and Nango" })).getByRole("button", { name: /Power BI and Nango/ }));
		expect(screen.queryByRole("button", { name: /^Question need/ })).not.toBeInTheDocument();
		rerender(<DecisionsBoard model={buildDecisionBoard(calls)} now={NOW} onSelect={onSelect} selectedId="need" />);
		expect(screen.getByRole("button", { name: /^Question need/ })).toHaveAttribute("aria-current", "true");
	});

	it("keeps the selected call on screen when it finishes and was the last open call in its stream", async () => {
		const { rerender, onSelect } = renderBoard({ selectedId: "need" });
		const finished = calls.map((item) => item.id === "need"
			? { ...item, state: "done" as const, stateLabel: "Done", chosenLabel: "Ship", outcome: "Shipped", answeredAt: "2026-11-18T11:30:00Z", outcomeAt: "2026-11-18T11:45:00Z" }
			: item);
		rerender(<DecisionsBoard model={buildDecisionBoard(finished)} now={NOW} onSelect={onSelect} selectedId="need" />);
		const row = screen.getByRole("group", { name: "Power BI and Nango" });
		const toggle = within(row).getByRole("button", { name: /Power BI and Nango/ });
		expect(toggle).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByRole("button", { name: /^Question need/ })).toHaveAttribute("aria-current", "true");

		// Folding it afterwards is still the person's choice, and a later refresh leaves it folded.
		await userEvent.click(toggle);
		expect(toggle).toHaveAttribute("aria-expanded", "false");
		rerender(<DecisionsBoard model={buildDecisionBoard(finished)} now={NOW + 60_000} onSelect={onSelect} selectedId="need" />);
		expect(within(screen.getByRole("group", { name: "Power BI and Nango" })).getByRole("button", { name: /Power BI and Nango/ })).toHaveAttribute("aria-expanded", "false");
	});

	describe("done cap", () => {
		const finished = [1, 2, 3, 4, 5].map((minute) => call({
			id: `fin${minute}`, projectId: "cloud", projectName: "skyvern-cloud", state: "done", outcome: "Shipped", outcomeAt: `2026-11-18T11:0${minute}:00Z`,
		}));
		const many = [...calls, ...finished];
		const doneCards = () => screen.getAllByRole("button").filter((button) => /^Question fin/.test(button.textContent ?? "")).map((button) => button.getAttribute("data-decision-card"));

		it("shows a row's three newest Done cards, then the rest on request", async () => {
			render(<DecisionsBoard model={buildDecisionBoard(many)} now={NOW} onSelect={vi.fn()} />);
			expect(doneCards()).toEqual(["fin5", "fin4", "fin3"]);
			const more = screen.getByRole("button", { name: "2 more" });
			expect(more).toHaveAttribute("aria-expanded", "false");
			await userEvent.click(more);
			expect(doneCards()).toEqual(["fin5", "fin4", "fin3", "fin2", "fin1"]);
			await userEvent.click(screen.getByRole("button", { name: "Show fewer" }));
			expect(doneCards()).toEqual(["fin5", "fin4", "fin3"]);
		});

		it("offers no button when a row has three or fewer", () => {
			renderBoard();
			expect(screen.queryByRole("button", { name: /more$/ })).not.toBeInTheDocument();
		});

		it("shows a selected Done card even when it is past the cap", () => {
			render(<DecisionsBoard model={buildDecisionBoard(many)} now={NOW} onSelect={vi.fn()} selectedId="fin1" />);
			expect(screen.getByRole("button", { name: /^Question fin1/ })).toHaveAttribute("aria-current", "true");
		});

		it("reopens the cell when the selection moves to another card past the cap in the same row", async () => {
			const view = render(<DecisionsBoard model={buildDecisionBoard(many)} now={NOW} onSelect={vi.fn()} selectedId="fin1" />);
			const select = (id: string) => view.rerender(<DecisionsBoard model={buildDecisionBoard(many)} now={NOW} onSelect={vi.fn()} selectedId={id} />);
			select("fin2");
			expect(screen.getByRole("button", { name: /^Question fin2/ })).toHaveAttribute("aria-current", "true");
			await userEvent.click(screen.getByRole("button", { name: "Show fewer" }));
			expect(screen.queryByRole("button", { name: /^Question fin2/ })).not.toBeInTheDocument();
			select("fin1");
			expect(screen.getByRole("button", { name: /^Question fin1/ })).toHaveAttribute("aria-current", "true");
		});

		it("keeps arrow keys to the cards on screen", async () => {
			render(<DecisionsBoard model={buildDecisionBoard(many)} now={NOW} onSelect={vi.fn()} />);
			screen.getByRole("button", { name: /^Question fin3/ }).focus();
			await userEvent.keyboard("{ArrowDown}");
			expect(screen.getByRole("button", { name: /^Question done/ })).toHaveFocus();
		});
	});

	it("selects a card through the route and marks the selected one", async () => {
		const { onSelect } = renderBoard({ selectedId: "sent" });
		expect(screen.getByRole("button", { name: /^Question sent/ })).toHaveAttribute("aria-current", "true");
		expect(screen.getByRole("button", { name: /^Question need/ })).not.toHaveAttribute("aria-current");
		await userEvent.click(screen.getByRole("button", { name: /^Question need/ }));
		expect(onSelect).toHaveBeenCalledWith("need");
	});

	it("collapses a band and reopens it when one of its calls is selected", async () => {
		const { rerender, onSelect } = renderBoard();
		const toggle = screen.getByRole("button", { name: /agent-orchestrator/ });
		expect(toggle).toHaveAttribute("aria-expanded", "true");
		await userEvent.click(toggle);
		expect(toggle).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByRole("button", { name: /^Question done/ })).not.toBeInTheDocument();

		rerender(<DecisionsBoard model={buildDecisionBoard(calls)} now={NOW} onSelect={onSelect} selectedId="done" />);
		expect(screen.getByRole("button", { name: /agent-orchestrator/ })).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByRole("button", { name: /^Question done/ })).toHaveAttribute("aria-current", "true");
	});

	it("moves a stream's name above its row in a narrow pane", () => {
		const { container } = renderBoard();
		expect(container.firstElementChild).toHaveClass("@container");
		const name = screen.getByText("Power BI and Nango");
		expect(name.closest("[data-slot='decision-row-name']")).toHaveClass("col-span-full", "@3xl:col-span-1");
	});

	it("keeps cards as the only bordered objects in the grid", () => {
		const { container } = renderBoard();
		expect(container.querySelectorAll(".rounded-md.border")).toHaveLength(calls.length);
	});

	it("tells same-named streams on two machines apart", () => {
		render(<DecisionsBoard
			model={buildDecisionBoard([
				call({ id: "here", projectId: "shop", projectName: "Shop", streamSessionId: "epic", streamName: "Checkout" }),
				call({ id: "there", projectId: "shop", projectName: "Shop", streamSessionId: "epic", streamName: "Checkout", hostId: "h_b", machineLabel: "laptop" }),
			])}
			now={NOW}
			onSelect={vi.fn()}
		/>);
		expect(screen.getAllByRole("group").map((row) => row.getAttribute("aria-label")).filter((label) => label?.startsWith("Checkout"))).toEqual([
			"Checkout · mac-mini",
			"Checkout · laptop",
		]);
	});

	describe("keyboard", () => {
		const card = (id: string) => screen.getByRole("button", { name: new RegExp(`^Question ${id}`) });
		const tabStops = () => screen.getAllByRole("button").filter((button) => button.hasAttribute("data-decision-card") && button.tabIndex === 0);

		it("has one tab stop: the selected card, else the first Needs you card", () => {
			const { rerender, onSelect } = renderBoard();
			expect(tabStops()).toEqual([card("wide")]);
			rerender(<DecisionsBoard model={buildDecisionBoard(calls)} now={NOW} onSelect={onSelect} selectedId="sent" />);
			expect(tabStops()).toEqual([card("sent")]);
		});

		it("moves with the arrow keys down a lane and across to the next lane in the row", async () => {
			renderBoard();
			card("wide").focus();
			await userEvent.keyboard("{ArrowDown}");
			expect(card("need")).toHaveFocus();
			await userEvent.keyboard("{ArrowUp}{ArrowRight}");
			expect(card("sent")).toHaveFocus();
			await userEvent.keyboard("{ArrowDown}");
			expect(card("pending")).toHaveFocus();
			await userEvent.keyboard("{ArrowRight}");
			expect(card("done")).toHaveFocus();
			await userEvent.keyboard("{ArrowRight}");
			expect(card("done")).toHaveFocus();
			expect(tabStops()).toEqual([card("done")]);
		});

		it("skips folded bands", async () => {
			renderBoard();
			await userEvent.click(screen.getByRole("button", { name: /agent-orchestrator/ }));
			card("sent").focus();
			await userEvent.keyboard("{ArrowDown}");
			expect(card("sent")).toHaveFocus();
		});

		it("keeps focus on the board when the focused card leaves", async () => {
			const { rerender, onSelect } = renderBoard();
			card("pending").focus();
			rerender(<DecisionsBoard model={buildDecisionBoard(calls.filter((item) => item.id !== "pending"))} now={NOW} onSelect={onSelect} />);
			expect(card("orphan")).toHaveFocus();
			expect(tabStops()).toEqual([card("orphan")]);
		});

		it("keeps focus on a card that moves to another lane", async () => {
			const { rerender, onSelect } = renderBoard();
			card("need").focus();
			const moved = calls.map((item) => item.id === "need" ? { ...item, state: "in_progress" as const, stateLabel: "In progress", chosenLabel: "Ship", delivery: "pending" as const, answeredAt: "2026-11-18T11:30:00Z" } : item);
			rerender(<DecisionsBoard model={buildDecisionBoard(moved)} now={NOW} onSelect={onSelect} />);
			expect(card("need")).toHaveFocus();
		});

		it("opens a card with Enter", async () => {
			const { onSelect } = renderBoard();
			card("need").focus();
			await userEvent.keyboard("{Enter}");
			expect(onSelect).toHaveBeenCalledWith("need");
		});
	});

	describe("decided for you", () => {
		const decided = (id: string, detail: Partial<NonNullable<DecisionCall["decided"]>> = {}) => call({
			id, state: "decided", stateLabel: "Decided for you", raisedAt: "2026-11-18T10:00:00Z",
			decided: { undo: "Reopen it", carriedOut: false, ...detail },
		});

		it("adds the Decided for you lane only while decisions stand", () => {
			const { rerender, onSelect } = renderBoard();
			expect(within(screen.getByRole("list", { name: "Decision states" })).getAllByRole("listitem")).toHaveLength(3);
			rerender(<DecisionsBoard model={buildDecisionBoard([...calls, decided("dec_a")])} now={NOW} onSelect={onSelect} />);
			expect(within(screen.getByRole("list", { name: "Decision states" })).getAllByRole("listitem").map((item) => item.textContent)).toEqual([
				"Needs you2",
				"Decided for you1",
				"In progress3",
				"Done1Last 7 days",
			]);
		});

		it("says whether a decision was carried out and flags one outside policy", () => {
			render(<DecisionsBoard
				model={buildDecisionBoard([decided("dec_a", { outsidePolicy: "kind_reserved" }), decided("dec_b", { carriedOut: true, carriedOutAt: "2026-11-18T10:05:00Z" })])}
				now={NOW}
				onSelect={vi.fn()}
			/>);
			const pending = screen.getByRole("button", { name: /^Question dec_a/ });
			expect(pending).toHaveTextContent("mac-mini · worker 5512");
			expect(pending).toHaveTextContent("Not confirmed yet");
			expect(within(pending).getByText("Recorded outside this project's policy")).toBeInTheDocument();
			const confirmed = screen.getByRole("button", { name: /^Question dec_b/ });
			expect(confirmed).toHaveTextContent("Carried out");
			expect(within(confirmed).queryByText("Recorded outside this project's policy")).not.toBeInTheDocument();
			expect(screen.getByText("Not confirmed yet")).not.toHaveClass("text-status-needs-you");
		});
	});
});
