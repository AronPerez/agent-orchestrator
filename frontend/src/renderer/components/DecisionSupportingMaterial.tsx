import { useQuery } from "@tanstack/react-query";
import { useId, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ChevronRight, Link2 } from "@/components/app-icons";
import { artifactAtPath } from "../../../../packages/shared/artifacts/deliverables";
import { useDecisionDetail } from "../hooks/useDecisionInbox";
import { usePullRequestPreview } from "../hooks/usePullRequestPreview";
import { decisionKeys, fetchDecisionSearch, type DecisionCall } from "../lib/decision-inbox";
import { decisionLink } from "../lib/decision-links";
import { citesDecision, type DecisionLink, type DecisionMaterial } from "../lib/decision-references";
import { isWebLink, openLinkInSystemBrowser } from "../lib/external-link-policy";
import { formatTimeCompact } from "../lib/format-time";
import { LOCAL_HOST, type HostId } from "../lib/hosts";
import { parseArtifactLink } from "../lib/session-links";
import { useDecisionLinkNavigation } from "../lib/use-decision-link-navigation";
import { useSessionLinkNavigation, useSessionLinkSource } from "../lib/use-session-link-navigation";
import { cn } from "../lib/utils";
import { artifactIcon, shortSessionName } from "./DecisionReference";
import { DecisionStateDot, decisionStateLabel } from "./DecisionStateLabel";
import { prStateIcon } from "./PRPills";
import { ArtifactContentPreview } from "./SessionLinkPreviewCard";
import { Button } from "./ui/button";
import { Pressable } from "./ui/pressable";

/** The most later calls one search reads; the daemon caps any search at 100. */
const LATER_SEARCH_LIMIT = 20;
const ROW = "flex w-full min-w-0 items-start gap-2 px-3 py-2 text-left transition-colors hover:bg-interactive-hover focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring";
const LIST = "overflow-hidden rounded-lg bg-card [&>*+*]:border-t [&>*+*]:border-border";

function Group({ label, children }: { label: string; children: ReactNode }) {
	const id = useId();
	return (
		<section aria-labelledby={id} className="flex min-w-0 flex-col gap-2">
			<h3 id={id} className="text-caption font-medium text-muted-foreground">{label}</h3>
			<div className={LIST}>{children}</div>
		</section>
	);
}

/** Later calls that name this one in their text, newest first. Search finds them by id; the text decides. */
export function useLaterDecisions(call: Pick<DecisionCall, "id">): DecisionCall[] {
	const search = useQuery({
		queryKey: decisionKeys.search(call.id, LATER_SEARCH_LIMIT),
		queryFn: ({ signal }) => fetchDecisionSearch(call.id, LATER_SEARCH_LIMIT, signal),
		staleTime: 30_000,
	});
	return (search.data ?? [])
		.filter((other) => other.id !== call.id && citesDecision(other.context, call.id))
		.sort((a, b) => b.raisedAt.localeCompare(a.raisedAt));
}

/** One related call: its state, title, what you chose and how it ended. Opens it. */
function DecisionRow({ call }: { call: DecisionCall }) {
	const { t } = useTranslation();
	const openDecision = useDecisionLinkNavigation();
	return (
		<Pressable className={ROW} onClick={() => openDecision(decisionLink(call.id))}>
			<DecisionStateDot state={call.state} className="mt-1.5" />
			<span className="flex min-w-0 flex-1 flex-col gap-0.5">
				<span className="text-sm font-medium text-foreground">{call.title}</span>
				<span className="text-caption text-muted-foreground">
					{call.chosenLabel ? t("decisions.material.youChose", { choice: call.chosenLabel }) : decisionStateLabel(call.state, t)}
				</span>
				{call.note ? <span className="line-clamp-2 text-caption text-muted-foreground">{t("decisions.material.yourNote", { note: call.note })}</span> : null}
				{call.outcome ? <span className="line-clamp-3 text-caption text-muted-foreground">{t("decisions.material.outcome", { text: call.outcome })}</span> : null}
			</span>
		</Pressable>
	);
}

function EarlierDecisionRow({ callId }: { callId: string }) {
	const detail = useDecisionDetail(callId);
	return detail.data ? <DecisionRow call={detail.data.call} /> : null;
}

/**
 * A file the call names, from its session's artifacts on the call's machine.
 * It expands in place to the same preview its hover card shows.
 */
function ArtifactRow({ href, name, host }: { href: string; name: string; host: HostId }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const previewId = useId();
	const source = useSessionLinkSource(host);
	const openArtifact = useSessionLinkNavigation(host);
	const target = parseArtifactLink(href);
	const session = target ? source.workspaces.find((workspace) => workspace.id === target.projectId)?.sessions.find((candidate) => candidate.id === target.sessionId) : undefined;
	const artifact = target ? artifactAtPath(session?.artifactFiles, target.path) : undefined;
	const Icon = artifactIcon(name);
	const author = session ? (session.kind === "orchestrator" ? t("shell.orchestrator") : shortSessionName(session.title || session.id)) : target?.sessionId;
	return (
		<div className="min-w-0">
			<Pressable className={ROW} aria-expanded={open} aria-controls={open ? previewId : undefined} disabled={!artifact} onClick={() => setOpen((value) => !value)}>
				<Icon aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
				<span className="flex min-w-0 flex-1 flex-col gap-0.5">
					<span className="truncate text-sm font-medium text-foreground">{name}</span>
					<span className="truncate text-caption text-muted-foreground">
						{[author, artifact ? formatTimeCompact(artifact.updatedAt) : t("artifactLink.unavailable")].filter(Boolean).join(" · ")}
					</span>
				</span>
				{artifact ? <ChevronRight aria-hidden="true" className={cn("mt-0.5 size-3.5 shrink-0 text-muted-foreground transition-transform duration-(--duration-fast) motion-reduce:transition-none", open && "rotate-90")} /> : null}
			</Pressable>
			{open && artifact && session ? (
				<div id={previewId} className="flex flex-col items-start gap-2 px-3 pb-3 pl-8">
					<div className="w-full min-w-0">
						<ArtifactContentPreview artifact={artifact} session={session} remote={host !== LOCAL_HOST} />
					</div>
					<Button type="button" size="xs" variant="secondary" onClick={() => openArtifact(href)}>{t("decisions.material.openFile")}</Button>
				</div>
			) : null}
		</div>
	);
}

/** A file the call names on a machine whose sessions this app has not read: named, never looked up elsewhere. */
function UnavailableArtifactRow({ name }: { name: string }) {
	const { t } = useTranslation();
	const Icon = artifactIcon(name);
	return (
		<div className="flex min-w-0 items-start gap-2 px-3 py-2">
			<Icon aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
			<span className="flex min-w-0 flex-1 flex-col gap-0.5">
				<span className="truncate text-sm font-medium text-foreground">{name}</span>
				<span className="truncate text-caption text-muted-foreground">{t("artifactLink.unavailable")}</span>
			</span>
		</div>
	);
}

/** A pull request the call names, with its state as GitHub reports it. Opens in the system browser. */
function PullRequestRow({ url }: { url: string }) {
	const { t } = useTranslation();
	const preview = usePullRequestPreview(url, true).data;
	const state = preview ? prStateIcon[preview.state] : undefined;
	const StateIcon = state?.icon;
	const number = preview?.number ?? url.split("/").filter(Boolean).pop();
	return (
		<Pressable className={ROW} onClick={() => void openLinkInSystemBrowser(url)}>
			{StateIcon ? <StateIcon aria-hidden="true" className={cn("mt-0.5 size-3.5 shrink-0", preview?.state === "draft" ? "text-muted-foreground" : state.className)} /> : <Link2 aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />}
			<span className="flex min-w-0 flex-1 flex-col gap-0.5">
				<span className="text-sm font-medium text-foreground"><span className="tabular-nums">#{number}</span>{preview ? ` ${preview.title}` : ""}</span>
				<span className="truncate text-caption text-muted-foreground">{preview ? [t(`prLink.state.${preview.state}`), preview.repo].join(" · ") : url.replace(/^https?:\/\//, "")}</span>
			</span>
		</Pressable>
	);
}

/** A web link the call carries; anything other than http(s) stays text. */
function LinkRow({ link }: { link: DecisionLink }) {
	const web = isWebLink(link.url);
	const body = (
		<>
			<Link2 aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
			<span className="flex min-w-0 flex-1 flex-col gap-0.5">
				<span className={cn("text-sm font-medium", web ? "text-foreground" : "text-muted-foreground")}>{link.label || link.url}</span>
				{web ? <span className="truncate text-caption text-muted-foreground">{link.url.replace(/^https?:\/\//, "")}</span> : null}
			</span>
		</>
	);
	if (!web) return <div className="flex min-w-0 items-start gap-2 px-3 py-2">{body}</div>;
	return (
		<a
			href={link.url}
			rel="noreferrer noopener"
			target="_blank"
			className={ROW}
			onClick={(event) => {
				event.preventDefault();
				void openLinkInSystemBrowser(link.url);
			}}
		>
			{body}
		</a>
	);
}

/** True when the call has anything to show here before later calls load. */
export function hasDecisionMaterial(material: DecisionMaterial): boolean {
	return material.decisions.length + material.artifacts.length + material.pullRequests.length + material.links.length > 0;
}

/**
 * What a call rests on, so the person can read it without leaving: the calls
 * before and after it, the files and pull requests it names, and its links.
 * Groups with nothing in them are not drawn.
 */
export function DecisionSupportingMaterial({ material, later, host }: { material: DecisionMaterial; later: DecisionCall[]; host: HostId | undefined }) {
	const { t } = useTranslation();
	if (!hasDecisionMaterial(material) && later.length === 0) return null;
	return (
		<div role="region" aria-label={t("decisions.material.label")} className="flex min-w-0 flex-col gap-4">
			{material.decisions.length > 0 ? (
				<Group label={t("decisions.material.earlier")}>
					{material.decisions.map((id) => <EarlierDecisionRow key={id} callId={id} />)}
				</Group>
			) : null}
			{later.length > 0 ? (
				<Group label={t("decisions.material.later")}>
					{later.map((call) => <DecisionRow key={call.id} call={call} />)}
				</Group>
			) : null}
			{material.artifacts.length > 0 ? (
				<Group label={t("decisions.material.files")}>
					{material.artifacts.map((artifact) => host === undefined
						? <UnavailableArtifactRow key={artifact.href} name={artifact.name} />
						// Keyed by host as well, so reaching the machine mounts a fresh row on that host's hooks.
						: <ArtifactRow key={`${host}\n${artifact.href}`} href={artifact.href} name={artifact.name} host={host} />)}
				</Group>
			) : null}
			{material.pullRequests.length > 0 ? (
				<Group label={t("decisions.material.pullRequests")}>
					{material.pullRequests.map((url) => <PullRequestRow key={url} url={url} />)}
				</Group>
			) : null}
			{material.links.length > 0 ? (
				<Group label={t("decisions.links")}>
					{material.links.map((link) => <LinkRow key={`${link.label}:${link.url}`} link={link} />)}
				</Group>
			) : null}
		</div>
	);
}
