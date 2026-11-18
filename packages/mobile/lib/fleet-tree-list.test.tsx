import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSession } from "./api";
import type { ArchiveSource } from "./archive/useArchivedSessions";
import type { HostedProject, HostedSession } from "./hostedRows";

const fixtures = vi.hoisted(() => ({
	push: vi.fn(),
	blur: () => {},
	getItem: vi.fn(async (_key: string) => null as string | null),
	setItem: vi.fn(async (_key: string, _value: string) => {}),
	getSession: vi.fn(),
	archiveOptions: null as null | { active: boolean; exhaustive: boolean },
	archive: {
		rows: [] as HostedSession[], total: 0, loading: false, error: null as string | null,
		hasMore: false, loadMore: vi.fn(), retry: vi.fn(),
	},
	app: {
		kill: vi.fn(async () => {}), renameWorker: vi.fn(async () => {}), setWorkerPinned: vi.fn(async () => {}),
		restore: vi.fn(async () => {}), resumeAgent: vi.fn(async () => {}), configForHost: vi.fn(),
		connection: "open", currentHostId: "box", hostStates: [], projects: [], allProjects: [],
	},
	theme: new Proxy({}, { get: (_, key) => `#${String(key)}` }),
}));

vi.mock("expo-router", () => ({
	useRouter: () => ({ push: fixtures.push }),
	useFocusEffect: (callback: () => void | (() => void)) => { fixtures.blur = callback() ?? (() => {}); },
}));
vi.mock("./api", () => ({ getSession: fixtures.getSession }));
vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: fixtures.getItem, setItem: fixtures.setItem },
}));
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		FlatList: React.forwardRef(function FlatList({ data, renderItem, ListHeaderComponent, ListEmptyComponent, ...props }: any, ref) {
			React.useImperativeHandle(ref, () => ({ scrollToOffset: vi.fn() }));
			return React.createElement("FlatList", { ...props, data },
				ListHeaderComponent,
				data.length ? data.map((item: unknown, index: number) => React.createElement(React.Fragment, { key: (item as { key: string }).key }, renderItem({ item, index }))) : ListEmptyComponent);
		}),
		Platform: { OS: "ios" },
		Pressable: host("Pressable"),
		RefreshControl: host("RefreshControl"),
		StyleSheet: { create: (styles: object) => styles },
		Text: host("Text"),
		View: host("View"),
		Alert: { alert: vi.fn() },
	};
});
vi.mock("./store", () => ({ useApp: () => fixtures.app }));
vi.mock("./archive/useArchivedSessions", () => ({
	useArchivedSessions: (_sources: unknown, options: { active: boolean; exhaustive: boolean }) => {
		fixtures.archiveOptions = options;
		return fixtures.archive;
	},
}));
vi.mock("./worker-list-row", () => ({ WorkerListRow: "WorkerListRow" }));
vi.mock("./archived-worker-row", () => ({ ArchivedWorkerRow: "ArchivedWorkerRow" }));
vi.mock("./haptics", () => ({ haptics: { success: vi.fn(), error: vi.fn(), warning: vi.fn() } }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemedStyles: (factory: (theme: object) => object) => factory(fixtures.theme),
}));
vi.mock("./AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("./ui", () => ({ Dot: "Dot" }));
vi.mock("./refreshControlColors", () => ({ refreshControlColors: () => ({}) }));

import { FleetTreeList } from "./fleet-tree-list";
import { FLEET_COLLAPSE_STORAGE_KEY } from "./fleet-collapse-store";

function worker(id: string, over: Partial<DashboardSession> = {}): HostedSession {
	return {
		id,
		projectId: "project",
		hostId: "box",
		hostName: "Build box",
		status: "working",
		mode: "chat",
		branch: null,
		issueId: null,
		issueTitle: null,
		userPrompt: null,
		displayName: id,
		summary: null,
		createdAt: "2026-11-17T12:00:00Z",
		lastActivityAt: "2026-11-17T12:00:00Z",
		...over,
	};
}

const project: HostedProject = { id: "project", name: "Project", hostId: "box", hostName: "Build box" };
let mountedViews: ReactTestRenderer[] = [];

function render(
	sessions: HostedSession[],
	archiveSources: readonly ArchiveSource[] = [],
	connection: "closed" | "connecting" | "open" = "closed",
): ReactTestRenderer {
	let view!: ReactTestRenderer;
	act(() => {
		view = create(
			<FleetTreeList
				contentBottomInset={0}
				archiveSources={archiveSources}
				hosts={[{ hostId: "box", name: "Build box", connection }]}
				onRefresh={() => {}}
				orchestrators={[]}
				projects={[project]}
				refreshing={false}
				sessions={sessions}
			/>,
		);
	});
	mountedViews.push(view);
	return view;
}

afterEach(() => {
	act(() => {
		for (const view of mountedViews) view.unmount();
	});
	mountedViews = [];
});

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.push.mockClear();
	fixtures.getItem.mockReset().mockResolvedValue(null);
	fixtures.setItem.mockClear();
	fixtures.getSession.mockReset().mockImplementation(() => new Promise(() => {}));
	fixtures.app.configForHost.mockReset().mockReturnValue({
		host: "box.test", httpPort: "3011", muxPort: "3011", password: "secret", hostId: "box",
	});
	fixtures.app.restore.mockReset().mockResolvedValue(undefined);
	fixtures.archiveOptions = null;
	Object.assign(fixtures.archive, { rows: [], total: 0, loading: false, error: null, hasMore: false });
	fixtures.archive.loadMore.mockClear();
	fixtures.archive.retry.mockClear();
});

describe("FleetTreeList", () => {
	it("keeps a large offline branch summarized while folded and persists disclosure", async () => {
		const view = render(Array.from({ length: 8 }, (_, index) => worker(`Worker ${index}`)));
		await act(async () => {});
		const projectRow = view.root.findByProps({ accessibilityLabel: "Project, 8 working" });
		expect(projectRow.props.accessibilityState).toEqual({ expanded: false });
		expect(view.root.findAllByProps({ children: "Build box" })).toHaveLength(0);

		act(() => projectRow.props.onPress());
		expect(view.root.findByProps({ accessibilityLabel: "Build box, offline, 8 working" })).toBeTruthy();
		expect(fixtures.setItem).toHaveBeenCalledWith(
			FLEET_COLLAPSE_STORAGE_KEY,
			expect.stringContaining(":false"),
		);
	});

	it("routes a worker row through its owning machine", () => {
		const view = render([worker("Manager"), worker("Child", { parentSessionId: "Manager", status: "needs_input" })]);
		const child = view.root.findAllByType("WorkerListRow" as never)
			.find((row) => row.props.session.id === "Child")!;

		expect(child.props.hostId).toBe("box");
		expect(child.props.session.projectId).toBe("project");
		expect(view.root.findAllByType("WorkerListRow" as never).find((row) => row.props.session.id === "Manager")?.props.treeDepth).toBe(2);
		expect(child.props.treeDepth).toBe(3);
	});

	it("announces descendant attention and missing-parent state", () => {
		const view = render([
			worker("Manager"),
			worker("Child", { parentSessionId: "Manager", status: "needs_input" }),
			worker("Orphan", { parentSessionId: "missing" }),
		]);

		const rows = view.root.findAllByType("WorkerListRow" as never);
		expect(rows.find((row) => row.props.session.id === "Manager")?.props.projectName).toBe("Manager · 1 needs you");
		expect(rows.find((row) => row.props.session.id === "Orphan")?.props.projectName).toBe("Worker · Parent unavailable");
	});

	it("renders worker rows through the existing action-capable row", () => {
		const view = render([worker("Worker")]);
		const row = view.root.findByType("WorkerListRow" as never);

		expect(row.props.session.id).toBe("Worker");
		expect(row.props.onSetPinned).toEqual(expect.any(Function));
		expect(row.props.onRename).toEqual(expect.any(Function));
		expect(row.props.onDelete).toEqual(expect.any(Function));
		expect(row.props.onResume).toEqual(expect.any(Function));
	});

	it("hydrates an archived parent and keeps the archive disclosure available", () => {
		fixtures.archive.rows = [
			worker("Grandparent", { status: "terminated", isTerminated: true }),
			worker("Parent", { parentSessionId: "Grandparent", status: "terminated", isTerminated: true }),
			worker("Unrelated", { projectId: "other", status: "terminated", isTerminated: true }),
		];
		fixtures.archive.total = 3;
		const sources: ArchiveSource[] = [{ hostId: "box", summary: { total: 3, byProject: { project: 3 } } }];
		const view = render([worker("Child", { parentSessionId: "Parent" })], sources);

		expect(view.root.findAllByType("WorkerListRow" as never).map((row) => row.props.session.id)).toEqual(["Grandparent", "Parent", "Child"]);
		expect(view.root.findByProps({ accessibilityLabel: "Archive, 3 sessions" })).toBeTruthy();
		expect(view.root.findAllByProps({ accessibilityLabel: "other, No sessions" })).toHaveLength(0);
		expect(fixtures.archiveOptions).toMatchObject({ active: false, exhaustive: false });
	});

	it("looks up a replaced orchestrator directly without exhausting the worker archive", async () => {
		let answer!: (session: DashboardSession) => void;
		fixtures.getSession.mockReturnValueOnce(new Promise((resolve) => { answer = resolve; }));
		const historical = {
			...worker("Past orchestrator", { kind: "orchestrator", status: "terminated", isTerminated: true }),
			hostId: undefined,
			hostName: undefined,
		};
		const view = render([worker("Child", { parentSessionId: "Past orchestrator" })]);

		expect(fixtures.getSession).toHaveBeenCalledWith(
			expect.objectContaining({ hostId: "box" }),
			"Past orchestrator",
			expect.objectContaining({ signal: expect.any(AbortSignal) }),
		);
		await act(async () => {
			answer(historical);
			await new Promise((resolve) => setTimeout(resolve, 0));
		});
		const labels = view.root.findAllByType("Pressable" as never)
			.map((row) => row.props.accessibilityLabel)
			.filter(Boolean);
		expect(labels).toEqual(expect.arrayContaining([expect.stringContaining("Past orchestrator, Orchestrator")]));
		expect(fixtures.archiveOptions).toMatchObject({ active: false, exhaustive: false });
	});

	it("serializes archive restore and opens the restored session", async () => {
		let finishRestore!: () => void;
		fixtures.app.restore.mockReturnValueOnce(new Promise<void>((resolve) => { finishRestore = resolve; }));
		const archived = worker("Archived", { status: "terminated", isTerminated: true });
		fixtures.archive.rows = [archived];
		fixtures.archive.total = 1;
		const view = render([worker("Live")], [], "open");

		act(() => view.root.findByProps({ accessibilityLabel: "Archive, 1 session" }).props.onPress());
		expect(fixtures.archiveOptions).toMatchObject({ active: true, exhaustive: false });
		expect(view.root.findByType("FlatList" as never).props.onEndReached).toBe(fixtures.archive.loadMore);
		let row = view.root.findByType("ArchivedWorkerRow" as never);
		act(() => row.props.onRestore(archived));
		row = view.root.findByType("ArchivedWorkerRow" as never);

		expect(row.props.restoring).toBe(true);
		expect(row.props.restoreDisabled).toBe(true);
		act(() => row.props.onRestore(archived));
		expect(fixtures.app.restore).toHaveBeenCalledTimes(1);

		await act(async () => finishRestore());
		expect(fixtures.push).toHaveBeenCalledWith({
			pathname: "/session/[id]",
			params: { id: "Archived", projectId: "project", hostId: "box" },
		});
	});

	it("does not navigate when archive restore finishes after the page loses focus", async () => {
		let finishRestore!: () => void;
		fixtures.app.restore.mockReturnValueOnce(new Promise<void>((resolve) => { finishRestore = resolve; }));
		const archived = worker("Archived", { status: "terminated", isTerminated: true });
		fixtures.archive.rows = [archived];
		fixtures.archive.total = 1;
		const view = render([worker("Live")], [], "open");

		act(() => view.root.findByProps({ accessibilityLabel: "Archive, 1 session" }).props.onPress());
		act(() => view.root.findByType("ArchivedWorkerRow" as never).props.onRestore(archived));
		act(() => fixtures.blur());
		await act(async () => finishRestore());

		expect(fixtures.push).not.toHaveBeenCalled();
	});

	it("merges a tap made before stored disclosure preferences finish loading", async () => {
		let resolveStored!: (value: string | null) => void;
		fixtures.getItem.mockReturnValueOnce(new Promise((resolve) => { resolveStored = resolve; }));
		const view = render(Array.from({ length: 8 }, (_, index) => worker(`Worker ${index}`)));
		const projectRow = view.root.findByProps({ accessibilityLabel: "Project, 8 working" });

		act(() => projectRow.props.onPress());
		expect(fixtures.setItem).not.toHaveBeenCalled();
		await act(async () => resolveStored(JSON.stringify({ remembered: true })));

		const saved = JSON.parse(fixtures.setItem.mock.calls.at(-1)![1]);
		expect(saved).toMatchObject({ remembered: true });
		expect(Object.values(saved)).toContain(false);
	});
});
