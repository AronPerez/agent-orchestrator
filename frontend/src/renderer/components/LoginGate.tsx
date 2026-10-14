import * as Dialog from "@radix-ui/react-dialog";
import { Loader2 } from "lucide-react";
import { useState, useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import { isUnauthorized, login, LoginFailedError, subscribeUnauthorized } from "../lib/auth-gate";
import { hasBrowserDaemon } from "../lib/preview-mode";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { settingsDialogBodyClass, settingsDialogContentClass, settingsDialogFooterClass } from "./ui/dialog";

/**
 * Connection-password prompt for the daemon-served web build. It appears when
 * the daemon answers 401 — only the LAN listener does — and there is no way to
 * dismiss it: nothing in the app works until the cookie exists.
 *
 * Inert in the desktop app, where the loopback daemon never asks for a password.
 */
export function LoginGate() {
	const { t } = useTranslation();
	const needsLogin = useSyncExternalStore(subscribeUnauthorized, isUnauthorized, () => false);
	const [password, setPassword] = useState("");
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | undefined>();

	if (!hasBrowserDaemon || !needsLogin) return null;

	const submit = async (event: React.FormEvent) => {
		event.preventDefault();
		if (busy || password.trim() === "") return;
		setBusy(true);
		setError(undefined);
		try {
			await login(password.trim());
			// Reload rather than retry: the queries, the SSE stream and the mux
			// socket were all created before the cookie existed.
			window.location.reload();
		} catch (err) {
			const status = err instanceof LoginFailedError ? err.status : 0;
			setError(
				status === 429
					? t("login.lockedOut")
					: status === 401
						? t("login.wrongPassword")
						: t("login.failed", { status }),
			);
			setBusy(false);
		}
	};

	return (
		<Dialog.Root open>
			<Dialog.Portal>
				<Dialog.Overlay className="dialog-overlay data-[state=open]:animate-overlay-in" />
				<Dialog.Content
					className={`${settingsDialogContentClass} fixed left-1/2 top-1/2 w-dialog-md -translate-x-1/2 -translate-y-1/2 data-[state=open]:animate-modal-in`}
					onEscapeKeyDown={(event) => event.preventDefault()}
					onInteractOutside={(event) => event.preventDefault()}
				>
					<form onSubmit={submit}>
						<div className={settingsDialogBodyClass}>
							<Dialog.Title className="settings-dialog-title">{t("login.title")}</Dialog.Title>
							<Dialog.Description className="text-control text-settings-muted">
								{t("login.body")}
							</Dialog.Description>
							<Input
								type="password"
								autoFocus
								autoComplete="current-password"
								value={password}
								disabled={busy}
								placeholder={t("login.placeholder")}
								aria-label={t("login.title")}
								onChange={(event) => setPassword(event.target.value)}
							/>
							{error ? <p className="text-xs text-destructive">{error}</p> : null}
						</div>
						<div className={settingsDialogFooterClass}>
							<Button type="submit" variant="footer-primary" disabled={busy || password.trim() === ""}>
								{busy ? <Loader2 className="size-icon-base animate-spin" aria-hidden="true" /> : null}
								{t("login.submit")}
							</Button>
						</div>
					</form>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}
