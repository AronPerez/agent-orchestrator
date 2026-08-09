import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronRight, Server } from "lucide-react";
import type { components } from "../../api/schema";
import { useRemoteHosts } from "../hooks/useRemoteHosts";
import { activeHost, switchToHost } from "../lib/active-host";
import { aoBridge } from "../lib/bridge";
import { cn } from "../lib/utils";

type Peek = { state: "loading" | "ready" | "unreachable"; projects: string[] };

// A peek at the other machines, not a second app: the rows are plain text, not
// links, because opening one means pointing the whole window at that host — the
// Open action, which reloads. Fetching is lazy so saved hosts you never expand
// cost nothing, and a host that stopped answering says so in place.
export function SidebarRemoteHosts() {
	const { t } = useTranslation();
	const { hosts } = useRemoteHosts();
	const active = activeHost();
	const [peeks, setPeeks] = useState<Record<string, Peek>>({});
	const [openUrls, setOpenUrls] = useState<string[]>([]);

	// The active host's projects are the main tree — listing them twice would
	// only invite the question of which copy is real.
	const remotes = hosts.filter((host) => host.url !== null && host.url !== active?.url);
	if (remotes.length === 0) return null;

	const toggle = async (url: string) => {
		const wasOpen = openUrls.includes(url);
		setOpenUrls((current) => (wasOpen ? current.filter((entry) => entry !== url) : [...current, url]));
		// Re-expanding a host that failed retries it; a host already listed does not.
		if (wasOpen || peeks[url]?.state === "ready") return;
		setPeeks((current) => ({ ...current, [url]: { state: "loading", projects: [] } }));
		const response = await aoBridge.remotes
			.request(url, { method: "GET", path: "/api/v1/projects" })
			.catch(() => ({ status: 0, body: null }));
		const projects = (response.body as components["schemas"]["ListProjectsResponse"] | null)?.projects;
		setPeeks((current) => ({
			...current,
			[url]:
				response.status === 200 && Array.isArray(projects)
					? { state: "ready", projects: projects.map((project) => project.name) }
					: { state: "unreachable", projects: [] },
		}));
	};

	return (
		<div className="sidebar-expanded-chrome flex shrink-0 flex-col pt-3 group-data-[collapsible=icon]:hidden">
			<div className="flex h-8 w-full min-w-0 items-center gap-2 rounded-md px-2.5 text-sm font-medium text-passive [&_svg]:size-icon-md [&_svg]:shrink-0">
				<Server strokeWidth={1.75} aria-hidden="true" />
				<span className="truncate">{t("hosts.remoteSection")}</span>
			</div>
			{remotes.map((host) => {
				const url = host.url as string;
				const open = openUrls.includes(url);
				const peek = peeks[url];
				return (
					<div key={host.id} className="flex min-w-0 flex-col">
						<button
							aria-expanded={open}
							aria-label={host.label}
							className="flex h-8 w-full min-w-0 items-center gap-2 rounded-md px-2.5 text-left text-sm text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground"
							onClick={() => void toggle(url)}
							type="button"
						>
							<ChevronRight
								aria-hidden="true"
								className={cn("size-3.5 shrink-0 transition-transform duration-150", open && "rotate-90")}
								strokeWidth={2}
							/>
							<span className="min-w-0 truncate">{host.label}</span>
						</button>
						{open ? (
							<div className="flex min-w-0 flex-col gap-0.5 pb-1 pl-8 pr-2.5">
								{peek?.state === "unreachable" ? (
									<span className="py-1 text-xs text-warning">{t("hosts.unreachable")}</span>
								) : peek?.state === "ready" && peek.projects.length === 0 ? (
									<span className="py-1 text-xs text-passive">{t("hosts.peekEmpty")}</span>
								) : (
									peek?.projects.map((name) => (
										<span key={name} className="min-w-0 truncate py-0.5 text-sm text-passive">
											{name}
										</span>
									))
								)}
								<button
									className="mt-1 w-fit rounded-md text-xs font-medium text-accent hover:underline"
									onClick={() => void switchToHost(url)}
									type="button"
								>
									{t("hosts.open", { host: host.label })}
								</button>
							</div>
						) : null}
					</div>
				);
			})}
		</div>
	);
}
