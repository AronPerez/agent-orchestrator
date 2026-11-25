import { useEffect, type CSSProperties, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, GitBranch, MessageSquare } from "@/components/app-icons";
import type { components } from "../../api/schema";
import type { WorkspaceSession } from "../types/workspace";
import { apiErrorCode } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";
import { clientForSessionHost } from "../lib/host-clients";
import { HOST_UNREACHABLE_CODES, LOCAL_HOST } from "../lib/hosts";
import { useConnectedHosts } from "../hooks/useHostConnection";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import { mockCoordinationEvents } from "../lib/mock-data";
import { cn } from "../lib/utils";
import { AppLink } from "./AppLink";
import { Button } from "./ui/button";

type Coordination = components["schemas"]["SessionCoordinationResponse"];
type CoordinationRequestError = Error & { status?: number; code?: string };

/**
 * One fleet session's work and recorded communication, unfolded under its row
 * in the fleet tree. History is read from the machine the session runs on.
 */
export function FleetSessionDetails({
	session,
	hostId,
	roleLabel,
	statusLabel,
	parentName,
	sessionNames,
	onOpen,
	onClose,
	className,
	style,
}: {
	session: WorkspaceSession;
	/** The machine the session runs on; undefined or LOCAL_HOST for this computer. */
	hostId?: string;
	roleLabel: string;
	statusLabel: string;
	/** Parent title or qualified address; undefined when no parent is recorded. */
	parentName?: string;
	/** Titles of sessions on the same machine, for history sources and targets. */
	sessionNames: ReadonlyMap<string, string>;
	onOpen: () => void;
	onClose: () => void;
	className?: string;
	style?: CSSProperties;
}) {
	const { t, i18n } = useTranslation();
	const queryClient = useQueryClient();
	const machineId = hostId && hostId !== LOCAL_HOST ? hostId : undefined;
	const remote = Boolean(machineId);
	const connected = useConnectedHosts().includes(machineId ?? "");
	useEffect(() => {
		if (remote && !connected) void queryClient.cancelQueries({ queryKey: ["session-coordination", machineId, session.id], exact: true });
	}, [queryClient, remote, connected, machineId, session.id]);
	const coordination = useQuery<Coordination>({
		queryKey: ["session-coordination", machineId ?? "", session.id],
		enabled: !remote || connected,
		retry: false,
		refetchInterval: (query) => [404, 501].includes((query.state.error as CoordinationRequestError | null)?.status ?? 0) ? false : 15_000,
		queryFn: async ({ signal }): Promise<Coordination> => {
			if (usesPreviewWorkspaceData && !remote)
				return {
					events: mockCoordinationEvents.filter(
						(event) =>
							event.sourceSessionId === session.id ||
							("targetSessionId" in event && event.targetSessionId === session.id),
					),
					hasMore: false,
				};
			const { data, error, response } = await clientForSessionHost(machineId).GET("/api/v1/sessions/{sessionId}/coordination", {
				params: { path: { sessionId: session.id } },
				signal,
			});
			if (error || !data) throw Object.assign(apiErrorException(error), { status: response?.status, code: apiErrorCode(error) });
			return data;
		},
	});
	const historyError = coordination.error as CoordinationRequestError | null;
	const offline = remote && (!connected || HOST_UNREACHABLE_CODES.has(historyError?.code));
	const name = (id: string) => sessionNames.get(id) ?? id;
	const handleKeyDown = (event: KeyboardEvent<HTMLElement>) => {
		if (event.key !== "Escape") return;
		event.preventDefault();
		event.stopPropagation();
		onClose();
	};

	return (
		<div
			aria-label={t("fleet.selected")}
			className={cn("grid gap-6 rounded-md bg-surface p-4 md:grid-cols-2", className)}
			data-host-offline={offline ? "" : undefined}
			onKeyDown={handleKeyDown}
			role="group"
			style={style}
		>
			<div className="min-w-0 space-y-5">
				<div className="space-y-2">
					<div className="flex items-start justify-between gap-3">
						<h2 className="min-w-0 break-words text-sm font-semibold">{session.title}</h2>
						<Button variant="secondary" size="sm" onClick={onOpen}>
							{t("notify.openSessionTitle")}
							<ExternalLink className="size-3" />
						</Button>
					</div>
					<p className="break-all font-mono text-xs text-muted-foreground">{session.id}</p>
					<p className="text-sm">{roleLabel} · {statusLabel}</p>
					<p className="text-xs text-muted-foreground">
						{parentName ? t("fleet.spawnedBy", { name: parentName }) : t("fleet.noParentExplanation")}
					</p>
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
			</div>
			<div className="min-w-0 space-y-3">
				<h3 className="flex items-center gap-2 text-sm font-medium">
					<MessageSquare className="size-3.5" />
					{t("fleet.communication")}
				</h3>
				<p className="text-xs text-muted-foreground">{t("fleet.coverage")}</p>
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
					<p role="status" className="text-xs text-muted-foreground">{t("fleet.loading")}</p>
				) : null}
				{coordination.data && (!coordination.isError || offline) ? (
					<>
						{coordination.data.hasMore ? <p className="text-xs text-muted-foreground">{t("fleet.limit")}</p> : null}
						{!coordination.data.events.length ? (
							<p className="text-xs text-muted-foreground">{t("fleet.noEvents")}</p>
						) : (
							<ol className="divide-y divide-border">
								{coordination.data.events.map((event) => (
									<li key={event.id} className="space-y-1 py-3 text-xs">
										<p className="break-words font-medium">
											{name(event.sourceSessionId)} →{" "}
											{event.targetSessionId ? name(event.targetSessionId) : t("fleet.recipientUnknown")}
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
		</div>
	);
}
