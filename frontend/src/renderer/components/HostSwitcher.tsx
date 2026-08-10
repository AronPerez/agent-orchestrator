import { useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import { LOCAL_HOST_ID, probeFailed, useRemoteHosts, type Host, type HostStatus } from "../hooks/useRemoteHosts";
import { activeHost, switchToHost } from "../lib/active-host";
import { isUnauthorized, subscribeUnauthorized } from "../lib/auth-gate";
import type { MessageKey } from "../i18n";
import { cn } from "../lib/utils";
import { Button } from "./ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";

const statusKeys: Record<Exclude<HostStatus, "local">, MessageKey> = {
	online: "hosts.status.online",
	checking: "hosts.status.checking",
	offline: "hosts.status.offline",
	unauthorized: "hosts.status.unauthorized",
	"not-a-daemon": "hosts.status.notADaemon",
};

// Which machine the whole app is showing. Local is deliberately quiet — it is
// the default and needs no announcement; a remote host is an accent pill that
// says its name, because every other cue in the window (project names, session
// titles, the terminal) looks identical no matter whose machine produced it.
export function HostSwitcher() {
	const { t } = useTranslation();
	const { hosts } = useRemoteHosts();
	const active = activeHost();
	// A 401 only means "this host rejected us" while we are viewing a remote one;
	// on local it is the web build's login prompt talking and not ours to answer.
	const rejected = useSyncExternalStore(subscribeUnauthorized, isUnauthorized) && active !== null;
	const label = active?.label ?? t("hosts.local");

	return (
		<div className="flex min-w-0 items-center gap-1.5">
			<Select
				value={active?.url ?? LOCAL_HOST_ID}
				onValueChange={(next) => void switchToHost(next === LOCAL_HOST_ID ? null : next)}
			>
				<SelectTrigger
					size="sm"
					aria-label={t("hosts.switcher", { host: label })}
					className={cn(
						"max-w-48",
						active && !rejected && "border-accent/40 bg-accent-weak text-accent",
						rejected && "border-warning/40 bg-warning/10 text-warning",
					)}
				>
					<SelectValue>
						<span className="min-w-0 truncate">
							{!active
								? label
								: rejected
									? t("hosts.passwordChanged", { host: label })
									: t("hosts.viewing", { host: label })}
						</span>
					</SelectValue>
				</SelectTrigger>
				<SelectContent position="popper" className="min-w-(--radix-select-trigger-width)">
					{hosts.map((host) => (
						<SelectItem key={host.id} value={host.id} disabled={unselectable(host, active?.url)}>
							<span className="flex min-w-0 flex-col items-start">
								<span className="min-w-0 truncate text-foreground">{host.label}</span>
								{host.status === "local" ? null : (
									<span className="text-xs text-muted-foreground">{t(statusKeys[host.status])}</span>
								)}
							</span>
						</SelectItem>
					))}
				</SelectContent>
			</Select>
			{rejected ? (
				<Button type="button" variant="ghost" size="sm" onClick={() => void switchToHost(null)}>
					{t("hosts.backToLocal")}
				</Button>
			) : null}
		</div>
	);
}

// A host you cannot reach can only fail one step later — except the one you are
// already viewing, whose row must stay in the list as the current selection.
function unselectable(host: Host, activeUrl: string | undefined): boolean {
	if (host.url === activeUrl) return false;
	return probeFailed(host.status);
}
