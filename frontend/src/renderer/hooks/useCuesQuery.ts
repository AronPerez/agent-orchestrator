import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCloudCp } from "./useCloudCp";
import {
	createCue,
	deleteCue,
	fetchProjectCues,
	invokeCue,
	projectCuesQueryKey,
	updateCue,
	type CueHost,
	type CueInput,
} from "../lib/cues";

/** Where a project's cues live: the control plane for a Cloud project (its org), else the local daemon. */
export function useCueHost(cloudOrgId?: string): CueHost | undefined {
	const { client } = useCloudCp(cloudOrgId !== undefined);
	return cloudOrgId ? { client, orgId: cloudOrgId } : undefined;
}

export function useProjectCuesQuery(projectId: string, enabled = true, hostId?: string, cloudOrgId?: string) {
	const cloud = useCueHost(cloudOrgId);
	return useQuery({
		queryKey: projectCuesQueryKey(projectId, hostId, cloudOrgId),
		queryFn: () => cloud ? fetchProjectCues(projectId, cloud) : hostId ? fetchProjectCues(projectId, undefined, hostId) : fetchProjectCues(projectId),
		enabled: enabled && Boolean(projectId),
		refetchOnMount: "always",
		retry: false,
	});
}

function useInvalidateCues(projectId: string, hostId?: string, cloudOrgId?: string) {
	const queryClient = useQueryClient();
	return () => {
		void queryClient.invalidateQueries({ queryKey: projectCuesQueryKey(projectId, hostId, cloudOrgId) });
	};
}

export function useCreateCueMutation(projectId: string, hostId?: string, cloudOrgId?: string) {
	const invalidate = useInvalidateCues(projectId, hostId, cloudOrgId);
	const cloud = useCueHost(cloudOrgId);
	return useMutation({
		mutationFn: (input: CueInput) => cloud ? createCue(projectId, input, cloud) : hostId ? createCue(projectId, input, undefined, hostId) : createCue(projectId, input),
		onSuccess: invalidate,
	});
}

export function useUpdateCueMutation(projectId: string, hostId?: string, cloudOrgId?: string) {
	const invalidate = useInvalidateCues(projectId, hostId, cloudOrgId);
	const cloud = useCueHost(cloudOrgId);
	return useMutation({
		mutationFn: ({ cueId, input }: { cueId: string; input: CueInput }) => cloud ? updateCue(cueId, input, cloud) : hostId ? updateCue(cueId, input, undefined, hostId) : updateCue(cueId, input),
		onSuccess: invalidate,
	});
}

export function useDeleteCueMutation(projectId: string, hostId?: string, cloudOrgId?: string) {
	const invalidate = useInvalidateCues(projectId, hostId, cloudOrgId);
	const cloud = useCueHost(cloudOrgId);
	return useMutation({
		mutationFn: (cueId: string) => cloud ? deleteCue(cueId, cloud) : hostId ? deleteCue(cueId, undefined, hostId) : deleteCue(cueId),
		onSuccess: invalidate,
	});
}

export function useInvokeCueMutation() {
	return useMutation({
		mutationFn: ({ cueId, sessionId, shell }: {
			cueId: string;
			sessionId?: string;
			shell?: string;
		}) => invokeCue(cueId, sessionId, shell),
		retry: false,
	});
}
