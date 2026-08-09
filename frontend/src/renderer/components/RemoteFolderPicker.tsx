import { CornerLeftUp, Folder } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { remotesBridge } from "../hooks/useRemoteHosts";
import { daemonErrorMessage } from "../lib/daemon-error";
import { Button } from "./ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "./ui/dialog";

type Listing = components["schemas"]["ListDirsResponse"];

/**
 * Browses a remote daemon's directories over GET /api/v1/fs/dirs so a project
 * path can be picked instead of typed blind. Every path decision belongs to the
 * daemon: this dialog never joins, normalises, or judges a path itself, because
 * it may be looking at a different OS than the one it runs on.
 */
export function RemoteFolderPicker({
	hostLabel,
	hostUrl,
	onOpenChange,
	onSelect,
	open,
}: {
	hostLabel: string;
	hostUrl: string;
	onOpenChange: (open: boolean) => void;
	onSelect: (path: string) => void;
	open: boolean;
}) {
	const { t } = useTranslation();
	// null means "wherever the daemon calls home" — the endpoint's own default.
	const [path, setPath] = useState<string | null>(null);
	const [listing, setListing] = useState<Listing | null>(null);
	const [error, setError] = useState<string | null>(null);

	useEffect(() => {
		if (!open) {
			setPath(null);
			setListing(null);
			setError(null);
			return;
		}
		let cancelled = false;
		void (async () => {
			const query = path === null ? "" : `?path=${encodeURIComponent(path)}`;
			try {
				const response = await remotesBridge().request(hostUrl, {
					method: "GET",
					path: `/api/v1/fs/dirs${query}`,
				});
				if (cancelled) return;
				if (response.status === 200) {
					setListing(response.body as Listing);
					setError(null);
					return;
				}
				// Keep the last good listing on screen so a refused directory is a
				// dead end, not a dead dialog.
				setError(daemonErrorMessage(response.body) ?? t("fsBrowse.failed"));
			} catch (err) {
				if (!cancelled) setError(err instanceof Error ? err.message : t("fsBrowse.failed"));
			}
		})();
		return () => {
			cancelled = true;
		};
	}, [hostUrl, open, path, t]);

	const canGoUp = listing !== null && listing.parent !== listing.path;

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent className={settingsDialogContentClass}>
				<div className={settingsDialogHeaderClass}>
					<DialogTitle className="settings-dialog-title">{t("fsBrowse.title", { host: hostLabel })}</DialogTitle>
					<DialogDescription className="truncate font-mono text-control leading-4 text-settings-muted">
						{listing?.path ?? t("fsBrowse.hint")}
					</DialogDescription>
				</div>

				<div className={settingsDialogBodyClass}>
					{error ? (
						<p role="alert" className="text-caption leading-4 text-error">
							{error}
						</p>
					) : null}

					<ul className="flex flex-col gap-0.5">
						{canGoUp && listing ? (
							<li>
								<FolderRow icon={CornerLeftUp} label={t("fsBrowse.up")} onClick={() => setPath(listing.parent)} />
							</li>
						) : null}
						{listing?.entries.map((entry) => (
							<li key={entry.path}>
								<FolderRow
									icon={Folder}
									label={entry.name}
									badge={entry.gitRepo ? t("fsBrowse.gitRepo") : undefined}
									onClick={() => setPath(entry.path)}
								/>
							</li>
						))}
					</ul>

					{listing !== null && listing.entries.length === 0 ? (
						<p className="text-caption leading-4 text-settings-muted">{t("fsBrowse.empty")}</p>
					) : null}
					{listing?.truncated ? (
						<p className="text-caption leading-4 text-settings-muted">
							{t("fsBrowse.truncated", { limit: listing.entries.length })}
						</p>
					) : null}
				</div>

				<div className={settingsDialogFooterClass}>
					<Button type="button" variant="footer" onClick={() => onOpenChange(false)}>
						{t("confirm.cancel")}
					</Button>
					<Button
						type="button"
						variant="footer-primary"
						disabled={listing === null}
						onClick={() => {
							if (!listing) return;
							onSelect(listing.path);
							onOpenChange(false);
						}}
					>
						{t("fsBrowse.chooseThis")}
					</Button>
				</div>
			</DialogContent>
		</Dialog>
	);
}

function FolderRow({
	badge,
	icon: Icon,
	label,
	onClick,
}: {
	badge?: string;
	icon: typeof Folder;
	label: string;
	onClick: () => void;
}) {
	return (
		<button
			type="button"
			className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-control text-settings-label transition-colors hover:bg-accent/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
			onClick={onClick}
		>
			<Icon className="size-4 shrink-0 text-settings-muted" aria-hidden="true" />
			<span className="min-w-0 flex-1 truncate">{label}</span>
			{badge ? (
				<span className="shrink-0 rounded-full border border-border px-2 py-0.5 text-caption text-settings-muted">
					{badge}
				</span>
			) : null}
		</button>
	);
}
