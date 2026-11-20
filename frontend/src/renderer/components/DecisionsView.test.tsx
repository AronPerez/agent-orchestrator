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
		expect(screen.getByRole("button", { name: /Shop/ })).toHaveAttribute("aria-expanded", "false");
		expect(screen.getByRole("button", { name: /skyvern-cloud/ })).toHaveAttribute("aria-expanded", "false");
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
