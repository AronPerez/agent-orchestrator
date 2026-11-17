import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import type { components } from "../../api/schema";
import { apiErrorException } from "../lib/api-error-exception";
import { clientForSessionHost } from "../lib/host-clients";
import {
	projectOverlapsQueryKey,
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
	const truncated = response.truncatedSessionIds.includes(sessionId);
	const unresolvedBase = response.unresolvedBaseSessionIds.includes(sessionId);
	const unavailable = response.unavailableSessionIds.includes(sessionId);
	if (peerPaths.size === 0 && !truncated && !unresolvedBase && !unavailable) return undefined;
	const peers = [...peerPaths]
		.map(([peer, paths]) => ({ sessionId: peer, paths: [...paths].sort() }))
		.sort((a, b) => a.sessionId.localeCompare(b.sessionId));
	const paths = [...new Set(peers.flatMap((peer) => peer.paths))].sort();
	return { peers, paths, truncated, unresolvedBase, unavailable };
}

export function useProjectOverlaps(
	projectId: string | undefined,
	options: { enabled?: boolean; hostId?: string; liveSessionIds?: readonly string[] } = {},
) {
	const queryClient = useQueryClient();
	const enabled = options.enabled !== false && Boolean(projectId);
	const liveSessionIds = options.liveSessionIds === undefined
		? undefined
		: [...new Set(options.liveSessionIds)].sort();
	const fingerprint = liveSessionIds?.join("\u0000");
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
		void queryClient.invalidateQueries({ queryKey: projectOverlapsQueryKey(projectId, options.hostId) });
		if ((liveSessionIds?.length ?? 0) < 2) return;
		return subscribeProjectOverlapChanges(projectId, fingerprint, queryClient, options.hostId);
	}, [enabled, fingerprint, options.hostId, projectId, queryClient]);

	return query;
}
