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
