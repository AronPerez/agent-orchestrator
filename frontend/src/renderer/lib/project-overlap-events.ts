import type { QueryClient } from "@tanstack/react-query";
import { eventStreamBaseUrl, getApiBaseUrl, hasTrustedApiBaseUrl, subscribeApiBaseUrl } from "./api-client";
import { baseUrlForHost, subscribeConnectedHosts } from "./host-clients";
import { probeRemoteSse } from "./remote-sse-probe";
import { computeSseRetryDelayMs } from "./sse-backoff";

const EVENTSOURCE_CLOSED = 2;
export const REMOTE_PROJECT_OVERLAP_POLL_MS = 15_000;

type ProjectOverlapStream = { refs: number; dispose: () => void };

const streams = new Map<string, ProjectOverlapStream>();

export const projectOverlapsQueryKey = (projectId: string, hostId?: string) =>
	["project-overlaps", hostId ?? "", projectId] as const;

/** Shares one project-level filesystem watcher across every board copy. */
export function subscribeProjectOverlapChanges(
	projectId: string,
	activeSessionFingerprint: string,
	queryClient: QueryClient,
	hostId?: string,
): () => void {
	const key = JSON.stringify([hostId ?? "", projectId, activeSessionFingerprint]);
	let stream = streams.get(key);
	if (!stream) {
		stream = { refs: 0, dispose: openProjectOverlapStream(projectId, queryClient, hostId) };
		streams.set(key, stream);
	}
	stream.refs += 1;
	let released = false;
	return () => {
		if (released) return;
		released = true;
		const current = streams.get(key);
		if (!current) return;
		current.refs -= 1;
		if (current.refs > 0) return;
		current.dispose();
		streams.delete(key);
	};
}

function openProjectOverlapStream(projectId: string, queryClient: QueryClient, hostId?: string): () => void {
	let disposed = false;
	let source: EventSource | undefined;
	let stopRemote: (() => void) | undefined;
	let retry: ReturnType<typeof setTimeout> | undefined;
	let fallbackPoll: ReturnType<typeof setInterval> | undefined;
	let sourceBaseUrl: string | undefined;
	let failures = 0;
	let generation = 0;
	let refreshState: { dirty: boolean } | undefined;

	const refresh = () => {
		if (disposed) return;
		if (refreshState) {
			refreshState.dirty = true;
			return;
		}
		const queryKey = projectOverlapsQueryKey(projectId, hostId);
		const state = { dirty: queryClient.isFetching({ queryKey, type: "active" }) > 0 };
		refreshState = state;
		const settled = () => {
			refreshState = undefined;
			if (state.dirty && !disposed) refresh();
		};
		void queryClient.invalidateQueries({ queryKey }, { cancelRefetch: false }).then(settled, settled);
	};
	const startFallbackPolling = () => {
		if (disposed || fallbackPoll) return;
		refresh();
		fallbackPoll = setInterval(refresh, REMOTE_PROJECT_OVERLAP_POLL_MS);
	};
	const stopFallbackPolling = () => {
		if (fallbackPoll) clearInterval(fallbackPoll);
		fallbackPoll = undefined;
	};
	const closeConnection = () => {
		generation += 1;
		if (retry) clearTimeout(retry);
		retry = undefined;
		stopRemote?.();
		stopRemote = undefined;
		source?.close();
		source = undefined;
		sourceBaseUrl = undefined;
	};
	const scheduleRetry = (connect: () => void, currentGeneration: number) => {
		if (disposed || retry) return;
		failures += 1;
		retry = setTimeout(() => {
			retry = undefined;
			if (!disposed && generation === currentGeneration) connect();
		}, computeSseRetryDelayMs(failures));
	};
	const connect = () => {
		if (disposed) return;
		const baseUrl = hostId ? baseUrlForHost(hostId) : hasTrustedApiBaseUrl() ? getApiBaseUrl() : undefined;
		if (!baseUrl || typeof EventSource === "undefined") {
			closeConnection();
			stopFallbackPolling();
			return;
		}
		if (sourceBaseUrl === baseUrl && (source || stopRemote || retry)) return;
		closeConnection();
		sourceBaseUrl = baseUrl;
		const currentGeneration = generation;
		const path = `/api/v1/projects/${encodeURIComponent(projectId)}/overlaps/events`;
		if (hostId) {
			let failedSynchronously = false;
			const stop = probeRemoteSse(
				`${baseUrl.replace(/\/+$/, "")}${path}`,
				["overlaps_changed"],
				refresh,
				() => {
					failures = 0;
					stopFallbackPolling();
					refresh();
				},
				() => {
					failedSynchronously = stopRemote === undefined;
					stopRemote = undefined;
					startFallbackPolling();
					scheduleRetry(connect, currentGeneration);
				},
			);
			if (failedSynchronously) stop();
			else stopRemote = stop;
			return;
		}
		try {
			const next = new EventSource(`${eventStreamBaseUrl(baseUrl)}${path}`);
			source = next;
			next.addEventListener("ready", () => {
				if (disposed || source !== next) return;
				failures = 0;
				refresh();
			});
			next.addEventListener("overlaps_changed", refresh);
			next.onerror = () => {
				if (disposed || source !== next || next.readyState !== EVENTSOURCE_CLOSED) return;
				next.close();
				source = undefined;
				scheduleRetry(connect, currentGeneration);
			};
		} catch {
			source = undefined;
			scheduleRetry(connect, currentGeneration);
		}
	};

	const stopConnectionChanges = hostId ? subscribeConnectedHosts(connect) : subscribeApiBaseUrl(connect);
	connect();
	return () => {
		disposed = true;
		stopConnectionChanges();
		stopFallbackPolling();
		closeConnection();
	};
}
