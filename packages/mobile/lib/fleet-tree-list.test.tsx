import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSession } from "./api";
import type { HostedProject, HostedSession } from "./hostedRows";

const fixtures = vi.hoisted(() => ({
	push: vi.fn(),
	getItem: vi.fn(async () => null as string | null),
	setItem: vi.fn(async () => {}),
	theme: new Proxy({}, { get: (_, key) => `#${String(key)}` }),
}));

vi.mock("expo-router", () => ({ useRouter: () => ({ push: fixtures.push }) }));
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
	};
});
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

function render(sessions: HostedSession[]): ReactTestRenderer {
	let view!: ReactTestRenderer;
	act(() => {
		view = create(
			<FleetTreeList
				contentBottomInset={0}
				hosts={[{ hostId: "box", name: "Build box", connection: "closed" }]}
				onRefresh={() => {}}
				orchestrators={[]}
				projects={[project]}
				refreshing={false}
				sessions={sessions}
			/>,
		);
	});
	return view;
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.push.mockClear();
	fixtures.getItem.mockReset().mockResolvedValue(null);
	fixtures.setItem.mockClear();
});

describe("FleetTreeList", () => {
	it("keeps a large offline branch summarized while folded and persists disclosure", () => {
		const view = render(Array.from({ length: 8 }, (_, index) => worker(`Worker ${index}`)));
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

	it("opens a worker through its owning machine", () => {
		const view = render([worker("Manager"), worker("Child", { parentSessionId: "Manager", status: "needs_input" })]);
		const child = view.root.findByProps({ accessibilityLabel: "Open Child, Worker, Needs input" });

		act(() => child.props.onPress());

		expect(fixtures.push).toHaveBeenCalledWith({
			pathname: "/session/[id]",
			params: { id: "Child", projectId: "project", hostId: "box" },
		});
	});
});
