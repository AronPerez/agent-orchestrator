import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DecisionCall } from "../lib/decision-inbox";

const { getMock, navigateMock } = vi.hoisted(() => ({ getMock: vi.fn(), navigateMock: vi.fn() }));
vi.mock("../lib/host-clients", () => ({ clientForSessionHost: () => ({ GET: getMock }) }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigateMock }));
vi.mock("../hooks/useDecisionSessionTarget", () => ({ useDecisionSessionTarget: () => ({ kind: "checking" }) }));
vi.mock("./PageTopbar", () => ({
	PageTopbar: ({ lead, actions }: { lead?: ReactNode; actions?: ReactNode }) => <header>{lead}{actions}</header>,
	PageTopbarTitle: ({ children }: { children: ReactNode }) => <h1>{children}</h1>,
}));

import { useDecisionBoard } from "../hooks/useDecisionInbox";
import { TooltipProvider } from "./ui/tooltip";
import { DECISION_FOLD_STORAGE_KEY, decisionRowFoldKey } from "../lib/decision-folds";
import { DECISION_TABS_STORAGE_KEY } from "../lib/decision-tabs";
import { DecisionsView } from "./DecisionsView";

function call(overrides: Partial<DecisionCall> & { id: string }): DecisionCall {
	return {
		link: `ao://decisions/${overrides.id}`,
		state: "needs_you",
		stateLabel: "Needs you",
		title: `Question ${overrides.id}`,
		context: "",
		options: [],
		links: [],
		projectId: "shop",
		projectName: "Shop",
		hostId: "h_a",
		machineLabel: "mac-mini",
		ownerSessionId: "w-1",
		ownerSessionName: "worker 1",
		raisedAt: "2026-11-18T09:00:00Z",
		retired: false,
		...overrides,
	};
}

const board = (calls: DecisionCall[]) => ({
	data: {
		calls,
		needsYouCount: calls.filter((item) => item.state === "needs_you").length,
		servingHostId: "h_a",
		doneSince: "2026-11-11T12:00:00Z",
	},
});

function answer(result: unknown, details: Record<string, DecisionCall> = {}) {
	getMock.mockImplementation((path: string, request?: { params?: { path?: { id?: string } } }) => {
		if (path === "/api/v1/decisions/board") return Promise.resolve(result);
		const found = details[request?.params?.path?.id ?? ""];
		if (found) return Promise.resolve({ data: { call: found, history: [] }, response: { status: 200 } });
		return Promise.resolve({ error: { code: "DECISION_NOT_FOUND", message: "Unknown decision" }, response: { status: 404 } });
	});
}

const viewClients = new WeakMap<object, QueryClient>();
function renderView(node: ReactNode) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const view = render(<QueryClientProvider client={client}><TooltipProvider>{node}</TooltipProvider></QueryClientProvider>);
	viewClients.set(view, client);
	return view;
}
const clientOf = (view: object) => viewClients.get(view)!;

const boardFetches = () => getMock.mock.calls.filter(([path]) => path === "/api/v1/decisions/board").length;

beforeEach(() => {
	getMock.mockReset();
	navigateMock.mockReset();
	window.localStorage.clear();
});

describe("DecisionsView", () => {
	it("titles the page and says whose ledger it shows", async () => {
		answer(board([call({ id: "dec_1" })]));
		renderView(<DecisionsView />);
		expect(screen.getByRole("heading", { level: 1, name: "Decisions" })).toBeInTheDocument();
		expect(await screen.findByText("Decisions on this computer")).toBeInTheDocument();
	});

	it("shows a loading board aligned to the grid, then the calls", async () => {
		let resolve!: (value: unknown) => void;
		getMock.mockReturnValue(new Promise((done) => { resolve = done; }));
		renderView(<DecisionsView />);
		expect(screen.getByRole("status", { name: "Loading decisions" })).toBeInTheDocument();
		resolve(board([call({ id: "dec_1" })]));
		expect(await screen.findByRole("button", { name: /^Question dec_1/ })).toBeInTheDocument();
	});

	it("explains an unreachable daemon with its request id and retries in place", async () => {
		answer({ error: { error: "internal", code: "DECISION_BOARD_FAILED", message: "Unable to load decisions", requestId: "req-42" } });
		renderView(<DecisionsView />);
		const alert = await screen.findByRole("alert", undefined, { timeout: 4_000 });
		expect(alert).toHaveTextContent("Unable to load decisions");
		expect(alert).toHaveTextContent("Request req-42");
		answer(board([call({ id: "dec_1" })]));
		await userEvent.click(within(alert).getByRole("button", { name: "Retry" }));
		expect(await screen.findByRole("button", { name: /^Question dec_1/ })).toBeInTheDocument();
	});

	it("tells an empty ledger how calls arrive, with no fake create action", async () => {
		answer(board([]));
		renderView(<DecisionsView />);
		expect(await screen.findByRole("heading", { name: "No decisions yet" })).toBeInTheDocument();
		expect(screen.getByText(/ao decision raise/)).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /raise|create|new/i })).not.toBeInTheDocument();
	});

	it("says quietly when nothing needs the person while other lanes still show", async () => {
		answer(board([call({ id: "dec_prog", state: "in_progress", chosenLabel: "Ship", delivery: "pending", answeredAt: "2026-11-18T10:00:00Z" })]));
		renderView(<DecisionsView />);
		expect(await screen.findByRole("status", { name: "Nothing needs you" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /^Question dec_prog/ })).toBeInTheDocument();
	});

	it("folds and opens every project and stream from the top bar", async () => {
		answer(board([call({ id: "dec_1" }), call({ id: "dec_2", projectId: "cloud", projectName: "skyvern-cloud", streamSessionId: "nango", streamName: "Nango" })]));
		renderView(<DecisionsView />);
		expect(await screen.findByRole("button", { name: /^Question dec_1/ })).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Collapse all" }));
		expect(screen.queryByRole("button", { name: /^Question dec_/ })).not.toBeInTheDocument();
		const onBoard = within(screen.getByRole("group", { name: "Decisions board" }));
		expect(onBoard.getByRole("button", { name: /Shop/ })).toHaveAttribute("aria-expanded", "false");
		expect(onBoard.getByRole("button", { name: /skyvern-cloud/ })).toHaveAttribute("aria-expanded", "false");
		await userEvent.click(screen.getByRole("button", { name: "Expand all" }));
		expect(screen.getByRole("button", { name: /^Question dec_1/ })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /^Question dec_2/ })).toBeInTheDocument();
	});

	it("offers no fold controls while searching or before any decision exists", async () => {
		answer(board([]));
		renderView(<DecisionsView />);
		expect(await screen.findByText(/ao decision raise/)).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Collapse all" })).not.toBeInTheDocument();
	});

	describe("project and stream list", () => {
		const cloud = { projectId: "cloud", projectName: "skyvern-cloud" };
		const ledger = () => board([
			call({ id: "dec_shop" }),
			call({ id: "dec_wide", ...cloud }),
			call({ id: "dec_nango", ...cloud, streamSessionId: "nango", streamName: "Nango" }),
			call({ id: "dec_done", ...cloud, streamSessionId: "nango", streamName: "Nango", state: "done", outcome: "Shipped", outcomeAt: "2026-11-18T11:00:00Z" }),
		]);
		const states = () => within(screen.getByRole("list", { name: "Decision states" })).getAllByRole("listitem").map((cell) => cell.textContent);

		it("sits beside the board and narrows it to the project in the URL, counts included", async () => {
			answer(ledger());
			renderView(<DecisionsView project="cloud" />);
			expect(await screen.findByRole("button", { name: /^Question dec_wide/ })).toBeInTheDocument();
			expect(screen.getByRole("treeitem", { name: /^skyvern-cloud/ })).toHaveAttribute("aria-selected", "true");
			expect(screen.queryByRole("button", { name: /^Question dec_shop/ })).not.toBeInTheDocument();
			expect(states()).toEqual(["Needs you2", "In progress0", "Done1Last 7 days"]);
			// The list names the project, so the board leaves its header out.
			expect(within(screen.getByRole("group", { name: "Decisions board" })).queryByRole("button", { name: /skyvern-cloud/ })).not.toBeInTheDocument();
		});

		it("narrows to one stream", async () => {
			answer(ledger());
			renderView(<DecisionsView project="cloud" stream="h_a:nango" />);
			expect(await screen.findByRole("button", { name: /^Question dec_nango/ })).toBeInTheDocument();
			expect(screen.queryByRole("button", { name: /^Question dec_wide/ })).not.toBeInTheDocument();
			expect(screen.getByRole("treeitem", { name: /^Nango/ })).toHaveAttribute("aria-selected", "true");
			expect(states()).toEqual(["Needs you1", "In progress0", "Done1Last 7 days"]);
		});

		it("shows every project for a scope that is no longer on the board", async () => {
			answer(ledger());
			renderView(<DecisionsView project="gone" />);
			expect(await screen.findByRole("button", { name: /^Question dec_shop/ })).toBeInTheDocument();
			expect(screen.getByRole("treeitem", { name: /^All projects/ })).toHaveAttribute("aria-selected", "true");
		});

		it("writes the chosen row to the URL, keeps the open call, and opens a chosen stream's row", async () => {
			answer(ledger(), { dec_shop: call({ id: "dec_shop" }) });
			// The Nango row was folded on an earlier visit.
			window.localStorage.setItem(DECISION_FOLD_STORAGE_KEY, JSON.stringify({ [decisionRowFoldKey("cloud", "h_a:nango")]: true }));
			const view = renderView(<DecisionsView callId="dec_shop" />);
			await screen.findByRole("button", { name: /^Question dec_shop/ });
			await userEvent.click(screen.getByRole("treeitem", { name: /^skyvern-cloud/ }));
			expect(navigateMock).toHaveBeenLastCalledWith({ to: "/decisions", search: { call: "dec_shop", project: "cloud" } });
			await userEvent.click(screen.getByRole("treeitem", { name: /^Nango/ }));
			expect(navigateMock).toHaveBeenLastCalledWith({ to: "/decisions", search: { call: "dec_shop", project: "cloud", stream: "h_a:nango" } });
			view.rerender(<QueryClientProvider client={clientOf(view)}><TooltipProvider><DecisionsView callId="dec_shop" project="cloud" stream="h_a:nango" /></TooltipProvider></QueryClientProvider>);
			expect(await screen.findByRole("button", { name: /^Question dec_nango/ })).toBeInTheDocument();
			await userEvent.click(screen.getByRole("treeitem", { name: /^All projects/ }));
			expect(navigateMock).toHaveBeenLastCalledWith({ to: "/decisions", search: { call: "dec_shop" } });
		});

		it("keeps the scope when a card is opened or closed", async () => {
			answer(ledger(), { dec_wide: call({ id: "dec_wide", ...cloud }) });
			const view = renderView(<DecisionsView project="cloud" />);
			await userEvent.click(await screen.findByRole("button", { name: /^Question dec_wide/ }));
			expect(navigateMock).toHaveBeenLastCalledWith({ to: "/decisions", search: { call: "dec_wide", project: "cloud" } });
			view.rerender(<QueryClientProvider client={clientOf(view)}><TooltipProvider><DecisionsView callId="dec_wide" project="cloud" /></TooltipProvider></QueryClientProvider>);
			await userEvent.click(await screen.findByRole("button", { name: "Close decision" }));
			expect(navigateMock).toHaveBeenLastCalledWith({ to: "/decisions", search: { project: "cloud" } });
		});

		it("hides from its top-bar button and stays hidden on the next visit", async () => {
			answer(ledger());
			const first = renderView(<DecisionsView />);
			await screen.findByRole("tree", { name: "Projects and streams" });
			await userEvent.click(screen.getByRole("button", { name: "Hide project list" }));
			expect(screen.queryByRole("tree", { name: "Projects and streams" })).not.toBeInTheDocument();
			first.unmount();
			renderView(<DecisionsView />);
			await screen.findByRole("button", { name: /^Question dec_shop/ });
			expect(screen.queryByRole("tree", { name: "Projects and streams" })).not.toBeInTheDocument();
			await userEvent.click(screen.getByRole("button", { name: "Show project list" }));
			expect(screen.getByRole("tree", { name: "Projects and streams" })).toBeInTheDocument();
		});

		it("names the chosen scope above the board with a way back to every project, for when the list is not showing", async () => {
			answer(ledger());
			renderView(<DecisionsView project="cloud" stream="h_a:nango" />);
			const chip = await screen.findByRole("button", { name: "Showing skyvern-cloud: Nango. Show all projects" });
			expect(chip).toHaveTextContent("skyvern-cloud:Nango");
			// While the list has room it names the scope itself, so the chip gives way to it.
			expect(chip.parentElement).toHaveClass("@2xl/decisions:hidden");
			// Hidden by its button, the list is gone at every width, so the chip stays.
			await userEvent.click(screen.getByRole("button", { name: "Hide project list" }));
			expect(screen.getByRole("button", { name: /^Showing skyvern-cloud: Nango/ }).parentElement).not.toHaveClass("@2xl/decisions:hidden");
			await userEvent.click(screen.getByRole("button", { name: /^Showing skyvern-cloud: Nango/ }));
			expect(navigateMock).toHaveBeenLastCalledWith({ to: "/decisions", search: {} });
		});

		it("names a chosen project alone, and shows no chip while every project is shown", async () => {
			answer(ledger());
			const view = renderView(<DecisionsView project="cloud" />);
			expect(await screen.findByRole("button", { name: "Showing skyvern-cloud. Show all projects" })).toHaveTextContent("skyvern-cloud");
			view.rerender(<QueryClientProvider client={clientOf(view)}><TooltipProvider><DecisionsView /></TooltipProvider></QueryClientProvider>);
			await screen.findByRole("button", { name: /^Question dec_shop/ });
			expect(screen.queryByRole("button", { name: /Show all projects$/ })).not.toBeInTheDocument();
		});

		it("leaves with the board during a search, and is not offered before any decision exists", async () => {
			answer(ledger());
			renderView(<DecisionsView query="auth" project="cloud" />);
			await waitFor(() => expect(getMock).toHaveBeenCalled());
			expect(screen.queryByRole("tree", { name: "Projects and streams" })).not.toBeInTheDocument();
			expect(screen.queryByRole("button", { name: "Hide project list" })).not.toBeInTheDocument();
		});
	});

	describe("decision tabs", () => {
		const option = [{ label: "Ship", consequence: "Release today.", recommended: true }];
		const one = call({ id: "dec_1", options: option });
		const two = call({ id: "dec_2", options: option, raisedAt: "2026-11-18T10:00:00Z" });
		const details = { dec_1: one, dec_2: two };
		const view = (props: Parameters<typeof DecisionsView>[0] = {}) => <QueryClientProvider client={client}><TooltipProvider><DecisionsView {...props} /></TooltipProvider></QueryClientProvider>;
		let client: QueryClient;
		beforeEach(() => { client = new QueryClient({ defaultOptions: { queries: { retry: false } } }); });
		const strip = () => screen.getByRole("tablist", { name: "Board and open decisions" });
		const tabNames = () => within(strip()).getAllByRole("tab").map((tab) => tab.textContent);
		const boardGroup = () => screen.getByRole("group", { name: "Decisions board", hidden: true });
		const kept = () => JSON.parse(window.localStorage.getItem(DECISION_TABS_STORAGE_KEY) ?? "[]");

		it("has no strip until a decision is kept open from the panel beside the board", async () => {
			answer(board([one, two]), details);
			render(view({ callId: "dec_1" }));
			const panel = await screen.findByRole("complementary", { name: "Decision" });
			expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
			await userEvent.click(await within(panel).findByRole("button", { name: "Keep open" }));
			expect(tabNames()).toEqual(["Board", "Question dec_1"]);
			expect(within(strip()).getByRole("tab", { name: "Board" })).toHaveAttribute("aria-selected", "true");
			expect(kept()).toEqual(["dec_1"]);
			// Keeping is not leaving: the same panel stays beside the board, now marked.
			expect(within(panel).getByText("Kept open")).toBeInTheDocument();
			expect(within(panel).queryByRole("button", { name: "Keep open" })).not.toBeInTheDocument();
			expect(screen.getByRole("button", { name: /^Question dec_1/ })).toHaveAttribute("data-kept", "true");
			expect(navigateMock).not.toHaveBeenCalled();
		});

		it("keeps a card on double-click, and keeps tabs across visits and links", async () => {
			answer(board([one, two]), details);
			const first = render(view());
			await userEvent.dblClick(await screen.findByRole("button", { name: /^Question dec_2/ }));
			expect(kept()).toEqual(["dec_2"]);
			first.unmount();
			// A link or the bell opens another call; the kept tab is still there.
			render(view({ callId: "dec_1" }));
			await screen.findByRole("button", { name: /^Question dec_1/ });
			expect(tabNames()).toEqual(["Board", "Question dec_2"]);
		});

		it("fills the page with a kept decision when its tab is the one in the URL", async () => {
			window.localStorage.setItem(DECISION_TABS_STORAGE_KEY, JSON.stringify(["dec_1", "dec_2"]));
			answer(board([one, two]), details);
			render(view({ tab: "dec_2" }));
			const page = await screen.findByRole("complementary", { name: "Decision" });
			expect(await within(page).findByRole("heading", { level: 2, name: "Question dec_2" })).toBeInTheDocument();
			expect(within(strip()).getByRole("tab", { name: "Question dec_2" })).toHaveAttribute("aria-selected", "true");
			expect(within(strip()).getByRole("tab", { name: "Board" })).toHaveAttribute("aria-selected", "false");
			expect(boardGroup()).not.toBeVisible();
			// Its tab closes it; the page has no panel close, and no board controls.
			expect(within(page).queryByRole("button", { name: "Close decision" })).not.toBeInTheDocument();
			expect(screen.queryByRole("button", { name: "Collapse all" })).not.toBeInTheDocument();
			expect(screen.queryByRole("tree", { name: "Projects and streams" })).not.toBeInTheDocument();
		});

		it("shows the board for a tab in the URL that is not kept", async () => {
			answer(board([one, two]), details);
			render(view({ tab: "dec_2" }));
			expect(await screen.findByRole("button", { name: /^Question dec_2/ })).toBeVisible();
			expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
		});

		it("writes the chosen tab to the URL and the board's tab clears it, keeping the rest", async () => {
			window.localStorage.setItem(DECISION_TABS_STORAGE_KEY, JSON.stringify(["dec_1"]));
			answer(board([one, two]), details);
			const mounted = render(view({ callId: "dec_2", project: "shop" }));
			await screen.findByRole("button", { name: /^Question dec_2/ });
			await userEvent.click(within(strip()).getByRole("tab", { name: "Question dec_1" }));
			expect(navigateMock).toHaveBeenLastCalledWith({ to: "/decisions", search: { call: "dec_2", project: "shop", tab: "dec_1" } });
			mounted.rerender(view({ callId: "dec_2", project: "shop", tab: "dec_1" }));
			await userEvent.click(within(strip()).getByRole("tab", { name: "Board" }));
			expect(navigateMock).toHaveBeenLastCalledWith({ to: "/decisions", search: { call: "dec_2", project: "shop" } });
		});

		it("closes a tab from its button or with Delete, returning to the board when it was showing", async () => {
			window.localStorage.setItem(DECISION_TABS_STORAGE_KEY, JSON.stringify(["dec_1", "dec_2"]));
			answer(board([one, two]), details);
			render(view({ tab: "dec_1" }));
			await screen.findByRole("heading", { level: 2, name: "Question dec_1" });
			await userEvent.click(screen.getByRole("button", { name: "Close Question dec_1" }));
			expect(kept()).toEqual(["dec_2"]);
			expect(navigateMock).toHaveBeenLastCalledWith({ to: "/decisions", search: {} });
			navigateMock.mockClear();
			act(() => within(strip()).getByRole("tab", { name: "Question dec_2" }).focus());
			await userEvent.keyboard("{Delete}");
			expect(kept()).toEqual([]);
			expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
			// It was not the one showing, so the page does not move.
			expect(navigateMock).not.toHaveBeenCalled();
		});

		it("moves along the strip with the arrow keys without choosing", async () => {
			window.localStorage.setItem(DECISION_TABS_STORAGE_KEY, JSON.stringify(["dec_1", "dec_2"]));
			answer(board([one, two]), details);
			render(view());
			await screen.findByRole("button", { name: /^Question dec_1/ });
			const tab = (name: string) => within(strip()).getByRole("tab", { name });
			act(() => tab("Board").focus());
			await userEvent.keyboard("{ArrowRight}{ArrowRight}");
			expect(tab("Question dec_2")).toHaveFocus();
			await userEvent.keyboard("{Home}");
			expect(tab("Board")).toHaveFocus();
			await userEvent.keyboard("{End}{ArrowLeft}");
			expect(tab("Question dec_1")).toHaveFocus();
			expect(navigateMock).not.toHaveBeenCalled();
		});

		it("keeps what was typed when a decision is kept open and when its tab is left and returned to", async () => {
			answer(board([one, two]), details);
			const mounted = render(view({ callId: "dec_1" }));
			const note = await screen.findByRole("textbox", { name: "Note for the session" });
			// Focus by hand: with no layout here, a pointer press beside the board lands on the resize divider.
			act(() => note.focus());
			await userEvent.keyboard("ship it dark");
			expect(note).toHaveValue("ship it dark");
			await userEvent.click(screen.getByRole("button", { name: "Keep open" }));
			// The same field, not a new one: keeping moves nothing.
			expect(screen.getByRole("textbox", { name: "Note for the session" })).toBe(note);
			expect(screen.getByRole("textbox", { name: "Note for the session" })).toHaveValue("ship it dark");
			// Its tab fills the page, the board comes back, another call is viewed, and the tab is opened again.
			mounted.rerender(view({ callId: "dec_1", tab: "dec_1" }));
			expect(screen.getByRole("textbox", { name: "Note for the session" })).toHaveValue("ship it dark");
			mounted.rerender(view({ callId: "dec_2" }));
			await screen.findByRole("heading", { level: 2, name: "Question dec_2" });
			mounted.rerender(view({ callId: "dec_2", tab: "dec_1" }));
			expect(within(screen.getByRole("complementary", { name: "Decision" })).getByRole("textbox", { name: "Note for the session" })).toHaveValue("ship it dark");
		});

		it("asks before closing a tab that holds unsent text, and not otherwise", async () => {
			window.localStorage.setItem(DECISION_TABS_STORAGE_KEY, JSON.stringify(["dec_1", "dec_2"]));
			answer(board([one, two]), details);
			render(view({ tab: "dec_1" }));
			await userEvent.type(await screen.findByRole("textbox", { name: "Note for the session" }), "not sent yet");
			await userEvent.click(screen.getByRole("button", { name: "Close Question dec_1" }));
			const dialog = await screen.findByRole("dialog", { name: "Close this decision?" });
			expect(kept()).toEqual(["dec_1", "dec_2"]);
			await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
			expect(kept()).toEqual(["dec_1", "dec_2"]);
			expect(screen.getByRole("textbox", { name: "Note for the session" })).toHaveValue("not sent yet");

			await userEvent.click(screen.getByRole("button", { name: "Close Question dec_2" }));
			expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
			expect(kept()).toEqual(["dec_1"]);

			await userEvent.click(screen.getByRole("button", { name: "Close Question dec_1" }));
			await userEvent.click(within(await screen.findByRole("dialog", { name: "Close this decision?" })).getByRole("button", { name: "Close and discard" }));
			expect(kept()).toEqual([]);
		});

		it("asks before closing a kept tab that is also beside the board, and closes that panel with it", async () => {
			answer(board([one, two]), details);
			render(view({ callId: "dec_1" }));
			const note = await screen.findByRole("textbox", { name: "Note for the session" });
			await userEvent.click(screen.getByRole("button", { name: "Keep open" }));
			act(() => note.focus());
			await userEvent.keyboard("not sent yet");
			await userEvent.click(screen.getByRole("button", { name: "Close Question dec_1" }));
			const dialog = await screen.findByRole("dialog", { name: "Close this decision?" });
			expect(kept()).toEqual(["dec_1"]);
			await userEvent.click(within(dialog).getByRole("button", { name: "Close and discard" }));
			expect(kept()).toEqual([]);
			// The text really is discarded: the panel beside the board that held it closes too.
			expect(navigateMock).toHaveBeenLastCalledWith({ to: "/decisions", search: {} });
		});

		it("closes a kept tab that is also beside the board without asking when nothing was typed, and leaves that panel", async () => {
			answer(board([one, two]), details);
			render(view({ callId: "dec_1" }));
			await userEvent.click(await screen.findByRole("button", { name: "Keep open" }));
			await userEvent.click(screen.getByRole("button", { name: "Close Question dec_1" }));
			expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
			expect(kept()).toEqual([]);
			expect(navigateMock).not.toHaveBeenCalled();
		});

		it("lets a full strip scroll, and brings the tab in view into sight", async () => {
			const original = Element.prototype.scrollIntoView;
			const scrollIntoView = vi.fn();
			Element.prototype.scrollIntoView = scrollIntoView;
			try {
				const many = Array.from({ length: 12 }, (_, index) => call({ id: `dec_${index + 1}`, options: option }));
				window.localStorage.setItem(DECISION_TABS_STORAGE_KEY, JSON.stringify(many.map((item) => item.id)));
				answer(board(many), Object.fromEntries(many.map((item) => [item.id, item])));
				render(view({ tab: "dec_12" }));
				const last = await within(strip()).findByRole("tab", { name: "Question dec_12" });
				expect(within(strip()).getAllByRole("tab")).toHaveLength(13);
				expect(last.closest("[data-slot='decision-tab-scroll']")).toHaveClass("overflow-x-auto");
				// The board's tab stays put outside the scrolling part.
				expect(within(strip()).getByRole("tab", { name: "Board" }).closest("[data-slot='decision-tab-scroll']")).toBeNull();
				await waitFor(() => expect(scrollIntoView.mock.contexts).toContain(last));
			} finally {
				Element.prototype.scrollIntoView = original;
			}
		});

		it("returns focus to the decision's card when the last tab closes, from the keyboard or its button", async () => {
			window.localStorage.setItem(DECISION_TABS_STORAGE_KEY, JSON.stringify(["dec_2"]));
			answer(board([one, two]), details);
			render(view());
			const card = await screen.findByRole("button", { name: /^Question dec_2/ });
			act(() => within(strip()).getByRole("tab", { name: "Question dec_2" }).focus());
			await userEvent.keyboard("{Delete}");
			expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
			await waitFor(() => expect(card).toHaveFocus());

			await userEvent.dblClick(card);
			act(() => screen.getByRole("button", { name: "Close Question dec_2" }).focus());
			await userEvent.keyboard("{Enter}");
			expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
			await waitFor(() => expect(card).toHaveFocus());
		});

		it("keeps focus on the strip when a tab closes and others remain", async () => {
			window.localStorage.setItem(DECISION_TABS_STORAGE_KEY, JSON.stringify(["dec_1", "dec_2"]));
			answer(board([one, two]), details);
			render(view());
			await screen.findByRole("button", { name: /^Question dec_1/ });
			act(() => screen.getByRole("button", { name: "Close Question dec_2" }).focus());
			await userEvent.keyboard("{Enter}");
			expect(kept()).toEqual(["dec_1"]);
			expect(within(strip()).getByRole("tab", { name: "Question dec_1" })).toHaveFocus();
		});

		it("leaves Escape alone on a decision's tab", async () => {
			window.localStorage.setItem(DECISION_TABS_STORAGE_KEY, JSON.stringify(["dec_1"]));
			answer(board([one, two]), details);
			render(view({ callId: "dec_2", tab: "dec_1" }));
			await userEvent.click(await screen.findByRole("textbox", { name: "Note for the session" }));
			await userEvent.keyboard("{Escape}");
			expect(navigateMock).not.toHaveBeenCalled();
		});
	});

	describe("done archive", () => {
		const nango = { projectId: "cloud", projectName: "skyvern-cloud", streamSessionId: "nango", streamName: "Nango" };
		const finished = (id: string, minute: number, extra: Partial<DecisionCall> = {}) => call({
			id, state: "done", stateLabel: "Done", chosenLabel: "Ship", outcome: `Shipped ${id}`, outcomeAt: `2026-11-18T11:0${minute}:00Z`, ...extra,
		});
		const ledger = () => board([
			call({ id: "dec_open", ...nango }),
			...[1, 2, 3, 4, 5].map((minute) => finished(`fin_${minute}`, minute, nango)),
			finished("shop_done", 9),
		]);

		it("opens from a stream's Done cell, narrowed to that stream, and widens to every stream", async () => {
			answer(ledger());
			renderView(<DecisionsView />);
			await userEvent.click(await screen.findByRole("button", { name: "2 more" }));
			const dialog = await screen.findByRole("dialog", { name: "Done decisions" });
			const cards = () => within(within(dialog).getByRole("grid", { name: "Done decisions" })).getAllByRole("gridcell");
			expect(cards()).toHaveLength(5);
			expect(within(dialog).getByRole("combobox", { name: "Search done decisions" })).toHaveFocus();
			await userEvent.click(within(dialog).getByRole("button", { name: "Showing skyvern-cloud: Nango. Show every done decision" }));
			expect(cards()).toHaveLength(6);
		});

		it("opens the chosen decision beside the board and closes", async () => {
			answer(ledger());
			renderView(<DecisionsView project="cloud" />);
			await userEvent.click(await screen.findByRole("button", { name: "2 more" }));
			const dialog = await screen.findByRole("dialog", { name: "Done decisions" });
			await userEvent.click(within(dialog).getByRole("button", { name: /^Question fin_1/ }));
			expect(navigateMock).toHaveBeenLastCalledWith({ to: "/decisions", search: { call: "fin_1", project: "cloud" } });
			await waitFor(() => expect(screen.queryByRole("dialog", { name: "Done decisions" })).not.toBeInTheDocument());
		});

		it("widens the board when the chosen decision lies outside what it is narrowed to", async () => {
			answer(ledger());
			renderView(<DecisionsView project="cloud" stream="h_a:nango" />);
			await userEvent.click(await screen.findByRole("button", { name: "2 more" }));
			const dialog = await screen.findByRole("dialog", { name: "Done decisions" });
			await userEvent.click(within(dialog).getByRole("button", { name: /Show every done decision$/ }));
			await userEvent.click(within(dialog).getByRole("button", { name: /^Question shop_done/ }));
			// The board would not hold that card while narrowed to the Nango stream, so it shows every project.
			expect(navigateMock).toHaveBeenLastCalledWith({ to: "/decisions", search: { call: "shop_done" } });
		});

		it("returns focus to the button that opened it when it closes", async () => {
			answer(ledger());
			renderView(<DecisionsView />);
			const opener = await screen.findByRole("button", { name: "2 more" });
			act(() => opener.focus());
			await userEvent.keyboard("{Enter}");
			expect(await screen.findByRole("combobox", { name: "Search done decisions" })).toHaveFocus();
			await userEvent.keyboard("{Escape}");
			await waitFor(() => expect(screen.queryByRole("dialog", { name: "Done decisions" })).not.toBeInTheDocument());
			await waitFor(() => expect(opener).toHaveFocus());
		});

		it("starts each opening from its own stream and an empty search", async () => {
			answer(ledger());
			renderView(<DecisionsView />);
			await userEvent.click(await screen.findByRole("button", { name: "2 more" }));
			const field = () => screen.getByRole("combobox", { name: "Search done decisions" });
			await userEvent.type(field(), "fin_2");
			expect(within(screen.getByRole("grid", { name: "Done decisions" })).getAllByRole("gridcell")).toHaveLength(1);
			await userEvent.keyboard("{Escape}");
			await waitFor(() => expect(screen.queryByRole("dialog", { name: "Done decisions" })).not.toBeInTheDocument());
			await userEvent.click(screen.getByRole("button", { name: "2 more" }));
			expect(field()).toHaveValue("");
			expect(within(screen.getByRole("grid", { name: "Done decisions" })).getAllByRole("gridcell")).toHaveLength(5);
		});
	});

	it("opens a card as the selected call in the URL and marks the selected one", async () => {
		answer(board([call({ id: "dec_1" }), call({ id: "dec_2", raisedAt: "2026-11-18T10:00:00Z" })]));
		renderView(<DecisionsView callId="dec_2" />);
		const selected = await screen.findByRole("button", { name: /^Question dec_2/ });
		expect(selected).toHaveAttribute("aria-current", "true");
		await userEvent.click(screen.getByRole("button", { name: /^Question dec_1/ }));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/decisions", search: { call: "dec_1" } });
	});

	it("shares one board query with the sidebar count", async () => {
		answer(board([call({ id: "dec_1" })]));
		function SidebarCount() {
			const { data } = useDecisionBoard();
			return <span>count {data?.needsYouCount ?? "-"}</span>;
		}
		renderView(<><SidebarCount /><DecisionsView /></>);
		expect(await screen.findByText("count 1")).toBeInTheDocument();
		await waitFor(() => expect(boardFetches()).toBe(1));
	});

	it("opens the selected call beside the board in a resizable panel", async () => {
		const selected = call({ id: "dec_1" });
		answer(board([selected]), { dec_1: selected });
		renderView(<DecisionsView callId="dec_1" />);
		const panel = await screen.findByRole("complementary", { name: "Decision" });
		expect(await within(panel).findByRole("heading", { level: 2, name: "Question dec_1" })).toBeInTheDocument();
		expect(screen.getByRole("separator")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: /^Question dec_1/ })).toHaveAttribute("aria-current", "true");
	});

	it("opens a call that is no longer on the board by its id", async () => {
		const old = call({ id: "dec_old", state: "done", outcome: "Shipped", outcomeAt: "2026-10-01T10:00:00Z" });
		answer(board([call({ id: "dec_1" })]), { dec_old: old });
		renderView(<DecisionsView callId="dec_old" />);
		expect(await screen.findByRole("heading", { level: 2, name: "Question dec_old" })).toBeInTheDocument();
	});

	it("closes the panel by clearing the selected call", async () => {
		const selected = call({ id: "dec_1" });
		answer(board([selected]), { dec_1: selected });
		renderView(<DecisionsView callId="dec_1" />);
		await userEvent.click(await screen.findByRole("button", { name: "Close decision" }));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/decisions", search: {} });
	});

	it("shows no panel without a selected call", async () => {
		answer(board([call({ id: "dec_1" })]));
		renderView(<DecisionsView />);
		await screen.findByRole("button", { name: /^Question dec_1/ });
		expect(screen.queryByRole("complementary", { name: "Decision" })).not.toBeInTheDocument();
		expect(screen.queryByRole("separator")).not.toBeInTheDocument();
	});

	describe("search", () => {
		const closedHistory = [
			call({ id: "dec_dismissed", title: "Auth: dismissed", state: "dismissed", stateLabel: "Dismissed" }),
			call({ id: "dec_withdrawn", title: "Auth: withdrawn", state: "withdrawn", stateLabel: "Withdrawn" }),
			call({ id: "dec_retired", title: "Auth: retired", state: "done", stateLabel: "Done", retired: true, outcomeAt: "2026-11-17T10:00:00Z" }),
			call({ id: "dec_ancient", title: "Auth: old done", state: "done", stateLabel: "Done", streamSessionId: "epic", streamName: "Checkout", outcomeAt: "2026-09-01T10:00:00Z" }),
		];
		function answerSearch(results: Record<string, DecisionCall[] | Promise<unknown>>) {
			getMock.mockImplementation((path: string, request?: { params?: { query?: { q?: string; limit?: number } } }) => {
				if (path === "/api/v1/decisions/board") return Promise.resolve(board([call({ id: "dec_board" })]));
				if (path === "/api/v1/decisions") {
					const found = results[request?.params?.query?.q ?? ""];
					if (found instanceof Promise) return found;
					return Promise.resolve({ data: { calls: found ?? [] } });
				}
				return Promise.resolve({ error: { code: "DECISION_NOT_FOUND", message: "Unknown decision" }, response: { status: 404 } });
			});
		}
		const searches = () => getMock.mock.calls.filter(([path]) => path === "/api/v1/decisions").map(([, request]) => (request as { params: { query: unknown } }).params.query);

		it("writes the search to the URL after a pause, once", async () => {
			answerSearch({});
			renderView(<DecisionsView />);
			await userEvent.click(await screen.findByRole("button", { name: "Search all decisions" }));
			await userEvent.type(screen.getByRole("searchbox", { name: "Search all decisions" }), "auth");
			expect(navigateMock).not.toHaveBeenCalled();
			await waitFor(() => expect(navigateMock).toHaveBeenCalledWith({ to: "/decisions", search: { q: "auth" }, replace: true }));
			expect(navigateMock).toHaveBeenCalledTimes(1);
		});

		it("replaces the board with every matching call, closed and old ones included", async () => {
			answerSearch({ auth: closedHistory });
			renderView(<DecisionsView query="auth" />);
			const results = await screen.findByRole("list", { name: "Search results" });
			expect(searches()).toEqual([{ q: "auth", limit: 50 }]);
			expect(screen.queryByRole("list", { name: "Decision states" })).not.toBeInTheDocument();
			const rows = within(results).getAllByRole("button");
			expect(rows.map((row) => row.getAttribute("data-decision-result"))).toEqual(["dec_dismissed", "dec_withdrawn", "dec_retired", "dec_ancient"]);
			expect(rows[0]).toHaveTextContent("Dismissed");
			expect(rows[1]).toHaveTextContent("Withdrawn");
			expect(rows[2]).toHaveTextContent("No longer applies");
			expect(rows[3]).toHaveTextContent("Shop · Checkout");
			expect(rows[3]).toHaveTextContent("mac-mini");
		});

		it("opens a result in the same panel and keeps the search", async () => {
			answerSearch({ auth: closedHistory });
			renderView(<DecisionsView query="auth" />);
			await userEvent.click(await screen.findByRole("button", { name: /Auth: old done/ }));
			expect(navigateMock).toHaveBeenCalledWith({ to: "/decisions", search: { call: "dec_ancient", q: "auth" } });
		});

		it("says when nothing matches and clears the search back to the board", async () => {
			answerSearch({ zzz: [] });
			renderView(<DecisionsView query="zzz" callId="dec_board" />);
			expect(await screen.findByText("No decisions match “zzz”")).toBeInTheDocument();
			await userEvent.click(screen.getByRole("button", { name: "Clear search" }));
			expect(navigateMock).toHaveBeenCalledWith({ to: "/decisions", search: { call: "dec_board" }, replace: true });
		});

		it("does not search for blank words", async () => {
			answerSearch({});
			renderView(<DecisionsView />);
			await screen.findByRole("button", { name: /^Question dec_board/ });
			expect(searches()).toEqual([]);
		});

		it("follows the URL when back or forward changes the search", async () => {
			answerSearch({ auth: closedHistory, nango: [call({ id: "dec_nango", title: "Nango scheme" })] });
			const view = renderView(<DecisionsView query="auth" />);
			await screen.findByRole("list", { name: "Search results" });
			view.rerender(<QueryClientProvider client={clientOf(view)}><TooltipProvider><DecisionsView query="nango" /></TooltipProvider></QueryClientProvider>);
			expect(await screen.findByRole("button", { name: /Nango scheme/ })).toBeInTheDocument();
			expect(screen.getByRole("searchbox", { name: "Search all decisions" })).toHaveValue("nango");
		});

		it("never lets a slower earlier search overwrite the newer one", async () => {
			let finishOld!: (value: unknown) => void;
			answerSearch({ old: new Promise((done) => { finishOld = done; }), new: [call({ id: "dec_new", title: "Newest" })] });
			const view = renderView(<DecisionsView query="old" />);
			view.rerender(<QueryClientProvider client={clientOf(view)}><TooltipProvider><DecisionsView query="new" /></TooltipProvider></QueryClientProvider>);
			expect(await screen.findByRole("button", { name: /Newest/ })).toBeInTheDocument();
			finishOld({ data: { calls: [call({ id: "dec_old", title: "Stale" })] } });
			await new Promise((done) => setTimeout(done, 50));
			expect(screen.queryByRole("button", { name: /Stale/ })).not.toBeInTheDocument();
		});

		it("reports a failed refresh over cached search results, keeping them", async () => {
			answerSearch({ auth: closedHistory });
			const view = renderView(<DecisionsView query="auth" />);
			await screen.findByRole("list", { name: "Search results" });
			answerSearch({ auth: Promise.resolve({ error: { code: "DECISION_LIST_FAILED", message: "Unable to list decisions" } }) });
			await act(() => clientOf(view).invalidateQueries());
			const alert = await screen.findByRole("alert", undefined, { timeout: 4_000 });
			expect(alert).toHaveTextContent("Unable to list decisions");
			expect(within(alert).getByRole("button", { name: "Retry" })).toBeInTheDocument();
			expect(screen.getByRole("list", { name: "Search results" })).toBeInTheDocument();
		}, 10_000);

		it("reports a failed search with a retry", async () => {
			answerSearch({ auth: Promise.resolve({ error: { code: "DECISION_LIST_FAILED", message: "Unable to list decisions" } }) });
			renderView(<DecisionsView query="auth" />);
			const alert = await screen.findByRole("alert", undefined, { timeout: 4_000 });
			expect(alert).toHaveTextContent("Unable to list decisions");
			expect(within(alert).getByRole("button", { name: "Retry" })).toBeInTheDocument();
		});
	});

	describe("keyboard and live updates", () => {
		it("closes the open call with Escape from a focused card", async () => {
			const selected = call({ id: "dec_1" });
			answer(board([selected]), { dec_1: selected });
			renderView(<DecisionsView callId="dec_1" />);
			const card = await screen.findByRole("button", { name: /^Question dec_1/ });
			card.focus();
			await userEvent.keyboard("{Escape}");
			expect(navigateMock).toHaveBeenCalledWith({ to: "/decisions", search: {} });
		});

		it("leaves Escape to the search field while a hovered card's details are showing", async () => {
			const selected = call({ id: "dec_1" });
			answer(board([selected]), { dec_1: selected });
			renderView(<DecisionsView callId="dec_1" />);
			const card = await screen.findByRole("button", { name: /^Question dec_1/ });
			await userEvent.click(screen.getByRole("button", { name: "Search all decisions" }));
			expect(screen.getByRole("searchbox", { name: "Search all decisions" })).toHaveFocus();
			await userEvent.hover(card);
			expect(await screen.findByRole("tooltip")).toHaveTextContent("Question dec_1");
			navigateMock.mockClear();

			await userEvent.keyboard("{Escape}");
			// The search field takes its first Escape; the open call stays open.
			expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
			expect(navigateMock).not.toHaveBeenCalled();
			expect(screen.getByRole("complementary", { name: "Decision" })).toBeInTheDocument();
		});

		it("closes the panel with one Escape from inside it while another card's details are showing", async () => {
			const selected = call({ id: "dec_1" });
			answer(board([selected, call({ id: "dec_2" })]), { dec_1: selected });
			renderView(<DecisionsView callId="dec_1" />);
			const other = await screen.findByRole("button", { name: /^Question dec_2/ });
			const close = await screen.findByRole("button", { name: "Close decision" });
			act(() => close.focus());
			await userEvent.hover(other);
			expect(await screen.findByRole("tooltip")).toHaveTextContent("Question dec_2");
			navigateMock.mockClear();

			await userEvent.keyboard("{Escape}");
			expect(navigateMock).toHaveBeenCalledTimes(1);
			expect(navigateMock).toHaveBeenCalledWith({ to: "/decisions", search: {} });
		});

		it("announces the Needs-you count in one polite live region", async () => {
			answer(board([call({ id: "dec_1" }), call({ id: "dec_2" })]));
			const { container } = renderView(<DecisionsView />);
			await screen.findByRole("button", { name: /^Question dec_1/ });
			const regions = container.querySelectorAll("[aria-live]");
			expect(regions).toHaveLength(1);
			expect(regions[0]).toHaveAttribute("aria-live", "polite");
			expect(regions[0]).toHaveTextContent("Decisions, needs you: 2");
		});

		it("keeps the last board and reports a failed refresh with a retry", async () => {
			answer(board([call({ id: "dec_1" })]));
			const view = renderView(<DecisionsView />);
			await screen.findByRole("button", { name: /^Question dec_1/ });
			answer({ error: { code: "DECISION_BOARD_FAILED", message: "Unable to load decisions" } });
			await act(() => clientOf(view).invalidateQueries());
			const alert = await screen.findByRole("alert", undefined, { timeout: 4_000 });
			expect(alert).toHaveTextContent("Unable to load decisions");
			expect(within(alert).getByRole("button", { name: "Retry" })).toBeInTheDocument();
			expect(screen.getByRole("button", { name: /^Question dec_1/ })).toBeInTheDocument();
		}, 10_000);
	});

	it("shows standing decisions instead of an empty ledger", async () => {
		answer(board([call({ id: "dec_decided", state: "decided", stateLabel: "Decided for you", decided: { undo: "Undo it", carriedOut: true } })]));
		renderView(<DecisionsView />);
		expect(await screen.findByRole("button", { name: /^Question dec_decided/ })).toBeInTheDocument();
		expect(screen.queryByRole("heading", { name: "No decisions yet" })).not.toBeInTheDocument();
		expect(screen.getByRole("status", { name: "Nothing needs you" })).toBeInTheDocument();
	});

	it("reports a failed refresh of an empty board instead of calling it empty", async () => {
		answer(board([]));
		const view = renderView(<DecisionsView />);
		await screen.findByRole("heading", { name: "No decisions yet" });
		answer({ error: { code: "DECISION_BOARD_FAILED", message: "Unable to load decisions" } });
		await act(() => clientOf(view).invalidateQueries());
		const alert = await screen.findByRole("alert", undefined, { timeout: 4_000 });
		expect(alert).toHaveTextContent("Unable to load decisions");
		expect(screen.queryByRole("heading", { name: "No decisions yet" })).not.toBeInTheDocument();
	}, 10_000);
});
