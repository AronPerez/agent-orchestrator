import { AoError } from "../../../../packages/shared/telemetry/ao-error";
import { useQueries } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { apiErrorException } from "../lib/api-error-exception";
import { clientForSessionHost } from "../lib/host-clients";
import { sessionUiKey } from "../lib/hosts";
import { repositoryOwnerAvatar } from "../lib/repository-avatar";
import { markApiError } from "../lib/reported-errors";
import { useUiStore } from "../stores/ui-store";
import { CLOUD_PROJECT_KIND, STANDALONE_PROJECT_KIND, type WorkspaceSummary } from "../types/workspace";
import { useConnectedHosts } from "./useHostConnection";
import { projectQueryKey } from "./useProjectSettingsQuery";

// Avatar URL -> the image as a data URL, so a launch paints known avatars with
// no network request.
type AvatarCache = Record<string, string>;

const cacheKey = "ao.projectAvatarCache";
// Project id -> its last avatar URL, so the first paint does not wait for
// project details.
const urlsKey = "ao.projectAvatarUrls";

function readStrings(key: string): Record<string, string> {
	try {
		const parsed: unknown = JSON.parse(window.localStorage.getItem(key) ?? "{}");
		if (!parsed || typeof parsed !== "object") return {};
		return Object.fromEntries(Object.entries(parsed).filter((entry): entry is [string, string] => typeof entry[1] === "string"));
	} catch {
		return {};
	}
}

function writeStrings(key: string, value: Record<string, string>) {
	try {
		window.localStorage.setItem(key, JSON.stringify(value));
	} catch {
		// A full localStorage only costs the instant first paint.
	}
}

function readCache(): AvatarCache {
	return Object.fromEntries(Object.entries(readStrings(cacheKey)).filter(([, src]) => src.startsWith("data:image/")));
}

// Loading as an image stays under img-src and rejects sign-in HTML. CORS lets
// us cache pixels; a normal image can still display public SCMs without CORS.
function loadAvatarImage(url: string, signal?: AbortSignal, asDataUrl = true): Promise<string | null> {
	return new Promise((resolve) => {
		if (signal?.aborted) return resolve(null);
		const image = new Image();
		const finish = (src: string | null) => {
			image.onload = null;
			image.onerror = null;
			signal?.removeEventListener("abort", abort);
			resolve(src);
		};
		const abort = () => { finish(null); image.src = ""; };
		signal?.addEventListener("abort", abort, { once: true });
		if (asDataUrl) image.crossOrigin = "anonymous";
		image.onload = () => {
			if (!asDataUrl) return finish(url);
			try {
				const canvas = document.createElement("canvas");
				canvas.width = 64;
				canvas.height = 64;
				const context = canvas.getContext("2d");
				if (!context) return finish(null);
				context.drawImage(image, 0, 0, 64, 64);
				finish(canvas.toDataURL("image/png"));
			} catch {
				finish(null);
			}
		};
		image.onerror = () => finish(null);
		image.src = url;
	});
}

// Refreshed once per launch, so an owner's new avatar replaces the cached one.
const refreshedUrls = new Set<string>();

// Image sources keyed by sessionUiKey(projectId, hostId). Local cached images
// come from localStorage on first render, so the
// sidebar paints them instantly; project details then confirm the owner and
// refresh the cache in the background. A failed refresh keeps the cached
// image. Empty when the setting is off.
export function useProjectAvatars(workspaces: readonly WorkspaceSummary[]): Map<string, string> {
	const enabled = useUiStore((state) => state.projectAvatars);
	const connected = useConnectedHosts();
	const [cache, setCache] = useState(readCache);
	const [knownUrls] = useState(() => readStrings(urlsKey));
	const projects = useMemo(
		() => workspaces.filter((workspace) => workspace.kind !== CLOUD_PROJECT_KIND && workspace.kind !== STANDALONE_PROJECT_KIND),
		[workspaces],
	);
	const details = useQueries({
		queries: projects.map((project) => ({
			queryKey: project.hostId ? ["project-avatar", project.hostId, project.id, "metadata"] : projectQueryKey(project.id),
			enabled: enabled && (!project.hostId || connected.includes(project.hostId)),
			staleTime: 30_000,
			queryFn: async ({ signal }) => {
				const { data, error } = await clientForSessionHost(project.hostId).GET("/api/v1/projects/{id}", {
					params: { path: { id: project.id } },
					signal,
				});
				if (error) throw apiErrorException(error);
				if (data?.status !== "ok") return undefined;
				return data.project;
			},
		})),
	});
	const urls = useMemo(() => {
		const map = new Map<string, string>();
		projects.forEach((project, index) => {
			if (project.hostId && !connected.includes(project.hostId)) return;
			const key = sessionUiKey(project.id, project.hostId);
			const query = details[index];
			if (!query?.isSuccess) {
				const known = !project.hostId && knownUrls[project.id];
				if (known) map.set(key, known);
				return;
			}
			const detail = query.data;
			const ownRepo = detail && "repo" in detail && typeof detail.repo === "string" && detail.repo ? detail.repo : undefined;
			const childRepo = detail && "workspaceRepos" in detail ? detail.workspaceRepos?.[0]?.repo : undefined;
			const repo = ownRepo ?? (typeof childRepo === "string" && childRepo ? childRepo : project.workspaceRepos?.[0]?.repo);
			// After a GitHub rename or transfer the remote's owner is stale; the
			// avatar URL then changes, so the old cached image is not reused.
			const canonical = ownRepo && detail && "canonicalRepo" in detail
				? { repo: detail.canonicalRepo, ownerAvatarUrl: detail.canonicalOwnerAvatarUrl }
				: undefined;
			const avatar = repo ? repositoryOwnerAvatar(repo, canonical) : null;
			if (avatar) map.set(key, avatar.url);
		});
		return map;
	}, [connected, details, knownUrls, projects]);
	const remoteImages = projects.flatMap((project) => {
		const key = sessionUiKey(project.id, project.hostId);
		const url = urls.get(key);
		return project.hostId && url ? [{ key, hostId: project.hostId, id: project.id, url }] : [];
	});
	// Only metadata needs host authentication; remote image sources stay memory-only.
	const remoteSources = useQueries({
		queries: remoteImages.map((image) => ({
			queryKey: ["project-avatar", image.hostId, image.id, image.url],
			enabled,
			staleTime: Infinity,
			refetchOnReconnect: true,
			queryFn: async ({ signal }) => {
				const src = (await loadAvatarImage(image.url, signal)) ?? (await loadAvatarImage(image.url, signal, false));
				if (src) return src;
				const error = new AoError("PROJECT_AVATAR_UNAVAILABLE", "Project avatar image unavailable");
				markApiError(error);
				throw error;
			},
		})),
	});
	const localUrls = new Map(projects.filter((project) => !project.hostId).flatMap((project) => {
		const url = urls.get(project.id);
		return url ? [[project.id, url] as const] : [];
	}));
	const urlKey = [...new Set(localUrls.values())].sort().join("\n");
	const settled = projects.every((project, index) => project.hostId || !details[index]?.isPending);
	const urlsJson = JSON.stringify(Object.fromEntries(localUrls));
	useEffect(() => {
		if (!enabled || !settled) return;
		try {
			window.localStorage.setItem(urlsKey, urlsJson);
		} catch {
			// A full localStorage only costs the instant first paint.
		}
	}, [enabled, settled, urlsJson]);

	useEffect(() => {
		if (!enabled) return;
		const current = new Set(urlKey ? urlKey.split("\n") : []);
		for (const url of current) {
			if (refreshedUrls.has(url)) continue;
			refreshedUrls.add(url);
			void loadAvatarImage(url).then((src) => {
				if (!src) return;
				setCache((previous) => {
					if (previous[url] === src) return previous;
					// Keep only avatars still in use, so removed projects drop out.
					const next: AvatarCache = { [url]: src };
					for (const [key, value] of Object.entries(previous)) if (current.has(key) && key !== url) next[key] = value;
					writeStrings(cacheKey, next);
					return next;
				});
			});
		}
	}, [enabled, urlKey]);

	return useMemo(() => {
		const map = new Map<string, string>();
		if (!enabled) return map;
		for (const [projectId, url] of localUrls) map.set(projectId, cache[url] ?? url);
		remoteImages.forEach((image, index) => {
			const src = remoteSources[index]?.data;
			if (src) map.set(image.key, src);
		});
		return map;
	}, [cache, enabled, localUrls, remoteImages, remoteSources]);
}
