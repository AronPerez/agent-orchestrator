import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SessionFleet } from "./SessionFleet";
import type { WorkspaceSession } from "../types/workspace";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: get } }));
vi.mock("../lib/preview-mode", () => ({ usesPreviewWorkspaceData: false }));

const sessions: WorkspaceSession[] = [
	{
		id: "orch",
		workspaceId: "p",
		workspaceName: "P",
		title: "Orchestrator",
		kind: "orchestrator",
		provider: "codex",
		status: "working",
	},
	{
		id: "manager",
		parentSessionId: "orch",
		workspaceId: "p",
		workspaceName: "P",
		title: "Release coordination",
		kind: "worker",
		provider: "codex",
		status: "idle",
	},
	{
		id: "worker",
		parentSessionId: "manager",
		workspaceId: "p",
		workspaceName: "P",
		title: "Implement login",
		kind: "worker",
		provider: "codex",
		status: "working",
		issueId: "TASK-1",
	},
];
function show(items = sessions) {
	const open = vi.fn();
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={queryClient}>
			<SessionFleet sessions={items} onOpen={open} />
		</QueryClientProvider>,
	);
	return open;
}
beforeEach(() => {
	get.mockReset();
	get.mockResolvedValue({ data: { events: [], hasMore: false } });
});

describe("SessionFleet", () => {
	it("selects a worker and opens the existing session", async () => {
		const open = show();
		fireEvent.click(screen.getByRole("button", { name: /Implement login.*Worker/ }));
		expect(screen.getByText("TASK-1")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Open session" }));
		expect(open).toHaveBeenCalledWith(sessions[2]);
		await waitFor(() =>
			expect(get).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/coordination", {
				params: { path: { sessionId: "worker" } },
			}),
		);
		fireEvent.click(screen.getByRole("button", { name: "Collapse Release coordination" }));
		expect(screen.queryByRole("button", { name: /Implement login.*Worker/ })).not.toBeInTheDocument();
	});
	it("shows source, target, recorded evidence and truncation without claiming action", async () => {
		get.mockResolvedValue({
			data: {
				hasMore: true,
				events: [
					{
						id: "m",
						kind: "message",
						sourceSessionId: "manager",
						targetSessionId: "worker",
						state: "recorded",
						summary: "Check CI",
						createdAt: "2026-11-03T01:00:00Z",
					},
				],
			},
		});
		show();
		expect(await screen.findByText("Check CI")).toBeInTheDocument();
		expect(screen.getByText("Release coordination → Implement login")).toBeInTheDocument();
		expect(screen.getByText("Latest 50 recorded events")).toBeInTheDocument();
		expect(screen.getByText(/does not mean acted on/)).toBeInTheDocument();
	});
	it("shows request failure and supports retry", async () => {
		get.mockResolvedValueOnce({ error: { code: "failed" } });
		show();
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not load communication");
		fireEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(await screen.findByText("No recorded communication in this history.")).toBeInTheDocument();
	});
	it("shows loading and an empty fleet without inventing events", () => {
		get.mockReturnValue(new Promise(() => {}));
		show();
		expect(screen.getByRole("status")).toHaveTextContent("Loading communication");
	});
	it("renders the empty fleet", () => {
		show([]);
		expect(screen.getByText("No sessions to display.")).toBeInTheDocument();
		expect(within(screen.getByRole("region")).queryByRole("complementary")).toBeNull();
		expect(get).not.toHaveBeenCalled();
	});
});
