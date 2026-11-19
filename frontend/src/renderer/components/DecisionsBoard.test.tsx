import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { buildDecisionBoard } from "../lib/decision-board";
import type { DecisionCall } from "../lib/decision-inbox";
import { DecisionsBoard } from "./DecisionsBoard";

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

	it("gives only Needs you the needs-you colour", () => {
		renderBoard();
		expect(screen.getByText("Waiting 3h")).toHaveClass("text-status-needs-you");
		for (const status of ["Sent to worker 5498", "Not delivered yet", "Migrated in PR #412"]) {
			expect(screen.getByText(status)).not.toHaveClass("text-status-needs-you");
		}
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
});
