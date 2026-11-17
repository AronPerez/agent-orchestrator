import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { RemoteHost } from "../hooks/useRemoteHosts";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { FleetOverview } from "./FleetOverview";

function session(
	id: string,
	title: string,
	status: WorkspaceSession["status"] = "working",
	overrides: Partial<WorkspaceSession> = {},
): WorkspaceSession {
	return {
		id,
		workspaceId: overrides.workspaceId ?? "project",
		workspaceName: overrides.workspaceName ?? "Project",
		title,
		provider: "codex",
		kind: "worker",
		status,
		updatedAt: "2026-11-17T12:00:00Z",
		prs: [],
		...overrides,
	};
}

function workspace(
	id: string,
	name: string,
	sessions: WorkspaceSession[],
	overrides: Partial<WorkspaceSummary> = {},
): WorkspaceSummary {
	return {
		id,
		name,
		path: `/code/${id}`,
		sessions: sessions.map((item) => ({ ...item, workspaceId: id, workspaceName: name })),
		...overrides,
	};
}

function host(hostId: string, label: string, status: RemoteHost["status"]): RemoteHost {
	return { hostId, label, status, url: `https://${hostId}.example.test` };
}

function pr(number: number, overrides: Partial<WorkspaceSession["prs"][number]> = {}): WorkspaceSession["prs"][number] {
	return {
		url: `https://example.test/pr/${number}`,
		number,
		state: "open",
		ci: "passing",
		review: "review_required",
		mergeability: "blocked",
		reviewComments: false,
		updatedAt: "2026-11-17T12:00:00Z",
		...overrides,
	};
}

describe("FleetOverview", () => {
	it("keeps host-local project and session identities separate and opens the selected machine", async () => {
		const onOpen = vi.fn();
		const local = workspace("project", "Alpha", [session("same", "Fix cache")]);
		const remote = workspace("project", "Alpha", [session("same", "Fix cache", "idle", { hostId: "build-box" })], { hostId: "build-box" });

		render(
			<FleetOverview
				localWorkspaces={[local]}
				onOpen={onOpen}
				remoteHosts={[host("build-box", "Build box", "connected")]}
				remoteWorkspaces={[remote]}
			/>,
		);

		expect(screen.getAllByText("Alpha")).toHaveLength(2);
		expect(screen.getByText("This computer")).toBeInTheDocument();
		expect(screen.getByText("Build box")).toBeInTheDocument();
		const sessions = screen.getAllByRole("treeitem", { name: /Fix cache/ });
		expect(sessions).toHaveLength(2);

		await userEvent.click(sessions[1]!);
		expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({ id: "same" }), "build-box");
	});

	it("orders branches that need a person first and shows ancestor rollups", () => {
		const manager = session("manager", "Release manager", "working");
		const blocked = session("blocked", "Answer deploy question", "needs_input", { parentSessionId: manager.id });
		const background = workspace("background", "Background", [session("background-worker", "Index docs")]);
		const urgent = workspace("urgent", "Urgent", [manager, blocked]);

		render(<FleetOverview localWorkspaces={[background, urgent]} onOpen={vi.fn()} />);

		const projects = screen.getAllByRole("treeitem").filter((row) => row.getAttribute("aria-level") === "1");
		expect(projects[0]).toHaveTextContent("Urgent");
		expect(projects[0]).toHaveTextContent("1 Needs you");
		expect(projects[0]).toHaveTextContent("1 Working");
		expect(projects[1]).toHaveTextContent("Background");

		const managerRow = screen.getByRole("treeitem", { name: /Release manager/ });
		expect(managerRow).toHaveTextContent("Working");
		expect(managerRow).toHaveTextContent("1 Needs you");
	});

	it("keeps cached sessions visible on unreachable machines and lists hosts without a cache", () => {
		const cached = workspace(
			"remote-project",
			"Remote project",
			[session("cached-worker", "Cached worker", "idle", { hostId: "build-box" })],
			{ hostId: "build-box" },
		);

		render(
			<FleetOverview
				localWorkspaces={[]}
				onOpen={vi.fn()}
				remoteHosts={[
					host("build-box", "Build box", "offline"),
					host("empty-box", "Empty box", "offline"),
					host("checking-box", "Checking box", "connecting"),
				]}
				remoteWorkspaces={[cached]}
			/>,
		);

		const cachedMachine = screen.getByText("Build box").closest<HTMLElement>('[role="treeitem"]');
		expect(cachedMachine).not.toBeNull();
		expect(within(cachedMachine!).getByText("Offline")).toBeInTheDocument();
		expect(screen.getByRole("treeitem", { name: /Cached worker/ })).toBeInTheDocument();
		const uncachedMachine = screen.getByText("Empty box").closest<HTMLElement>('[role="treeitem"]');
		expect(uncachedMachine).not.toBeNull();
		expect(within(uncachedMachine!).getByText("Offline")).toBeInTheDocument();
		expect(screen.getByRole("treeitem", { name: /Checking box, Checking/ })).toBeInTheDocument();
	});

	it("hides cached project and session names immediately when a host rejects access", () => {
		const refused = {
			...host("build-box", "Build box", "offline"),
			failureReason: "unauthorized" as const,
		};
		const cached = workspace(
			"private-project",
			"Private project",
			[session("private-worker", "Private worker", "idle", { hostId: "build-box" })],
			{ hostId: "build-box" },
		);

		render(<FleetOverview localWorkspaces={[]} onOpen={vi.fn()} remoteHosts={[refused]} remoteWorkspaces={[cached]} />);

		expect(screen.queryByText("Private project")).not.toBeInTheDocument();
		expect(screen.queryByText("Private worker")).not.toBeInTheDocument();
		expect(screen.getByRole("treeitem", { name: /Build box, Access rejected/ })).toBeInTheDocument();
	});

	it("labels failed cached reads as stale and a connected first read as checking", () => {
		const cached = workspace(
			"remote-project",
			"Remote project",
			[session("cached", "Cached worker", "idle", { hostId: "build-box" })],
			{ hostId: "build-box" },
		);
		const { rerender } = render(
			<FleetOverview
				localWorkspaces={[]}
				onOpen={vi.fn()}
				remoteFailedHostIds={["build-box"]}
				remoteHosts={[host("build-box", "Build box", "connected")]}
				remoteLoadedProjectHostIds={["build-box"]}
				remoteLoadedSessionHostIds={["build-box"]}
				remoteWorkspaces={[cached]}
			/>,
		);

		expect(screen.getByRole("treeitem", { name: /Build box, Could not refresh sessions/ })).toBeInTheDocument();

		rerender(
			<FleetOverview
				localWorkspaces={[]}
				onOpen={vi.fn()}
				remoteFailedHostIds={[]}
				remoteHosts={[host("first-read", "New machine", "connected")]}
				remoteLoadedProjectHostIds={[]}
				remoteLoadedSessionHostIds={[]}
				remoteWorkspaces={[]}
			/>,
		);
		expect(screen.getByRole("treeitem", { name: /New machine, Checking/ })).toBeInTheDocument();
	});

	it("rolls multiple PRs and failing checks from an active worker through every ancestor", () => {
		const coordinator = session("orch", "Coordinator", "working", { kind: "orchestrator" });
		const worker = session("worker", "Ship release", "working", {
			parentSessionId: coordinator.id,
			prs: [
				pr(1, { ci: "failing", failingChecks: [{ name: "unit", status: "failed", conclusion: "failure" }] }),
				pr(2),
			],
		});
		render(<FleetOverview localWorkspaces={[workspace("project", "Project", [coordinator, worker])]} onOpen={vi.fn()} />);

		for (const row of [
			screen.getByRole("treeitem", { name: /^Project/ }),
			screen.getByRole("treeitem", { name: /This computer/ }),
			screen.getByRole("treeitem", { name: /Coordinator/ }),
			screen.getByRole("treeitem", { name: /Ship release/ }),
		]) {
			expect(within(row).getByText("PRs: 2")).toBeInTheDocument();
			expect(within(row).getByText("Checks failing: 1")).toBeInTheDocument();
		}
		expect(screen.getByRole("treeitem", { name: /^Project/ })).toHaveTextContent("1 Needs you");
	});

	it("uses daemon PR precedence when scmStatus is unavailable", () => {
		const commented = session("commented", "Address feedback", "working", {
			prs: [pr(1, { review: "none", mergeability: "blocked", reviewComments: true })],
		});
		const blocked = session("blocked", "Wait for base", "working", {
			prs: [pr(2, { review: "approved", mergeability: "blocked" })],
		});
		render(<FleetOverview localWorkspaces={[workspace("project", "Project", [commented, blocked])]} onOpen={vi.fn()} />);

		const project = screen.getByRole("treeitem", { name: /^Project/ });
		expect(project).toHaveTextContent("1 Needs you");
		expect(project).toHaveTextContent("1 In review");
		expect(project).not.toHaveTextContent("Ready");
	});

	it("collapses branches and reveals archived sessions on request", async () => {
		const active = session("active", "Active worker");
		const archived = session("archived", "Archived worker", "terminated", { isTerminated: true });
		render(<FleetOverview localWorkspaces={[workspace("project", "Project", [active, archived])]} onOpen={vi.fn()} />);

		expect(screen.queryByText("Archived worker")).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Show archived" }));
		expect(screen.getByText("Archived worker")).toBeInTheDocument();

		const project = screen.getByRole("treeitem", { name: /^Project/ });
		expect(project).toHaveAttribute("aria-expanded", "true");
		await userEvent.click(project);
		expect(project).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByText("This computer")).not.toBeInTheDocument();
	});

	it("includes archived and missing-parent state in the accessible name without a leaf rollup", async () => {
		const orphan = session("orphan", "Archived orphan", "terminated", {
			isTerminated: true,
			parentSessionId: "deleted-parent",
		});
		render(<FleetOverview localWorkspaces={[workspace("project", "Project", [orphan])]} onOpen={vi.fn()} />);
		await userEvent.click(screen.getByRole("button", { name: "Show archived" }));

		const row = screen.getByRole("treeitem", { name: /Archived orphan.*Archived.*Parent unavailable/ });
		expect(row).not.toHaveAccessibleName(/No sessions/);
	});

	it("uses tree keyboard navigation and opens sessions with Enter", () => {
		const onOpen = vi.fn();
		const worker = session("worker", "Keyboard worker");
		render(<FleetOverview localWorkspaces={[workspace("project", "Project", [worker])]} onOpen={onOpen} />);

		const project = screen.getByRole("treeitem", { name: /^Project/ });
		const machine = screen.getByRole("treeitem", { name: /This computer/ });
		const workerRow = screen.getByRole("treeitem", { name: /Keyboard worker/ });
		project.focus();

		fireEvent.keyDown(project, { key: "ArrowRight" });
		expect(machine).toHaveFocus();
		fireEvent.keyDown(machine, { key: "ArrowRight" });
		expect(workerRow).toHaveFocus();
		fireEvent.keyDown(workerRow, { key: "Enter" });
		expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({ id: "worker" }), undefined);
		fireEvent.keyDown(workerRow, { key: "ArrowLeft" });
		expect(machine).toHaveFocus();
	});
});
