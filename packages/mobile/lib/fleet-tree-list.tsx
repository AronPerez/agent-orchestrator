import { useRouter } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactElement, type RefObject } from "react";
import {
	Alert,
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
import type { DashboardSession } from "./api";
import { ArchivedWorkerRow } from "./archived-worker-row";
import { useArchivedSessions, type ArchiveSource } from "./archive/useArchivedSessions";
import { isArchived } from "./agentsView";
import { ARCHIVE_CLOUD_SESSION_CONFIRM } from "./cloud/sessionMenu";
import { isCloudHost } from "./cloud/mapping";
import { userFacingError } from "./connectionError";
import {
	readFleetCollapsePreferences,
	writeFleetCollapsePreferences,
} from "./fleet-collapse-store";
import {
	buildMobileFleetTree,
	type MobileFleetHost,
	type MobileFleetSession,
} from "./fleet-tree-model";
import { haptics } from "./haptics";
import {
	hostedRowKey,
	hostedSessionKey,
	sessionHostId,
	type HostedOrchestrator,
	type HostedProject,
	type HostedSession,
} from "./hostedRows";
import { refreshControlColors } from "./refreshControlColors";
import { statusVisual, type Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { Dot } from "./ui";
import { gutter, iconSize, space, touchTarget, type } from "./tokens";
import { rowDividerWidth } from "./divider";
import { useApp } from "./store";
import { ARCHIVE_SESSION_CONFIRM } from "./worker-action-model";
import { WorkerListRow } from "./worker-list-row";

type ProjectRow = { kind: "project"; key: string; depth: number; node: FleetProjectNode<MobileFleetSession> };
type MachineRow = { kind: "machine"; key: string; depth: number; node: FleetMachineNode<MobileFleetSession> };
type SessionRow = { kind: "session"; key: string; depth: number; node: FleetSessionNode<MobileFleetSession> };
type ArchiveHeaderRow = { kind: "archive"; key: string };
type ArchivedSessionRow = { kind: "archived"; key: string; session: DashboardSession };
type ArchiveStatusRow = { kind: "archive-status"; key: string };
export type MobileFleetRow = ProjectRow | MachineRow | SessionRow | ArchiveHeaderRow | ArchivedSessionRow | ArchiveStatusRow;

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

function rowHasChildren(row: ProjectRow | MachineRow | SessionRow): boolean {
	if (row.kind === "project") return row.node.machines.length > 0;
	if (row.kind === "machine") return row.node.sessions.length > 0;
	return row.node.children.length > 0;
}

function isWorkerSession(source: DashboardSession | { projectId: string }): source is DashboardSession {
	return "createdAt" in source;
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
	archiveSources = [],
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
	archiveSources?: readonly ArchiveSource[];
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
	const {
		currentHostId,
		kill,
		renameWorker,
		setWorkerPinned,
		restore,
		resumeAgent,
	} = useApp();
	const [preferences, setPreferences] = useState<Record<string, boolean>>({});
	const preferencesRef = useRef(preferences);
	const preferencesLoaded = useRef(false);
	const pendingPreferenceWrite = useRef(false);
	useEffect(() => {
		let active = true;
		void readFleetCollapsePreferences().then((stored) => {
			if (!active) return;
			const merged = { ...stored, ...preferencesRef.current };
			preferencesRef.current = merged;
			preferencesLoaded.current = true;
			setPreferences(merged);
			if (pendingPreferenceWrite.current) {
				pendingPreferenceWrite.current = false;
				void writeFleetCollapsePreferences(merged);
			}
		});
		return () => { active = false; };
	}, []);

	const inlineArchive = useMemo(() => sessions.filter(isArchived), [sessions]);
	const unresolvedParents = useMemo(() => {
		const known = new Set([
			...sessions.map((session) => hostedRowKey(session.hostId, session.id)),
			...orchestrators.map((orchestrator) => hostedRowKey(orchestrator.hostId, orchestrator.id)),
		]);
		return sessions.some((session) =>
			!isArchived(session) && Boolean(session.parentSessionId) &&
			!known.has(hostedRowKey(session.hostId, session.parentSessionId!)));
	}, [orchestrators, sessions]);
	const [archiveOpen, setArchiveOpen] = useState(false);
	const [archiveRefresh, setArchiveRefresh] = useState(0);
	const archive = useArchivedSessions(archiveSources, {
		active: archiveOpen || unresolvedParents,
		exhaustive: unresolvedParents,
		inline: inlineArchive,
		refreshToken: archiveRefresh,
	});
	const liveKeys = useMemo(
		() => new Set(sessions.filter((session) => !isArchived(session)).map(hostedSessionKey)),
		[sessions],
	);
	const archivedRows = useMemo(
		() => archive.rows.filter((session) => !liveKeys.has(hostedSessionKey(session))),
		[archive.rows, liveKeys],
	);
	const treeSessions = useMemo(() => {
		const byAddress = new Map<string, HostedSession>();
		for (const session of archivedRows) {
			if (sessionHostId(session)) byAddress.set(hostedSessionKey(session), session as HostedSession);
		}
		// A live poll is newer than the archive page when the same address appears in both.
		for (const session of sessions) byAddress.set(hostedSessionKey(session), session);
		return [...byAddress.values()];
	}, [archivedRows, sessions]);
	const tree = useMemo(
		() => buildMobileFleetTree({ projects, sessions: treeSessions, orchestrators, hosts }),
		[hosts, orchestrators, projects, treeSessions],
	);
	const collapsed = useMemo(
		() => resolveFleetCollapsedKeys(tree, preferences),
		[preferences, tree],
	);
	const treeRows = useMemo(() => flattenRows(tree, collapsed), [collapsed, tree]);
	const rows = useMemo<MobileFleetRow[]>(() => {
		if (archive.total === 0) return treeRows;
		const archiveRows: MobileFleetRow[] = [{ kind: "archive", key: "archive:header" }];
		if (archiveOpen) {
			archiveRows.push(...archivedRows.map((session) => ({
				kind: "archived" as const,
				key: `archive:${hostedSessionKey(session)}`,
				session,
			})));
			if (archive.loading || archive.error || archive.hasMore) {
				archiveRows.push({ kind: "archive-status", key: "archive:status" });
			}
		}
		return [...treeRows, ...archiveRows];
	}, [archive.error, archive.hasMore, archive.loading, archive.total, archiveOpen, archivedRows, treeRows]);
	const hostsById = useMemo(() => new Map(hosts.map((host) => [host.hostId, host])), [hosts]);
	const projectNames = useMemo(
		() => new Map(projects.map((project) => [hostedRowKey(project.hostId, project.id), project.name])),
		[projects],
	);

	const setCollapsed = useCallback((key: string, nextCollapsed: boolean) => {
		const next = { ...preferencesRef.current, [key]: nextCollapsed };
		preferencesRef.current = next;
		setPreferences(next);
		if (preferencesLoaded.current) void writeFleetCollapsePreferences(next);
		else pendingPreferenceWrite.current = true;
	}, []);
	const toggle = useCallback((key: string) => setCollapsed(key, !collapsed.has(key)), [collapsed, setCollapsed]);
	const nowBucket = Math.floor(Date.now() / 60_000);
	const [renamingWorkerId, setRenamingWorkerId] = useState<string>();
	const [activeSwipeId, setActiveSwipeId] = useState<string>();
	const activeSwipeRef = useRef<{ id: string; close(): void } | undefined>(undefined);
	const openExclusiveSwipe = useCallback((id: string, close: () => void) => {
		const previous = activeSwipeRef.current;
		if (previous?.id !== id) previous?.close();
		activeSwipeRef.current = { id, close };
		setActiveSwipeId(id);
	}, []);
	const closeExclusiveSwipe = useCallback((id: string) => {
		if (activeSwipeRef.current?.id === id) activeSwipeRef.current = undefined;
		setActiveSwipeId((active) => active === id ? undefined : active);
	}, []);
	const updateWorkerPin = useCallback(async (session: DashboardSession, pinned: boolean) => {
		try {
			await setWorkerPinned(session.id, pinned, sessionHostId(session));
			haptics.success();
		} catch (cause) {
			haptics.error();
			Alert.alert("Couldn't update pin", userFacingError(cause, "Please try again."));
		}
	}, [setWorkerPinned]);
	const runWorkerRecovery = useCallback(async (session: DashboardSession, kind: "resume" | "restore") => {
		try {
			await (kind === "resume"
				? resumeAgent(session.id, sessionHostId(session))
				: restore(session.id, sessionHostId(session)));
			haptics.success();
		} catch (cause) {
			haptics.error();
			Alert.alert(
				kind === "resume" ? "Couldn't resume the agent" : "Couldn't restore the session",
				userFacingError(cause, "Please try again."),
			);
		}
	}, [restore, resumeAgent]);
	const confirmDeleteSession = useCallback((session: DashboardSession) => {
		haptics.warning();
		const copy = isCloudHost(sessionHostId(session)) ? ARCHIVE_CLOUD_SESSION_CONFIRM : ARCHIVE_SESSION_CONFIRM;
		Alert.alert(copy.title, copy.message, [
			{ text: copy.cancel, style: "cancel" },
			{
				text: copy.confirm,
				style: "destructive",
				onPress: () => void kill(session.id, sessionHostId(session)).catch(() => {}),
			},
		]);
	}, [kill]);
	const machineOffline = useCallback((session: DashboardSession) => {
		const hostId = sessionHostId(session);
		return hostId ? hostsById.get(hostId)?.connection !== "open" : false;
	}, [hostsById]);
	const refreshTree = useCallback(async () => {
		try {
			await onRefresh();
		} finally {
			setArchiveRefresh((value) => value + 1);
		}
	}, [onRefresh]);

	const renderItem = useCallback(({ item }: { item: MobileFleetRow }) => {
		if (item.kind === "archive") {
			return (
				<Pressable
					accessibilityLabel={`Archive, ${archive.total} session${archive.total === 1 ? "" : "s"}`}
					accessibilityRole="button"
					accessibilityState={{ expanded: archiveOpen }}
					onPress={() => setArchiveOpen((open) => !open)}
					style={({ pressed }) => [styles.row, styles.archiveRow, pressed && styles.pressed]}
				>
					<AppSymbol name={archiveOpen ? "chevronDown" : "chevronForward"} size={iconSize.xs} />
					<AppSymbol name="archive" size={iconSize.md} />
					<Text style={styles.title}>Archive</Text>
					<Text style={styles.rollup}>{archive.total}</Text>
				</Pressable>
			);
		}
		if (item.kind === "archive-status") {
			if (archive.error) {
				return (
					<Pressable accessibilityLabel="Retry loading archived sessions" accessibilityRole="button"
						onPress={archive.retry} style={({ pressed }) => [styles.archiveStatus, pressed && styles.pressed]}>
						<Text accessibilityRole="alert" style={styles.archiveError}>{archive.error}</Text>
						<Text style={styles.archiveAction}>Retry</Text>
					</Pressable>
				);
			}
			if (archive.loading) {
				return <View accessible accessibilityLabel="Loading archived sessions" style={styles.archiveStatus}>
					<Text style={styles.detail}>Loading archived sessions…</Text>
				</View>;
			}
			return (
				<Pressable accessibilityLabel="Show more archived sessions" accessibilityRole="button"
					onPress={archive.loadMore} style={({ pressed }) => [styles.archiveStatus, pressed && styles.pressed]}>
					<Text style={styles.archiveAction}>Show more</Text>
				</Pressable>
			);
		}
		if (item.kind === "archived") {
			const hostId = sessionHostId(item.session);
			return (
				<ArchivedWorkerRow
					nowBucket={nowBucket}
					offline={machineOffline(item.session)}
					onRestore={(session) => void runWorkerRecovery(session, "restore")}
					projectName={projectNames.get(hostId ? hostedRowKey(hostId, item.session.projectId) : item.session.projectId)}
					restoreDisabled={false}
					restoring={false}
					session={item.session}
					showMachine
				/>
			);
		}
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
		const source = item.node.session.source;
		if (isWorkerSession(source)) {
			const rowKey = hostedSessionKey(source);
			const projectName = [
				role,
				item.node.parentUnavailable ? "Parent unavailable" : undefined,
				hasDescendantRollup ? descendants : undefined,
			].filter(Boolean).join(" · ");
			return (
				<WorkerListRow
					activeSwipeId={activeSwipeId}
					hostId={item.node.machineId ?? currentHostId}
					isRenaming={renamingWorkerId === rowKey}
					nowBucket={nowBucket}
					onDelete={() => confirmDeleteSession(source)}
					onRename={(title) => renameWorker(source.id, title, sessionHostId(source))}
					onRenameCancel={() => setRenamingWorkerId(undefined)}
					onRenameStart={() => setRenamingWorkerId(rowKey)}
					onRestore={() => void runWorkerRecovery(source, "restore")}
					onResume={() => void runWorkerRecovery(source, "resume")}
					onSetPinned={(pinned) => updateWorkerPin(source, pinned)}
					onSwipeClose={closeExclusiveSwipe}
					onSwipeOpen={openExclusiveSwipe}
					onTreeToggle={expandable ? () => toggle(item.key) : undefined}
					projectName={projectName}
					rowKey={rowKey}
					session={source}
					treeDepth={Math.max(0, item.depth - 2)}
					treeExpanded={expanded}
				/>
			);
		}
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
	}, [activeSwipeId, archive.error, archive.loadMore, archive.loading, archive.retry, archive.total, archiveOpen,
		closeExclusiveSwipe, collapsed, confirmDeleteSession, currentHostId, hostsById, machineOffline, nowBucket,
		openExclusiveSwipe, projectNames, renameWorker, renamingWorkerId, router, runWorkerRecovery, styles, theme, toggle,
		updateWorkerPin]);

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
			onEndReached={archiveOpen ? archive.loadMore : undefined}
			onEndReachedThreshold={1.5}
			onScroll={onScroll}
			refreshControl={(
				<RefreshControl
					refreshing={refreshing}
					onRefresh={refreshTree}
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
	archiveRow: { marginTop: space.md },
	indentedRow: { paddingLeft: gutter + space.lg },
	pressed: { backgroundColor: theme.bgSubtle },
	archiveStatus: { minHeight: touchTarget, paddingHorizontal: gutter, flexDirection: "row", alignItems: "center", justifyContent: "center", gap: space.sm },
	archiveError: { ...type.footnote, color: theme.red, flexShrink: 1 },
	archiveAction: { ...type.footnote, color: theme.accent, fontWeight: "600" },
	disclosure: { width: iconSize.md, minHeight: iconSize.md, alignItems: "center", justifyContent: "center" },
	copy: { minWidth: 0, flex: 1 },
	title: { ...type.headline, color: theme.textPrimary, minWidth: 0, flexShrink: 1 },
	detail: { ...type.footnote, color: theme.textTertiary },
	rollup: { ...type.caption1, color: theme.textTertiary, flexShrink: 1, textAlign: "right" },
});
