import { useEffect, useState } from "react";

const MINUTE_MS = 60_000;

/** A low-frequency render clock for minute-resolution labels. */
export function useMinuteClock(active = true): number {
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (!active) return;
		const update = () => setNow(Date.now());
		update();
		const timer = window.setInterval(update, MINUTE_MS);
		return () => window.clearInterval(timer);
	}, [active]);
	return now;
}
