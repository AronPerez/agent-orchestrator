import {
	buildFleetTree,
	getDisplayStatusLabel,
	type AttentionZone,
	type FleetMachineNode,
	type FleetProjectNode,
	type FleetRollup,
	type FleetSessionNode,
} from "@aoagents/product-ui";
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { Bot, ChevronDown, ChevronRight, Folder, Monitor, Server, Users, Workflow } from "@/components/app-icons";
import type { RemoteHost } from "../hooks/useRemoteHosts";
import type { MessageKey } from "../i18n";
import { LOCAL_HOST } from "../lib/hosts";
import { getAttentionZoneViewForZone, getSessionStatusView } from "../lib/session-presentation";
import { cn } from "../lib/utils";
import { CLOUD_PROJECT_KIND, type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";
import { Button } from "./ui/button";
import { Pressable } from "./ui/pressable";

type ProjectRow = {
	kind: "project";
	key: string;
	depth: number;
	node: FleetProjectNode<WorkspaceSession>;
};

type MachineRow = {
	kind: "machine";
	key: string;
	parentKey: string;
	depth: number;
	node: FleetMachineNode<WorkspaceSession>;
};

type SessionRow = {
	kind: "session";
	key: string;
	parentKey: string;
	depth: number;
	node: FleetSessionNode<WorkspaceSession>;
};

type UnscopedMachineRow = {
	kind: "unscoped-machine";
	key: string;
	depth: number;
	host: RemoteHost;
};

type FleetOverviewRow = ProjectRow | MachineRow | SessionRow | UnscopedMachineRow;

const rollupOrder: AttentionZone[] = ["action", "merge", "pending", "working", "done"];

function attentionPriority(rollup: FleetRollup, zone?: AttentionZone): number {
	const count = (candidate: AttentionZone) => rollup[candidate] + (zone === candidate ? 1 : 0);
	for (let index = 0; index < rollupOrder.length; index++) {
		if (count(rollupOrder[index]!) > 0) return index;
	}
	return rollupOrder.length;
}

function stableAttentionSort<Item>(items: readonly Item[], priority: (item: Item) => number): Item[] {
	return items
		.map((item, index) => ({ item, index }))
		.sort((left, right) => priority(left.item) - priority(right.item) || left.index - right.index)
		.map(({ item }) => item);
}

function orderSessions(nodes: readonly FleetSessionNode<WorkspaceSession>[]): FleetSessionNode<WorkspaceSession>[] {
	return stableAttentionSort(nodes, (node) => attentionPriority(node.rollup, node.zone)).map((node) => ({
		...node,
		children: orderSessions(node.children),
	}));
}

function orderProjects(nodes: readonly FleetProjectNode<WorkspaceSession>[]): FleetProjectNode<WorkspaceSession>[] {
	return stableAttentionSort(nodes, (node) => attentionPriority(node.rollup)).map((project) => ({
		...project,
		machines: stableAttentionSort(project.machines, (machine) => attentionPriority(machine.rollup)).map((machine) => ({
			...machine,
			sessions: orderSessions(machine.sessions),
		})),
	}));
}

function flattenRows(
	projects: readonly FleetProjectNode<WorkspaceSession>[],
	unscopedHosts: readonly RemoteHost[],
	collapsed: ReadonlySet<string>,
): FleetOverviewRow[] {
	const rows: FleetOverviewRow[] = [];
	const addSessions = (
		nodes: readonly FleetSessionNode<WorkspaceSession>[],
		depth: number,
		parentKey: string,
	) => {
		for (const node of nodes) {
			rows.push({ kind: "session", key: node.key, parentKey, depth, node });
			if (!collapsed.has(node.key)) addSessions(node.children, depth + 1, node.key);
		}
	};
	for (const node of projects) {
		rows.push({ kind: "project", key: node.key, depth: 1, node });
		if (collapsed.has(node.key)) continue;
		for (const machine of node.machines) {
			rows.push({ kind: "machine", key: machine.key, parentKey: node.key, depth: 2, node: machine });
			if (!collapsed.has(machine.key)) addSessions(machine.sessions, 3, machine.key);
		}
	}
	for (const item of unscopedHosts) {
		rows.push({ kind: "unscoped-machine", key: JSON.stringify(["unscoped-machine", item.hostId]), depth: 1, host: item });
	}
	return rows;
}

function Rollup({ rollup }: { rollup: FleetRollup }) {
	const { t } = useTranslation();
	const entries = rollupOrder.filter((zone) => rollup[zone] > 0);
	if (!entries.length) return <span className="text-xs text-muted-foreground">{t("fleet.empty")}</span>;
	return (
		<span className="flex min-w-0 flex-wrap items-center justify-end gap-x-3 gap-y-1 text-xs text-muted-foreground">
			{entries.map((zone) => {
				const view = getAttentionZoneViewForZone(zone, t);
				return (
					<span className="inline-flex items-center gap-1.5 whitespace-nowrap" key={zone}>
						<span aria-hidden="true" className={cn("size-1.5 rounded-full", view.dotClassName)} />
						<span className="tabular-nums">{rollup[zone]}</span> {view.label}
					</span>
				);
			})}
		</span>
	);
}

function hasChildren(row: FleetOverviewRow): boolean {
	switch (row.kind) {
		case "project": return row.node.machines.length > 0;
		case "machine": return row.node.sessions.length > 0;
		case "session": return row.node.children.length > 0;
		case "unscoped-machine": return false;
	}
}

export function FleetOverview({
	localWorkspaces,
	remoteWorkspaces = [],
	remoteHosts = [],
	onOpen,
}: {
	localWorkspaces: readonly WorkspaceSummary[];
	remoteWorkspaces?: readonly WorkspaceSummary[];
	remoteHosts?: readonly RemoteHost[];
	onOpen: (session: WorkspaceSession, hostId?: string) => void;
}) {
	const { t } = useTranslation();
	const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
	const [showArchived, setShowArchived] = useState(false);
	const [focusedKey, setFocusedKey] = useState<string>();
	const rowRefs = useRef(new Map<string, HTMLElement>());
	const hostsById = useMemo(() => new Map(remoteHosts.map((host) => [host.hostId, host])), [remoteHosts]);
	const projects = useMemo(() => {
		const scopes = [
			...localWorkspaces
				.filter((workspace) => workspace.kind !== CLOUD_PROJECT_KIND)
				.map((workspace) => ({
					project: { id: workspace.id, name: workspace.name },
					machine: { id: LOCAL_HOST, name: t("remote.thisComputer"), reachable: true },
					sessions: workspace.sessions,
				})),
			...remoteWorkspaces
				.filter((workspace) => workspace.kind !== CLOUD_PROJECT_KIND && Boolean(workspace.hostId))
				.map((workspace) => {
					const hostId = workspace.hostId!;
					const host = hostsById.get(hostId);
					return {
						project: { id: workspace.id, name: workspace.name },
						machine: {
							id: hostId,
							name: host?.label ?? hostId,
							reachable: host ? host.status === "connected" : true,
						},
						sessions: workspace.sessions,
					};
				}),
		];
		return orderProjects(buildFleetTree(scopes, { showArchived }));
	}, [hostsById, localWorkspaces, remoteWorkspaces, showArchived, t]);
	const scopedHostIds = useMemo(() => new Set(remoteWorkspaces.map((workspace) => workspace.hostId).filter(Boolean)), [remoteWorkspaces]);
	const unscopedHosts = useMemo(
		() => remoteHosts.filter((host) => !scopedHostIds.has(host.hostId)),
		[remoteHosts, scopedHostIds],
	);
	const rows = useMemo(() => flattenRows(projects, unscopedHosts, collapsed), [projects, unscopedHosts, collapsed]);
	const activeFocusKey = rows.some((row) => row.key === focusedKey) ? focusedKey : rows[0]?.key;

	useEffect(() => {
		if (focusedKey !== activeFocusKey) setFocusedKey(activeFocusKey);
	}, [activeFocusKey, focusedKey]);

	const toggle = (key: string) => {
		setCollapsed((current) => {
			const next = new Set(current);
			if (next.has(key)) next.delete(key);
			else next.add(key);
			return next;
		});
	};
	const focusRow = (row?: FleetOverviewRow) => {
		if (!row) return;
		setFocusedKey(row.key);
		rowRefs.current.get(row.key)?.focus();
	};
	const activate = (row: FleetOverviewRow) => {
		if (row.kind === "session") {
			onOpen(row.node.session, row.node.machineId === LOCAL_HOST ? undefined : row.node.machineId);
			return;
		}
		if (hasChildren(row)) toggle(row.key);
	};
	const handleKeyDown = (event: KeyboardEvent<HTMLElement>, row: FleetOverviewRow, index: number) => {
		let handled = true;
		switch (event.key) {
			case "ArrowDown": focusRow(rows[index + 1]); break;
			case "ArrowUp": focusRow(rows[index - 1]); break;
			case "Home": focusRow(rows[0]); break;
			case "End": focusRow(rows.at(-1)); break;
			case "ArrowRight":
				if (hasChildren(row) && collapsed.has(row.key)) toggle(row.key);
				else if (hasChildren(row)) focusRow(rows[index + 1]);
				else handled = false;
				break;
			case "ArrowLeft":
				if (hasChildren(row) && !collapsed.has(row.key)) toggle(row.key);
				else focusRow(rows.find((candidate) => candidate.key === ("parentKey" in row ? row.parentKey : undefined)));
				break;
			case "Enter":
			case " ": activate(row); break;
			default: handled = false;
		}
		if (handled) event.preventDefault();
	};
	const roleLabel = (row: SessionRow) => {
		switch (row.node.role) {
			case "orchestrator": return t("shell.orchestrator");
			case "manager": return t("fleet.manager");
			case "worker": return t("settings.models.workerRole");
		}
	};
	const rollupText = (rollup: FleetRollup) => rollupOrder
		.filter((zone) => rollup[zone] > 0)
		.map((zone) => `${rollup[zone]} ${getAttentionZoneViewForZone(zone, t).label}`)
		.join(", ") || t("fleet.empty");
	const sessionStatus = (row: SessionRow) => row.node.session.displayStatus
		? getDisplayStatusLabel(row.node.session.displayStatus, (key, values) => t(key as MessageKey, values))
		: getSessionStatusView(row.node.session.status, t).label;
	const machineConnectionLabel = (row: MachineRow) => {
		const status = hostsById.get(row.node.machine.id)?.status;
		if (status === "connecting") return t("session.statusChecking");
		if (status === "offline" || !row.node.machine.reachable) return t("remote.offline");
		return undefined;
	};
	const rowLabel = (row: FleetOverviewRow) => {
		switch (row.kind) {
			case "project": return `${row.node.project.name}, ${rollupText(row.node.rollup)}`;
			case "machine": return [
				row.node.machine.name,
				machineConnectionLabel(row),
				rollupText(row.node.rollup),
			].filter(Boolean).join(", ");
			case "session": return [row.node.session.title, roleLabel(row), sessionStatus(row), rollupText(row.node.rollup)].join(", ");
			case "unscoped-machine": return [
				row.host.label,
				row.host.status === "connected" ? t("fleet.empty") : row.host.status === "connecting" ? t("session.statusChecking") : t("remote.offline"),
			].join(", ");
		}
	};

	return (
		<section aria-label={t("fleet.title")} className="flex min-h-0 flex-1 flex-col">
			<div className="flex justify-end border-b border-border px-3 py-2">
				<Button aria-pressed={showArchived} onClick={() => setShowArchived((value) => !value)} size="sm" variant="ghost">
					{t("fleet.showArchived")}
				</Button>
			</div>
			{rows.length === 0 ? (
				<p className="p-6 text-sm text-muted-foreground">{t("fleet.empty")}</p>
			) : (
				<div aria-label={t("fleet.hierarchy")} className="board-scrollbar min-h-0 flex-1 overflow-y-auto py-1" role="tree">
					{rows.map((row, index) => {
						const expandable = hasChildren(row);
						const expanded = expandable ? !collapsed.has(row.key) : undefined;
						const title = row.kind === "project" ? row.node.project.name
							: row.kind === "machine" ? row.node.machine.name
								: row.kind === "session" ? row.node.session.title : row.host.label;
						const icon = row.kind === "project" ? <Folder aria-hidden="true" className="size-icon-sm" />
							: row.kind === "machine" ? row.node.machine.id === LOCAL_HOST
								? <Monitor aria-hidden="true" className="size-icon-sm" />
								: <Server aria-hidden="true" className="size-icon-sm" />
								: row.kind === "unscoped-machine" ? <Server aria-hidden="true" className="size-icon-sm" />
									: row.node.role === "orchestrator" ? <Workflow aria-hidden="true" className="size-icon-sm" />
										: row.node.role === "manager" ? <Users aria-hidden="true" className="size-icon-sm" />
											: <Bot aria-hidden="true" className="size-icon-sm" />;
						return (
							<Pressable
								asChild
								key={row.key}
								onClick={() => activate(row)}
							>
								<div
									aria-expanded={expanded}
									aria-label={rowLabel(row)}
									aria-level={row.depth}
									className={cn(
										"flex min-h-9 w-full min-w-0 cursor-default items-center gap-2 pr-3 text-sm hover:bg-interactive-hover/50 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring",
										row.depth === 1 && index > 0 && "border-t border-border/70",
										row.kind === "project" && "font-medium",
										row.kind === "unscoped-machine" && "text-muted-foreground",
									)}
									onFocus={() => setFocusedKey(row.key)}
									onKeyDown={(event) => handleKeyDown(event, row, index)}
									ref={(element) => {
										if (element) rowRefs.current.set(row.key, element);
										else rowRefs.current.delete(row.key);
									}}
									role="treeitem"
									style={{ paddingLeft: `${12 + (row.depth - 1) * 20}px` }}
									tabIndex={activeFocusKey === row.key ? 0 : -1}
								>
									{expandable ? (
										<Button
											aria-label={t(expanded ? "fleet.collapse" : "fleet.expand", { title })}
											className="-ml-1"
											onClick={(event) => {
												event.stopPropagation();
												toggle(row.key);
											}}
											size="icon-xs"
											tabIndex={-1}
											variant="quiet"
										>
											{expanded ? <ChevronDown /> : <ChevronRight />}
										</Button>
									) : <span aria-hidden="true" className="w-5 shrink-0" />}
									<span className={cn("shrink-0 text-muted-foreground", row.kind === "session" && getSessionStatusView(row.node.session.status, t).className)}>{icon}</span>
									<span className="min-w-0 flex-1 truncate">{title}</span>
									{row.kind === "project" ? <Rollup rollup={row.node.rollup} />
										: row.kind === "machine" ? (
											<span className="flex min-w-0 flex-wrap items-center justify-end gap-x-3 gap-y-1">
												{machineConnectionLabel(row) ? <span className="text-xs text-muted-foreground">{machineConnectionLabel(row)}</span> : null}
												<Rollup rollup={row.node.rollup} />
											</span>
										)
										: row.kind === "unscoped-machine" ? (
											<span className="text-xs text-muted-foreground">
												{row.host.status === "connected" ? t("fleet.empty") : row.host.status === "connecting" ? t("session.statusChecking") : t("remote.offline")}
											</span>
										) : (
											<span className="flex min-w-0 flex-wrap items-center justify-end gap-x-3 gap-y-1 text-xs text-muted-foreground">
												<span>{roleLabel(row)} · {sessionStatus(row)}</span>
												{row.node.session.isTerminated ? <span>{t("fleet.archived")}</span> : null}
												{row.node.parentUnavailable ? <span>{t("fleet.parentUnavailable")}</span> : null}
												{rollupOrder.some((zone) => row.node.rollup[zone] > 0) ? <Rollup rollup={row.node.rollup} /> : null}
											</span>
										)}
								</div>
							</Pressable>
						);
					})}
				</div>
			)}
		</section>
	);
}
