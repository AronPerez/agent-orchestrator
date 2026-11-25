import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { components } from "../../api/schema";
import type { RemoteHost } from "../hooks/useRemoteHosts";
import { toWorkspaceSession } from "../hooks/useWorkspaceQuery";
import { LOCAL_HOST } from "../lib/hosts";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { FleetOverview } from "./FleetOverview";

// The details' history reads are covered in FleetSessionDetails.test.tsx; here
// only what the tree hands them matters.
vi.mock("./FleetSessionDetails", () => ({
	FleetSessionDetails: (props: {
		session: WorkspaceSession;
		hostId?: string;
		parentName?: string;
		roleLabel: string;
		statusLabel: string;
		sessionNames: ReadonlyMap<string, string>;
		onOpen: () => void;
		onClose: () => void;
	}) => (
		<div role="group" aria-label={`Details for ${props.session.title}`} data-host={props.hostId}>
			<p>{`${props.roleLabel} · ${props.statusLabel}`}</p>
			<p>{props.parentName ? `Spawned by ${props.parentName}` : "No recorded parent"}</p>
			<p>{`Known: ${[...props.sessionNames.values()].join(", ")}`}</p>
			<button type="button" onClick={props.onOpen}>Open session</button>
			<button type="button" onClick={props.onClose}>Close details</button>
		</div>
	),
}));

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

function mappedSessionWithFailingPR(): WorkspaceSession {
	const view: components["schemas"]["ControllersSessionView"] = {
		activity: { state: "idle", lastActivityAt: "2026-11-17T12:00:00Z" },
		autoInjectCI: true,
		autoInjectReview: true,
		autoReviewEnabled: false,
		chatProviderPreserved: false,
		contractStalled: false,
		createdAt: "2026-11-17T12:00:00Z",
		displayStatus: "CI failing",
		id: "mapped-worker",
		isPinned: false,
		isTerminated: false,
		kanbanColumn: "validating",
		kind: "worker",
		lastEventAt: "2026-11-17T12:00:00Z",
		mode: "chat",
		needsResume: false,
		outputType: "pr",
		prs: [{
			url: "https://example.test/pr/1",
			number: 1,
			state: "open",
			ci: "failing",
			review: "review_required",
			mergeability: "blocked",
			reviewComments: false,
			updatedAt: "2026-11-17T12:00:00Z",
		}],
		scmStatus: "ci_failed",
		status: "working",
		statusReadiness: "ready",
		terminateOnPrMerge: false,
		updatedAt: "2026-11-17T12:00:00Z",
		workspacePublished: true,
	};
	return toWorkspaceSession(view, { id: "project", name: "Project" });
}

describe("FleetOverview", () => {
	beforeEach(() => window.localStorage.clear());

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
		expect(onOpen).not.toHaveBeenCalled();
		expect(screen.getByRole("group", { name: "Details for Fix cache" })).toHaveAttribute("data-host", "build-box");
		await userEvent.dblClick(sessions[1]!);
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

	it("does not apply empty remote readiness arrays to the local machine", () => {
		render(
			<FleetOverview
				localWorkspaces={[workspace("project", "Project", [session("local", "Local worker")])]}
				onOpen={vi.fn()}
				remoteFailedHostIds={[]}
				remoteLoadedProjectHostIds={[]}
				remoteLoadedSessionHostIds={[]}
			/>,
		);

		expect(screen.getByRole("treeitem", { name: /This computer/ })).not.toHaveAccessibleName(/Checking/);
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
				pr(1, { ci: "failing" }),
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
			expect(within(row).getByText("Checks failing")).toBeInTheDocument();
		}
		expect(screen.getByRole("treeitem", { name: /^Project/ })).toHaveTextContent("1 Needs you");
	});

	it("rolls failing checks from the real lightweight workspace mapper without inventing a count", () => {
		render(<FleetOverview localWorkspaces={[workspace("project", "Project", [mappedSessionWithFailingPR()])]} onOpen={vi.fn()} />);

		const project = screen.getByRole("treeitem", { name: /^Project/ });
		expect(within(project).getByText("PRs: 1")).toBeInTheDocument();
		expect(within(project).getByText("Checks failing")).toBeInTheDocument();
		expect(project).not.toHaveTextContent("Checks failing:");
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

	it("keeps urgent live state above a less urgent open PR", () => {
		const blocked = session("blocked", "Answer deploy question", "needs_input", {
			scmStatus: "pr_open",
			prs: [pr(1)],
		});
		render(<FleetOverview localWorkspaces={[workspace("project", "Project", [blocked])]} onOpen={vi.fn()} />);

		const project = screen.getByRole("treeitem", { name: /^Project/ });
		expect(project).toHaveTextContent("1 Needs you");
		expect(project).not.toHaveTextContent("In review");
	});

	it.each(["merged", undefined] as const)("keeps live work above a merged-only PR with scmStatus %s", (scmStatus) => {
		const worker = session("worker", "Continue work", "working", {
			scmStatus,
			prs: [pr(1, { state: "merged" })],
		});
		render(<FleetOverview localWorkspaces={[workspace("project", "Project", [worker])]} onOpen={vi.fn()} />);

		const project = screen.getByRole("treeitem", { name: /^Project/ });
		expect(project).toHaveTextContent("1 Working");
		expect(project).not.toHaveTextContent("Ready");
	});

	it.each(["merged", undefined] as const)("keeps a non-terminated merged session live with scmStatus %s", (scmStatus) => {
		const worker = session("worker", "Continue work", "merged", {
			isTerminated: false,
			scmStatus,
			prs: [pr(1, { state: "merged" })],
		});
		render(<FleetOverview localWorkspaces={[workspace("project", "Project", [worker])]} onOpen={vi.fn()} />);

		const project = screen.getByRole("treeitem", { name: /^Project/ });
		expect(project).toHaveTextContent("1 Working");
		expect(project).not.toHaveTextContent("Ready");
		expect(screen.getByRole("treeitem", { name: /Continue work/ })).toHaveTextContent("Merged");
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

	it("draws a remote worker under its orchestrator on this computer by exact address", async () => {
		const onOpen = vi.fn();
		const orchestrator = session("orch", "Coordinator", "working", { kind: "orchestrator" });
		const sameId = session("orch", "Remote namesake", "idle", { hostId: "build-box" });
		const worker = session("worker", "Remote worker", "needs_input", {
			hostId: "build-box",
			parentHostId: "h_local",
			parentSessionId: "orch",
		});
		render(
			<FleetOverview
				localHostId="h_local"
				localWorkspaces={[workspace("project", "Alpha", [orchestrator])]}
				onOpen={onOpen}
				remoteHosts={[host("build-box", "Build box", "connected")]}
				remoteWorkspaces={[
					workspace("remote-project", "Alpha box", [worker], { hostId: "build-box" }),
					workspace("other", "Other", [sameId], { hostId: "build-box" }),
				]}
			/>,
		);

		const parent = screen.getByRole("treeitem", { name: /^Coordinator/ });
		const child = screen.getByRole("treeitem", { name: /^Remote worker/ });
		expect(parent).toHaveAttribute("aria-level", "3");
		expect(child).toHaveAttribute("aria-level", "4");
		expect(child).toHaveAccessibleName(/On Build box/);
		expect(child).not.toHaveAccessibleName(/Parent/);
		expect(within(child).getByText("On Build box")).toBeInTheDocument();
		expect(screen.queryByText("Alpha box")).not.toBeInTheDocument();
		expect(screen.getByRole("treeitem", { name: /^Remote namesake/ })).toHaveAttribute("aria-level", "3");

		await userEvent.click(child);
		const details = screen.getByRole("group", { name: "Details for Remote worker" });
		expect(details).toHaveAttribute("data-host", "build-box");
		expect(details).toHaveTextContent("Spawned by Coordinator");
		// History names come from the worker's own machine, not from this computer.
		expect(details).toHaveTextContent("Known: Remote worker, Remote namesake");
		await userEvent.click(within(details).getByRole("button", { name: "Open session" }));
		expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({ id: "worker" }), "build-box");
	});

	it("keeps a remote child as a marked root when its parent machine is offline or unknown", () => {
		const parent = session("orch", "Cached coordinator", "working", { kind: "orchestrator", hostId: "parent-box" });
		const offlineChild = session("offline-child", "Waiting worker", "working", {
			hostId: "build-box",
			parentHostId: "parent-box",
			parentSessionId: "orch",
		});
		const unknownChild = session("unknown-child", "Stranger worker", "working", {
			hostId: "build-box",
			parentHostId: "h_unpaired",
			parentSessionId: "orch",
		});
		render(
			<FleetOverview
				localWorkspaces={[]}
				onOpen={vi.fn()}
				remoteHosts={[host("build-box", "Build box", "connected"), host("parent-box", "Parent box", "offline")]}
				remoteWorkspaces={[
					workspace("parents", "Parents", [parent], { hostId: "parent-box" }),
					workspace("children", "Children", [offlineChild, unknownChild], { hostId: "build-box" }),
				]}
			/>,
		);

		const offline = screen.getByRole("treeitem", { name: /^Waiting worker/ });
		expect(offline).toHaveAttribute("aria-level", "3");
		expect(within(offline).getByText("Parent's machine not connected")).toBeInTheDocument();
		const unknown = screen.getByRole("treeitem", { name: /^Stranger worker/ });
		expect(unknown).toHaveAttribute("aria-level", "3");
		expect(unknown).toHaveAccessibleName(/Parent on an unknown machine/);
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

	it("starts a large branch collapsed, keeps its rollup visible, and remembers an explicit expansion", async () => {
		const busy = Array.from({ length: 8 }, (_, index) => session(`worker-${index}`, `Worker ${index}`));
		const view = render(<FleetOverview localWorkspaces={[workspace("project", "Busy project", busy)]} onOpen={vi.fn()} />);

		const project = screen.getByRole("treeitem", { name: /^Busy project/ });
		expect(project).toHaveAttribute("aria-expanded", "false");
		expect(project).toHaveTextContent("8 Working");
		expect(screen.queryByText("This computer")).not.toBeInTheDocument();

		await userEvent.click(project);
		expect(screen.getByText("This computer")).toBeInTheDocument();
		expect(Object.values(JSON.parse(window.localStorage.getItem("ao.fleet.branch-state.v1") ?? "{}"))).toContain(false);

		view.unmount();
		render(<FleetOverview localWorkspaces={[workspace("project", "Busy project", busy)]} onOpen={vi.fn()} />);
		expect(screen.getByRole("treeitem", { name: /^Busy project/ })).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByText("This computer")).toBeInTheDocument();
	});

	it("evicts the oldest collapse choice when storage is full", async () => {
		window.localStorage.setItem("ao.fleet.branch-state.v1", JSON.stringify(Object.fromEntries(
			Array.from({ length: 2_000 }, (_, index) => [`old-${index}`, true]),
		)));
		render(<FleetOverview localWorkspaces={[workspace("project", "Project", [session("worker", "Worker")])]} onOpen={vi.fn()} />);

		await userEvent.click(screen.getByRole("treeitem", { name: /^Project/ }));

		const saved = JSON.parse(window.localStorage.getItem("ao.fleet.branch-state.v1") ?? "{}");
		expect(Object.keys(saved)).toHaveLength(2_000);
		expect(saved["old-0"]).toBeUndefined();
		expect(Object.keys(saved).some((key) => key.startsWith("["))).toBe(true);
	});

	it("remembers a collapsed host-local project without folding the same id on another machine", async () => {
		const local = workspace("same", "Same", [session("local", "Local")]);
		const remote = workspace("same", "Same", [session("remote", "Remote", "idle", { hostId: "build-box" })], { hostId: "build-box" });
		const view = render(
			<FleetOverview
				localWorkspaces={[local]}
				onOpen={vi.fn()}
				remoteHosts={[host("build-box", "Build box", "connected")]}
				remoteWorkspaces={[remote]}
			/>,
		);
		const [first, second] = screen.getAllByRole("treeitem", { name: /^Same/ });
		await userEvent.click(first!);
		expect(first).toHaveAttribute("aria-expanded", "false");
		expect(second).toHaveAttribute("aria-expanded", "true");

		view.unmount();
		render(
			<FleetOverview
				localWorkspaces={[local]}
				onOpen={vi.fn()}
				remoteHosts={[host("build-box", "Build box", "connected")]}
				remoteWorkspaces={[remote]}
			/>,
		);
		const restored = screen.getAllByRole("treeitem", { name: /^Same/ });
		expect(restored[0]).toHaveAttribute("aria-expanded", "false");
		expect(restored[1]).toHaveAttribute("aria-expanded", "true");
	});
	it("scopes to one project on one machine by exact identity and starts at its machine", () => {
		const local = workspace("alpha", "Alpha", [session("same", "Local fix")]);
		const remote = workspace("alpha", "Alpha", [session("same", "Remote fix", "idle", { hostId: "build-box" })], { hostId: "build-box" });
		const other = workspace("beta", "Beta", [session("other", "Other project work")]);
		const props = {
			localWorkspaces: [local, other],
			onOpen: vi.fn(),
			remoteHosts: [host("build-box", "Build box", "connected"), host("idle-box", "Idle box", "connected")],
			remoteWorkspaces: [remote],
		};
		const view = render(<FleetOverview {...props} scope={{ projectId: "alpha", machineId: LOCAL_HOST }} />);

		expect(screen.queryByRole("treeitem", { name: /^Alpha/ })).not.toBeInTheDocument();
		expect(screen.getByRole("treeitem", { name: /^This computer/ })).toHaveAttribute("aria-level", "1");
		expect(screen.getByRole("treeitem", { name: /^Local fix/ })).toHaveAttribute("aria-level", "2");
		expect(screen.queryByText("Remote fix")).not.toBeInTheDocument();
		expect(screen.queryByText("Other project work")).not.toBeInTheDocument();
		expect(screen.queryByText("Build box")).not.toBeInTheDocument();
		expect(screen.queryByText("Idle box")).not.toBeInTheDocument();

		view.unmount();
		render(<FleetOverview {...props} scope={{ projectId: "alpha", machineId: "build-box" }} />);
		expect(screen.getByRole("treeitem", { name: /^Build box/ })).toHaveAttribute("aria-level", "1");
		expect(screen.getByRole("treeitem", { name: /^Remote fix/ })).toBeInTheDocument();
		expect(screen.queryByText("Local fix")).not.toBeInTheDocument();
		expect(screen.queryByText("This computer")).not.toBeInTheDocument();
	});

	it("keeps a child on another machine under its parent in a project-scoped tree", () => {
		const orchestrator = session("orch", "Coordinator", "working", { kind: "orchestrator" });
		const worker = session("worker", "Remote worker", "needs_input", {
			hostId: "build-box",
			parentHostId: "h_local",
			parentSessionId: "orch",
		});
		render(
			<FleetOverview
				localHostId="h_local"
				localWorkspaces={[workspace("project", "Alpha", [orchestrator])]}
				onOpen={vi.fn()}
				remoteHosts={[host("build-box", "Build box", "connected")]}
				remoteWorkspaces={[workspace("remote-project", "Alpha box", [worker], { hostId: "build-box" })]}
				scope={{ projectId: "project", machineId: LOCAL_HOST }}
			/>,
		);

		expect(screen.getByRole("treeitem", { name: /^Coordinator/ })).toHaveAttribute("aria-level", "2");
		const child = screen.getByRole("treeitem", { name: /^Remote worker/ });
		expect(child).toHaveAttribute("aria-level", "3");
		expect(within(child).getByText("On Build box")).toBeInTheDocument();
		expect(screen.getByRole("treeitem", { name: /^This computer/ })).toHaveTextContent("1 Needs you");
	});

	it("opens a project-scoped tree on its machine and top sessions, folding only deeper large branches", () => {
		const orchestrator = session("orch", "Coordinator", "working", { kind: "orchestrator" });
		const manager = session("manager", "Busy manager", "working", { parentSessionId: "orch" });
		const workers = Array.from({ length: 8 }, (_, index) => session(`worker-${index}`, `Worker ${index}`, "working", { parentSessionId: "manager" }));
		const local = workspace("project", "Alpha", [orchestrator, manager, ...workers]);
		const view = render(<FleetOverview localWorkspaces={[local]} onOpen={vi.fn()} />);
		expect(screen.getByRole("treeitem", { name: /^Alpha/ })).toHaveAttribute("aria-expanded", "false");

		view.unmount();
		render(<FleetOverview localWorkspaces={[local]} onOpen={vi.fn()} scope={{ projectId: "project", machineId: LOCAL_HOST }} />);
		expect(screen.getByRole("treeitem", { name: /^This computer/ })).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByRole("treeitem", { name: /^Coordinator/ })).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByRole("treeitem", { name: /^Busy manager/ })).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByText("Worker 0")).not.toBeInTheDocument();
	});

	it("shows an empty project's machine with no sessions", () => {
		render(
			<FleetOverview
				localWorkspaces={[workspace("empty", "Empty", []), workspace("busy", "Busy", [session("w", "Elsewhere")])]}
				onOpen={vi.fn()}
				scope={{ projectId: "empty", machineId: LOCAL_HOST }}
			/>,
		);

		expect(screen.getAllByRole("treeitem")).toHaveLength(1);
		expect(screen.getByRole("treeitem", { name: /^This computer/ })).toHaveTextContent("No sessions to display.");
		expect(screen.queryByText("Elsewhere")).not.toBeInTheDocument();
	});

	it("unfolds a session's details under its row and folds them with a second pick, Escape or Close", async () => {
		const onOpen = vi.fn();
		const orchestrator = session("orch", "Coordinator", "working", { kind: "orchestrator" });
		const worker = session("worker", "Picked worker", "working", { parentSessionId: "orch" });
		const orphan = session("orphan", "Orphan", "working", { parentSessionId: "gone", parentHostId: "h_unpaired" });
		render(<FleetOverview localWorkspaces={[workspace("project", "Project", [orchestrator, worker, orphan])]} onOpen={onOpen} />);

		const row = screen.getByRole("treeitem", { name: /^Picked worker/ });
		expect(row).toHaveAttribute("aria-selected", "false");
		await userEvent.click(row);
		expect(row).toHaveAttribute("aria-selected", "true");
		const details = screen.getByRole("group", { name: "Details for Picked worker" });
		expect(details).toHaveTextContent("Worker · Working");
		expect(details).toHaveTextContent("Spawned by Coordinator");
		// The details sit directly under the picked row.
		expect(row.nextElementSibling).toBe(details);
		expect(onOpen).not.toHaveBeenCalled();

		await userEvent.click(row);
		expect(screen.queryByRole("group")).not.toBeInTheDocument();

		row.focus();
		fireEvent.keyDown(row, { key: " " });
		expect(screen.getByRole("group", { name: "Details for Picked worker" })).toBeInTheDocument();
		fireEvent.keyDown(row, { key: "Escape" });
		expect(screen.queryByRole("group")).not.toBeInTheDocument();
		expect(row).toHaveFocus();

		await userEvent.click(screen.getByRole("treeitem", { name: /^Orphan/ }));
		expect(screen.getByRole("group", { name: "Details for Orphan" })).toHaveTextContent("Spawned by h_unpaired/gone");
		await userEvent.click(screen.getByRole("treeitem", { name: /^Coordinator/ }));
		expect(screen.queryByRole("group", { name: "Details for Orphan" })).not.toBeInTheDocument();
		expect(screen.getByRole("group", { name: "Details for Coordinator" })).toHaveTextContent("No recorded parent");
		await userEvent.click(screen.getByRole("button", { name: "Close details" }));
		expect(screen.queryByRole("group")).not.toBeInTheDocument();
		expect(screen.getByRole("treeitem", { name: /^Coordinator/ })).toHaveFocus();
	});
	it("keeps this project's session spawned from another machine as a root that names its parent", async () => {
		const orchestrator = session("orch", "M6 coordinator", "working", { kind: "orchestrator" });
		const aide = session("aide", "Rotation aide", "working", { hostId: "m4", parentHostId: "h_local", parentSessionId: "orch" });
		render(
			<FleetOverview
				localHostId="h_local"
				localWorkspaces={[workspace("sky", "Sky", [orchestrator])]}
				onOpen={vi.fn()}
				remoteHosts={[host("m4", "M4 Mini", "connected")]}
				remoteWorkspaces={[workspace("sky", "Sky", [aide], { hostId: "m4" })]}
				scope={{ projectId: "sky", machineId: "m4" }}
			/>,
		);

		expect(screen.getByRole("treeitem", { name: /^M4 Mini/ })).toHaveTextContent("1 Working");
		const row = screen.getByRole("treeitem", { name: /^Rotation aide.*Spawned by M6 coordinator/ });
		expect(row).toHaveAttribute("aria-level", "2");
		expect(screen.queryByText("M6 coordinator")).not.toBeInTheDocument();
		await userEvent.click(row);
		expect(screen.getByRole("group", { name: "Details for Rotation aide" })).toHaveTextContent("Spawned by M6 coordinator");
	});
});
