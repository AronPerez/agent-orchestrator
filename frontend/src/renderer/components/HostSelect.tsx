import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Pencil, Trash2 } from "lucide-react";
import { LOCAL_HOST_ID, probeFailed, type Host, type HostStatus, type RemoteHostView } from "../hooks/useRemoteHosts";
import type { MessageKey } from "../i18n";
import { Button } from "./ui/button";
import { Select, SelectContent, SelectItem, SelectSeparator, SelectTrigger, SelectValue } from "./ui/select";

// Sentinel row value: "Add remote host" lives in the same list as the hosts so
// it is reachable by keyboard, but it opens a dialog instead of selecting.
const ADD_HOST_VALUE = "__add-remote-host__";

const statusKeys: Record<Exclude<HostStatus, "local">, MessageKey> = {
	online: "hosts.status.online",
	checking: "hosts.status.checking",
	offline: "hosts.status.offline",
	unauthorized: "hosts.status.unauthorized",
	"not-a-daemon": "hosts.status.notADaemon",
};

// A host you cannot reach can only fail one step later, so it is not selectable.
function unreachable(host: Host): boolean {
	return probeFailed(host.status);
}

type HostSelectProps = {
	hosts: Host[];
	value: string;
	onChange: (hostId: string) => void;
	onAddHost: () => void;
	/** Re-probe one host — a host has no session to open, reachability is the whole state. */
	onReconnect?: (url: string) => void;
	/** Fix a saved host in place: renamed, re-pointed, or given a rotated password. */
	onEditHost?: (host: RemoteHostView) => void;
	onRemoveHost?: (host: RemoteHostView) => void;
};

export function HostSelect({
	hosts,
	value,
	onChange,
	onAddHost,
	onReconnect,
	onEditHost,
	onRemoveHost,
}: HostSelectProps) {
	const { t } = useTranslation();
	const selected = hosts.find((host) => host.id === value);
	// Open is controlled only so Edit and Remove can close it: each opens a modal
	// dialog, and Radix leaves the list up — and aria-hides everything under it —
	// when a sibling of a row is what got clicked. Connect deliberately does not
	// close it, because its result is the status shown in the list.
	const [open, setOpen] = useState(false);

	return (
		<Select
			open={open}
			onOpenChange={setOpen}
			value={value}
			onValueChange={(next) => (next === ADD_HOST_VALUE ? onAddHost() : onChange(next))}
		>
			<SelectTrigger className="w-full" aria-label={t("hosts.label")}>
				<SelectValue>
					<span className="min-w-0 truncate">{selected?.label ?? ""}</span>
				</SelectValue>
			</SelectTrigger>
			<SelectContent position="popper" className="min-w-(--radix-select-trigger-width)">
				{hosts.map((host) => {
					if (host.id === LOCAL_HOST_ID) {
						return (
							<SelectItem key={host.id} value={host.id}>
								{host.label}
							</SelectItem>
						);
					}
					const url = host.url;
					// The inline action is a sibling of the row, not a child: a disabled
					// SelectItem is pointer-events-none, which would swallow its clicks.
					return (
						<div key={host.id} className="flex items-center gap-1">
							<SelectItem value={host.id} disabled={unreachable(host)} className="min-w-0 flex-1">
								<span className="flex min-w-0 flex-col items-start">
									<span className="min-w-0 truncate text-foreground">{host.label}</span>
									{host.status === "local" ? null : (
										<span className="text-xs text-muted-foreground">{t(statusKeys[host.status])}</span>
									)}
								</span>
							</SelectItem>
							{unreachable(host) && url && onReconnect ? (
								<Button type="button" variant="ghost" size="sm" className="shrink-0" onClick={() => onReconnect(url)}>
									{t("hosts.connect")}
								</Button>
							) : null}
							{/* Icons, not words: a row already carries a name, a status and
							    sometimes Connect, and three text buttons would push the name
							    it identifies out of view. Each carries its host's name in its
							    label so the action is never just "Edit" to a screen reader. */}
							{url && onEditHost ? (
								<Button
									type="button"
									variant="ghost"
									size="icon-sm"
									className="shrink-0"
									aria-label={t("hosts.edit", { host: host.label })}
									onClick={() => {
										setOpen(false);
										onEditHost({ label: host.label, url });
									}}
								>
									<Pencil aria-hidden="true" />
								</Button>
							) : null}
							{url && onRemoveHost ? (
								<Button
									type="button"
									variant="ghost"
									size="icon-sm"
									className="shrink-0"
									aria-label={t("hosts.remove", { host: host.label })}
									onClick={() => {
										setOpen(false);
										onRemoveHost({ label: host.label, url });
									}}
								>
									<Trash2 aria-hidden="true" />
								</Button>
							) : null}
						</div>
					);
				})}
				<SelectSeparator />
				<SelectItem value={ADD_HOST_VALUE}>
					<span className="flex min-w-0 flex-col items-start">
						<span className="text-foreground">{t("hosts.addRemote")}</span>
						<span className="text-xs text-muted-foreground">{t("hosts.addRemote.hint")}</span>
					</span>
				</SelectItem>
			</SelectContent>
		</Select>
	);
}
