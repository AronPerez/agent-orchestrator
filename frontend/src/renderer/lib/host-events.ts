import { baseUrlFor } from "./host-clients";
import { LOCAL_HOST, type HostId } from "./hosts";
import { setEventsConnectionState } from "./events-connection";

const SSE_RETRY_MS = 5_000;
const EVENTSOURCE_CLOSED = 2;

const CDC_EVENT_TYPES = [
	"session_created",
	"session_updated",
	"pr_created",
	"pr_updated",
	"pr_check_recorded",
	"pr_session_changed",
	"pr_review_thread_added",
	"pr_review_thread_resolved",
] as const;

type HostEventHandler = (host: HostId, event?: Event) => void;
type HostStream = {
	source: EventSource;
	base: string;
	state: "connected" | "disconnected";
	onEvent: HostEventHandler;
	retryTimer?: ReturnType<typeof setTimeout>;
};

const streams = new Map<HostId, HostStream>();

function setConnectionState(host: HostId, stream: HostStream, state: HostStream["state"]): void {
	stream.state = state;
	if (host === LOCAL_HOST) setEventsConnectionState(state);
}

function closeHostStream(host: HostId): void {
	const stream = streams.get(host);
	if (!stream) return;
	if (stream.retryTimer) clearTimeout(stream.retryTimer);
	stream.source.close();
	streams.delete(host);
	if (host === LOCAL_HOST) setEventsConnectionState("disconnected");
}

function connectHostStream(host: HostId, onEvent: HostEventHandler): void {
	// EventSource is unavailable in jsdom and some preview surfaces.
	if (typeof EventSource === "undefined") return;
	const base = baseUrlFor(host);
	if (base === null) {
		closeHostStream(host);
		if (host === LOCAL_HOST) setEventsConnectionState("disconnected");
		return;
	}

	const current = streams.get(host);
	if (current && current.base === base && current.source.readyState !== EVENTSOURCE_CLOSED) {
		current.onEvent = onEvent;
		return;
	}
	closeHostStream(host);

	try {
		const source = new EventSource(`${base.replace(/\/+$/, "")}/api/v1/events`);
		const stream: HostStream = { source, base, state: "disconnected", onEvent };
		streams.set(host, stream);

		const reportEvent = (event?: Event) => {
			if (event === undefined) stream.onEvent(host);
			else stream.onEvent(host, event);
		};
		source.onopen = () => {
			if (streams.get(host) !== stream) return;
			setConnectionState(host, stream, "connected");
			// Events emitted during the gap were lost; refetch once on (re)open.
			stream.onEvent(host);
		};
		source.onerror = () => {
			if (streams.get(host) !== stream) return;
			setConnectionState(host, stream, "disconnected");
			// While CONNECTING the browser retries and resumes via Last-Event-ID.
			if (source.readyState !== EVENTSOURCE_CLOSED || stream.retryTimer) return;
			stream.retryTimer = setTimeout(() => {
				stream.retryTimer = undefined;
				if (streams.get(host) === stream) connectHostStream(host, stream.onEvent);
			}, SSE_RETRY_MS);
		};
		source.onmessage = reportEvent;
		for (const type of CDC_EVENT_TYPES) source.addEventListener(type, reportEvent);
	} catch {
		if (host === LOCAL_HOST) setEventsConnectionState("disconnected");
	}
}

export function syncHostStreams(hosts: HostId[], onEvent: HostEventHandler): void {
	const wanted = new Set(hosts);
	for (const host of streams.keys()) {
		if (!wanted.has(host)) closeHostStream(host);
	}
	for (const host of wanted) connectHostStream(host, onEvent);
}

export function hostConnectionState(host: HostId): "connected" | "disconnected" {
	return streams.get(host)?.state ?? "disconnected";
}

export function closeAllHostStreams(): void {
	for (const host of [...streams.keys()]) closeHostStream(host);
}
