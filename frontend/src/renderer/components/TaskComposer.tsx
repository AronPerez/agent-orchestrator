import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
	TaskComposerView,
	type TaskComposerAgentControl,
	type TaskComposerEffortControl,
	type TaskComposerModelCatalog,
	type TaskComposerModelControl,
} from "@aoagents/product-ui";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Loader2 } from "lucide-react";
import { RequiredAgentField } from "./CreateProjectAgentSheet";
import type { components } from "../../api/schema";
import { apiClient, apiErrorCode, apiErrorMessage } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";
import { useConnectedHosts } from "../hooks/useHostConnection";
import { useResetStaleProjectTuning } from "../hooks/useResetStaleProjectTuning";
import { captureRendererEvent } from "../lib/telemetry";
import {
	cacheAgentReadiness,
	ensureAgentReadiness,
	useAgentReadinessQuery,
} from "../hooks/useAgentReadinessQuery";
import { type FileAttachmentPayload, useFileAttachments } from "../hooks/useFileAttachments";
import { useSettings } from "../hooks/useSettings";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { useProviderConnections } from "../hooks/useProviderConnections";
import { CLOUD_AGENT_PROVIDERS, cloudAgentInfos, connectedCredentialType, credentialModelScope } from "../lib/cloud-agents";
import { createCloudSession } from "../lib/cloud-session-create";
import { reportCloudSessionCreateRequested } from "../lib/cloud-telemetry";
import { rememberStandaloneAgent } from "../lib/cloud-standalone-agents";
import { cloudDefaultModel, cloudModelLabel, isConcreteModelID, isModelSelectable, modelChoiceLabel, splitClaudeModels, withoutLocalAvailability } from "../lib/agent-model-choices";
import { collapseModelVariants, findModelVariant, listsModelVariants, pickModelVariant } from "../lib/model-variants";
import { ToggleRow } from "./chat/AgentSwitchPicker";
import { fallbackEffort } from "../lib/effort";
import {
	buildRankedAgentOptions,
	DEFAULT_AGENT_PRIORITY_RANK,
	isLaunchableAgent,
	isReadyAgent,
} from "../lib/agent-select-options";
import { cloudSessionsQueryKey, seedCreatedFreestyleSession, seedCreatedStandaloneAgent, useCloudProjectsQuery } from "../hooks/useWorkspaceQuery";
import {
	agentModelsQueryKey,
	agentModelsQueryOptions,
	modelCatalogAuthIssue,
	refreshAgentModels,
	revalidateAgentModels,
} from "../hooks/useAgentModelsQuery";
import { useModelCatalogAuthRecovery } from "../hooks/useModelCatalogAuthRecovery";
import { useUiStore } from "../stores/ui-store";
import { ModelCatalogNotice } from "./ModelCatalogNotice";
import { CLOUD_STANDALONE_WORKSPACE_ID, STANDALONE_WORKSPACE_ID, toAgentProvider } from "../types/workspace";
import { CODER_PROVIDER, projectSandboxProvider } from "../lib/cloud-offering";
import { type EffortAvailability } from "./settings/EffortPicker";
import { COMPOSER_MENU_WIDTH, TaskEffortPicker, TaskModelPicker } from "./TaskModelControls";
import { useModelTuning } from "./settings/ModelTuningControls";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";
import {
	readTaskComposerPreferences,
	rememberTaskComposerPreference,
	type TaskComposerAgentPreference,
} from "../lib/task-composer-preferences";

type Project = components["schemas"]["Project"];
type DelegateAgent = components["schemas"]["DelegateTaskRequest"]["agent"];

type CreateTaskInput = {
	clientRequestId?: string;
	projectId: string;
	brief: string;
	agent?: DelegateAgent;
	model?: string;
	effort?: string;
	mode?: "chat" | "tui";
	approvalMode?: "bypass-permissions";
	attachments?: FileAttachmentPayload[];
	taskPreparation?: string;
};

const CHAT_PREFLIGHT_CODES = new Set([
	"SESSION_MODE_UNSUPPORTED",
	"CHAT_DRIVER_UNAVAILABLE",
	"CHAT_DRIVER_INCOMPATIBLE",
	"CHAT_AUTH_REQUIRED",
]);

const READINESS_RECONCILE_CODES = new Set(["AGENT_BINARY_NOT_FOUND", "AGENT_AUTH_REQUIRED", "CHAT_AUTH_REQUIRED"]);

function cancelTaskPreparation(token: string, hostId?: string): void {
	if (!token) return;
	try {
		void (hostId ? clientForHost(hostId) : apiClient).DELETE("/api/v1/task-preparations/{token}", {
			params: { path: { token } },
		}).catch(() => { /* The host reclaims abandoned preparations after their TTL. */ });
	} catch {
		// A disconnected host will reclaim this preparation on its own TTL.
	}
}

class TaskCreateError extends Error {
	constructor(
		message: string,
		readonly code?: string,
		readonly details?: components["schemas"]["APIError"]["details"],
	) {
		super(message);
		this.name = "TaskCreateError";
	}
}

type FallbackAction = "tui" | "bypass-permissions";

function hasErrorDetail(details: components["schemas"]["APIError"]["details"] | undefined, key: string, value: string) {
	const values = details?.[key];
	return Array.isArray(values) && values.includes(value);
}

export type TaskComposerProps = {
	projectId?: string;
	hostId?: string;
	onCreated: (sessionId: string) => void;
	onDirtyChange?: (dirty: boolean) => void;
	onSubmittingChange?: (submitting: boolean) => void;
	autoFocusTitle?: boolean;
	createLabel?: string;
};

export function TaskComposer({
	projectId,
	hostId,
	onCreated,
	onDirtyChange,
	onSubmittingChange,
	autoFocusTitle,
	createLabel,
}: TaskComposerProps) {
	const { t } = useTranslation();
	const taskPlaceholder = useMemo(() => {
		const placeholders = t("newTask.taskPlaceholders" as never, { returnObjects: true }) as string[];
		return Array.isArray(placeholders)
			? (placeholders[Math.floor(Math.random() * placeholders.length)] ?? "")
			: "";
	}, [t]);
	const queryClient = useQueryClient();
	const remoteConnections = useConnectedHosts();
	const hostConnected = !hostId || remoteConnections.includes(hostId);
	const [isPromptDirty, setIsPromptDirty] = useState(false);
	const [model, setModel] = useState("");
	const [mode, setMode] = useState("");
	const [effort, setEffort] = useState("");
	const [agent, setAgent] = useState("");
	const [agentTouched, setAgentTouched] = useState(false);
	const [modelTouched, setModelTouched] = useState(false);
	const [effortTouched, setEffortTouched] = useState(false);
	const [isSubmitting, setIsSubmitting] = useState(false);
	const [error, setError] = useState<string | undefined>();
	const [fallbackAction, setFallbackAction] = useState<FallbackAction>();
	const taskPreparationRef = useRef("");
	const requestRef = useRef<{ payload: string; id: string } | undefined>(undefined);
	const {
		attachments,
		error: attachmentError,
		addFiles,
		remove: removeAttachment,
		clear: clearAttachments,
		toSettledPayload,
	} = useFileAttachments();
	// Cloud vs local is decided here and nowhere else: a cloud project routes task
	// creation to the control plane (which provisions a sandbox), while a local
	// project keeps the existing daemon flow untouched.
	const { client: cloudClient } = useCloudCp();
	const { org: cloudOrg } = useCloudOrg();
	const cloudProjects = useCloudProjectsQuery();
	const cloudProject = (cloudProjects.data ?? []).find((project) => project.id === projectId);
	// A Cloud standalone agent starts as a Cloud task with no project's
	// settings: the control plane gives it a project of its own.
	const isCloudStandalone = !hostId && projectId === CLOUD_STANDALONE_WORKSPACE_ID;
	const isCloudProject = !hostId && (Boolean(cloudProject) || isCloudStandalone);
	// Coder projects keep their own New Task dialog and start-up untouched.
	const isCoderCloudProject = isCloudProject && !isCloudStandalone && projectSandboxProvider(cloudProject?.config, cloudOffering) === CODER_PROVIDER;
	const isStandalone = projectId === STANDALONE_WORKSPACE_ID;
	const preferenceContext = hostId ? `${hostId}:${projectId ?? ""}` : projectId ?? "";
	const persistedPreferences = useMemo(
		() => readTaskComposerPreferences(preferenceContext),
		[preferenceContext],
	);
	const agentDrafts = useRef<Record<string, TaskComposerAgentPreference>>({}).current;
	const createCloudTask = useCallback(
		async (input: CreateTaskInput): Promise<string> => {
			if (input.attachments?.length) throw new Error(t("newTask.cloudAttachmentsUnsupported", { defaultValue: "File attachments are not supported for cloud tasks yet." }));
			void captureRendererEvent("ao.renderer.task_create_requested", { project_id: input.projectId });
			if (!cloudOrg?.id) throw new Error(t("newTask.unableToStart"));
			try {
				// No provider: the control plane runs the session on the project's
				// stored provider (config.sandboxProvider), else the org's default.
				// The task starts in the interface the composer resolved (its choice,
				// else the default session interface); an older control plane that
				// predates the field starts it in the terminal (createCloudSession).
				const body = {
					harness: input.agent,
					displayName: input.brief.trim().slice(0, 100) || input.agent,
					prompt: input.brief,
					...(input.model ? { model: input.model } : {}),
					...(input.effort ? { reasoningEffort: input.effort } : {}),
					...(input.mode ? { interfaceMode: input.mode } : {}),
					// Only a permission the user picked, as for a local task; without
					// one the worker derives it from the session's mode.
					...(input.approvalMode ? { approvalMode: input.approvalMode } : {}),
				};
				const createOptions = input.clientRequestId ? { idempotencyKey: input.clientRequestId } : undefined;
				let session;
				if (isCloudStandalone) {
					// The agent and its project come back together; its project joins
					// the list now so the Scratchpad shows the agent at once.
					reportCloudSessionCreateRequested({ kind: "worker", projectId: "" });
					const created = await cloudClient.createStandaloneAgent(cloudOrg.id, body, createOptions);
					seedCreatedStandaloneAgent(queryClient, cloudBaseUrl, cloudOrg.id, created.project);
					rememberStandaloneAgent(created.session.id, created.project.id);
					session = created.session;
				} else {
					({ session } = await createCloudSession(cloudClient, cloudOrg.id, { projectId: input.projectId, kind: "worker", ...body }, createOptions));
				}
				// The session's Chat composer shows, and its turns keep, what the task
				// was started with, as a local task's chat does.
				rememberCloudChatSettings(cloudOrg.id, session.id, session.harness, input);
				// The session view shows the brief from its first frame, with its start
				// under it, while the sandbox comes up.
				seedCloudOpening(queryClient, cloudOrg.id, session.id, {
					clientMessageId: input.clientRequestId ?? session.id,
					text: input.brief,
					createdAt: sentAt,
					...(session.interfaceMode ? { interfaceMode: session.interfaceMode } : {}),
				});
				// The control plane provisions the sandbox asynchronously; surface the
				// new session on the board immediately.
				void queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
				void captureRendererEvent("ao.renderer.task_create_succeeded", { project_id: input.projectId });
				return session.id;
			} catch (err) {
				void captureRendererEvent("ao.renderer.task_create_failed", { project_id: input.projectId });
				throw err instanceof Error ? err : new Error(t("newTask.unableToStart"));
			}
		},
		[cloudBaseUrl, cloudClient, cloudOrg, cloudUserId, isCloudStandalone, queryClient, t],
	);

	const createDaemonTask = useCallback(
		async (input: CreateTaskInput): Promise<string> => {
			void captureRendererEvent("ao.renderer.task_create_requested", { project_id: input.projectId });
			try {
				const { data, error } = await (hostId ? clientForHost(hostId) : apiClient).POST("/api/v1/orchestrators/delegate", {
					headers: input.attachments?.length ? { "X-AO-Attachment-Upload": "1" } : undefined,
					body: {
						clientRequestId: input.clientRequestId,
						projectId: input.projectId,
						brief: input.brief,
						agent: input.agent,
						...(input.model ? { model: input.model } : {}),
						...(input.effort !== undefined ? { effort: input.effort } : {}),
						...(input.mode ? { mode: input.mode } : {}),
						...(input.approvalMode ? { approvalMode: input.approvalMode } : {}),
						...(input.attachments && input.attachments.length > 0 ? { attachments: input.attachments } : {}),
						...(input.taskPreparation ? { taskPreparation: input.taskPreparation } : {}),
					},
				});
				if (error) {
					throw new TaskCreateError(
						apiErrorMessage(error, t("newTask.unableToStart")),
						apiErrorCode(error),
						error.details,
					);
				}
				if (!data?.workerId) throw new Error(t("newTask.noSession"));
				void captureRendererEvent("ao.renderer.task_create_succeeded", { project_id: input.projectId });
				return data.workerId;
			} catch (err) {
				void captureRendererEvent("ao.renderer.task_create_failed", { project_id: input.projectId });
				if (
					err instanceof TaskCreateError &&
					err.code &&
					READINESS_RECONCILE_CODES.has(err.code) &&
					input.agent
				) {
					try {
						cacheAgentReadiness(queryClient, await ensureAgentReadiness([input.agent], "launch", hostId), hostId);
					} catch {
						// Preserve the launch error when opportunistic reconciliation fails.
					}
				}
				throw err instanceof Error ? err : new Error(t("newTask.unableToStart"));
			}
		},
		[hostId, queryClient, t],
	);

	const createStandaloneTask = useCallback(
		async (input: CreateTaskInput): Promise<string> => {
			void captureRendererEvent("ao.renderer.task_create_requested", { scope: "standalone" });
			const displayName = input.brief.trim().slice(0, 100) || input.agent || "Standalone agent";
			const { data, error } = await (hostId ? clientForHost(hostId) : apiClient).POST("/api/v1/sessions", {
				headers: input.attachments?.length ? { "X-AO-Attachment-Upload": "1" } : undefined,
				body: {
					clientRequestId: input.clientRequestId,
					kind: "worker",
					harness: input.agent as components["schemas"]["SpawnSessionRequest"]["harness"],
					prompt: input.brief,
					displayName,
					model: input.model,
					...(input.effort ? { effort: input.effort } : {}),
					...(input.mode ? { mode: input.mode } : {}),
					...(input.approvalMode ? { approvalMode: input.approvalMode } : {}),
					...(input.attachments && input.attachments.length > 0 ? { attachments: input.attachments } : {}),
				},
			});
			if (error) {
				throw new TaskCreateError(apiErrorMessage(error, t("newTask.unableToStart")), apiErrorCode(error), error.details);
			}
			if (!data?.session.id) throw new Error(t("newTask.noSession"));
			void captureRendererEvent("ao.renderer.task_create_succeeded", { scope: "standalone" });
			return data.session.id;
		},
		[hostId, t],
	);

	const createTask = useCallback(
		(input: CreateTaskInput): Promise<string> =>
			isStandalone ? createStandaloneTask(input) : isCloudProject ? createCloudTask(input) : createDaemonTask(input),
		[isStandalone, isCloudProject, createStandaloneTask, createCloudTask, createDaemonTask],
	);

	const projectQuery = useQuery({
		// A cloud project lives in the control plane, not the local daemon, so this
		// local lookup would 404 (PROJECT_NOT_FOUND); skip it for cloud projects.
		queryKey: hostId ? ["project", hostId, projectId] : ["project", projectId],
		enabled: Boolean(projectId) && !isCloudProject && !isStandalone,
		queryFn: async () => {
			const { data, error: apiError } = await (hostId ? clientForHost(hostId) : apiClient).GET("/api/v1/projects/{id}", {
				params: { path: { id: projectId ?? "" } },
			});
			if (apiError) throw new Error(apiErrorMessage(apiError));
			if (data?.status !== "ok") throw new Error(t("newTask.configUnavailable"));
			return data.project as Project;
		},
	});
	useEffect(() => {
		const id = projectQuery.data?.id;
		if (!id) return;
		let client;
		try {
			client = hostId ? clientForHost(hostId) : apiClient;
		} catch {
			return;
		}
		let disposed = false;
		taskPreparationRef.current = "";
		void client.POST("/api/v1/projects/{id}/tasks/prepare", {
			params: { path: { id } },
		}).then(
			({ data }) => {
				const token = data?.taskPreparation ?? "";
				if (!token) return;
				if (disposed) {
					cancelTaskPreparation(token, hostId);
					return;
				}
				taskPreparationRef.current = token;
			},
			() => undefined,
		);
		return () => {
			disposed = true;
			const token = taskPreparationRef.current;
			taskPreparationRef.current = "";
			cancelTaskPreparation(token, hostId);
		};
	}, [hostId, projectQuery.data?.id]);
	const agentsQuery = useAgentReadinessQuery(true, hostId);
	const { settings, error: settingsError } = useSettings(hostId);
	// The composer preselects the agent and model a spawn would actually use
	// instead of parking the controls on a "default" label the user has to
	// remember. Both resolved values remain directly editable.
	// A cloud project is unknown to the local daemon, so the local projectQuery
	// above is disabled for it; its config lives in the control-plane projects
	// list (already fetched as cloudProjects). Reading the worker defaults from
	// the disabled local query is what made a cloud worker ignore
	// config.worker.agent and fall back to claude-code.
	const projectConfig = (isCloudProject ? cloudProject?.config : projectQuery.data?.config) as
		| {
				worker?: { agent?: string; agentConfig?: { model?: string; mode?: string; effort?: string } };
				agentConfig?: { model?: string; mode?: string; effort?: string };
		  }
		| undefined;
	const projectWorkerAgent = projectConfig?.worker?.agent ?? "";
	const globalDefaultAgent = projectQuery.data?.agent ?? "";
	const configuredProjectAgent = projectWorkerAgent || globalDefaultAgent;
	const agentCatalog = agentsQuery.data;
	// Cloud projects support the control-plane agents listed in CLOUD_AGENT_PROVIDERS
	// (the single source), with readiness derived from the user's provider connections.
	const cloudConnectionsQuery = useProviderConnections();
	const cloudAgents = useMemo(() => cloudAgentInfos(cloudConnectionsQuery.data), [cloudConnectionsQuery.data]);
	const standaloneDefaultAgent = useMemo(() => {
		if (!isStandalone || !agentCatalog) return "";
		return (
			buildRankedAgentOptions({
				agents: agentCatalog.agents,
				priorityRank: DEFAULT_AGENT_PRIORITY_RANK,
				fallbackAgents: [],
			}).find(isReadyAgent)?.id ?? ""
		);
	}, [agentCatalog, isStandalone]);
	const configuredDefaultAgent = isStandalone
		? standaloneDefaultAgent
		: configuredProjectAgent;
	const rememberedAgent = persistedPreferences?.lastAgent ?? "";
	const availableAgents = isCloudProject ? cloudAgents : agentCatalog?.agents;
	const rememberedAgentIsAvailable = Boolean(
		availableAgents?.some((candidate) => candidate.id === rememberedAgent && (hostId ? isLaunchableAgent(candidate) : isReadyAgent(candidate))),
	);
	// Saved choices must not override a project's current role defaults.
	const defaultWorkerAgent = projectWorkerAgent || (rememberedAgentIsAvailable ? rememberedAgent : configuredDefaultAgent);
	const selectedAgent = agent || defaultWorkerAgent;
	// A cloud project is unknown to the local daemon, so its model catalog is
	// queried agent-level (no project scope); otherwise the request 404s and the
	// dropdown spins forever. opencode is the exception: its catalog depends on
	// the connected provider, so scope the query by that credential type
	// (credentialModelScope) to show the models the cloud VM will actually run.
	// Local projects keep their real project scope.
	const modelsProjectId = useMemo(() => {
		if (!isCloudProject && !isStandalone) return projectId ?? "";
		if (isCloudProject && selectedAgent === "opencode") {
			const credentialType = connectedCredentialType(cloudConnectionsQuery.data, "opencode");
			if (credentialType !== "") return credentialModelScope(credentialType);
		}
		return "";
	}, [isCloudProject, isStandalone, projectId, selectedAgent, cloudConnectionsQuery.data]);
	const defaultWorkerModel =
		projectConfig?.worker?.agentConfig?.model ?? projectConfig?.agentConfig?.model ?? "";
	const defaultWorkerMode = projectConfig?.worker?.agentConfig?.mode ?? projectConfig?.agentConfig?.mode ?? "";
	const defaultWorkerEffort =
		projectConfig?.worker?.agentConfig?.effort ?? projectConfig?.agentConfig?.effort ?? "";
	const projectModelForSelectedAgent = selectedAgent === configuredProjectAgent
		? defaultWorkerModel
		: "";
	const projectModeForSelectedAgent = selectedAgent === configuredProjectAgent
		? defaultWorkerMode
		: "";
	const rememberedConfigForSelectedAgent = agentDrafts[selectedAgent];
	const rememberedModel = rememberedConfigForSelectedAgent?.model ?? "";
	const rememberedMode = rememberedConfigForSelectedAgent?.mode ?? "";
	// A cloud task runs the control plane's catalog for its agent: the models a
	// Cloud worker on the same CLI version and credential reported, or the ones
	// its pinned CLI release supports. Neither depends on this computer's login.
	const cloudCatalogAgent = isCloudProject && (CLOUD_AGENT_PROVIDERS as readonly string[]).includes(selectedAgent);
	const cloudCatalogState = useCloudModelCatalog({ orgId: cloudOrg?.id, agent: selectedAgent, enabled: cloudCatalogAgent });
	const cloudModelCatalogQuery = cloudCatalogState.query;
	const cloudCatalog = cloudCatalogState.catalog;
	// Until the catalog arrives no listed model vouches for the effort, so a task
	// does not start yet with a level its model refuses.
	const cloudCatalogLoading = cloudCatalogState.loading;
	const cloudCatalogGap = cloudCatalogState.gap;
	// A model the account's own Cloud list verifiably lacks fails every turn, so
	// nothing picks it for a cloud task on its own: not a remembered pick, not the
	// project's model, not a suggestion carried in from elsewhere.
	const cloudModelUnavailable = (id: string) => isCloudProject && cloudModelVerdict(cloudCatalog, id) === "unavailable";
	// A task started beside a Cloud project's running orchestrator, with the
	// orchestrator's agent and no worker model of the project's, starts on the
	// model the orchestrator runs now, as long as the account can run it.
	const cloudOrchestrator = useCloudOrchestratorModel({
		baseUrl: cloudBaseUrl,
		orgId: cloudOrg?.id,
		projectId,
		// A standalone agent has no orchestrator to start beside.
		enabled: isCloudProject && !isCloudStandalone,
	});
	const orchestratorModelForSelectedAgent = isCloudProject && cloudOrchestrator && cloudOrchestrator.harness === selectedAgent &&
		isConcreteModelID(cloudOrchestrator.model) && !cloudModelUnavailable(cloudOrchestrator.model)
		? cloudOrchestrator.model
		: "";
	const localCatalogForCloud = listsLocalModelsForCloud(cloudCatalogGap, selectedAgent, modelScope);
	const localCatalogInUse = !isCloudProject || localCatalogForCloud;
	// Shares the picker's query key, so this is the same fetch, not a second one.
	const modelCatalogQuery = useQuery(agentModelsQueryOptions(selectedAgent, modelsProjectId, hostId));
	const revalidationQuery = useQuery({
		queryKey: [
			"agent-model-revalidation",
			hostId ?? "",
			selectedAgent,
			modelsProjectId,
			modelCatalogQuery.data?.validatedAt ?? "",
		],
		queryFn: () => revalidateAgentModels(selectedAgent, modelsProjectId, hostId),
		enabled: selectedAgent !== "" && modelCatalogQuery.data?.refreshRecommended === true,
		staleTime: Number.POSITIVE_INFINITY,
		retry: false,
	});
	useEffect(() => {
		if (revalidationQuery.data) {
			queryClient.setQueryData(
				agentModelsQueryKey(selectedAgent, modelsProjectId, hostId),
				revalidationQuery.data,
			);
		}
	}, [hostId, modelsProjectId, queryClient, revalidationQuery.data, selectedAgent]);
	const revalidationWarning = revalidationQuery.isError
		? revalidationQuery.error instanceof Error
			? revalidationQuery.error.message
			: t("settings.models.validateFailed")
		: undefined;
	const modelWarningText =
		revalidationWarning ??
		modelCatalogQuery.data?.warning ??
		(modelCatalogQuery.isError
			? modelCatalogQuery.error instanceof Error
				? modelCatalogQuery.error.message
				: t("settings.models.loadFailed")
			: undefined);
	// A login problem behind the catalog warning gets a plain-language notice
	// with the fix in place. Discovery runs against this computer's login even
	// for a cloud project, whose task runs with the cloud connection instead,
	// so there the notice is informational and never reads as blocking.
	const modelAuthIssue = revalidationWarning === undefined ? modelCatalogAuthIssue(modelCatalogQuery.data) : undefined;
	useModelCatalogAuthRecovery(selectedAgent, hostId, modelAuthIssue);
	const modelCatalog: TaskComposerModelCatalog | undefined = modelCatalogQuery.data
		? {
				allowCustom: modelCatalogQuery.data.allowCustom,
				customModelEntry: modelCatalogQuery.data.customModelEntry,
				models: modelCatalogQuery.data.models,
				refreshError: modelCatalogQuery.data.refreshError,
				refreshState: modelCatalogQuery.data.refreshState,
				retryAt: modelCatalogQuery.data.retryAt,
				selectionMode: modelCatalogQuery.data.selectionMode,
			}
		: undefined;
	// An unmarked first row is not evidence of what the provider will run.
	const catalogModels = modelCatalogQuery.data?.models?.filter((item) => isConcreteModelID(item.id)) ?? [];
	const catalogDefaultOption =
		catalogModels.find((item) => item.isDefault)?.id ?? "";
	const catalogUsesModes = modelCatalogQuery.data?.selectionMode === "mode";
	// The model this user ran most recently.
	const lastUsedOption =
		catalogModels
			.filter((item) => item.lastUsedAt)
			.sort((a, b) => Date.parse(b.lastUsedAt ?? "") - Date.parse(a.lastUsedAt ?? ""))[0]?.id ?? "";
	// With nothing remembered, run, or configured, Claude Code opens on the newest Opus.
	const claudeFallbackOption =
		selectedAgent === "claude-code" && !catalogUsesModes
			? (splitClaudeModels(catalogModels).current.find((item) => /opus/i.test(modelChoiceLabel(item)))?.id ?? "")
			: "";
	const draftConfigForSelectedAgent = agentDrafts[selectedAgent];
	const rememberedConfigForSelectedAgent = draftConfigForSelectedAgent ?? persistedPreferences?.agents[selectedAgent];
	const rememberedModel = rememberedConfigForSelectedAgent?.model ?? "";
	const rememberedMode = rememberedConfigForSelectedAgent?.mode ?? "";
	const rememberedModelIsValid =
		isConcreteModelID(rememberedModel) &&
		Boolean(
			modelCatalogQuery.data &&
				(modelCatalogQuery.data.allowCustom || catalogModels.some((item) => item.id === rememberedModel)),
		);
	const rememberedModeIsValid =
		isConcreteModelID(rememberedMode) && catalogModels.some((item) => item.id === rememberedMode);
	const defaultModelForSelectedAgent =
		(draftConfigForSelectedAgent && rememberedModelIsValid ? rememberedModel : "") ||
		(isConcreteModelID(projectModelForSelectedAgent) ? projectModelForSelectedAgent : "") ||
		(rememberedModelIsValid ? rememberedModel : "") ||
		(!catalogUsesModes && isConcreteModelID(lastUsedOption) ? lastUsedOption : "") ||
		(catalogUsesModes ? "" : catalogDefaultOption) ||
		claudeFallbackOption;
	const defaultModeForSelectedAgent =
		(draftConfigForSelectedAgent && rememberedModeIsValid ? rememberedMode : "") ||
		(isConcreteModelID(projectModeForSelectedAgent) ? projectModeForSelectedAgent : "") ||
		(rememberedModeIsValid ? rememberedMode : "") ||
		(catalogUsesModes && isConcreteModelID(lastUsedOption) ? lastUsedOption : "") ||
		(catalogUsesModes ? catalogDefaultOption : "");
	const selectedModel = model || (modelTouched ? (catalogUsesModes ? "" : catalogDefaultOption) : defaultModelForSelectedAgent);
	const selectedMode = mode || (modelTouched ? (catalogUsesModes ? catalogDefaultOption : "") : defaultModeForSelectedAgent);
	const selectedModelOrMode = (selectedModel || selectedMode).trim();
	const projectModelOrMode = projectModelForSelectedAgent || projectModeForSelectedAgent;
	const requestedModel = selectedModelOrMode && selectedModelOrMode !== projectModelOrMode && (
		selectedModelOrMode !== catalogDefaultOption || isConcreteModelID(projectModelOrMode)
	) ? selectedModelOrMode : undefined;
	const rememberedEffortIsExplicit = Boolean(
		rememberedConfigForSelectedAgent &&
			Object.prototype.hasOwnProperty.call(rememberedConfigForSelectedAgent, "effort"),
	);
	const defaultEffortForSelectedAgent = draftConfigForSelectedAgent?.effort ||
		(selectedAgent === configuredProjectAgent ? defaultWorkerEffort : "") ||
		(rememberedEffortIsExplicit ? rememberedConfigForSelectedAgent?.effort ?? "" : "");
	const { selected: effortModel } = useModelTuning({
		models: catalogModels,
		model: selectedModel,
		effort,
		onEffortChange: setEffort,
		onEffortReset: setEffort,
	});
	const effortOptions = effortModel?.efforts?.filter((option) => option && option.toLowerCase() !== "default") ?? [];
	const cloudDefaultEffort = isCloudProject && effortOptions.includes("medium") ? "medium" : "";
	const inheritedEffort = selectedAgent === configuredProjectAgent ? defaultWorkerEffort : "";
	const implicitEffort = inheritedEffort || effortModel?.defaultEffort || "";
	const selectedAgentLabel = availableAgents?.find((item) => item.id === selectedAgent)?.label || selectedAgent;
	useResetStaleProjectTuning({
		projectId: isCloudProject || isStandalone ? undefined : projectQuery.data?.id,
		hostId,
		agent: selectedAgent,
		agentLabel: selectedAgentLabel,
		projectAgent: configuredProjectAgent,
		config: isCloudProject ? undefined : projectQuery.data?.config,
		catalog: localCatalog,
	});
	// A cloud project's Chat-capable harnesses come from the control plane, as a
	// local project's come from the daemon.
	const chatHarnesses = isCloudProject ? cloudOffering.chatHarnesses : settings?.chatHarnesses;
	const requiresTuiFallback =
		selectedAgent !== "" &&
		settings?.defaultSessionMode === "chat" &&
		!settings.chatHarnesses.includes(selectedAgent);
	// With no provider default for the reported levels AO picks one, shows it as
	// selected, and sends it, so the picker matches what the task runs with.
	const aoDefaultEffort = requiresTuiFallback ? undefined : fallbackEffort(effortOptions, implicitEffort);
	const effectiveEffort = effort && effort !== "default" ? effort : aoDefaultEffort ?? "";
	const requestedEffort = isCloudProject
		? effort || cloudDefaultEffort || undefined
		: effortTouched || rememberedEffortIsExplicit
			? !effectiveEffort || effectiveEffort === implicitEffort ? undefined : effectiveEffort
			: aoDefaultEffort;
	const effortAvailability: EffortAvailability = requiresTuiFallback
		? "launch-unavailable"
		: !effortModel || effortModel.efforts === undefined
			? "unknown"
			: effortOptions.length > 0
				? "supported"
				: "unsupported";
	const canSubmit =
		hostConnected &&
		Boolean(projectId) &&
		(!isStandalone || selectedAgent !== "") &&
		(!hostId || Boolean(agentCatalog?.agents.some((candidate) => candidate.id === selectedAgent && isLaunchableAgent(candidate)))) &&
		(isCloudProject || isStandalone || projectQuery.data !== undefined) &&
		(!hostId || (agentsQuery.isSuccess && settings !== undefined));
	const remoteLoadError = !hostId ? undefined : !hostConnected ? t("remote.hostOffline") :
		[projectQuery.error, agentsQuery.error].find((cause): cause is Error => cause instanceof Error)?.message ?? settingsError ??
			(agentsQuery.isSuccess && !agentCatalog?.agents.some(isLaunchableAgent) ? t("remote.noReadyAgent") : undefined);
	const refreshSelectedModels = useCallback(async () => {
		const refreshed = await refreshAgentModels(selectedAgent, modelsProjectId, hostId);
		queryClient.setQueryData(agentModelsQueryKey(selectedAgent, modelsProjectId, hostId), refreshed);
	}, [hostId, modelsProjectId, queryClient, selectedAgent]);
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const startAgentLogin = useCallback(() => {
		openGlobalSettings("harness", {
			focusAgentId: selectedAgent,
			...(hostId ? { hostId } : {}),
			// A cloud task runs on the cloud connection, so that is the login to fix.
			harnessView: isCloudProject ? "cloud" : "local",
			startLogin: true,
			preserveProject: true,
		});
	}, [hostId, isCloudProject, openGlobalSettings, selectedAgent]);
	// The catalog is read through this computer's login, but a cloud task runs on
	// the cloud connection: once that is connected, this computer's login state is
	// irrelevant to the task and the notice only confuses.
	const cloudHarnessReady = isCloudProject && isCloudHarnessConnected(cloudConnectionsQuery.data, selectedAgent);
	const modelWarning = modelAuthIssue && cloudHarnessReady ? undefined : modelAuthIssue ? (
		<ModelCatalogNotice
			agentLabel={selectedAgentLabel}
			issue={modelAuthIssue}
			detail={modelWarningText}
			cloud={isCloudProject}
			onLogin={startAgentLogin}
			onRetry={refreshSelectedModels}
		/>
	) : modelWarningText;
	useEffect(() => {
		if (!agentTouched) setAgent(defaultWorkerAgent);
	}, [agentTouched, defaultWorkerAgent]);
	useEffect(() => {
		if (!modelTouched) {
			setModel(defaultModelForSelectedAgent);
			setMode(defaultModeForSelectedAgent);
		}
	}, [defaultModelForSelectedAgent, defaultModeForSelectedAgent, modelTouched]);
	useEffect(() => {
		if (!effortTouched) setEffort(defaultEffortForSelectedAgent || cloudDefaultEffort);
	}, [cloudDefaultEffort, defaultEffortForSelectedAgent, effortTouched]);

	const isDirty = isPromptDirty || modelTouched || effortTouched || attachments.length > 0;
	const handlePromptChange = useCallback((value: string) => {
		const nextDirty = value.trim() !== "";
		setIsPromptDirty((wasDirty) => (wasDirty === nextDirty ? wasDirty : nextDirty));
	}, []);
	useEffect(() => {
		onDirtyChange?.(isDirty);
	}, [isDirty, onDirtyChange]);
	useEffect(() => () => onDirtyChange?.(false), [onDirtyChange]);

	useEffect(() => {
		onSubmittingChange?.(isSubmitting);
	}, [isSubmitting, onSubmittingChange]);
	useEffect(() => () => onSubmittingChange?.(false), [onSubmittingChange]);
	useEffect(() => () => clearAttachments(), [clearAttachments]);

	const submitTask = async (
		brief: string,
		interfaceMode?: "chat" | "tui",
		approvalMode?: "bypass-permissions",
	) => {
		if (!projectId || !canSubmit || isSubmitting) return;

		setIsSubmitting(true);
		setError(undefined);
		setFallbackAction(undefined);
		try {
			if (!isCloudProject && selectedAgent) {
				try {
					cacheAgentReadiness(queryClient, await ensureAgentReadiness([selectedAgent], "launch", hostId), hostId);
				} catch {
					// This check lacks the selected project's cwd and environment, so it
					// is advisory. The project-aware launch path remains authoritative.
				}
			}
			const attachmentPayloads = await toSettledPayload();
			const submittedPreparation = taskPreparationRef.current;
			const request: CreateTaskInput = {
				projectId,
				brief,
				agent: selectedAgent ? (selectedAgent as CreateTaskInput["agent"]) : undefined,
				model: requestedModel,
				// Only explicit Codex picks set this; agent changes reset it, and TUI retries preserve it.
				effort: requestedEffort,
				mode: interfaceMode,
				approvalMode,
				attachments: attachmentPayloads.length > 0 ? attachmentPayloads : undefined,
				taskPreparation: submittedPreparation || undefined,
			};
			const { taskPreparation: _, attachments: _attachments, ...requestPayload } = request;
			const payload = JSON.stringify({ ...requestPayload, attachmentIds: attachments.map((attachment) => attachment.id) });
			if (requestRef.current?.payload !== payload) {
				requestRef.current = { payload, id: globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random()}` };
			}
			const sessionId = await createTask({ ...request, clientRequestId: requestRef.current.id });
			const preparationAfterSubmit = taskPreparationRef.current;
			taskPreparationRef.current = "";
			// DELETE is intentionally idempotent after a successful claim. It also
			// reclaims a preparation that resolved after this submission captured its
			// token, instead of leaving that unused worktree until TTL expiry.
			cancelTaskPreparation(submittedPreparation, hostId);
			cancelTaskPreparation(preparationAfterSubmit, hostId);
			if (selectedAgent) {
				const preference: TaskComposerAgentPreference = {
					model: selectedModel ? requestedModel ?? "" : "",
					mode: selectedMode ? requestedModel ?? "" : "",
					...(requestedEffort !== undefined ? { effort: requestedEffort } : {}),
				};
				agentDrafts[selectedAgent] = preference;
				rememberTaskComposerPreference(preferenceContext, selectedAgent, preference);
			}
			onCreated(sessionId);
		} catch (err) {
			const canBypassApprovals =
				err instanceof TaskCreateError &&
				err.code === "SESSION_MODE_UNSUPPORTED" &&
				hasErrorDetail(err.details, "missingCapabilities", "approvals") &&
				hasErrorDetail(err.details, "allowedApprovalModes", "bypass-permissions");
			setFallbackAction(
				canBypassApprovals
					? "bypass-permissions"
					: selectedAgent !== "unreal-agent" && interfaceMode !== "tui" &&
							err instanceof TaskCreateError &&
							Boolean(err.code && CHAT_PREFLIGHT_CODES.has(err.code))
						? "tui"
						: undefined,
			);
			setError(err instanceof Error ? err.message : t("newTask.unableToStart"));
		} finally {
			setIsSubmitting(false);
		}
	};
	// The interface a plain submit asks for; a TUI-only agent cannot open Chat.
	const submitMode: TaskLaunchOptions["interfaceMode"] = selectedAgent === "unreal-agent" ? "chat" : requiresTuiFallback ? "tui" : undefined;
	const launchFallback = (brief: string, clientRequestId?: string, files?: FileAttachmentPayload[]) =>
		fallbackAction === "bypass-permissions"
			? launch(brief, { interfaceMode: selectedAgent === "unreal-agent" ? "chat" : undefined, approvalMode: "bypass-permissions", clientRequestId, attachments: files })
			: launch(brief, { interfaceMode: "tui", clientRequestId, attachments: files });

	const changeAgent = (value: string) => {
		setError(undefined);
		setStatus(undefined);
		setErrorAction(undefined);
		setBranchOverride("");
		if (selectedAgent) agentDrafts[selectedAgent] = rememberedPreference("agent-switch");
		setAgent(value);
		setAgentTouched(true);
		setModel("");
		setMode("");
		setEffort("");
		setModelTouched(false);
		setEffortTouched(false);
	};

	// The agents the new chat page's picker offers, ranked as in New Task. Locally only
	// installed agents whose sign-in is known to be usable: an "Auth unknown" agent may
	// not start, so it stays in Harness settings. A remote host's readiness can be
	// unknown without anything being wrong, so there definite failures alone are left out.
	const agentChoices = useMemo(() => {
		const agents = isCloudProject ? cloudAgents : agentCatalog?.agents;
		const ranked = buildRankedAgentOptions({
			agents,
			priorityRank: DEFAULT_AGENT_PRIORITY_RANK,
			fallbackAgents: AGENT_OPTIONS.map((agent) => unknownAgentReadiness(agent, agentLabel(agent))),
		});
		return (agents ? ranked.filter(hostId ? isLaunchableAgent : startsLocally) : ranked).map(({ id, label }) => ({ id, label }));
	}, [agentCatalog?.agents, cloudAgents, hostId, isCloudProject]);
	const catalogProjectId = isCloudProject || isStandalone ? "" : projectId ?? "";
	// A pick from the picker's other agents: that agent, with the model and effort chosen
	// alongside it, if any. Its catalog is cached, since the picker lists its models.
	const switchAgent = (harness: string, pick: { model?: string; effort?: string } = {}) => {
		if (harness !== selectedAgent) changeAgent(harness);
		if (pick.model) {
			// Cloud's lists name models only; this computer's may name launch modes.
			const catalog = isCloudProject ? undefined : queryClient.getQueryData(agentModelsQueryOptions(harness, catalogProjectId, hostId).queryKey);
			if (catalog?.selectionMode === "mode") {
				setMode(pick.model);
				setModel("");
			} else {
				setModel(pick.model);
				setMode("");
			}
			setModelTouched(true);
		}
		if (pick.effort) {
			setEffort(pick.effort);
			setEffortTouched(true);
		}
	};

	// A cloud task shows the effort it sends.
	const shownEffort = isCloudProject ? effectiveEffort : effort;
	const showEffort = cursorChoice ? cursorChoice.family.efforts.length > 1 : !requiresTuiFallback && (effortOptions.length > 0 || Boolean(shownEffort && shownEffort !== "default"));
	// The same choices in the chat composer's shape, so the new chat page can use its settings bar.
	// The orchestrator's model says so in the model menus.
	const orchestratorModelHint = orchestratorModelIsValid
		? { id: orchestratorModelForSelectedAgent, label: t("newTask.sameAsOrchestrator") }
		: undefined;
	const chatModels = useMemo<ChatModel[]>(() => catalogModels.map((item) => ({
		id: item.id,
		displayName: item.id === "auto" ? t("settings.models.autoRouteLabel") : catalogUsesModes ? modelChoiceLabel(item) : item.label,
		default: Boolean(item.isDefault),
		efforts: item.efforts,
		defaultEffort: item.defaultEffort,
		...(orchestratorModelHint?.id === item.id ? { hint: orchestratorModelHint.label } : {}),
	})), [catalogModels, catalogUsesModes, orchestratorModelHint?.id, orchestratorModelHint?.label, t]);
	const turnSettings: TurnSettings = {
		model: (selectedModel || selectedMode) || undefined,
		reasoningEffort: showEffort ? effectiveEffort || undefined : undefined,
		// Display the inherited policy even when the daemon applies it at launch.
		approvalMode: effectiveApproval,
	};
	const chooseTurnSettings = (next: TurnSettings) => {
		if ((next.model ?? "") !== (turnSettings.model ?? "")) {
			if (catalogUsesModes) {
				setMode(next.model ?? "");
				setModel("");
				setEffort("");
			} else {
				setModel(next.model ?? "");
				setMode("");
			}
			setModelTouched(true);
		}
		if ((next.reasoningEffort ?? "") !== (turnSettings.reasoningEffort ?? "")) {
			setEffort(next.reasoningEffort ?? "");
			setEffortTouched(true);
		}
		if (next.approvalMode && next.approvalMode !== turnSettings.approvalMode) setApprovalMode(next.approvalMode);
	};

	return {
		launch,
		launchFallback,
		// What a plain send would request right now, to save alongside a send.
		launchSnapshot: (): LaunchReplay => ({
			agent: selectedAgent || undefined,
			model: requestedModel,
			effort: requestedEffort,
			approvalMode: launchApproval,
			mode: submitMode,
		}),
		// The interface a plain send starts in, including the Settings default.
		effectiveInterface: (submitMode ?? settings?.defaultSessionMode ?? "tui") as "chat" | "tui",
		refreshModels: refreshSelectedModels,
		preparedSessionId,
		submitMode,
		isCloudProject,
		isCoderCloudProject,
		canSubmit,
		isSubmitting,
		isDirty: modelTouched || effortTouched || attachments.length > 0,
		error,
		status,
		errorAction: errorAction === "install-agent" ? {
			label: t("newTask.openHarnessSettings", { defaultValue: "Open Agent Harnesses" }),
			onClick: () => openGlobalSettings("harness", { focusAgentId: selectedAgent, ...(hostId ? { hostId } : {}), harnessView: "local", preserveProject: true }),
		} : errorAction === "choose-branch" ? {
			label: t("newTask.chooseNewBranch", { defaultValue: "Choose new branch" }),
			input: {
				label: t("newTask.newBranchPrompt", { defaultValue: "New branch name" }),
				value: branchDraft,
				onChange: setBranchDraft,
			},
			onClick: () => {
				if (!branchDraft.trim()) return;
				setBranchOverride(branchDraft.trim());
				setErrorAction(undefined);
				setError(undefined);
				setStatus(t("newTask.newBranchReady", { branch: branchDraft.trim(), defaultValue: "Branch {{branch}} selected for your next task." }));
			},
		} : undefined,
		remoteLoadError,
		modelWarning,
		modelWarningRetries,
		fallbackAction,
		fallbackLabel: fallbackAction === "bypass-permissions"
			? t("newTask.startWithoutApprovals", { defaultValue: "Start without approvals" })
			: t("newTask.createAsTui"),
		agent: {
			label: t("newTask.agent"),
			placeholder: t("newTask.selectAgent"),
			value: selectedAgent,
			agents: isCloudProject ? cloudAgents : agentCatalog?.agents,
			disabled:
				isSubmitting || (!isCloudProject && agentsQuery.isFetching && agentCatalog === undefined),
			onChange: changeAgent,
		},
		model: {
			agentId: selectedAgent,
			agentLabel: selectedAgentLabel,
			projectId: isStandalone || isCloudStandalone ? "" : (projectId ?? ""),
			disabled: isSubmitting,
			value: selectedModel,
			mode: selectedMode,
			catalog: modelCatalog,
			fetching: localCatalogInUse ? localModelCatalogQuery.isFetching : cloudModelCatalogQuery.isFetching,
			loading:
				selectedAgent !== "" &&
				(localCatalogInUse
					? localModelCatalogQuery.isFetching && localModelCatalogQuery.data === undefined
					: cloudModelCatalogQuery.isFetching && cloudCatalog === undefined),
			onModelChange: (value: string) => {
				setModel(value);
				setMode("");
				setModelTouched(true);
				// Effort levels are per-model, so a level the newly chosen model
				// does not advertise has to be dropped rather than carried over. A
				// cloud model Cloud's catalog does not list may still take it. The
				// cloud default shows no effort, so it starts from the project's.
				if (isCloudProject && value === "") {
					setEffort(inheritedEffort);
					return;
				}
				const listed = cloudCatalog?.models.find((item) => item.id === value);
				const nextEfforts = listed
					? listed.efforts
					: modelCatalog?.models?.find((item) => item.id === value)?.efforts;
				const keepUnknown = isCloudProject && !listed && nextEfforts === undefined;
				setEffort((current) => (current !== "" && !keepUnknown && !nextEfforts?.includes(current) ? "" : current));
			},
			onModeChange: (value: string) => {
				setMode(value);
				setModel("");
				setModelTouched(true);
				// A mode replaces the model entirely, so no model vouches for a
				// previously chosen level any more.
				setEffort("");
			},
		},
		effort: cursorChoice ? {
			disabled: isSubmitting,
			options: cursorChoice.family.efforts,
			value: cursorChoice.variant.effort ?? "",
			onChange: (value: string) => pickCursorVariant({ effort: value }),
		} : {
			disabled: isSubmitting,
			options: effortOptions,
			value: effectiveEffort,
			onChange: (value: string) => {
				setEffort(value);
				setEffortTouched(true);
			},
		},
		attachments: {
			items: attachments.map(({ id, name, dataUrl }) => ({ id, name, previewUrl: dataUrl })),
			error: attachmentError,
			onAddFiles: (files: File[]) => void addFiles(files),
			onRemove: removeAttachment,
		},
		showEffort,
		chatModels,
		approvalModes: usesChatPermissions ? chatPermissionModes ?? [] : undefined,
		turnSettings,
		chooseTurnSettings,
		agentChoices,
		catalogProjectId,
		// A Cloud task's organization: the agent switch lists Cloud's models for the account.
		catalogCloudOrgId: isCloudProject ? cloudOrg?.id : undefined,
		switchAgent,
		renderAgentControl: (control: TaskComposerAgentControl) => <DesktopAgentControl {...control} hostId={hostId} manageView={isCloudProject ? "cloud" : "local"} />,
		renderEffortControl: (control: TaskComposerEffortControl) => <TaskEffortPicker {...control} value={control.value || cloudDefaultEffort} defaultEffort={cursorChoice ? undefined : inheritedEffort || effortModel?.defaultEffort} availability={cursorChoice ? "supported" : effortAvailability}
			footer={cursorChoice?.family.hasThinkingChoice ? <ToggleRow label={t("agentSwitch.thinking")} checked={cursorChoice.variant.thinking} onChange={(thinking) => pickCursorVariant({ thinking })} /> : undefined} />,
		renderModelControl: (control: TaskComposerModelControl) => <TaskModelPicker {...control}
			hostId={hostId}
			onRefresh={isCloudProject && !cloudCatalogAgent ? undefined : refreshSelectedModels}
			allowNativeSelection={isCloudProject}
			footer={cloudModelSource}
			modelHint={orchestratorModelHint}
			showFollowAgentAction={Boolean(catalogDefaultOption || !isConcreteModelID(projectModelOrMode))} />,
	};
}

export type TaskLaunch = ReturnType<typeof useTaskLaunch>;

/** Installed, with a sign-in that works, is not needed, or has credentials not yet verified. */
function startsLocally(agent: AgentInfo): boolean {
	return agent.installation.state === "installed" && (isReadyAgent(agent) || agent.authentication.state === "configured");
}

/** The picks a composer had for one project, parked while it shows a project of the other kind (local or Cloud). */
type ParkedComposerPicks = {
	/** The agent the picks were made with; they never come back for another. */
	agent: string;
	model: string;
	mode: string;
	effort: string;
	modelTouched: boolean;
	effortTouched: boolean;
};

// The launch form as a standalone composer. The app starts tasks from the new
// chat page; this view keeps the launch logic exercised in isolation.
export function TaskComposer({
	projectId,
	hostId,
	onCreated,
	onDirtyChange,
	onSubmittingChange,
	autoFocusTitle,
	createLabel,
}: TaskComposerProps) {
	const { t } = useTranslation();
	const taskPlaceholder = useMemo(() => {
		const placeholders = t("newTask.taskPlaceholders" as never, { returnObjects: true }) as string[];
		return Array.isArray(placeholders)
			? (placeholders[Math.floor(Math.random() * placeholders.length)] ?? "")
			: "";
	}, [t]);
	const task = useTaskLaunch({ projectId, hostId });
	const [isPromptDirty, setIsPromptDirty] = useState(false);
	const isDirty = isPromptDirty || task.isDirty;
	const handlePromptChange = useCallback((value: string) => {
		const nextDirty = value.trim() !== "";
		setIsPromptDirty((wasDirty) => (wasDirty === nextDirty ? wasDirty : nextDirty));
	}, []);
	useEffect(() => {
		onDirtyChange?.(isDirty);
	}, [isDirty, onDirtyChange]);
	useEffect(() => () => onDirtyChange?.(false), [onDirtyChange]);
	useEffect(() => {
		onSubmittingChange?.(task.isSubmitting);
	}, [task.isSubmitting, onSubmittingChange]);
	useEffect(() => () => onSubmittingChange?.(false), [onSubmittingChange]);
	// The hook records a failure for display, so a rejected create just stays put.
	const settle = (created: Promise<string>) => void created.then(onCreated, () => undefined);

	return (
		<TaskComposerView
			autoFocusPrompt={autoFocusTitle}
			canSubmit={canSubmit}
			onPromptChange={handlePromptChange}
			labels={{
				addFile: t("newTask.addFile"),
				effort: t("settings.models.effort"),
				fallbackAction: fallbackAction === "bypass-permissions"
					? t("newTask.startWithoutApprovals", { defaultValue: "Start without approvals" })
					: t("newTask.createAsTui"),
				removeFile: (name) => t("newTask.removeFile", { name }),
				runsWith: t("newTask.runsWith"),
				start: createLabel ?? t("newTask.start"),
				starting: t("newTask.starting"),
				task: t("newTask.task"),
				taskPlaceholder,
			}}
			agent={{
				label: t("newTask.agent"),
				placeholder: t("newTask.selectAgent"),
				value: selectedAgent,
				agents: isCloudProject ? cloudAgents : agentCatalog?.agents,
				disabled:
				isSubmitting || (!isCloudProject && agentsQuery.isFetching && agentCatalog === undefined),
				onChange: (value) => {
					if (selectedAgent) {
						agentDrafts[selectedAgent] = {
							model: selectedModel ? requestedModel ?? "" : "",
							mode: selectedMode ? requestedModel ?? "" : "",
					...(requestedEffort !== undefined ? { effort: requestedEffort } : {}),
						};
					}
					setAgent(value);
					setAgentTouched(true);
					setModel("");
					setMode("");
					setEffort("");
					setModelTouched(false);
					setEffortTouched(false);
				},
			}}
			model={{
				agentId: selectedAgent,
				agentLabel: selectedAgentLabel,
				projectId: isStandalone ? "" : (projectId ?? ""),
				disabled: isSubmitting,
				value: selectedModel,
				mode: selectedMode,
				catalog: modelCatalog,
				fetching: modelCatalogQuery.isFetching,
				loading:
					selectedAgent !== "" &&
					modelCatalogQuery.isFetching &&
					modelCatalogQuery.data === undefined,
				onModelChange: (value) => {
					setModel(value);
					setMode("");
					setModelTouched(true);
					// Effort levels are per-model, so a level the newly chosen model
					// does not advertise has to be dropped rather than carried over.
					const nextEfforts =
						modelCatalog?.models?.find((item) => item.id === value)?.efforts ?? [];
					setEffort((current) => (current !== "" && !nextEfforts.includes(current) ? "" : current));
				},
				onModeChange: (value) => {
					setMode(value);
					setModel("");
					setModelTouched(true);
					// A mode replaces the model entirely, so no model vouches for a
					// previously chosen level any more.
					setEffort("");
				},
			}}
			effort={{
				disabled: isSubmitting,
				options: effortOptions,
				value: effectiveEffort,
				onChange: (value) => {
					setEffort(value);
					setEffortTouched(true);
				},
			}}
			attachments={{
				items: attachments.map(({ id, name, dataUrl }) => ({ id, name, previewUrl: dataUrl })),
				error: attachmentError,
				onAddFiles: (files) => void addFiles(files),
				onRemove: removeAttachment,
			}}
			submission={{
				showFallbackAction: fallbackAction !== undefined,
				error: error ?? remoteLoadError ?? undefined,
				isSubmitting,
				modelWarning,
				onFallbackAction: (brief) =>
					void (fallbackAction === "bypass-permissions"
						? submitTask(brief, selectedAgent === "unreal-agent" ? "chat" : undefined, "bypass-permissions")
						: submitTask(brief, "tui")),
				onSubmit: (brief) => void submitTask(brief, selectedAgent === "unreal-agent" ? "chat" : requiresTuiFallback ? "tui" : undefined),
			}}
			renderAgentControl={(control) => <DesktopAgentControl {...control} hostId={hostId} manageView={isCloudProject ? "cloud" : "local"} />}
			renderEffortControl={(control) => <TaskEffortPicker {...control} value={control.value || cloudDefaultEffort} defaultEffort={inheritedEffort || effortModel?.defaultEffort} availability={effortAvailability} />}
			renderModelControl={(control) => <TaskModelPicker {...control} onRefresh={refreshSelectedModels}
				showFollowAgentAction={Boolean(catalogDefaultOption || !isConcreteModelID(projectModelOrMode))} />}
			showEffort={!requiresTuiFallback && (effortOptions.length > 0 || Boolean(effort && effort !== "default"))}
		/>
	);
}

// The agent, model and effort menus share one compact width.
const COMPOSER_MENU_WIDTH = "w-[min(14rem,calc(100vw-2rem))]! min-w-0! max-w-[calc(100vw-2rem)]!";

function TaskEffortPicker({
	disabled,
	label,
	onChange,
	options,
	value,
	defaultEffort,
	availability,
}: TaskComposerEffortControl & { defaultEffort?: string; availability: EffortAvailability }) {
	return (
		<EffortPicker
			label={label}
			disabled={disabled}
			value={value.toLowerCase() === "default" ? "" : value}
			choices={options.map((option) => ({ value: option }))}
			defaultEffort={defaultEffort}
			availability={availability}
			onChange={onChange}
			triggerClassName="composer-chip composer-toolbar-option w-fit"
			menuClassName={COMPOSER_MENU_WIDTH}
		/>
	);
}

// Both local and cloud list only harnesses that can run, plus a way to manage
// them: local logins, or cloud connections for cloud projects.
function DesktopAgentControl({ hostId, manageView, ...control }: TaskComposerAgentControl & { hostId?: string; manageView: "local" | "cloud" }) {
	const { t } = useTranslation();
	return (
		<RequiredAgentField
			{...control}
			hostId={hostId}
			manageView={manageView}
			managementLabel={(manageView === "cloud" ? t("agentSelector.manageCloud") : t("agentSelector.manage")).replace(/[.…]+$/u, "")}
			variant="chip"
			triggerClassName="composer-toolbar-option w-fit"
			contentClassName={COMPOSER_MENU_WIDTH}
		/>
	);
}

function TaskModelPicker({
	agentId,
	agentLabel,
	catalog,
	disabled,
	loading,
	value,
	mode,
	onModelChange,
	onModeChange,
	onRefresh,
	showFollowAgentAction,
}: TaskComposerModelControl & { onRefresh: () => Promise<void>; showFollowAgentAction: boolean }) {
	const { t } = useTranslation();

	// No agent selected: there is nothing loading and no model to choose yet, so
	// show a clear "select an agent" placeholder, never a spinner. This returns
	// before the loading check so a no-agent state can never render one.
	if (agentId === "") {
		return (
			<span
				className="composer-chip composer-toolbar-option w-fit cursor-not-allowed justify-start opacity-50"
				aria-disabled="true"
				aria-label={t("newTask.model")}
			>
				<span className="truncate text-settings-muted">{t("newTask.selectAgent")}</span>
			</span>
		);
	}

	if (loading) {
		return (
			<span
				className="composer-chip composer-toolbar-option w-fit cursor-not-allowed justify-start opacity-50"
				aria-label={t("newTask.model")}
			>
				<span
					className="inline-flex min-w-0 items-center gap-1.5"
					role="status"
					aria-label={t("settings.models.loading")}
					aria-busy="true"
				>
					<Loader2 className="size-icon-sm shrink-0 animate-spin text-settings-muted" aria-hidden="true" />
					<span className="truncate text-settings-muted">{t("settings.models.loading")}</span>
				</span>
			</span>
		);
	}

	if (catalog?.selectionMode === "mode") {
		const options = (catalog.models ?? []).filter((item) => isConcreteModelID(item.id)).map((item) => ({
			value: item.id,
			label: modelChoiceLabel(item),
		}));
		const explicitMode = isConcreteModelID(mode) ? mode : "";
		const defaultMode = catalog.models?.find((item) => item.isDefault && isConcreteModelID(item.id))?.id || "";
		const effectiveMode = explicitMode || defaultMode;
		const visibleModeLabel = options.find((option) => option.value === effectiveMode)?.label ?? t("settings.models.modeNotReported");
		return (
			<SettingsOptionMenu
				aria-label={t("newTask.model")}
				disabled={disabled || options.length === 0}
				value={effectiveMode}
				options={options}
				action={explicitMode && !defaultMode && showFollowAgentAction
					? { label: t("settings.models.useAgentMode"), onSelect: () => onModeChange("") }
					: undefined}
				triggerClassName="composer-chip composer-toolbar-option w-fit"
				menuAlign="start"
				menuClassName={COMPOSER_MENU_WIDTH}
				renderTrigger={() => (
					<span className="min-w-0 truncate text-control text-foreground" title={visibleModeLabel}>
						{visibleModeLabel}
					</span>
				)}
				onChange={(value) => onModeChange(value === defaultMode ? "" : value)}
			/>
		);
	}

	const displayModels = (catalog?.models ?? []).map((item) => {
		if (item.id === "auto") return { ...item, label: t("settings.models.autoRouteLabel") };
		return { ...item, label: agentModelDisplayLabel(agentId, item.label) };
	});
	const selectCatalogModel = (nextModel: string) => {
		onModelChange(nextModel);
	};

	return (
		<AgentModelCombobox
			key={agentId}
			aria-label={t("newTask.model")}
			value={value}
			models={displayModels}
			allowCustom={false}
			customModelEntry={catalog?.customModelEntry === "configured" ? "configured" : "none"}
			agentLabel={agentLabel}
			onRefresh={onRefresh}
			refreshing={catalog?.refreshState === "queued" || catalog?.refreshState === "refreshing"}
			refreshError={catalog?.refreshError}
			retryAt={catalog?.retryAt}
			disabled={disabled || agentId === ""}
			agentId={agentId}
			onChange={selectCatalogModel}
			compact
			recentScope={agentId}
			triggerClassName="composer-chip composer-toolbar-option w-fit"
			menuAlign="start"
			menuClassName={COMPOSER_MENU_WIDTH}
			renderTrigger={(label) => <span className="min-w-0 truncate text-control text-foreground" title={label}>{label}</span>}
		/>
	);
}
