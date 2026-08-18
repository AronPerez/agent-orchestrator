import { useQuery } from "@tanstack/react-query";
import { clientFor } from "../lib/host-clients";
import type { HostId } from "../lib/hosts";

/**
 * Resolves whether a session's browser should run on the project's shared,
 * persistent profile, and under which key.
 *
 * Kept out of useBrowserView deliberately: that hook is used outside a
 * QueryClientProvider, and making it depend on react-query would force a
 * provider on every consumer of a native browser view.
 *
 * The daemon resolves the same answer from the same project config for every
 * agent command, so the two paths cannot disagree. This exists only so that a
 * human opening the Browser panel before any agent command lands does not
 * create the session on the wrong partition — a WebContentsView's partition is
 * fixed at construction and cannot be changed afterwards.
 *
 * Every unresolved, errored, or degraded answer yields `{pending: false, key: ""}`
 * — no opt-in found, so the isolated default. Failing closed costs a re-login;
 * failing open would share a cookie jar nobody asked to share.
 */
export function usePersistentBrowserProfile(
	host: HostId,
	projectId: string | undefined,
): { pending: true } | { pending: false; key: string } {
	const enabled = Boolean(window.ao && projectId);
	const query = useQuery({
		queryKey: ["browser-persistent-profile", host, projectId],
		enabled,
		staleTime: 60_000,
		queryFn: async () => {
			const { data, error } = await clientFor(host).GET("/api/v1/projects/{id}", {
				params: { path: { id: projectId as string } },
			});
			if (error || !data?.project || !("config" in data.project)) return "";
			return data.project.config?.browserPersistentProfile ? (projectId as string) : "";
		},
	});
	if (!enabled) return { pending: false, key: "" };
	if (query.isError) return { pending: false, key: "" };
	if (!query.isFetched) return { pending: true };
	return { pending: false, key: query.data ?? "" };
}
