import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DecisionCall } from "../lib/decision-inbox";

const { getMock, navigateMock } = vi.hoisted(() => ({ getMock: vi.fn(), navigateMock: vi.fn() }));
vi.mock("../lib/host-clients", () => ({ clientForSessionHost: () => ({ GET: getMock }) }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigateMock }));
vi.mock("./PageTopbar", () => ({
	PageTopbar: ({ lead, actions }: { lead?: ReactNode; actions?: ReactNode }) => <header>{lead}{actions}</header>,
	PageTopbarTitle: ({ children }: { children: ReactNode }) => <h1>{children}</h1>,
}));

import { useDecisionBoard } from "../hooks/useDecisionInbox";
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

function renderView(node: ReactNode) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>);
}

const boardFetches = () => getMock.mock.calls.filter(([path]) => path === "/api/v1/decisions/board").length;

beforeEach(() => {
	getMock.mockReset();
	navigateMock.mockReset();
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
});
