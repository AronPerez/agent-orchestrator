import { parseDecisionLink } from "./decision-links";
import { isPullRequestLink } from "./link-preload";
import { artifactLink, artifactLinkForPath, findSessionLinks, parseArtifactLink, parseSessionLink } from "./session-links";

/** The sessions a call's text may name: the workspaces of the machine that owns the call. */
export type ReferenceWorkspace = {
	id: string;
	sessions: ReadonlyArray<{ id: string; artifactFiles?: Array<{ path: string; previousPaths?: string[] }> }>;
};

/**
 * One run of a call's plain text. Everything but `text` is something the
 * writer named that the app can open: another call, a session, a file in a
 * session's artifacts, a pull request or a web page. `text` is always the
 * writer's own characters, so joining every part gives the text back.
 */
export type DecisionTextPart =
	| { kind: "text"; text: string }
	| { kind: "decision"; text: string; callId: string; href: string }
	| { kind: "session"; text: string; href: string; sessionId: string }
	| { kind: "artifact"; text: string; href: string; name: string; sessionId: string }
	| { kind: "pr"; text: string; url: string; number: number }
	| { kind: "web"; text: string; url: string };

export type DecisionTextContext = {
	workspaces: ReadonlyArray<ReferenceWorkspace>;
	/** The call's project, searched first when a bare name or session id is ambiguous. */
	projectId?: string;
	/** The raising session, searched first for a bare file name. */
	ownerSessionId?: string;
	/** Pull request URLs the call links, so "PR #12" in its text can name one. */
	pullRequestUrls?: readonly string[];
};

type Hit = { start: number; end: number; part: Exclude<DecisionTextPart, { kind: "text" }> };

const WEB_URL = /https?:\/\/[^\s<>"'`]+/g;
const ARTIFACT_PATH = /(?:~|\.{0,2})?\/?[^\s"'`()<>]*\/artifacts\/[^\s"'`()<>]+/g;
// A name with an extension, not inside a longer path or word.
const FILE_NAME = /(?<![\w./@-])[\w][\w.-]*\.[A-Za-z][A-Za-z0-9]{0,7}(?![\w/@-])/g;
// A session id: a name ending in a dash and a number, such as shop-224.
const SESSION_ID = /(?<![\w./@-])[A-Za-z0-9][\w.-]*-\d+(?![\w/@-])/g;
const PR_REFERENCE = /(?<![\w/#])(?:PR\s*)?#(\d+)\b/g;
const TRAILING_PUNCTUATION = /[.,;:!?'"’”]+$/;

function trimUrl(value: string): string {
	let url = value.replace(TRAILING_PUNCTUATION, "");
	// A closing bracket belongs to the URL only when the URL opened one.
	while (/[)\]]$/.test(url)) {
		const closing = url.endsWith(")") ? ")" : "]";
		const opening = closing === ")" ? "(" : "[";
		if (url.split(opening).length >= url.split(closing).length) break;
		url = url.slice(0, -1).replace(TRAILING_PUNCTUATION, "");
	}
	return url;
}

function pullRequestNumber(url: string): number | undefined {
	if (!isPullRequestLink(url)) return undefined;
	const number = Number(new URL(url).pathname.split("/").filter(Boolean)[3]);
	return Number.isInteger(number) ? number : undefined;
}

/** Workspaces in the order a bare reference is looked up: the call's project first. */
function ordered(context: DecisionTextContext): ReferenceWorkspace[] {
	const own = context.workspaces.filter((workspace) => workspace.id === context.projectId);
	return [...own, ...context.workspaces.filter((workspace) => workspace.id !== context.projectId)];
}

/** The one session with this id: in the call's project if there, otherwise anywhere it is unique. */
function sessionById(context: DecisionTextContext, sessionId: string): { projectId: string } | undefined {
	const matches = ordered(context).filter((workspace) => workspace.sessions.some((session) => session.id === sessionId));
	if (matches.length === 0) return undefined;
	if (matches[0]!.id === context.projectId || matches.length === 1) return { projectId: matches[0]!.id };
	return undefined;
}

/** The one listed artifact with this file name: the raising session's own, else unique in the project, else unique anywhere. */
function artifactByName(context: DecisionTextContext, name: string): { projectId: string; sessionId: string; path: string } | undefined {
	const named = (path: string) => path === name || path.endsWith(`/${name}`);
	const all = ordered(context).flatMap((workspace) => workspace.sessions.flatMap((session) => (session.artifactFiles ?? [])
		.filter((file) => named(file.path))
		.map((file) => ({ projectId: workspace.id, sessionId: session.id, path: file.path }))));
	const owner = all.filter((match) => match.sessionId === context.ownerSessionId);
	if (owner.length === 1) return owner[0];
	const project = all.filter((match) => match.projectId === context.projectId);
	if (project.length === 1) return project[0];
	return all.length === 1 ? all[0] : undefined;
}

function collect(text: string, context: DecisionTextContext): Hit[] {
	const hits: Hit[] = [];
	const free = (start: number, end: number) => hits.every((hit) => end <= hit.start || start >= hit.end);
	const add = (start: number, part: Hit["part"]) => {
		const end = start + part.text.length;
		if (part.text && free(start, end)) hits.push({ start, end, part });
	};
	for (const link of findSessionLinks(text)) {
		const decision = parseDecisionLink(link.text);
		const artifact = parseArtifactLink(link.text);
		const session = parseSessionLink(link.text);
		if (decision) add(link.start, { kind: "decision", text: link.text, callId: decision.callId, href: link.text });
		else if (artifact) add(link.start, { kind: "artifact", text: link.text, href: link.text, name: artifact.path.split("/").pop()!, sessionId: artifact.sessionId });
		else if (session) add(link.start, { kind: "session", text: link.text, href: link.text, sessionId: session.sessionId });
	}
	for (const match of text.matchAll(WEB_URL)) {
		const url = trimUrl(match[0]);
		const number = pullRequestNumber(url);
		add(match.index, number === undefined ? { kind: "web", text: url, url } : { kind: "pr", text: url, url, number });
	}
	const workspaces = context.workspaces as Parameters<typeof artifactLinkForPath>[1];
	for (const match of text.matchAll(ARTIFACT_PATH)) {
		const written = match[0].replace(TRAILING_PUNCTUATION, "");
		const href = artifactLinkForPath(written, workspaces);
		const target = href ? parseArtifactLink(href) : undefined;
		if (href && target) add(match.index, { kind: "artifact", text: written, href, name: target.path.split("/").pop()!, sessionId: target.sessionId });
	}
	for (const match of text.matchAll(FILE_NAME)) {
		const name = match[0].replace(/\.+$/, "");
		const found = artifactByName(context, name);
		if (found) add(match.index, { kind: "artifact", text: name, href: artifactLink(found.projectId, found.sessionId, found.path), name, sessionId: found.sessionId });
	}
	for (const match of text.matchAll(SESSION_ID)) {
		const found = sessionById(context, match[0]);
		if (found) add(match.index, { kind: "session", text: match[0], href: `ao://sessions/${encodeURIComponent(found.projectId)}/${encodeURIComponent(match[0])}`, sessionId: match[0] });
	}
	const pullRequests = new Map<number, string[]>();
	for (const url of context.pullRequestUrls ?? []) {
		const number = pullRequestNumber(url);
		if (number !== undefined) pullRequests.set(number, [...new Set([...(pullRequests.get(number) ?? []), url])]);
	}
	for (const match of text.matchAll(PR_REFERENCE)) {
		const urls = pullRequests.get(Number(match[1]));
		// Two linked repositories with the same number leave "#12" as text.
		if (urls?.length === 1) add(match.index, { kind: "pr", text: match[0], url: urls[0]!, number: Number(match[1]) });
	}
	return hits.sort((a, b) => a.start - b.start);
}

/** A call's plain text split into the writer's words and the things it names. It never reads Markdown. */
export function decisionTextParts(text: string, context: DecisionTextContext): DecisionTextPart[] {
	const parts: DecisionTextPart[] = [];
	let cursor = 0;
	for (const hit of collect(text, context)) {
		if (hit.start > cursor) parts.push({ kind: "text", text: text.slice(cursor, hit.start) });
		parts.push(hit.part);
		cursor = hit.end;
	}
	if (cursor < text.length) parts.push({ kind: "text", text: text.slice(cursor) });
	return parts;
}

export type DecisionLink = { label: string; url: string };

/** What a call rests on, gathered from its text and links, each once, in the order first named. */
export type DecisionMaterial = {
	/** Earlier calls this one names. */
	decisions: string[];
	artifacts: Array<{ href: string; name: string; sessionId: string }>;
	pullRequests: string[];
	/** The call's links that are not pull requests. */
	links: DecisionLink[];
};

export function decisionMaterial(
	call: { id: string; links: readonly DecisionLink[]; outcomeLink?: string },
	parts: readonly DecisionTextPart[],
): DecisionMaterial {
	const decisions: string[] = [];
	const artifacts: DecisionMaterial["artifacts"] = [];
	const pullRequests: string[] = [];
	const addPullRequest = (url: string) => { if (!pullRequests.includes(url)) pullRequests.push(url); };
	for (const link of call.links) if (pullRequestNumber(link.url) !== undefined) addPullRequest(link.url);
	for (const part of parts) {
		if (part.kind === "decision" && part.callId !== call.id && !decisions.includes(part.callId)) decisions.push(part.callId);
		if (part.kind === "artifact" && !artifacts.some((artifact) => artifact.href === part.href)) artifacts.push({ href: part.href, name: part.name, sessionId: part.sessionId });
		if (part.kind === "pr") addPullRequest(part.url);
	}
	if (call.outcomeLink && pullRequestNumber(call.outcomeLink) !== undefined) addPullRequest(call.outcomeLink);
	return { decisions, artifacts, pullRequests, links: call.links.filter((link) => pullRequestNumber(link.url) === undefined) };
}

/** True when a later call's text names this call, by its link or its whole id: dec_1 is not named by dec_10. */
export function citesDecision(text: string, callId: string): boolean {
	const id = callId.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
	return new RegExp(`(?<![\\w-])${id}(?![\\w-])`).test(text);
}
