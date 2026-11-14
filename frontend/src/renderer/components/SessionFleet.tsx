import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, ExternalLink, GitBranch, MessageSquare } from "lucide-react";
import type { components } from "../../api/schema";
import type { WorkspaceSession } from "../types/workspace";
import { apiErrorCode } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";
import { clientForSessionHost } from "../lib/host-clients";
import { HOST_UNREACHABLE_CODES, LOCAL_HOST } from "../lib/hosts";
import { useConnectedHosts } from "../hooks/useHostConnection";
import { getSessionStatusView } from "../lib/session-presentation";
import { fleetRows } from "../lib/session-fleet";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import { mockCoordinationEvents } from "../lib/mock-data";
import { cn } from "../lib/utils";
import { AppLink } from "./AppLink";
import { Button } from "./ui/button";

type Coordination = components["schemas"]["SessionCoordinationResponse"];
type CoordinationRequestError = Error & { status?: number; code?: string };

export function SessionFleet({
	sessions,
	onOpen,
	hostId,
}: {
	sessions: WorkspaceSession[];
	onOpen: (session: WorkspaceSession) => void;
	hostId?: string;
}) {
	const { t, i18n } = useTranslation();
	const queryClient = useQueryClient();
	const remote = Boolean(hostId && hostId !== LOCAL_HOST);
	const connected = useConnectedHosts().includes(hostId ?? "");
	const roleLabels = {
		Orchestrator: t("shell.orchestrator"),
		Manager: t("fleet.manager"),
		Worker: t("settings.models.workerRole"),
	};
	const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
	const [showArchived, setShowArchived] = useState(false);
	const [selectedId, setSelectedId] = useState<string>();
	const rows = useMemo(() => fleetRows(sessions, collapsed, showArchived), [sessions, collapsed, showArchived]);
	const selected = rows.find((row) => row.session.id === selectedId) ?? rows[0];
	const session = selected?.session;
	useEffect(() => {
		if (remote && !connected) void queryClient.cancelQueries({ queryKey: ["session-coordination", hostId, session?.id], exact: true });
	}, [queryClient, remote, connected, hostId, session?.id]);
	const coordination = useQuery<Coordination>({
		queryKey: ["session-coordination", hostId ?? "", session?.id],
		enabled: !!session && (!remote || connected),
		retry: false,
		refetchInterval: (query) => [404, 501].includes((query.state.error as CoordinationRequestError | null)?.status ?? 0) ? false : 15_000,
		queryFn: async ({ signal }): Promise<Coordination> => {
			if (usesPreviewWorkspaceData && !remote)
				return {
					events: mockCoordinationEvents.filter(
						(event) =>
							event.sourceSessionId === session!.id ||
							("targetSessionId" in event && event.targetSessionId === session!.id),
					),
					hasMore: false,
				};
			const { data, error, response } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/coordination", {
				params: { path: { sessionId: session!.id } },
				signal,
			});
			if (error || !data) throw Object.assign(apiErrorException(error), { status: response?.status, code: apiErrorCode(error) });
			return data;
		},
	});
	const historyError = coordination.error as CoordinationRequestError | null;
	const offline = remote && (!connected || HOST_UNREACHABLE_CODES.has(historyError?.code));
	const names = new Map(sessions.map((item) => [item.id, item.title]));
	const toggle = (id: string) =>
		setCollapsed((current) => {
			const next = new Set(current);
			if (next.has(id)) next.delete(id);
			else next.add(id);
			return next;
		});
	return (
		<section className="flex min-h-0 flex-1 flex-col" aria-label={t("fleet.title")} data-host-offline={offline ? "" : undefined}>
			<div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-2">
				<p className="text-xs text-muted-foreground">
					{t("fleet.explanation")}
				</p>
				<Button variant="ghost" size="sm" aria-pressed={showArchived} onClick={() => setShowArchived(!showArchived)}>
					{t("fleet.showArchived")}
				</Button>
			</div>
			{usesPreviewWorkspaceData && !remote ? (
				<p className="px-4 py-2 text-xs text-muted-foreground">
					{t("fleet.preview")}
				</p>
			) : null}
			{!rows.length ? (
				<p className="p-6 text-sm text-muted-foreground">{t("fleet.empty")}</p>
			) : (
				<div className="flex min-h-0 flex-1 flex-wrap overflow-auto">
					<ol className="min-w-0 flex-[2_1_24rem] overflow-auto p-3" aria-label={t("fleet.hierarchy")}>
						{rows.map((row) => (
							<li
								key={row.session.id}
								className={cn(
									"flex min-h-12 items-start rounded-md py-1 pr-2",
									row.session.id === session?.id ? "bg-accent" : "hover:bg-muted",
								)}
								style={{ paddingLeft: `${Math.min(row.depth, 12) * 16}px` }}
							>
								{row.childCount ? (
									<Button
										variant="ghost"
										size="icon-sm"
										aria-label={t(collapsed.has(row.session.id) ? "fleet.expand" : "fleet.collapse", { title: row.session.title })}
										aria-expanded={!collapsed.has(row.session.id)}
										onClick={() => toggle(row.session.id)}
									>
										{collapsed.has(row.session.id) ? <ChevronRight /> : <ChevronDown />}
									</Button>
								) : (
									<span className="w-8 shrink-0" />
								)}
								<button
									type="button"
									className="min-w-0 flex-1 rounded-sm py-1 text-left focus-visible:outline-2 focus-visible:outline-ring"
									aria-pressed={row.session.id === session?.id}
									onClick={() => setSelectedId(row.session.id)}
								>
									<span className="sr-only">{t("fleet.level", { level: row.depth + 1 })} </span>
									<span className="block truncate text-sm font-medium">{row.session.title}</span>
									<span className="block truncate text-xs text-muted-foreground">
										{roleLabels[row.role]} · {row.session.displayStatus ?? getSessionStatusView(row.session.status).label}
										{row.session.isTerminated ? ` · ${t("fleet.archived")}` : ""}
										{row.parentUnavailable
											? ` · ${t("fleet.parentUnavailable")}`
											: !row.session.parentSessionId && row.session.kind !== "orchestrator"
												? ` · ${t("fleet.noParent")}`
												: ""}
									</span>
								</button>
								{row.session.prs?.length ? (
									<span className="shrink-0 pt-2 text-xs text-muted-foreground">
										{t("fleet.prCount", { count: row.session.prs.length })}
									</span>
								) : null}
							</li>
						))}
					</ol>
					{session ? (
						<aside
							className="min-w-0 flex-[1_1_20rem] space-y-5 overflow-auto border-l border-border p-4"
							aria-label={t("fleet.selected")}
						>
							<div className="space-y-2">
								<div className="flex items-start justify-between gap-3">
									<h2 className="min-w-0 break-words text-sm font-semibold">{session.title}</h2>
									<Button variant="secondary" size="sm" onClick={() => onOpen(session)}>
										{t("notify.openSessionTitle")}
										<ExternalLink className="size-3" />
									</Button>
								</div>
								<p className="break-all font-mono text-xs text-muted-foreground">{session.id}</p>
								<p className="text-sm">
									{roleLabels[selected.role]} · {session.displayStatus ?? getSessionStatusView(session.status).label}
								</p>
								{session.parentSessionId ? (
									<p className="text-xs text-muted-foreground">
										{t("fleet.spawnedBy", { name: names.get(session.parentSessionId) ?? session.parentSessionId })}
									</p>
								) : (
									<p className="text-xs text-muted-foreground">
										{t("fleet.noParentExplanation")}
									</p>
								)}
							</div>
							<div className="space-y-2">
								<h3 className="text-sm font-medium">{t("fleet.work")}</h3>
								{session.issueId ? <p className="text-xs">{session.issueId}</p> : null}
								{session.branch ? (
									<p className="flex items-center gap-2 break-all text-xs text-muted-foreground">
										<GitBranch className="size-3 shrink-0" />
										{session.branch}
									</p>
								) : null}
								{session.prs?.length ? (
									session.prs.map((pr) => (
										<div key={pr.url} className="text-xs">
											<AppLink
												className="text-primary underline underline-offset-2"
												href={pr.url}
												target="_blank"
												rel="noreferrer"
											>
												{t("fleet.prLink", { number: pr.number })}
											</AppLink>{" "}
											· {pr.state}
											<p className="mt-1 text-muted-foreground">
												{t("fleet.checks", { ci: pr.ci, review: pr.review })}
											</p>
										</div>
									))
								) : (
									<p className="text-xs text-muted-foreground">{t("fleet.noPRs")}</p>
								)}
								{session.artifactFiles?.length ? (
									<p className="text-xs text-muted-foreground">
										{t("fleet.artifacts", { count: session.artifactFiles.length })}
									</p>
								) : null}
							</div>
							<div className="space-y-3">
								<h3 className="flex items-center gap-2 text-sm font-medium">
									<MessageSquare className="size-3.5" />
									{t("fleet.communication")}
								</h3>
								<p className="text-xs text-muted-foreground">
									{t("fleet.coverage")}
								</p>
								{offline ? (
									<p role="status" className="text-xs text-muted-foreground">
										{t("remote.offline")}{coordination.data ? ` · ${t("remote.offlineCopy")}` : ""}
									</p>
								) : coordination.isError ? (
									<div role="alert" className={cn("text-xs", historyError?.status === 404 || historyError?.status === 501 ? "text-muted-foreground" : "text-destructive")}>
										{t(historyError?.status === 404 ? "fleet.historyNotFound" : historyError?.status === 501 ? "fleet.historyUnsupported" : "fleet.loadError")}{" "}
										<Button variant="ghost" size="sm" disabled={coordination.isFetching} onClick={() => void coordination.refetch()}>
											{t("notify.retry")}
										</Button>
									</div>
								) : coordination.isPending ? (
									<p role="status" className="text-xs text-muted-foreground">
										{t("fleet.loading")}
									</p>
								) : null}
								{coordination.data && (!coordination.isError || offline) ? (
									<>
										{coordination.data?.hasMore ? (
											<p className="text-xs text-muted-foreground">{t("fleet.limit")}</p>
										) : null}
										{!coordination.data?.events.length ? (
											<p className="text-xs text-muted-foreground">{t("fleet.noEvents")}</p>
										) : (
											<ol className="divide-y divide-border">
												{coordination.data.events.map((event) => (
													<li key={event.id} className="space-y-1 py-3 text-xs">
														<p className="break-words font-medium">
															{names.get(event.sourceSessionId) ?? event.sourceSessionId} →{" "}
															{event.targetSessionId
																? (names.get(event.targetSessionId) ?? event.targetSessionId)
																: t("fleet.recipientUnknown")}
														</p>
														<p className="text-muted-foreground">
															{event.kind === "report"
																? `${t("fleet.report")}${event.reportState ? ` · ${event.reportState.replaceAll("_", " ")}` : ""}`
																: t("remote.message")}{" "}
															· {event.state} ·{" "}
															<time dateTime={event.createdAt}>{new Date(event.createdAt).toLocaleString(i18n.resolvedLanguage)}</time>
														</p>
														<p className="whitespace-pre-wrap break-words">{event.summary || t("fleet.noSummary")}</p>
													</li>
												))}
											</ol>
										)}
									</>
								) : null}
							</div>
						</aside>
					) : null}
				</div>
			)}
		</section>
	);
}
