import type { MouseEvent, ReactNode } from "react";
import { FileText, MessageSquare } from "@/components/app-icons";
import { useDecisionDetail } from "../hooks/useDecisionInbox";
import type { DecisionTextPart } from "../lib/decision-references";
import { decisionSessionTarget, type DecisionMachine } from "../lib/decision-session-target";
import { openLinkInSystemBrowser } from "../lib/external-link-policy";
import type { HostId } from "../lib/hosts";
import { useNavigateToSession } from "../lib/navigate-to-session";
import { useDecisionLinkNavigation } from "../lib/use-decision-link-navigation";
import { useSessionLinkNavigation } from "../lib/use-session-link-navigation";
import { cn } from "../lib/utils";
import { AppLink } from "./AppLink";
import { PullRequestChipContent, PullRequestHoverCard } from "./chat/PullRequestLink";
import { DecisionLinkPreviewCard } from "./DecisionLinkPreviewCard";
import { DecisionStateDot } from "./DecisionStateLabel";
import { BrowserIcon } from "./icons";
import { ArtifactLinkPreviewCard, SessionLinkPreviewCard } from "./SessionLinkPreviewCard";

/** The chat's pull request chip, in two sizes: body text and caption lines. */
const CHIP = "inline-flex max-w-full min-w-0 items-center gap-1 rounded-sm bg-interactive-active font-medium whitespace-nowrap text-foreground tabular-nums no-underline transition-colors hover:bg-[color-mix(in_oklch,var(--color-text-primary)_11%,transparent)] focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring";
const CHIP_SIZE = {
	md: "h-5 px-1.5 align-[-0.3em] text-control leading-5",
	sm: "h-[18px] px-1 align-[-0.25em] text-caption leading-[18px]",
} as const;
export type DecisionChipSize = keyof typeof CHIP_SIZE;

export function decisionChipClass(size: DecisionChipSize = "md", className?: string) {
	return cn(CHIP, CHIP_SIZE[size], className);
}

const WEB_LINK = "break-words text-markdown-link underline decoration-markdown-link/45 underline-offset-2 hover:text-markdown-link-hover";

/** A page opens in a browser; anything else is a file. */
export function artifactIcon(name: string) {
	return /\.html?$/i.test(name) ? BrowserIcon : FileText;
}

/** A file in a session's artifacts on the call's machine; it opens in that session's Artifacts. */
function ArtifactChip({ part, host }: { part: Extract<DecisionTextPart, { kind: "artifact" }>; host: HostId }) {
	const openArtifact = useSessionLinkNavigation(host);
	const Icon = artifactIcon(part.name);
	return (
		<AppLink
			href={part.href}
			className={decisionChipClass("md", "max-w-80")}
			hoverPreview={() => <ArtifactLinkPreviewCard href={part.href} sourceHostId={host} />}
			onClick={(event: MouseEvent<HTMLAnchorElement>) => {
				event.preventDefault();
				openArtifact(part.href);
			}}
		>
			<Icon aria-hidden="true" className="size-3 shrink-0 text-muted-foreground" />
			<span className="truncate">{part.name}</span>
		</AppLink>
	);
}

/** Another call, drawn with its state and title; the written link stays its accessible text until the call loads. */
function DecisionChip({ part }: { part: Extract<DecisionTextPart, { kind: "decision" }> }) {
	const detail = useDecisionDetail(part.callId);
	const call = detail.data?.call;
	const openDecision = useDecisionLinkNavigation();
	return (
		<AppLink
			href={part.href}
			className={decisionChipClass("md", "max-w-80")}
			hoverPreview={() => <DecisionLinkPreviewCard href={part.href} onOpen={() => openDecision(part.href)} />}
			onClick={(event) => {
				event.preventDefault();
				openDecision(part.href);
			}}
		>
			{call ? <DecisionStateDot state={call.state} /> : null}
			<span className="truncate">{call?.title ?? part.text}</span>
		</AppLink>
	);
}

/** One thing a call's text names, drawn in place of the writer's words. */
export function DecisionTextLink({ part, machine }: { part: Exclude<DecisionTextPart, { kind: "text" }>; machine: DecisionMachine }) {
	switch (part.kind) {
		case "decision":
			return <DecisionChip part={part} />;
		case "session":
			return <DecisionSessionChip machine={machine} sessionId={part.sessionId} size="md" icon>{part.text}</DecisionSessionChip>;
		case "artifact": {
			// A file is only ever looked up on the call's own machine. Until that
			// machine's sessions are read, the reference stays text: a same-named
			// file on this computer is not the one the call means. Keyed by host,
			// so reaching the machine mounts the chip afresh instead of switching
			// its hooks from one host's to another's.
			const host = decisionMachineHost(machine);
			return host === undefined ? <span>{part.text}</span> : <ArtifactChip key={host} part={part} host={host} />;
		}
		case "pr":
			return (
				<AppLink href={part.url} systemBrowser className={decisionChipClass()} hoverPreview={() => <PullRequestHoverCard url={part.url} onBrowserOpen={(url) => void openLinkInSystemBrowser(url)} />}>
					<PullRequestChipContent href={part.url} number={part.number} />
				</AppLink>
			);
		case "web":
			return <AppLink href={part.url} systemBrowser className={WEB_LINK}>{part.text}</AppLink>;
	}
}

/**
 * A call's own plain text with what it names drawn as links. Sessions and files
 * are looked up on the machine that owns the call; nothing is read as Markdown.
 */
export function DecisionText({ parts, machine, className }: { parts: readonly DecisionTextPart[]; machine: DecisionMachine; className?: string }) {
	return (
		<p className={cn("whitespace-pre-wrap break-words", className)}>
			{parts.map((part, index) => part.kind === "text"
				? <span key={index}>{part.text}</span>
				: <DecisionTextLink key={index} part={part} machine={machine} />)}
		</p>
	);
}

/** A session's name before its first colon, when that reads as a name: "worker-3: builds the API" shows "worker-3". */
export function shortSessionName(name: string): string {
	const head = name.split(": ")[0]!;
	return head !== name && head.length <= 40 ? head : name;
}

/** The host a call's files are read from: its own machine, once this app has read that machine's sessions. */
export function decisionMachineHost(machine: DecisionMachine): HostId | undefined {
	return machine.kind === "ready" ? machine.host : undefined;
}

/**
 * A session on the call's machine, by name. It opens the session when that
 * machine lists it, with its card on hover; otherwise it is plain text.
 */
export function DecisionSessionChip({ machine, sessionId, children, size = "sm", icon = false }: { machine: DecisionMachine; sessionId: string; children?: ReactNode; size?: DecisionChipSize; icon?: boolean }) {
	const navigateToSession = useNavigateToSession();
	const target = decisionSessionTarget(machine, sessionId);
	const label = children ?? sessionId;
	if (target.kind !== "open") return <span className={size === "md" ? undefined : "font-medium text-foreground"}>{label}</span>;
	const href = `ao://sessions/${encodeURIComponent(target.projectId)}/${encodeURIComponent(target.sessionId)}`;
	return (
		<AppLink
			href={href}
			className={decisionChipClass(size, "max-w-72")}
			hoverPreview={() => <SessionLinkPreviewCard href={href} sourceHostId={target.host} />}
			onClick={(event) => {
				event.preventDefault();
				// A finished session opens to its existing view; nothing restarts it.
				navigateToSession(target.projectId, target.sessionId, target.host);
			}}
		>
			{icon ? <MessageSquare aria-hidden="true" className="size-3 shrink-0 text-muted-foreground" /> : null}
			<span className="truncate">{label}</span>
		</AppLink>
	);
}
