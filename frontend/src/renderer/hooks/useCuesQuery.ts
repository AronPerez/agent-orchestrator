import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
	createCue,
	deleteCue,
	fetchProjectCues,
	invokeCue,
	projectCuesQueryKey,
	updateCue,
	type CueInput,
} from "../lib/cues";

export function useProjectCuesQuery(projectId: string, enabled = true, hostId?: string) {
	return useQuery({
		queryKey: projectCuesQueryKey(projectId, hostId),
		queryFn: () => fetchProjectCues(projectId, hostId),
		enabled: enabled && Boolean(projectId),
		refetchOnMount: "always",
		retry: false,
	});
}

function useInvalidateCues(projectId: string, hostId?: string) {
	const queryClient = useQueryClient();
	return () => {
		void queryClient.invalidateQueries({ queryKey: projectCuesQueryKey(projectId, hostId) });
	};
}

export function useCreateCueMutation(projectId: string, hostId?: string) {
	const invalidate = useInvalidateCues(projectId, hostId);
	return useMutation({
		mutationFn: (input: CueInput) => createCue(projectId, input, hostId),
		onSuccess: invalidate,
	});
}

export function useUpdateCueMutation(projectId: string, hostId?: string) {
	const invalidate = useInvalidateCues(projectId, hostId);
	return useMutation({
		mutationFn: ({ cueId, input }: { cueId: string; input: CueInput }) => updateCue(cueId, input, hostId),
		onSuccess: invalidate,
	});
}

export function useDeleteCueMutation(projectId: string, hostId?: string) {
	const invalidate = useInvalidateCues(projectId, hostId);
	return useMutation({
		mutationFn: (cueId: string) => deleteCue(cueId, hostId),
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
