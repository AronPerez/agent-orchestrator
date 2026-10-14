// DevTools follows the focused Browser panel when there is one, and otherwise
// opens on the app shell. Without the shell fallback the toggle is a silent
// no-op whenever no panel has been focused (or no browser host exists yet), and
// on macOS — where the app installs no menu of its own and Electron's default
// View item throws when no web contents holds focus — that leaves the shell's
// console unreachable for the whole session.
export function toggleDevToolsForFocusedSurface(
	toggleFocusedPanel: (() => Promise<unknown>) | null | undefined,
	toggleShell: () => void,
): void {
	if (!toggleFocusedPanel) {
		toggleShell();
		return;
	}
	// Promise.resolve().then() so a synchronous throw from the host falls back
	// too, rather than escaping as an unhandled main-process exception.
	void Promise.resolve()
		.then(toggleFocusedPanel)
		.then((state) => {
			if (!state) toggleShell();
		})
		.catch(() => toggleShell());
}
