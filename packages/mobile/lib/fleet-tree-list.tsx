import { useRouter } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactElement, type RefObject } from "react";
import {
	FlatList,
	Platform,
	Pressable,
	RefreshControl,
	StyleSheet,
	Text,
	View,
	type ScrollViewProps,
} from "react-native";
import {
	resolveFleetCollapsedKeys,
	type FleetMachineNode,
	type FleetProjectNode,
	type FleetRollup,
	type FleetSessionNode,
} from "../../product-ui/src/fleet-tree";
import { AppSymbol } from "./AppSymbol";
import {
	readFleetCollapsePreferences,
	writeFleetCollapsePreferences,
} from "./fleet-collapse-store";
import {
	buildMobileFleetTree,
	type MobileFleetHost,
	type MobileFleetSession,
} from "./fleet-tree-model";
import type { HostedOrchestrator, HostedProject, HostedSession } from "./hostedRows";
import { refreshControlColors } from "./refreshControlColors";
import { statusVisual, type Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { Dot } from "./ui";
import { gutter, iconSize, space, touchTarget, type } from "./tokens";
import { rowDividerWidth } from "./divider";

type ProjectRow = { kind: "project"; key: string; depth: number; node: FleetProjectNode<MobileFleetSession> };
type MachineRow = { kind: "machine"; key: string; depth: number; node: FleetMachineNode<MobileFleetSession> };
type SessionRow = { kind: "session"; key: string; depth: number; node: FleetSessionNode<MobileFleetSession> };
export type MobileFleetRow = ProjectRow | MachineRow | SessionRow;

const rollupOrder = ["action", "merge", "pending", "working", "done"] as const;
const rollupLabels = {
	action: "needs you",
	merge: "ready",
	pending: "in review",
	working: "working",
	done: "done",
} as const;

function rollupText(rollup: FleetRollup): string {
	return rollupOrder
		.filter((zone) => rollup[zone] > 0)
		.map((zone) => `${rollup[zone]} ${rollupLabels[zone]}`)
		.join(" · ") || "No sessions";
}

function flattenRows(
	projects: readonly FleetProjectNode<MobileFleetSession>[],
	collapsed: ReadonlySet<string>,
): MobileFleetRow[] {
	const rows: MobileFleetRow[] = [];
	const addSessions = (nodes: readonly FleetSessionNode<MobileFleetSession>[], depth: number) => {
		for (const node of nodes) {
			rows.push({ kind: "session", key: node.key, depth, node });
			if (!collapsed.has(node.key)) addSessions(node.children, depth + 1);
		}
	};
	for (const node of projects) {
		rows.push({ kind: "project", key: node.key, depth: 0, node });
		if (collapsed.has(node.key)) continue;
		for (const machine of node.machines) {
			rows.push({ kind: "machine", key: machine.key, depth: 1, node: machine });
			if (!collapsed.has(machine.key)) addSessions(machine.sessions, 2);
		}
	}
	return rows;
}

function rowHasChildren(row: MobileFleetRow): boolean {
	if (row.kind === "project") return row.node.machines.length > 0;
	if (row.kind === "machine") return row.node.sessions.length > 0;
	return row.node.children.length > 0;
}

function sessionRole(node: FleetSessionNode<MobileFleetSession>): "Orchestrator" | "Manager" | "Worker" {
	if (node.role === "orchestrator") return "Orchestrator";
	if (node.role === "manager") return "Manager";
	return "Worker";
}

export function FleetTreeList({
	projects,
	sessions,
	orchestrators,
	hosts,
	listRef,
	contentBottomInset,
	contentInsetAdjustmentBehavior,
	overlayTopInset = 0,
	refreshing,
	onRefresh,
	ListHeaderComponent,
	ListEmptyComponent,
	onScroll,
}: {
	projects: readonly HostedProject[];
	sessions: readonly HostedSession[];
	orchestrators: readonly HostedOrchestrator[];
	hosts: readonly MobileFleetHost[];
	listRef?: RefObject<FlatList<MobileFleetRow> | null>;
	contentBottomInset: number;
	contentInsetAdjustmentBehavior?: ScrollViewProps["contentInsetAdjustmentBehavior"];
	overlayTopInset?: number;
	refreshing: boolean;
	onRefresh(): void | Promise<void>;
	ListHeaderComponent?: ReactElement | null;
	ListEmptyComponent?: ReactElement | null;
	onScroll?: ScrollViewProps["onScroll"];
}) {
	const router = useRouter();
	const theme = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [preferences, setPreferences] = useState<Record<string, boolean>>({});
	const touched = useRef(false);
	useEffect(() => {
		let active = true;
		void readFleetCollapsePreferences().then((stored) => {
			if (active && !touched.current) setPreferences(stored);
		});
		return () => { active = false; };
	}, []);

	const tree = useMemo(
		() => buildMobileFleetTree({ projects, sessions, orchestrators, hosts }),
		[hosts, orchestrators, projects, sessions],
	);
	const collapsed = useMemo(
		() => resolveFleetCollapsedKeys(tree, preferences),
		[preferences, tree],
	);
	const rows = useMemo(() => flattenRows(tree, collapsed), [collapsed, tree]);
	const hostsById = useMemo(() => new Map(hosts.map((host) => [host.hostId, host])), [hosts]);

	const setCollapsed = useCallback((key: string, nextCollapsed: boolean) => {
		touched.current = true;
		setPreferences((current) => {
			const next = { ...current, [key]: nextCollapsed };
			void writeFleetCollapsePreferences(next);
			return next;
		});
	}, []);
	const toggle = useCallback((key: string) => setCollapsed(key, !collapsed.has(key)), [collapsed, setCollapsed]);

	const renderItem = useCallback(({ item }: { item: MobileFleetRow }) => {
		const expandable = rowHasChildren(item);
		const expanded = expandable ? !collapsed.has(item.key) : undefined;
		if (item.kind === "project") {
			const summary = rollupText(item.node.rollup);
			return (
				<Pressable
					accessibilityLabel={`${item.node.project.name}, ${summary}`}
					accessibilityRole="button"
					accessibilityState={{ expanded }}
					onPress={() => toggle(item.key)}
					style={({ pressed }) => [styles.row, styles.projectRow, pressed && styles.pressed]}
				>
					<AppSymbol name={expanded ? "chevronDown" : "chevronForward"} size={iconSize.xs} />
					<AppSymbol name="folder" size={iconSize.md} />
					<Text numberOfLines={1} style={styles.title}>{item.node.project.name}</Text>
					<Text numberOfLines={1} style={styles.rollup}>{summary}</Text>
				</Pressable>
			);
		}
		if (item.kind === "machine") {
			const host = hostsById.get(item.node.machine.id);
			const connection = host?.connection === "connecting" ? "connecting"
				: host?.connection === "closed" || !item.node.machine.reachable ? "offline" : undefined;
			const summary = rollupText(item.node.rollup);
			return (
				<Pressable
					accessibilityLabel={[item.node.machine.name, connection, summary].filter(Boolean).join(", ")}
					accessibilityRole="button"
					accessibilityState={{ expanded }}
					onPress={() => toggle(item.key)}
					style={({ pressed }) => [styles.row, styles.indentedRow, pressed && styles.pressed]}
				>
					<AppSymbol name={expanded ? "chevronDown" : "chevronForward"} size={iconSize.xs} />
					<AppSymbol name="server" size={iconSize.md} />
					<View style={styles.copy}>
						<Text numberOfLines={1} style={styles.title}>{item.node.machine.name}</Text>
						{connection ? <Text style={styles.detail}>{connection === "offline" ? "Offline" : "Connecting…"}</Text> : null}
					</View>
					<Text numberOfLines={1} style={styles.rollup}>{summary}</Text>
				</Pressable>
			);
		}

		const role = sessionRole(item.node);
		const visual = statusVisual(theme, item.node.session.source.status);
		const detail = [role, visual.label, item.node.parentUnavailable ? "Parent unavailable" : undefined]
			.filter(Boolean).join(" · ");
		const descendants = rollupText(item.node.rollup);
		const hasDescendantRollup = rollupOrder.some((zone) => item.node.rollup[zone] > 0);
		return (
			<Pressable
				accessibilityLabel={[
					`Open ${item.node.session.title}`,
					role,
					visual.label,
					item.node.parentUnavailable ? "Parent unavailable" : undefined,
					hasDescendantRollup ? descendants : undefined,
				].filter(Boolean).join(", ")}
				accessibilityRole="button"
				onPress={() => router.push({
					pathname: "/session/[id]",
					params: { id: item.node.session.id, projectId: item.node.projectId, hostId: item.node.machineId },
				})}
				style={({ pressed }) => [
					styles.row,
					{ paddingLeft: gutter + item.depth * space.lg },
					pressed && styles.pressed,
				]}
			>
				{expandable ? (
					<Pressable
						accessibilityLabel={`${expanded ? "Collapse" : "Expand"} ${item.node.session.title}`}
						accessibilityRole="button"
						accessibilityState={{ expanded }}
						hitSlop={space.sm}
						onPress={(event) => {
							event.stopPropagation();
							toggle(item.key);
						}}
						style={styles.disclosure}
					>
						<AppSymbol name={expanded ? "chevronDown" : "chevronForward"} size={iconSize.xs} />
					</Pressable>
				) : <View style={styles.disclosure} />}
				<Dot color={visual.color} breathing={visual.breathing} size={8} />
				<View style={styles.copy}>
					<Text numberOfLines={1} style={styles.title}>{item.node.session.title}</Text>
					<Text numberOfLines={1} style={styles.detail}>{detail}</Text>
				</View>
				{hasDescendantRollup ? <Text numberOfLines={1} style={styles.rollup}>{descendants}</Text> : null}
			</Pressable>
		);
	}, [collapsed, hostsById, router, styles, theme, toggle]);

	return (
		<FlatList
			ref={listRef}
			data={rows}
			keyExtractor={(item) => item.key}
			contentContainerStyle={{ flexGrow: 1, paddingBottom: contentBottomInset }}
			contentInsetAdjustmentBehavior={contentInsetAdjustmentBehavior}
			keyboardShouldPersistTaps="handled"
			ListEmptyComponent={ListEmptyComponent}
			ListHeaderComponent={ListHeaderComponent}
			onScroll={onScroll}
			refreshControl={(
				<RefreshControl
					refreshing={refreshing}
					onRefresh={onRefresh}
					progressViewOffset={Platform.OS === "android" ? overlayTopInset : undefined}
					{...refreshControlColors(theme)}
				/>
			)}
			renderItem={renderItem}
		/>
	);
}

const makeStyles = (theme: Theme) => StyleSheet.create({
	row: {
		minHeight: touchTarget,
		flexDirection: "row",
		alignItems: "center",
		gap: space.sm,
		paddingHorizontal: gutter,
		paddingVertical: space.sm,
		borderBottomColor: theme.borderSubtle,
		borderBottomWidth: rowDividerWidth,
	},
	projectRow: { minHeight: touchTarget + space.xxs },
	indentedRow: { paddingLeft: gutter + space.lg },
	pressed: { backgroundColor: theme.bgSubtle },
	disclosure: { width: iconSize.md, minHeight: iconSize.md, alignItems: "center", justifyContent: "center" },
	copy: { minWidth: 0, flex: 1 },
	title: { ...type.headline, color: theme.textPrimary, minWidth: 0, flexShrink: 1 },
	detail: { ...type.footnote, color: theme.textTertiary },
	rollup: { ...type.caption1, color: theme.textTertiary, flexShrink: 1, textAlign: "right" },
});
