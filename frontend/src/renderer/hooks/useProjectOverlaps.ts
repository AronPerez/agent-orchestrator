import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import type { components } from "../../api/schema";
import { apiErrorException } from "../lib/api-error-exception";
import { clientForSessionHost } from "../lib/host-clients";
import {
	projectOverlapsQueryKey,
	refreshProjectOverlaps,
	subscribeProjectOverlapChanges,
} from "../lib/project-overlap-events";

export type ProjectOverlapsResponse = components["schemas"]["ProjectOverlapsResponse"];

export type SessionProjectOverlap = {
	peers: Array<{ sessionId: string; paths: string[] }>;
	paths: string[];
	truncated: boolean;
	unresolvedBase: boolean;
	unavailable: boolean;
};

export type ProjectOverlapLiveSession = {
	id: string;
	workspacePublished?: boolean;
};

export function projectOverlapSessionFingerprint(sessions: readonly ProjectOverlapLiveSession[]): string {
	const publishedByID = new Map(sessions.map((session) => [session.id, session.workspacePublished !== false]));
	return JSON.stringify([...publishedByID].sort(([left], [right]) => left.localeCompare(right)));
}

export function projectOverlapForSession(
	response: ProjectOverlapsResponse | undefined,
	sessionId: string,
): SessionProjectOverlap | undefined {
	if (!response) return undefined;
	const peerPaths = new Map<string, Set<string>>();
	for (const overlap of response.overlaps) {
		const peer = overlap.sessionA === sessionId
			? overlap.sessionB
			: overlap.sessionB === sessionId ? overlap.sessionA : undefined;
		if (!peer) continue;
		const paths = peerPaths.get(peer) ?? new Set<string>();
		for (const path of overlap.paths) paths.add(path);
		peerPaths.set(peer, paths);
	}
	// Every comparison in the project can be incomplete when any peer scan is
	// incomplete, even if this session's own workspace was read successfully.
	const truncated = response.truncatedSessionIds.length > 0;
	const unresolvedBase = response.unresolvedBaseSessionIds.length > 0;
	const unavailable = response.unavailableSessionIds.length > 0;
	if (peerPaths.size === 0 && !truncated && !unresolvedBase && !unavailable) return undefined;
	const peers = [...peerPaths]
		.map(([peer, paths]) => ({ sessionId: peer, paths: [...paths].sort() }))
		.sort((a, b) => a.sessionId.localeCompare(b.sessionId));
	const paths = [...new Set(peers.flatMap((peer) => peer.paths))].sort();
	return { peers, paths, truncated, unresolvedBase, unavailable };
}

export function useProjectOverlaps(
	projectId: string | undefined,
	options: { enabled?: boolean; hostId?: string; liveSessions?: readonly ProjectOverlapLiveSession[] } = {},
) {
	const queryClient = useQueryClient();
	const enabled = options.enabled !== false && Boolean(projectId);
	const fingerprint = options.liveSessions === undefined
		? undefined
		: projectOverlapSessionFingerprint(options.liveSessions);
	const liveSessionCount = options.liveSessions === undefined
		? 0
		: new Set(options.liveSessions.map((session) => session.id)).size;
	const query = useQuery({
		queryKey: projectOverlapsQueryKey(projectId ?? "", options.hostId),
		enabled,
		retry: false,
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(options.hostId).GET("/api/v1/projects/{id}/overlaps", {
				params: { path: { id: projectId! } },
			});
			if (error) throw apiErrorException(error);
			return data;
		},
	});

	useEffect(() => {
		if (!enabled || !projectId || fingerprint === undefined) return;
		refreshProjectOverlaps(queryClient, projectId, options.hostId);
		if (liveSessionCount < 2) return;
		return subscribeProjectOverlapChanges(projectId, fingerprint, queryClient, options.hostId);
	}, [enabled, fingerprint, liveSessionCount, options.hostId, projectId, queryClient]);

	return query;
}
