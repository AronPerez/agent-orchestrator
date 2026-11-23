import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { buildDecisionBoard, PROJECT_WIDE_ROW } from "../lib/decision-board";
import type { DecisionCall } from "../lib/decision-inbox";
import { DECISION_LIST_FOLD_STORAGE_KEY, decisionScopeTree, type DecisionScope } from "../lib/decision-scope";
import { DecisionScopeList } from "./DecisionScopeList";
import { TooltipProvider } from "./ui/tooltip";

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
		ownerSessionId: "w-1",
		ownerSessionName: "worker 1",
		raisedAt: "2026-11-18T09:00:00Z",
		retired: false,
		...overrides,
	};
}
const cloud = { projectId: "cloud", projectName: "skyvern-cloud" };
const nango = { ...cloud, streamSessionId: "nango", streamName: "Nango" };
const tree = decisionScopeTree(buildDecisionBoard([
	call({ id: "wide", ...cloud }),
	call({ id: "need", ...nango }),
	call({ id: "sent", ...cloud, streamSessionId: "billing", streamName: "Billing", state: "in_progress", chosenLabel: "Ship", delivery: "sent", answeredAt: "2026-11-18T10:00:00Z" }),
	call({ id: "shop", projectId: "shop", projectName: "Shop", state: "done", outcome: "Shipped", outcomeAt: "2026-11-18T11:30:00Z" }),
	call({ id: "shop2", projectId: "shop", projectName: "Shop", streamSessionId: "epic", streamName: "Checkout", state: "done", outcome: "Shipped", outcomeAt: "2026-11-18T11:40:00Z" }),
]));

function renderList(scope: DecisionScope = {}) {
	const onChoose = vi.fn();
	const view = render(<TooltipProvider><DecisionScopeList tree={tree} scope={scope} onChoose={onChoose} /></TooltipProvider>);
	return { ...view, onChoose };
}
const item = (name: RegExp | string) => screen.getByRole("treeitem", { name });
const names = () => screen.getAllByRole("treeitem").map((row) => row.getAttribute("aria-label"));

describe("DecisionScopeList", () => {
	beforeEach(() => window.localStorage.clear());

	it("lists all projects, then each project with its streams, and says what needs the person or is open", () => {
		renderList();
		expect(screen.getByRole("tree", { name: "Projects and streams" })).toBeInTheDocument();
		// Shop has nothing open, so its streams start folded away.
		expect(names()).toEqual([
			"All projects, Needs you: 2",
			"skyvern-cloud, Needs you: 2",
			"Project-wide, Needs you: 1",
			"Nango, Needs you: 1",
			"Billing, Open: 1",
			"Shop",
		]);
		expect(item(/^skyvern-cloud/)).toHaveAttribute("aria-level", "1");
		expect(item(/^Nango/)).toHaveAttribute("aria-level", "2");
		expect(item(/^skyvern-cloud/)).toHaveAttribute("aria-expanded", "true");
		expect(item(/^Shop/)).toHaveAttribute("aria-expanded", "false");
	});

	it("marks the chosen scope, All projects when none is", () => {
		const { rerender, onChoose } = renderList();
		expect(item(/^All projects/)).toHaveAttribute("aria-selected", "true");
		rerender(<TooltipProvider><DecisionScopeList tree={tree} scope={{ project: "cloud", stream: "h_a:nango" }} onChoose={onChoose} /></TooltipProvider>);
		expect(item(/^Nango/)).toHaveAttribute("aria-selected", "true");
		expect(item(/^All projects/)).toHaveAttribute("aria-selected", "false");
		expect(item(/^skyvern-cloud/)).toHaveAttribute("aria-selected", "false");
	});

	it("chooses a project, a stream, the project's own row, or everything", async () => {
		const { onChoose } = renderList();
		await userEvent.click(item(/^skyvern-cloud/));
		expect(onChoose).toHaveBeenLastCalledWith({ project: "cloud" });
		await userEvent.click(item(/^Nango/));
		expect(onChoose).toHaveBeenLastCalledWith({ project: "cloud", stream: "h_a:nango" });
		await userEvent.click(item(/^Project-wide/));
		expect(onChoose).toHaveBeenLastCalledWith({ project: "cloud", stream: PROJECT_WIDE_ROW });
		await userEvent.click(item(/^All projects/));
		expect(onChoose).toHaveBeenLastCalledWith({});
	});

	it("folds a project's streams from its chevron without choosing it, and remembers the fold", async () => {
		const first = renderList();
		await userEvent.click(screen.getByRole("button", { name: "Hide streams in skyvern-cloud" }));
		expect(first.onChoose).not.toHaveBeenCalled();
		expect(item(/^skyvern-cloud/)).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByRole("treeitem", { name: /^Nango/ })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Show streams in Shop" }));
		expect(item(/^Checkout/)).toBeInTheDocument();
		expect(JSON.parse(window.localStorage.getItem(DECISION_LIST_FOLD_STORAGE_KEY) ?? "{}")).toEqual({ cloud: true, shop: false });

		first.unmount();
		renderList();
		expect(item(/^skyvern-cloud/)).toHaveAttribute("aria-expanded", "false");
		expect(item(/^Shop/)).toHaveAttribute("aria-expanded", "true");
	});

	it("always shows the chosen stream, even in a project folded by default", () => {
		renderList({ project: "shop", stream: "h_a:epic" });
		expect(item(/^Shop/)).toHaveAttribute("aria-expanded", "true");
		expect(item(/^Checkout/)).toHaveAttribute("aria-selected", "true");
	});

	it("is one tab stop, moved with the arrow keys, and chosen with Enter", async () => {
		const { onChoose } = renderList();
		const stops = () => screen.getAllByRole("treeitem").filter((row) => row.tabIndex === 0);
		expect(stops()).toEqual([item(/^All projects/)]);
		await userEvent.tab();
		expect(item(/^All projects/)).toHaveFocus();
		await userEvent.keyboard("{ArrowDown}{ArrowDown}");
		expect(item(/^Project-wide/)).toHaveFocus();
		expect(stops()).toEqual([item(/^Project-wide/)]);
		// Left goes to the project, then folds it; Right opens it and steps in.
		await userEvent.keyboard("{ArrowLeft}");
		expect(item(/^skyvern-cloud/)).toHaveFocus();
		await userEvent.keyboard("{ArrowLeft}");
		expect(item(/^skyvern-cloud/)).toHaveAttribute("aria-expanded", "false");
		await userEvent.keyboard("{ArrowRight}");
		expect(item(/^skyvern-cloud/)).toHaveAttribute("aria-expanded", "true");
		await userEvent.keyboard("{ArrowRight}{ArrowDown}{Enter}");
		expect(onChoose).toHaveBeenLastCalledWith({ project: "cloud", stream: "h_a:nango" });
		await userEvent.keyboard("{End}");
		expect(item(/^Shop/)).toHaveFocus();
		await userEvent.keyboard("{Home}");
		expect(item(/^All projects/)).toHaveFocus();
	});

	it("draws the needs-you count with its dot and an open count without one", () => {
		renderList();
		const needs = within(item(/^Nango/)).getByText("1");
		expect(needs).toHaveClass("text-status-needs-you");
		expect(item(/^Nango/).querySelector("[data-slot='decision-scope-dot']")).not.toBeNull();
		const open = within(item(/^Billing/)).getByText("1");
		expect(open).not.toHaveClass("text-status-needs-you");
		expect(item(/^Billing/).querySelector("[data-slot='decision-scope-dot']")).toBeNull();
	});
});
