import { useEffect, useLayoutEffect, useRef } from "react";

/**
 * One DOM home per open decision, made once and kept for as long as the
 * decision is open. Each decision's view is rendered into its home through a
 * portal, and the home is moved between slots (beside the board, or filling
 * the page) or parked off the page. Because the portal's container never
 * changes, the view is one live instance wherever it shows: what a person has
 * typed in it survives Keep open, a change of tab, and a trip back to the board.
 */
export function useDecisionPanelHosts(ids: readonly string[]): ReadonlyMap<string, HTMLElement> {
	const hosts = useRef(new Map<string, HTMLElement>());
	for (const id of ids) {
		if (hosts.current.has(id)) continue;
		const host = document.createElement("div");
		host.className = "h-full min-w-0";
		host.dataset.decisionPanelHost = id;
		hosts.current.set(id, host);
	}
	useEffect(() => {
		for (const [id, host] of hosts.current) {
			if (ids.includes(id)) continue;
			host.remove();
			hosts.current.delete(id);
		}
	}, [ids]);
	return hosts.current;
}

/** A place on the page that shows one decision's home while it holds it. */
export function DecisionPanelSlot({ host, className }: { host: HTMLElement | undefined; className?: string }) {
	const slot = useRef<HTMLDivElement>(null);
	// Before paint, so the decision is never drawn in its old place first.
	useLayoutEffect(() => {
		const element = slot.current;
		if (!element || !host) return;
		element.appendChild(host);
		return () => {
			if (host.parentNode === element) element.removeChild(host);
		};
	}, [host]);
	return <div ref={slot} className={className} />;
}
