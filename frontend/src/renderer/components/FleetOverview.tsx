import {
	attentionZone,
	buildFleetTree,
	getDisplayStatusLabel,
	type AttentionZone,
	type FleetMachineNode,
	type FleetProjectNode,
	type FleetRollup,
	type FleetSessionNode,
	type SessionStatus,
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

type ScmRollup = {
	pullRequests: number;
	failingChecks: number;
	checksFailing: boolean;
};

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

function ScmRollupView({ rollup }: { rollup: ScmRollup }) {
	const { t } = useTranslation();
	if (rollup.pullRequests === 0 && !rollup.checksFailing) return null;
	return (
		<span className="inline-flex min-w-0 flex-wrap items-center justify-end gap-x-3 gap-y-1 text-xs text-muted-foreground">
			{rollup.pullRequests > 0 ? <span className="whitespace-nowrap">{t("fleet.prCount", { count: rollup.pullRequests })}</span> : null}
			{rollup.checksFailing ? (
				<span className="whitespace-nowrap text-destructive">
					{t("pr.card.checksFailing")}{rollup.failingChecks > 0 ? `: ${rollup.failingChecks}` : ""}
				</span>
			) : null}
		</span>
	);
}

function emptyScmRollup(): ScmRollup {
	return { pullRequests: 0, failingChecks: 0, checksFailing: false };
}

function addScmRollup(target: ScmRollup, source: ScmRollup): void {
	target.pullRequests += source.pullRequests;
	target.failingChecks += source.failingChecks;
	target.checksFailing ||= source.checksFailing;
}

function ownScmRollup(session: WorkspaceSession): ScmRollup {
	return {
		pullRequests: session.prs.length,
		failingChecks: session.prs.reduce((count, pr) => count + (pr.failingChecks?.length ?? 0), 0),
		checksFailing: session.prs.some((pr) => pr.ci === "failing"),
	};
}

function fleetPrStatus(pr: WorkspaceSession["prs"][number]): SessionStatus {
	if (pr.ci === "failing") return "ci_failed";
	if (pr.state === "draft") return "draft";
	if (pr.review === "changes_requested") return "changes_requested";
	if (pr.reviewComments) return "commented";
	if (pr.mergeability === "mergeable") return "mergeable";
	if (pr.review === "review_required") return "review_pending";
	if (pr.mergeability === "blocked") return "pr_open";
	if (pr.review === "approved") return "approved";
	return "pr_open";
}

function fleetRollupStatus(session: WorkspaceSession): SessionStatus {
	const active = session.prs.filter((pr) => pr.state === "open" || pr.state === "draft");
	const statuses = new Set(active.map(fleetPrStatus));
	const derivedStatus = ([
		"ci_failed",
		"changes_requested",
		"commented",
		"draft",
		"review_pending",
		"pr_open",
		"approved",
		"mergeable",
	] as const).find((status) => statuses.has(status));
	const deliveryStatus = session.scmStatus ?? derivedStatus ??
		(session.prs.some((pr) => pr.state === "merged") ? "merged" : undefined);
	if (!deliveryStatus) return session.status;
	return rollupOrder.indexOf(attentionZone(session.status)) <= rollupOrder.indexOf(attentionZone(deliveryStatus))
		? session.status
		: deliveryStatus;
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
	remoteFailedHostIds,
	remoteLoadedProjectHostIds,
	remoteLoadedSessionHostIds,
	onOpen,
}: {
	localWorkspaces: readonly WorkspaceSummary[];
	remoteWorkspaces?: readonly WorkspaceSummary[];
	remoteHosts?: readonly RemoteHost[];
	remoteFailedHostIds?: readonly string[];
	remoteLoadedProjectHostIds?: readonly string[];
	remoteLoadedSessionHostIds?: readonly string[];
	onOpen: (session: WorkspaceSession, hostId?: string) => void;
}) {
	const { t } = useTranslation();
	const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
	const [showArchived, setShowArchived] = useState(false);
	const [focusedKey, setFocusedKey] = useState<string>();
	const rowRefs = useRef(new Map<string, HTMLElement>());
	const hostsById = useMemo(() => new Map(remoteHosts.map((host) => [host.hostId, host])), [remoteHosts]);
	const refusedHostIds = useMemo(
		() => new Set(remoteHosts.filter((host) => host.failureReason === "unauthorized").map((host) => host.hostId)),
		[remoteHosts],
	);
	const visibleRemoteWorkspaces = useMemo(
		() => remoteWorkspaces.filter((workspace) => !workspace.hostId || !refusedHostIds.has(workspace.hostId)),
		[refusedHostIds, remoteWorkspaces],
	);
	const remoteReadState = (hostId: string): "ready" | "pending" | "failed" => {
		if (remoteFailedHostIds?.includes(hostId)) return "failed";
		if (remoteLoadedProjectHostIds === undefined && remoteLoadedSessionHostIds === undefined) return "ready";
		return remoteLoadedProjectHostIds?.includes(hostId) && remoteLoadedSessionHostIds?.includes(hostId) ? "ready" : "pending";
	};
	const projects = useMemo(() => {
		const scopes = [
			...localWorkspaces
				.filter((workspace) => workspace.kind !== CLOUD_PROJECT_KIND)
				.map((workspace) => ({
					project: { id: workspace.id, name: workspace.name },
					machine: { id: LOCAL_HOST, name: t("remote.thisComputer"), reachable: true },
					sessions: workspace.sessions.map((session) => ({ ...session, rollupStatus: fleetRollupStatus(session) })),
				})),
			...visibleRemoteWorkspaces
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
						sessions: workspace.sessions.map((session) => ({ ...session, rollupStatus: fleetRollupStatus(session) })),
					};
				}),
		];
		return orderProjects(buildFleetTree(scopes, { showArchived }));
	}, [hostsById, localWorkspaces, showArchived, t, visibleRemoteWorkspaces]);
	const scopedHostIds = useMemo(() => new Set(visibleRemoteWorkspaces.map((workspace) => workspace.hostId).filter(Boolean)), [visibleRemoteWorkspaces]);
	const unscopedHosts = useMemo(
		() => remoteHosts.filter((host) => !scopedHostIds.has(host.hostId)),
		[remoteHosts, scopedHostIds],
	);
	const rows = useMemo(() => flattenRows(projects, unscopedHosts, collapsed), [projects, unscopedHosts, collapsed]);
	const scmRollups = useMemo(() => {
		const values = new Map<string, ScmRollup>();
		const visitSession = (node: FleetSessionNode<WorkspaceSession>): ScmRollup => {
			const rollup = ownScmRollup(node.session);
			for (const child of node.children) addScmRollup(rollup, visitSession(child));
			values.set(node.key, rollup);
			return rollup;
		};
		for (const project of projects) {
			const projectRollup = emptyScmRollup();
			for (const machine of project.machines) {
				const machineRollup = emptyScmRollup();
				for (const session of machine.sessions) addScmRollup(machineRollup, visitSession(session));
				values.set(machine.key, machineRollup);
				addScmRollup(projectRollup, machineRollup);
			}
			values.set(project.key, projectRollup);
		}
		return values;
	}, [projects]);
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
		const host = hostsById.get(row.node.machine.id);
		const status = host?.status;
		if (status === "connecting") return t("session.statusChecking");
		if (status === "offline" || !row.node.machine.reachable) return t("remote.offline");
		if (remoteReadState(row.node.machine.id) === "failed") return t("fleet.stale");
		if (remoteReadState(row.node.machine.id) === "pending") return t("session.statusChecking");
		return undefined;
	};
	const unscopedMachineLabel = (host: RemoteHost) => {
		if (host.failureReason === "unauthorized") return t("remote.passwordRejected");
		if (host.status === "connecting") return t("session.statusChecking");
		if (host.status === "offline") return t("remote.offline");
		if (remoteReadState(host.hostId) === "failed") return t("fleet.stale");
		if (remoteReadState(host.hostId) === "pending") return t("session.statusChecking");
		return t("fleet.empty");
	};
	const scmRollupText = (key: string) => {
		const rollup = scmRollups.get(key) ?? emptyScmRollup();
		return [
			rollup.pullRequests > 0 ? t("fleet.prCount", { count: rollup.pullRequests }) : undefined,
			rollup.checksFailing ? `${t("pr.card.checksFailing")}${rollup.failingChecks > 0 ? `: ${rollup.failingChecks}` : ""}` : undefined,
		].filter(Boolean).join(", ");
	};
	const rowLabel = (row: FleetOverviewRow) => {
		switch (row.kind) {
			case "project": return [row.node.project.name, rollupText(row.node.rollup), scmRollupText(row.key)].filter(Boolean).join(", ");
			case "machine": return [
				row.node.machine.name,
				machineConnectionLabel(row),
				rollupText(row.node.rollup),
				scmRollupText(row.key),
			].filter(Boolean).join(", ");
			case "session": return [
				row.node.session.title,
				roleLabel(row),
				sessionStatus(row),
				row.node.session.isTerminated ? t("fleet.archived") : undefined,
				row.node.parentUnavailable ? t("fleet.parentUnavailable") : undefined,
				rollupOrder.some((zone) => row.node.rollup[zone] > 0) ? rollupText(row.node.rollup) : undefined,
				scmRollupText(row.key),
			].filter(Boolean).join(", ");
			case "unscoped-machine": return [
				row.host.label,
				unscopedMachineLabel(row.host),
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
									{row.kind === "project" ? <span className="flex min-w-0 flex-wrap items-center justify-end gap-x-3 gap-y-1"><Rollup rollup={row.node.rollup} /><ScmRollupView rollup={scmRollups.get(row.key) ?? emptyScmRollup()} /></span>
										: row.kind === "machine" ? (
											<span className="flex min-w-0 flex-wrap items-center justify-end gap-x-3 gap-y-1">
												{machineConnectionLabel(row) ? <span className="text-xs text-muted-foreground">{machineConnectionLabel(row)}</span> : null}
												<Rollup rollup={row.node.rollup} />
												<ScmRollupView rollup={scmRollups.get(row.key) ?? emptyScmRollup()} />
											</span>
										)
										: row.kind === "unscoped-machine" ? (
											<span className="text-xs text-muted-foreground">
												{unscopedMachineLabel(row.host)}
											</span>
										) : (
											<span className="flex min-w-0 flex-wrap items-center justify-end gap-x-3 gap-y-1 text-xs text-muted-foreground">
												<span>{roleLabel(row)} · {sessionStatus(row)}</span>
												{row.node.session.isTerminated ? <span>{t("fleet.archived")}</span> : null}
												{row.node.parentUnavailable ? <span>{t("fleet.parentUnavailable")}</span> : null}
												{rollupOrder.some((zone) => row.node.rollup[zone] > 0) ? <Rollup rollup={row.node.rollup} /> : null}
												<ScmRollupView rollup={scmRollups.get(row.key) ?? emptyScmRollup()} />
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
