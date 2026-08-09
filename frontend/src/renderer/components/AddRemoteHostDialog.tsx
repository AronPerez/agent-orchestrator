import { useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { X } from "lucide-react";
import { remotesBridge, type RemoteHealth } from "../hooks/useRemoteHosts";
import type { MessageKey } from "../i18n";
import { Button } from "./ui/button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "./ui/dialog";

// Mirrors the CLI's hasUserinfo (backend/internal/cli/remote.go:116) so both
// surfaces refuse the same addresses with the same words.
function hasUserinfo(raw: string): boolean {
	let authority = raw;
	const scheme = authority.indexOf("://");
	if (scheme >= 0) authority = authority.slice(scheme + 3);
	const path = authority.search(/[/?#]/);
	if (path >= 0) authority = authority.slice(0, path);
	return authority.includes("@");
}

const healthErrorKeys: Record<Exclude<RemoteHealth, "online">, MessageKey> = {
	unauthorized: "hosts.add.errorUnauthorized",
	offline: "hosts.add.errorOffline",
};

type AddRemoteHostDialogProps = {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	/** Fires only after a successful save, so the caller can select the new host. */
	onAdded: (url: string) => void;
};

export function AddRemoteHostDialog({ open, onOpenChange, onAdded }: AddRemoteHostDialogProps) {
	const { t } = useTranslation();
	const nameId = useId();
	const addressId = useId();
	const passwordId = useId();
	const [label, setLabel] = useState("");
	const [url, setUrl] = useState("");
	const [password, setPassword] = useState("");
	const [error, setError] = useState<string | null>(null);
	const [busy, setBusy] = useState(false);

	useEffect(() => {
		if (open) return;
		setLabel("");
		setUrl("");
		setPassword("");
		setError(null);
		setBusy(false);
	}, [open]);

	const submit = async () => {
		const address = url.trim();
		if (hasUserinfo(address)) {
			setError(t("hosts.add.errorCredentialInUrl"));
			return;
		}
		setBusy(true);
		setError(null);
		try {
			// The main process probes before it saves: a host that never answered is
			// worse than no host, because it looks configured.
			const health = await remotesBridge().add({ label: label.trim(), url: address, password });
			if (health === "online") {
				onAdded(address);
				onOpenChange(false);
				return;
			}
			setError(t(healthErrorKeys[health]));
		} finally {
			setBusy(false);
		}
	};

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent showCloseButton={false} className={settingsDialogContentClass}>
				<DialogClose asChild>
					<button
						type="button"
						disabled={busy}
						className="settings-dialog-close-button settings-close-button"
						aria-label={t("common.close")}
					>
						<X className="size-5" aria-hidden="true" />
					</button>
				</DialogClose>

				<div className={settingsDialogHeaderClass}>
					<DialogTitle className="settings-dialog-title">{t("hosts.add.title")}</DialogTitle>
					<DialogDescription className="text-control leading-4 text-settings-muted">
						{t("hosts.addRemote.hint")}
					</DialogDescription>
				</div>

				<div className={settingsDialogBodyClass}>
					<div className="flex flex-col gap-1.5">
						<label className="settings-field-label" htmlFor={nameId}>
							{t("hosts.add.name")}
						</label>
						<input
							id={nameId}
							className="settings-field-control h-(--size-settings-action-height)"
							value={label}
							onChange={(event) => setLabel(event.target.value)}
						/>
					</div>

					<div className="flex flex-col gap-1.5">
						<label className="settings-field-label" htmlFor={addressId}>
							{t("hosts.add.address")}
						</label>
						<input
							id={addressId}
							className="settings-field-control h-(--size-settings-action-height)"
							value={url}
							onChange={(event) => setUrl(event.target.value)}
						/>
					</div>

					<div className="flex flex-col gap-1.5">
						<label className="settings-field-label" htmlFor={passwordId}>
							{t("hosts.add.password")}
						</label>
						<input
							id={passwordId}
							type="password"
							className="settings-field-control h-(--size-settings-action-height)"
							value={password}
							onChange={(event) => setPassword(event.target.value)}
						/>
						<p className="text-caption leading-4 text-settings-muted">{t("hosts.add.passwordHint")}</p>
					</div>

					{error ? (
						<p role="alert" className="text-caption leading-4 text-error">
							{error}
						</p>
					) : null}
				</div>

				<div className={settingsDialogFooterClass}>
					<DialogClose asChild>
						<Button type="button" variant="footer" disabled={busy}>
							{t("confirm.cancel")}
						</Button>
					</DialogClose>
					<Button type="button" variant="footer-primary" disabled={busy} onClick={() => void submit()}>
						{t("hosts.add.submit")}
					</Button>
				</div>
			</DialogContent>
		</Dialog>
	);
}
