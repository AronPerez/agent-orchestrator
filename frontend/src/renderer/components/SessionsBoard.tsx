import {
  memo,
  useCallback,
  useEffect,
  useRef,
  useState,
  type MouseEvent,
} from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
  SessionsArchiveView,
  SessionsBoardGridView,
  archiveToggleOffsetClassName,
} from "@aoagents/product-ui";
import { AlertTriangle, LayoutDashboard, Plus, RotateCw } from "lucide-react";
import {
  type WorkspaceSession,
  flattenHostSections,
  hasConfiguredOrchestratorAgent,
  newestActiveOrchestrator,
  orchestratorHealth,
  workerSessions,
} from "../types/workspace";
import {
  boardAttentionZoneOrder,
  getAgentActivityView,
  getAttentionZoneViewForZone,
  getSessionStatusView,
  type AttentionZoneView,
} from "../lib/session-presentation";
import { matchScore, type MatchTarget } from "../lib/command-palette";
import { isDialogOrMenuOpen } from "../lib/dom-selectors";
import { matchesRendererShortcut } from "../stores/keybindings-store";
import {
  useSessionUsageSummaries,
  type SessionUsageSummary,
} from "../hooks/useSessionUsageSummaries";
import { useRestoreSession } from "../hooks/useRestoreSession";
import { useTerminateSession } from "../hooks/useTerminateSession";
import {
  cloudSessionsQueryKey,
  useWorkspaceQuery,
  workspaceQueryKey,
} from "../hooks/useWorkspaceQuery";
import { NotificationCenter } from "./NotificationCenter";
import { BoardFindBar } from "./BoardFindBar";
import { BoardWelcome, ProjectBoardEmpty } from "./BoardEmptyStates";
import { OrchestratorIcon } from "./icons";
import { OrchestratorActivityIndicator } from "./OrchestratorActivityIndicator";
import {
  TopbarActionError,
  TopbarButton,
  topbarProjectLabelClass,
} from "./TopbarButton";
import { spawnCloudOrchestrator } from "../lib/cloud-orchestrator";
import {
  isChatPreflightError,
  spawnOrchestrator,
} from "../lib/spawn-orchestrator";
import { restartProjectOrchestrator } from "../lib/restart-orchestrator";
import {
  hasBrowserDaemon,
  usesPreviewWorkspaceData,
} from "../lib/preview-mode";
import {
  isLinuxPlatform,
  isMacPlatform,
  usesBoardActionsInPanel,
} from "../lib/platform";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import { RestoreUnavailableDialog } from "./RestoreUnavailableDialog";
import { DaemonStartupLoader } from "./DaemonStartupLoader";
import { useShellMaybe } from "../lib/shell-context";
import { refKey, type Ref } from "../lib/hosts";
import { hostActionSuffix } from "../lib/host-disclosure";
import { useSystemRequirementsGate } from "../hooks/useSystemRequirementsGate";
import {
  ArchivedSessionCardAdapter,
  BoardSessionCardAdapter,
  sessionsBoardLabels,
} from "./SessionsBoardAdapters";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

type SessionsBoardProps = {
  /** When set, the board shows only this project's sessions. */
  project?: Ref;
};

type UsageBySession = ReadonlyMap<string, SessionUsageSummary>;
const emptyUsageBySession: UsageBySession = new Map();

// Live merged sessions remain in-flow. A terminated runtime is archived even
// when its SCM outcome remains `merged`.
function isArchivedSession(session: WorkspaceSession): boolean {
  return session.isTerminated === true || session.status === "terminated";
}

// The find bar reuses the command palette's scorer rather than growing a second
// matcher; a session only has to be shaped into what that scorer reads.
function sessionMatchTarget(
  session: WorkspaceSession,
  t: TFunction,
): MatchTarget {
  return {
    title: session.title,
    subtitle: session.branch,
    keywords: [
      session.workspaceName,
      getSessionStatusView(session.status, t).label,
      ...session.prs.flatMap((pr) => [`#${pr.number}`, String(pr.number)]),
    ],
  };
}

function filterSessions(
  sessions: WorkspaceSession[],
  query: string,
  t: TFunction,
): WorkspaceSession[] {
  if (!query.trim()) return sessions;
  return sessions.filter(
    (session) => matchScore(query, sessionMatchTarget(session, t)) > 0,
  );
}

const isMac = isMacPlatform();
const dragStyle = isMac
  ? ({ WebkitAppRegion: "drag" } as React.CSSProperties)
  : undefined;
const noDragStyle = isMac
  ? ({ WebkitAppRegion: "no-drag" } as React.CSSProperties)
  : undefined;

export function SessionsBoard({ project }: SessionsBoardProps) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const columns: AttentionZoneView[] = boardAttentionZoneOrder.map((zone) =>
    getAttentionZoneViewForZone(zone, t),
  );
  const workspaceQuery = useWorkspaceQuery();
  const shell = useShellMaybe();
  const usageBySession =
    useSessionUsageSummaries(project).data ?? emptyUsageBySession;
  // Evaluated at render so platform mocks in tests can flip the in-panel chrome.
  const boardActionsInPanel = usesBoardActionsInPanel();
  /** Bell lives in the board action row when the shell topbar does not host it. */
  const boardOwnsNotificationCenter = isLinuxPlatform() || boardActionsInPanel;
  const all = flattenHostSections(workspaceQuery.data);
  const workspaces = project
    ? all.filter(
        (workspace) =>
          workspace.host === project.host && workspace.id === project.id,
      )
    : all;
  const workspace = project ? workspaces[0] : undefined;
  const projectKey = workspace ? refKey(workspace) : undefined;
  // Board chrome stays route-oriented; project context remains in the sidebar.
  const boardLabel = t("shell.board");
  const sessions = workspaces.flatMap((workspace) =>
    workerSessions(workspace.sessions),
  );
  const orchestrator = project
    ? newestActiveOrchestrator(workspaces[0]?.sessions ?? [])
    : undefined;
  const orchestratorActivityLabel = orchestrator
    ? getAgentActivityView(orchestrator.activity, t).label
    : undefined;
  const [isSpawning, setIsSpawning] = useState(false);
  const [isFindOpen, setIsFindOpen] = useState(false);
  const [findQuery, setFindQuery] = useState("");
  const [spawnError, setSpawnError] = useState<string | null>(null);
  const [canCreateAsTui, setCanCreateAsTui] = useState(false);
  const restartingProjectIds = useUiStore(
    (state) => state.restartingProjectIds,
  );
  const orchestratorStartupError = useUiStore((state) =>
    projectKey ? (state.orchestratorStartupErrors[projectKey] ?? null) : null,
  );
  const setProjectRestarting = useUiStore(
    (state) => state.setProjectRestarting,
  );
  const setOrchestratorReplacementError = useUiStore(
    (state) => state.setOrchestratorReplacementError,
  );
  const setOrchestratorStartupError = useUiStore(
    (state) => state.setOrchestratorStartupError,
  );
  const requestNewTask = useUiStore((state) => state.requestNewTask);
  const isProjectRestarting = projectKey
    ? restartingProjectIds.has(projectKey)
    : false;
  const health = workspace
    ? orchestratorHealth(workspace, isProjectRestarting)
    : { state: "ok" as const };
  const visibleSpawnError = spawnError ?? orchestratorStartupError;

  // The board instance survives project-to-project navigation (same route,
  // new param), so a spawn failure must not follow the user to another board.
  useEffect(() => {
    setSpawnError(null);
    setCanCreateAsTui(false);
    setIsFindOpen(false);
    setFindQuery("");
  }, [projectKey]);
  const previousProjectRef = useRef<Ref | undefined>(workspace);
  useEffect(() => {
    const previousProject = previousProjectRef.current;
    if (previousProject && refKey(previousProject) !== projectKey) {
      setOrchestratorStartupError(previousProject, null);
    }
    previousProjectRef.current = workspace;
  }, [projectKey, setOrchestratorStartupError, workspace]);
  useEffect(() => {
    if (workspace && orchestrator && orchestratorStartupError) {
      setOrchestratorStartupError(workspace, null);
    }
  }, [
    orchestrator,
    orchestratorStartupError,
    setOrchestratorStartupError,
    workspace,
  ]);

  const archived = filterSessions(
    sessions
      .filter(isArchivedSession)
      .sort((left, right) => right.updatedAt.localeCompare(left.updatedAt)),
    findQuery,
    t,
  );
  const activeSessions = filterSessions(
    sessions.filter((candidate) => !isArchivedSession(candidate)),
    findQuery,
    t,
  );
  const findMatches = activeSessions.length + archived.length;
  const boardLabels = sessionsBoardLabels(t);
  // First-run orientation replaces the empty column shells (only once the
  // query has resolved, so the welcome never flashes over real data): the
  // global board teaches the app before any project exists, and a fresh
  // project board invites the first task instead of showing four zeros.
  const isDaemonReady =
    usesPreviewWorkspaceData ||
    hasBrowserDaemon ||
    (shell ? shell.daemonStatus.state === "ready" : true);
  const daemonHasFailed = Boolean(shell?.daemonStatus.code);
  const workspaceStartupState = shell?.workspaceStartupState ?? "ready";
  // Requirements blocking (missing git/tmux/coding agent) must keep the
  // startup screen up even after the daemon and workspace query are both
  // otherwise ready — see useSystemRequirementsGate. Without this, the gate
  // was purely cosmetic: it would unmount on its own timer regardless of
  // whether the machine actually satisfied the requirements it was showing.
  const { blocked: requirementsBlocked } = useSystemRequirementsGate();
  const isLoaded =
    isDaemonReady &&
    workspaceStartupState === "ready" &&
    workspaceQuery.isSuccess &&
    !requirementsBlocked;
  const showStartup =
    shell !== null &&
    !daemonHasFailed &&
    (!isDaemonReady ||
      workspaceStartupState === "loading" ||
      (!workspaceQuery.isSuccess && !workspaceQuery.isError) ||
      requirementsBlocked);
  const showWelcome = !project && isLoaded && all.length === 0;
  const showProjectEmpty =
    project !== undefined &&
    isLoaded &&
    workspaces.length > 0 &&
    sessions.length === 0;
  const boardLoadFailed = Boolean(
    workspaceStartupState === "error" ||
    workspaceQuery.isError ||
    workspaceQuery.localFailure,
  );
  const showBoardGrid = !boardLoadFailed && !showWelcome && !showProjectEmpty;
  const showFindEmpty = findQuery.trim() !== "" && findMatches === 0;
  const hasArchive = archived.length > 0;

  // Renderer-side listener only: neither xterm nor a native WebContentsView is
  // mounted on the board, so the chord never needs a main-process intercept.
  useEffect(() => {
    if (!showBoardGrid) return;
    const handleKeyDown = (event: KeyboardEvent) => {
      // Dialogs and the command palette own the keyboard while they are open.
      if (isDialogOrMenuOpen()) return;
      if (isFindOpen && event.key === "Escape") {
        event.preventDefault();
        setIsFindOpen(false);
        setFindQuery("");
        return;
      }
      if (!matchesRendererShortcut("find", event)) return;
      const active = document.activeElement;
      if (
        active instanceof HTMLInputElement ||
        active instanceof HTMLTextAreaElement
      )
        return;
      event.preventDefault();
      setIsFindOpen(true);
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [isFindOpen, showBoardGrid]);
  const terminateSession = useTerminateSession();
  const activeProjectKeyRef = useRef(projectKey);
  activeProjectKeyRef.current = projectKey;

  const openSession = useCallback((session: WorkspaceSession) =>
    void navigate({
      to: "/host/$hostId/session/$sessionId",
      params: { hostId: session.host, sessionId: session.id },
    }), [navigate]);

  const openOrchestrator = async (mode?: "tui") => {
    if (!workspace || isProjectRestarting) return;
    if (orchestrator) {
      void navigate({
        to: "/host/$hostId/session/$sessionId",
        params: { hostId: orchestrator.host, sessionId: orchestrator.id },
      });
      return;
    }
    if (workspace.kind === "cloud") {
      setSpawnError(null);
      setIsSpawning(true);
      try {
        const sessionId = await spawnCloudOrchestrator(
          queryClient,
          workspace.id,
        );
        await queryClient.invalidateQueries({
          queryKey: cloudSessionsQueryKey,
        });
        void navigate({
          to: "/host/$hostId/session/$sessionId",
          params: { hostId: workspace.host, sessionId },
        });
      } catch (error) {
        console.error("Failed to spawn cloud orchestrator:", error);
        setSpawnError(
          error instanceof Error ? error.message : t("shell.couldNotSpawn"),
        );
      } finally {
        setIsSpawning(false);
      }
      return;
    }
    if (!hasConfiguredOrchestratorAgent(workspace)) {
      if (workspace) useUiStore.getState().openProjectSettings(workspace);
      return;
    }
    setSpawnError(null);
    setCanCreateAsTui(false);
    setOrchestratorStartupError(workspace, null);
    setIsSpawning(true);
    try {
      const sessionId = await spawnOrchestrator(
        workspace,
        "board",
        false,
        mode,
      );
      await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
      setOrchestratorStartupError(workspace, null);
      void navigate({
        to: "/host/$hostId/session/$sessionId",
        params: { hostId: workspace.host, sessionId },
      });
    } catch (error) {
      // Never fail silently: the daemon's message (e.g. a worktree/branch
      // conflict) is the only actionable signal the user gets.
      console.error("Failed to spawn orchestrator:", error);
      setSpawnError(
        error instanceof Error ? error.message : t("shell.couldNotSpawn"),
      );
      setCanCreateAsTui(isChatPreflightError(error));
    } finally {
      setIsSpawning(false);
    }
  };

  const restartOrchestrator = async () => {
    if (!workspace) return;
    await restartProjectOrchestrator({
      project: workspace,
      queryClient,
      navigate,
      setProjectRestarting,
      setOrchestratorReplacementError,
    });
  };

  const actions = project ? (
    <>
      {visibleSpawnError && !showProjectEmpty && (
        <TopbarActionError
          className="max-w-content-max truncate"
          title={visibleSpawnError}
        >
          {visibleSpawnError}
        </TopbarActionError>
      )}
      {visibleSpawnError && canCreateAsTui && !showProjectEmpty ? (
        <TopbarButton
          disabled={isSpawning || isProjectRestarting}
          onClick={() => void openOrchestrator("tui")}
        >
          {t("newTask.createAsTui")}
        </TopbarButton>
      ) : null}
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="inline-flex">
            <TopbarButton
              aria-label={t("shell.newTask")}
              className="topbar-control--labeled"
              data-priority="primary"
              disabled={isProjectRestarting}
              onClick={() => workspace && requestNewTask(workspace)}
              variant="accent"
            >
              <Plus className="size-icon-md" aria-hidden="true" />
              <span data-compact-label>{t("newTask.task")}</span>
            </TopbarButton>
          </span>
        </TooltipTrigger>
        <TooltipContent side="bottom">{t("shell.newTask")}</TooltipContent>
      </Tooltip>
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="inline-flex">
            <TopbarButton
              aria-label={
                orchestratorActivityLabel
                  ? t("shell.orchestratorWithActivity", {
                      activity: orchestratorActivityLabel,
                    })
                  : t("shell.spawnOrchestrator")
              }
              className="topbar-control--labeled"
              data-priority="secondary"
              disabled={isSpawning || isProjectRestarting}
              onClick={() => void openOrchestrator()}
              variant="primary"
            >
              <OrchestratorIcon className="size-icon-md" aria-hidden="true" />
              <span data-compact-label>{t("shell.orchestrator")}</span>
              {orchestrator ? (
                <OrchestratorActivityIndicator session={orchestrator} />
              ) : null}
            </TopbarButton>
          </span>
        </TooltipTrigger>
        <TooltipContent side="bottom">
          {isProjectRestarting
            ? t("shell.restarting")
            : isSpawning
              ? t("shell.spawning")
              : orchestrator
                ? t("shell.openOrchestrator")
                : t("shell.spawnOrchestrator")}
        </TooltipContent>
      </Tooltip>
      {boardOwnsNotificationCenter ? (
        <>
          <span
            aria-hidden="true"
            className="workspace-topbar__utility-separator"
          />
          <NotificationCenter />
        </>
      ) : null}
    </>
  ) : boardOwnsNotificationCenter ? (
    <NotificationCenter />
  ) : undefined;

  return (
    <div
      className="relative flex h-full min-h-0 flex-col bg-background text-foreground"
      data-testid="board"
    >
      {/* macOS: shell topbar is hidden on board routes, so the project/"Board"
			    crumb + New task / Orchestrator / bell live in this in-panel row.
			    Win/Linux keep the crumb and actions in the framed ShellTopbar.
			    Welcome skips the row — a dangling "Board" above the import
			    chooser was review feedback on #2432. */}
      {!showWelcome && boardActionsInPanel && (boardLabel || actions) ? (
        <div
          className="workspace-topbar-container center-panel-titlebar flex h-toolbar shrink-0 items-center gap-2 border-b border-border-strong pr-4"
          style={dragStyle}
        >
          {boardLabel ? (
            <span
              className={cn(
                topbarProjectLabelClass,
                "inline-flex items-center gap-1.5",
              )}
              data-testid="board-topbar-label"
            >
              <LayoutDashboard aria-hidden="true" className="size-icon-md" />
              {boardLabel}
            </span>
          ) : null}
          <div className="min-w-0 flex-1" />
          {actions ? (
            <div
              className="workspace-topbar-actions flex shrink-0 items-center"
              style={noDragStyle}
            >
              {actions}
            </div>
          ) : null}
        </div>
      ) : null}

      <div
        className={cn(
          "relative min-h-0 flex-1 overflow-hidden",
          hasArchive && archiveToggleOffsetClassName,
        )}
      >
        {showBoardGrid && isFindOpen ? (
          <BoardFindBar
            matches={findMatches}
            onClose={() => {
              setIsFindOpen(false);
              setFindQuery("");
            }}
            onQueryChange={setFindQuery}
            query={findQuery}
            total={sessions.length}
          />
        ) : null}
        {project && health.state !== "ok" ? (
          <div className="mx-3 my-3 flex items-center gap-3 rounded-md border border-border bg-surface px-3 py-2 text-xs text-muted-foreground">
            <AlertTriangle
              className="size-icon-base shrink-0 text-warning"
              aria-hidden="true"
            />
            <span className="min-w-0 flex-1">{health.message}</span>
            {health.state === "restart_needed" ||
            health.state === "duplicates" ? (
              <TopbarButton
                disabled={isProjectRestarting}
                onClick={() => void restartOrchestrator()}
                variant="primary"
              >
                <RotateCw className="size-3.5" aria-hidden="true" />
                {t("shell.restart")}
                {workspace ? hostActionSuffix(t, workspace.host) : ""}
              </TopbarButton>
            ) : null}
          </div>
        ) : null}
        {boardLoadFailed ? (
          <p className="py-10 text-center text-xs text-passive">
            {t("shell.couldNotLoadSessions")}
          </p>
        ) : showWelcome ? (
          <BoardWelcome />
        ) : showProjectEmpty ? (
          <ProjectBoardEmpty
            hasOrchestrator={orchestrator !== undefined}
            isSpawning={isSpawning}
            isProjectRestarting={isProjectRestarting}
            onNewTask={() => workspace && requestNewTask(workspace)}
            onOpenOrchestrator={() => void openOrchestrator()}
            onOpenOrchestratorAsTui={
              canCreateAsTui ? () => void openOrchestrator("tui") : undefined
            }
            spawnError={visibleSpawnError}
          />
        ) : showFindEmpty ? (
          <p className="py-10 text-center text-xs text-passive">
            {t("board.find.noResults")}
          </p>
        ) : (
          <SessionsBoardGridView
            columns={columns}
            key={projectKey ?? "all"}
            labels={boardLabels}
            renderSessionCard={(session) => (
              <BoardSessionCardAdapter
                onOpenSession={openSession}
                onTerminateSession={terminateSession.mutate}
                session={session}
                usage={usageBySession.get(refKey(session))}
              />
            )}
            sessions={activeSessions}
          />
        )}
      </div>

      {hasArchive ? (
        <BoardArchivePanel
          activeProjectKeyRef={activeProjectKeyRef}
          projectKey={projectKey}
          sessions={archived}
          usageBySession={usageBySession}
        />
      ) : null}
      {showStartup ? <DaemonStartupLoader /> : null}
    </div>
  );
}

/** Keep archive expansion and restore state from re-rendering the kanban lanes. */
const BoardArchivePanel = memo(function BoardArchivePanel({
  activeProjectKeyRef,
  projectKey,
  sessions,
  usageBySession,
}: {
  activeProjectKeyRef: { current: string | undefined };
  projectKey?: string;
  sessions: WorkspaceSession[];
  usageBySession: UsageBySession;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const restoreSession = useRestoreSession();
  const [restoringSessionKey, setRestoringSessionKey] = useState<string>();
  const [restoreErrors, setRestoreErrors] = useState<Record<string, string>>(
    {},
  );
  const [restoreUnavailableSession, setRestoreUnavailableSession] =
    useState<WorkspaceSession>();
  const restoreGenerationRef = useRef(0);

  useEffect(() => {
    setRestoringSessionKey(undefined);
    setRestoreErrors({});
    setRestoreUnavailableSession(undefined);
    restoreGenerationRef.current += 1;
  }, [projectKey]);

  useEffect(() => {
    const generation = restoreGenerationRef.current;
    return () => {
      if (restoreGenerationRef.current === generation)
        restoreGenerationRef.current += 1;
    };
  }, []);

  const restoreArchivedSession = async (
    event: MouseEvent<HTMLButtonElement>,
    session: WorkspaceSession,
  ) => {
    event.stopPropagation();
    if (restoringSessionKey) return;
    const sessionKey = refKey(session);
    const restoreProjectKey = projectKey;
    const generation = restoreGenerationRef.current;
    const isStillActiveProject = () =>
      generation === restoreGenerationRef.current &&
      (!restoreProjectKey || activeProjectKeyRef.current === restoreProjectKey);
    setRestoringSessionKey(sessionKey);
    setRestoreErrors((current) => {
      const next = { ...current };
      delete next[sessionKey];
      return next;
    });
    try {
      const result = await restoreSession(session);
      if (!isStillActiveProject()) return;
      if (result.status === "success") {
        void navigate({
          to: "/host/$hostId/session/$sessionId",
          params: { hostId: session.host, sessionId: session.id },
        });
        return;
      }
      if (result.status === "not_resumable") {
        setRestoreUnavailableSession(session);
        return;
      }
      setRestoreErrors((current) => ({
        ...current,
        [sessionKey]: result.message,
      }));
    } finally {
      if (isStillActiveProject()) setRestoringSessionKey(undefined);
    }
  };

  return (
    <>
      <SessionsArchiveView
        labels={{
          archive: t("shell.archive"),
          archiveAria: t("shell.archiveSessionsAria", {
            count: sessions.length,
          }),
          archivedSessions: t("shell.archivedSessions"),
        }}
        renderSessionCard={(session) => {
          const sessionKey = refKey(session);
          return (
            <ArchivedSessionCardAdapter
              isRestoreDisabled={restoringSessionKey !== undefined}
              isRestoring={restoringSessionKey === sessionKey}
              restoreAction={(event) =>
                void restoreArchivedSession(event, session)
              }
              restoreError={restoreErrors[sessionKey]}
              session={session}
              usage={usageBySession.get(sessionKey)}
            />
          );
        }}
        resetKey={projectKey}
        sessions={sessions}
      />
      {restoreUnavailableSession ? (
        <RestoreUnavailableDialog
          open={true}
          session={restoreUnavailableSession}
          onOpenChange={(open) => {
            if (!open) setRestoreUnavailableSession(undefined);
          }}
          onRecreated={async () => {
            await queryClient.invalidateQueries({
              queryKey: workspaceQueryKey,
            });
          }}
        />
      ) : null}
    </>
  );
});
