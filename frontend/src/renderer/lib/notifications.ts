import type { InfiniteData, QueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { aoBridge } from "./bridge";
import { apiErrorMessage, subscribeApiBaseUrl } from "./api-client";
import { baseUrlFor, clientFor, connectedHosts, subscribeConnectedHosts } from "./host-clients";
import { LOCAL_HOST, parseRefKey, refKey, type HostId, type Ref } from "./hosts";

type NotificationResponse = components["schemas"]["NotificationResponse"];
type ListNotificationsResponse = components["schemas"]["ListNotificationsResponse"];

export type NotificationDTO = NotificationResponse & { host: HostId };
export type NotificationsPage = Omit<ListNotificationsResponse, "notifications"> & {
	notifications: NotificationDTO[];
};
export type NotificationsCache = InfiniteData<NotificationsPage>;
export type NotificationListStatus = "unread" | "all";

export const unreadNotificationsQueryKey = ["notifications", "history", "unread"] as const;
export const recentNotificationsQueryKey = ["notifications", "history", "all"] as const;
export const NOTIFICATION_PAGE_SIZE = 100;

const SSE_RETRY_MS = 5_000;
const EVENTSOURCE_CLOSED = 2;

/** Mirrors NotificationType.NeedsResolution for live cache updates. */
const UNRESOLVABLE_TYPES = new Set(["needs_input", "ready_to_merge"]);

type NotificationsQueryKey = typeof unreadNotificationsQueryKey | typeof recentNotificationsQueryKey;

export function notificationsQueryKey(status: NotificationListStatus): NotificationsQueryKey {
	return status === "unread" ? unreadNotificationsQueryKey : recentNotificationsQueryKey;
}

function isUnresolved(notification: NotificationDTO): boolean {
	return UNRESOLVABLE_TYPES.has(notification.type) && !notification.resolvedAt;
}

function notificationHosts(): HostId[] {
	return [LOCAL_HOST, ...connectedHosts()];
}

type HostCursor = [host: HostId, cursor: string];

function decodeHostCursors(cursor: string): HostCursor[] {
	return JSON.parse(cursor) as HostCursor[];
}

export async function fetchNotificationsPage(status: NotificationListStatus, cursor = ""): Promise<NotificationsPage> {
	const requests = cursor
		? decodeHostCursors(cursor)
		: notificationHosts().map((host): HostCursor => [host, ""]);
	const settled = await Promise.allSettled(
		requests.map(async ([host, hostCursor]) => {
			const { data, error } = await clientFor(host).GET("/api/v1/notifications", {
				params: {
					query: {
						status,
						limit: NOTIFICATION_PAGE_SIZE,
						cursor: hostCursor || undefined,
					},
				},
			});
			if (error || !data) throw new Error(apiErrorMessage(error, "Could not load notifications"));
			const notifications = data.notifications.map((notification) => ({ ...notification, host }));
			return {
				host,
				nextCursor: data.nextCursor,
				notifications,
				unreadCount: data.unreadCount,
				unresolvedCount: data.unresolvedCount,
			};
		}),
	);
	const pages = settled.flatMap((result) => (result.status === "fulfilled" ? [result.value] : []));
	if (pages.length === 0) {
		const failed = settled.find((result) => result.status === "rejected");
		throw failed?.status === "rejected" && failed.reason instanceof Error
			? failed.reason
			: new Error("Could not load notifications");
	}
	const notifications = sortNotifications(pages.flatMap((page) => page.notifications));
	const nextCursors = pages.flatMap((page): HostCursor[] =>
		page.nextCursor ? [[page.host, page.nextCursor]] : [],
	);
	return {
		notifications,
		nextCursor: nextCursors.length > 0 ? JSON.stringify(nextCursors) : undefined,
		unreadCount: pages.reduce((count, page) => count + page.unreadCount, 0),
		unresolvedCount: pages.reduce((count, page) => count + page.unresolvedCount, 0),
	};
}

/** Fired when the panel opens — seeing the loaded notifications is the acknowledgement. */
export async function markAllNotificationsRead(keys: string[]): Promise<number> {
	const idsByHost = new Map<HostId, string[]>();
	for (const key of keys) {
		const { host, id } = parseRefKey(key);
		const ids = idsByHost.get(host);
		if (ids) ids.push(id);
		else idsByHost.set(host, [id]);
	}
	const updatedCounts = await Promise.all(
		[...idsByHost].map(async ([host, ids]) => {
			const { data, error } = await clientFor(host).POST("/api/v1/notifications/read-all", {
				body: { ids },
			});
			if (error) throw new Error(apiErrorMessage(error, "Could not mark notifications read"));
			return data?.updatedCount ?? 0;
		}),
	);
	return updatedCounts.reduce((count, updated) => count + updated, 0);
}

export function mergeUnreadNotification(queryClient: QueryClient, notification: NotificationDTO): boolean {
	if (notification.status !== "unread") return false;
	const inserted = mergeNotificationIntoCache(queryClient, unreadNotificationsQueryKey, notification);
	rebaseOversizedFirstPage(queryClient, unreadNotificationsQueryKey);
	return inserted;
}

function mergeRecentNotification(queryClient: QueryClient, notification: NotificationDTO): boolean {
	const inserted = mergeNotificationIntoCache(queryClient, recentNotificationsQueryKey, notification);
	rebaseOversizedFirstPage(queryClient, recentNotificationsQueryKey);
	return inserted;
}

/**
 * AO resolved the issue behind a notification. Update the row in unread/all
 * caches; the seen state is a separate axis and is deliberately left untouched.
 */
export function applyResolvedNotification(queryClient: QueryClient, notification: NotificationDTO): void {
	const key = refKey(notification);
	for (const queryKey of [unreadNotificationsQueryKey, recentNotificationsQueryKey] as const) {
		queryClient.setQueryData<NotificationsCache>(queryKey, (current) => {
			if (!current) return current;
			return {
				...current,
				pages: current.pages.map((page) => ({
					...page,
					notifications: page.notifications.map((item) => (refKey(item) === key ? notification : item)),
					unresolvedCount: Math.max(0, page.unresolvedCount - 1),
				})),
			};
		});
	}
}

function mergeNotificationIntoCache(
	queryClient: QueryClient,
	queryKey: NotificationsQueryKey,
	notification: NotificationDTO,
): boolean {
	let inserted = false;
	const key = refKey(notification);
	queryClient.setQueryData<NotificationsCache>(queryKey, (current) => {
		if (!current || current.pages.length === 0) {
			inserted = true;
			return {
				pageParams: [""],
				pages: [
					{
						notifications: [notification],
						unreadCount: notification.status === "unread" ? 1 : 0,
						unresolvedCount: isUnresolved(notification) ? 1 : 0,
					},
				],
			};
		}

		const existing = getCachedNotifications(current).find((item) => refKey(item) === key);
		const unreadDelta = (notification.status === "unread" ? 1 : 0) - (existing?.status === "unread" ? 1 : 0);
		const unresolvedDelta =
			(isUnresolved(notification) ? 1 : 0) - (existing && isUnresolved(existing) ? 1 : 0);
		const pages = current.pages.map((page) => ({
			...page,
			notifications: page.notifications.map((item) => (refKey(item) === key ? notification : item)),
			unreadCount: Math.max(0, page.unreadCount + unreadDelta),
			unresolvedCount: Math.max(0, page.unresolvedCount + unresolvedDelta),
		}));

		if (existing) {
			return { ...current, pages };
		}

		inserted = true;
		pages[0] = {
			...pages[0],
			notifications: sortNotifications([notification, ...pages[0].notifications]),
		};
		return { ...current, pages };
	});
	return inserted;
}

/**
 * Marks notifications read in the React Query caches.
 *
 * Host-qualified keys mark exactly those rows and keep unread pagination cursors intact so
 * later pages stay reachable when a client acknowledges incrementally.
 *
 * `updatedCount` is the mutation's server tally. Prefer it over locally cleared
 * rows: later all-list pages can acknowledge unread ids that were never loaded
 * into the unread cache, which would otherwise leave the bell badge stuck.
 */
export function markAllCachedNotificationsRead(
	queryClient: QueryClient,
	keys: string[],
	updatedCount?: number,
): void {
	const acknowledged = new Set(keys);
	for (const queryKey of [unreadNotificationsQueryKey, recentNotificationsQueryKey] as const) {
		queryClient.setQueryData<NotificationsCache>(queryKey, (current) => {
			if (!current) return current;

			let clearedAcrossPages = 0;
			const pages = current.pages.map((page) => {
				const notifications = page.notifications.map((item) => {
					if (!acknowledged.has(refKey(item)) || item.status === "read") return item;
					clearedAcrossPages++;
					return { ...item, status: "read" as const };
				});
				return { ...page, notifications };
			});
			const delta = Math.max(updatedCount ?? 0, clearedAcrossPages);

			return {
				...current,
				pages: pages.map((page) => ({
					...page,
					unreadCount: Math.max(0, page.unreadCount - delta),
				})),
			};
		});
	}
}

export function getCachedNotifications(cache: NotificationsCache | undefined): NotificationDTO[] {
	if (!cache) return [];
	const byRef = new Map<string, NotificationDTO>();
	for (const page of cache.pages) {
		for (const notification of page.notifications) {
			const key = refKey(notification);
			if (!byRef.has(key)) byRef.set(key, notification);
		}
	}
	return sortNotifications([...byRef.values()]);
}

export function getCachedUnreadCount(cache: NotificationsCache | undefined): number {
	return cache?.pages[0]?.unreadCount ?? 0;
}

export function keepLatestNotificationsPage(
	queryClient: QueryClient,
	queryKey: NotificationsQueryKey = unreadNotificationsQueryKey,
): void {
	queryClient.setQueryData<NotificationsCache>(queryKey, (current) => {
		if (!current || current.pages.length <= 1) return current;
		return {
			pages: [current.pages[0]],
			pageParams: [current.pageParams[0]],
		};
	});
	rebaseOversizedFirstPage(queryClient, queryKey);
}

/**
 * A `needs_input` toast is redundant only when the user can already see the
 * prompt, which takes three things: the agent's terminal for that session is
 * the one on screen, this window is visible, and this window has focus.
 *
 * Each check covers a way "looks visible" lies. Visibility alone is not enough
 * — on Windows and Linux an unfocused or fully covered Electron window still
 * reports `visibilityState === "visible"`. The route alone is not enough
 * either: the session pane renders one terminal at a time, so an open shell or
 * reviewer tab hides the agent while the URL still names that session. The
 * caller resolves that, passing the session only while its agent pane shows.
 *
 * Only `needs_input` is suppressed. PR outcomes (`ready_to_merge`,
 * `pr_merged`, `pr_closed_unmerged`) are not visible in the terminal pane, so
 * they still deserve a toast even for the session in the foreground.
 */
function suppressToastForWatchedSession(
	notification: NotificationDTO,
	visibleAgentSession: Ref | undefined,
): boolean {
	if (notification.type !== "needs_input") return false;
	if (
		!notification.sessionId ||
		!visibleAgentSession ||
		refKey({ host: notification.host, id: notification.sessionId }) !== refKey(visibleAgentSession)
	) {
		return false;
	}
	return document.visibilityState === "visible" && document.hasFocus();
}

export function createNotificationsTransport(
	queryClient: QueryClient,
	/** The session whose agent terminal is currently on screen, if any. */
	getVisibleAgentSession: () => Ref | undefined = () => undefined,
) {
	return {
		connect() {
			type HostStream = {
				base: string;
				source: EventSource;
				retryTimer?: ReturnType<typeof setTimeout>;
			};
			const streams = new Map<HostId, HostStream>();

			const invalidateNotifications = () => {
				void queryClient.invalidateQueries({ queryKey: unreadNotificationsQueryKey });
				void queryClient.invalidateQueries({ queryKey: recentNotificationsQueryKey });
			};

			const closeHostStream = (host: HostId) => {
				const stream = streams.get(host);
				if (!stream) return;
				if (stream.retryTimer) clearTimeout(stream.retryTimer);
				stream.source.close();
				streams.delete(host);
			};

			const connectHostStream = (host: HostId) => {
				if (typeof EventSource === "undefined") return;
				const base = baseUrlFor(host);
				if (base === null) {
					closeHostStream(host);
					return;
				}
				const current = streams.get(host);
				if (current && current.base === base && current.source.readyState !== EVENTSOURCE_CLOSED) return;
				closeHostStream(host);
				try {
					const source = new EventSource(`${base.replace(/\/+$/, "")}/api/v1/notifications/stream`);
					const stream: HostStream = { base, source };
					streams.set(host, stream);
					source.onopen = () => {
						if (streams.get(host) === stream) invalidateNotifications();
					};
					source.onerror = () => {
						if (
							streams.get(host) !== stream ||
							source.readyState !== EVENTSOURCE_CLOSED ||
							stream.retryTimer
						) {
							return;
						}
						stream.retryTimer = setTimeout(() => {
							stream.retryTimer = undefined;
							if (streams.get(host) === stream) connectHostStream(host);
						}, SSE_RETRY_MS);
					};
					source.addEventListener("notification_created", (event) => {
						const notification = parseNotificationEvent(host, event);
						if (!notification) return;
						const inserted = mergeUnreadNotification(queryClient, notification);
						mergeRecentNotification(queryClient, notification);
						if (inserted && !suppressToastForWatchedSession(notification, getVisibleAgentSession())) {
							void aoBridge.notifications.show({
								id: refKey(notification),
								title: notification.title,
								body: notification.body || undefined,
								type: notification.type,
							});
						}
					});
					// AO closed the underlying issue (the session got its input, the
					// PR stopped waiting on a merge). Patch the row live so an open
					// panel reflects that without waiting for a refetch.
					source.addEventListener("notification_resolved", (event) => {
						const notification = parseNotificationEvent(host, event);
						if (!notification) return;
						applyResolvedNotification(queryClient, notification);
					});
				} catch {
					closeHostStream(host);
				}
			};

			const syncSources = () => {
				const wanted = new Set(notificationHosts());
				for (const host of streams.keys()) {
					if (!wanted.has(host)) closeHostStream(host);
				}
				for (const host of wanted) connectHostStream(host);
			};

			const syncAndInvalidate = () => {
				syncSources();
				invalidateNotifications();
			};
			const removeDaemonListener = aoBridge.daemon.onStatus(syncAndInvalidate);
			const removeBaseUrlListener = subscribeApiBaseUrl(syncAndInvalidate);
			const removeHostsListener = subscribeConnectedHosts(syncAndInvalidate);
			syncSources();

			return () => {
				removeDaemonListener();
				removeBaseUrlListener();
				removeHostsListener();
				for (const host of [...streams.keys()]) closeHostStream(host);
			};
		},
	};
}

function parseNotificationEvent(host: HostId, event: Event): NotificationDTO | null {
	const data = (event as MessageEvent<string>).data;
	if (typeof data !== "string" || data === "") return null;
	try {
		return { ...(JSON.parse(data) as NotificationResponse), host };
	} catch {
		return null;
	}
}

function sortNotifications(notifications: NotificationDTO[]): NotificationDTO[] {
	return [...notifications].sort((a, b) => {
		const byTime = Date.parse(b.createdAt) - Date.parse(a.createdAt);
		return byTime || refKey(b).localeCompare(refKey(a));
	});
}

function rebaseOversizedFirstPage(queryClient: QueryClient, queryKey: NotificationsQueryKey): void {
	const cache = queryClient.getQueryData<NotificationsCache>(queryKey);
	const maxSize = NOTIFICATION_PAGE_SIZE * notificationHosts().length;
	if (!cache || cache.pages[0]?.notifications.length <= maxSize) return;
	const query = queryClient.getQueryCache().find({ queryKey, exact: true });
	if (!query?.isActive()) {
		queryClient.setQueryData<NotificationsCache>(queryKey, (current) => {
			if (!current?.pages[0]) return current;
			return {
				...current,
				pages: [
					{
						...current.pages[0],
						notifications: current.pages[0].notifications.slice(0, maxSize),
					},
					...current.pages.slice(1),
				],
			};
		});
	}
	void queryClient.invalidateQueries({ queryKey, exact: true, refetchType: "active" });
}
