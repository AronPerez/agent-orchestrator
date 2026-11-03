import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, ExternalLink, GitBranch, MessageSquare } from "lucide-react";
import type { components } from "../../api/schema";
import type { WorkspaceSession } from "../types/workspace";
import { apiClient } from "../lib/api-client";
import { getSessionStatusView } from "../lib/session-presentation";
import { fleetRows } from "../lib/session-fleet";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import { mockCoordinationEvents } from "../lib/mock-data";
import { cn } from "../lib/utils";
import { AppLink } from "./AppLink";
import { Button } from "./ui/button";

type Coordination = components["schemas"]["SessionCoordinationResponse"];

export function SessionFleet({
	sessions,
	onOpen,
}: {
	sessions: WorkspaceSession[];
	onOpen: (session: WorkspaceSession) => void;
}) {
	const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
	const [showArchived, setShowArchived] = useState(false);
	const [selectedId, setSelectedId] = useState<string>();
	const rows = useMemo(() => fleetRows(sessions, collapsed, showArchived), [sessions, collapsed, showArchived]);
	const selected = rows.find((row) => row.session.id === selectedId) ?? rows[0];
	const session = selected?.session;
	const coordination = useQuery({
		queryKey: ["session-coordination", session?.id],
		enabled: !!session,
		refetchInterval: 15_000,
		queryFn: async (): Promise<Coordination> => {
			if (usesPreviewWorkspaceData)
				return {
					events: mockCoordinationEvents.filter(
						(event) =>
							event.sourceSessionId === session!.id ||
							("targetSessionId" in event && event.targetSessionId === session!.id),
					),
					hasMore: false,
				};
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/coordination", {
				params: { path: { sessionId: session!.id } },
			});
			if (error || !data) throw new Error("Could not load communication");
			return data;
		},
	});
	const names = new Map(sessions.map((item) => [item.id, item.title]));
	const toggle = (id: string) =>
		setCollapsed((current) => {
			const next = new Set(current);
			if (next.has(id)) next.delete(id);
			else next.add(id);
			return next;
		});
	return (
		<section className="flex min-h-0 flex-1 flex-col" aria-label="Session fleet">
			<div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-2">
				<p className="text-xs text-muted-foreground">
					Spawn hierarchy · Manager means a worker with recorded children.
				</p>
				<Button variant="ghost" size="sm" aria-pressed={showArchived} onClick={() => setShowArchived(!showArchived)}>
					Show archived
				</Button>
			</div>
			{usesPreviewWorkspaceData ? (
				<p className="px-4 py-2 text-xs text-muted-foreground">
					Preview data · Synthetic sessions, not the live fleet.
				</p>
			) : null}
			{!rows.length ? (
				<p className="p-6 text-sm text-muted-foreground">No sessions to display.</p>
			) : (
				<div className="flex min-h-0 flex-1 flex-wrap overflow-auto">
					<ol className="min-w-0 flex-[2_1_24rem] overflow-auto p-3" aria-label="Session hierarchy">
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
										aria-label={`${collapsed.has(row.session.id) ? "Expand" : "Collapse"} ${row.session.title}`}
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
									<span className="sr-only">Level {row.depth + 1}. </span>
									<span className="block truncate text-sm font-medium">{row.session.title}</span>
									<span className="block truncate text-xs text-muted-foreground">
										{row.role} · {row.session.displayStatus ?? getSessionStatusView(row.session.status).label}
										{row.session.isTerminated ? " · Archived" : ""}
										{row.parentUnavailable
											? " · Parent unavailable"
											: !row.session.parentSessionId && row.session.kind !== "orchestrator"
												? " · No recorded parent"
												: ""}
									</span>
								</button>
								{row.session.prs?.length ? (
									<span className="shrink-0 pt-2 text-xs text-muted-foreground">
										{row.session.prs.length} PR{row.session.prs.length > 1 ? "s" : ""}
									</span>
								) : null}
							</li>
						))}
					</ol>
					{session ? (
						<aside
							className="min-w-0 flex-[1_1_20rem] space-y-5 overflow-auto border-l border-border p-4"
							aria-label="Selected session"
						>
							<div className="space-y-2">
								<div className="flex items-start justify-between gap-3">
									<h2 className="min-w-0 break-words text-sm font-semibold">{session.title}</h2>
									<Button variant="secondary" size="sm" onClick={() => onOpen(session)}>
										Open session
										<ExternalLink className="size-3" />
									</Button>
								</div>
								<p className="break-all font-mono text-xs text-muted-foreground">{session.id}</p>
								<p className="text-sm">
									{selected.role} · {session.displayStatus ?? getSessionStatusView(session.status).label}
								</p>
								{session.parentSessionId ? (
									<p className="text-xs text-muted-foreground">
										Spawned by {names.get(session.parentSessionId) ?? session.parentSessionId}
									</p>
								) : (
									<p className="text-xs text-muted-foreground">
										No recorded parent. Older sessions may predate lineage tracking.
									</p>
								)}
							</div>
							<div className="space-y-2">
								<h3 className="text-sm font-medium">Work</h3>
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
												PR #{pr.number}
											</AppLink>{" "}
											· {pr.state}
											<p className="mt-1 text-muted-foreground">
												CI: {pr.ci} · Review: {pr.review}
											</p>
										</div>
									))
								) : (
									<p className="text-xs text-muted-foreground">No linked pull requests.</p>
								)}
								{session.artifactFiles?.length ? (
									<p className="text-xs text-muted-foreground">
										{session.artifactFiles.length} artifact(s) · Open session to inspect
									</p>
								) : null}
							</div>
							<div className="space-y-3">
								<h3 className="flex items-center gap-2 text-sm font-medium">
									<MessageSquare className="size-3.5" />
									Communication
								</h3>
								<p className="text-xs text-muted-foreground">
									Attributed session Chat messages and this worker’s reports only. TUI sends and shared orchestrator
									history are not included. Recorded or acknowledged does not mean acted on.
								</p>
								{coordination.isError ? (
									<div role="alert" className="text-xs text-destructive">
										Could not load communication.{" "}
										<Button variant="ghost" size="sm" onClick={() => void coordination.refetch()}>
											Retry
										</Button>
									</div>
								) : coordination.isPending ? (
									<p role="status" className="text-xs text-muted-foreground">
										Loading communication…
									</p>
								) : (
									<>
										{coordination.data?.hasMore ? (
											<p className="text-xs text-muted-foreground">Latest 50 recorded events</p>
										) : null}
										{!coordination.data?.events.length ? (
											<p className="text-xs text-muted-foreground">No recorded communication in this history.</p>
										) : (
											<ol className="divide-y divide-border">
												{coordination.data.events.map((event) => (
													<li key={event.id} className="space-y-1 py-3 text-xs">
														<p className="break-words font-medium">
															{names.get(event.sourceSessionId) ?? event.sourceSessionId} →{" "}
															{event.targetSessionId
																? (names.get(event.targetSessionId) ?? event.targetSessionId)
																: "Project orchestrator (recipient not recorded)"}
														</p>
														<p className="text-muted-foreground">
															{event.kind === "report"
																? `Report${event.reportState ? ` · ${event.reportState.replaceAll("_", " ")}` : ""}`
																: "Message"}{" "}
															· {event.state} ·{" "}
															<time dateTime={event.createdAt}>{new Date(event.createdAt).toLocaleString()}</time>
														</p>
														<p className="whitespace-pre-wrap break-words">{event.summary || "No summary"}</p>
													</li>
												))}
											</ol>
										)}
									</>
								)}
							</div>
						</aside>
					) : null}
				</div>
			)}
		</section>
	);
}
