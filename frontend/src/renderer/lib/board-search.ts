import type { WorkspaceSession } from "../types/workspace";
import { sessionBranchPresentation } from "./session-branch";

/**
 * What a board search matches on a card, lowercased: its title, session id,
 * the branch the card shows (a provisional branch stays hidden, as on the
 * card) and its PR numbers as "#123".
 */
export function boardSearchText(session: WorkspaceSession): string {
	return [
		session.title,
		session.id,
		sessionBranchPresentation(session).branch,
		...session.prs.map((pr) => `#${pr.number}`),
	]
		.filter(Boolean)
		.join("\n")
		.toLowerCase();
}

/** The query's words; empty when there is nothing to search for. */
export function boardSearchTokens(query: string): string[] {
	return query.trim().toLowerCase().split(/\s+/).filter(Boolean);
}

/**
 * Sessions whose search text holds every token, in their original order.
 * `extraText` adds what else a card shows (its project, its rendered status),
 * which needs the renderer's translations to compute.
 */
export function filterBoardSessions<T extends WorkspaceSession>(
	sessions: T[],
	query: string,
	extraText?: (session: T) => Array<string | undefined>,
): T[] {
	const tokens = boardSearchTokens(query);
	if (tokens.length === 0) return sessions;
	return sessions.filter((session) => {
		const extra = extraText?.(session).filter(Boolean).join("\n").toLowerCase() ?? "";
		const text = extra ? `${boardSearchText(session)}\n${extra}` : boardSearchText(session);
		return tokens.every((token) => text.includes(token));
	});
}
