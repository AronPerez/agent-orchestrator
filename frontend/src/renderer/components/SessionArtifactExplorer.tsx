import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { ChevronLeft, ExternalLink } from "lucide-react";
import { isLocal, refKey, type Ref } from "../lib/hosts";
import {
  localArtifactUrl,
  MAX_ARTIFACT_TEXT_BYTES,
  readArtifactText,
} from "../lib/session-artifacts";
import { useUiStore } from "../stores/ui-store";
import type { SessionArtifact } from "../types/workspace";
import { Button } from "./ui/button";
import { WorkspaceEntryIcon } from "./WorkspaceEntryIcon";
import { PanelMessage, RetryButton } from "./WorkspaceDiffView";
import { HighlightedContent } from "./ReadOnlyFileView";
import { MarkdownFileView } from "./markdown/MarkdownFileView";

type Props = {
  session: Ref;
  artifacts: SessionArtifact[];
  filter: string;
  onOpenPreview?: (url: string) => void;
};

export function SessionArtifactExplorer(props: Props) {
  const { t } = useTranslation();
  if (!window.ao)
    return <PanelMessage>{t("files.artifacts.webUnavailable")}</PanelMessage>;
  if (!isLocal(props.session.host))
    return <PanelMessage>{t("files.artifacts.localOnly")}</PanelMessage>;
  return <ArtifactList key={refKey(props.session)} {...props} />;
}

function ArtifactList({ session, artifacts, filter, onOpenPreview }: Props) {
  const { t } = useTranslation();
  const sessionKey = refKey(session);
  const selectedPath = useUiStore((state) =>
    state.inspectorSessions[sessionKey]?.selectedArtifactPath ?? null,
  );
  const setSelectedArtifactPath = useUiStore((state) => state.setSelectedArtifactPath);
  const selected = artifacts.find((artifact) => artifact.path === selectedPath);
  if (selectedPath !== null) {
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="flex h-8 shrink-0 items-center gap-1 border-b border-border px-1">
          <Button
            aria-label={t("files.explorer.backToTree")}
            onClick={() => setSelectedArtifactPath(sessionKey, null)}
            size="icon-sm"
            variant="ghost"
          >
            <ChevronLeft className="size-icon-sm" aria-hidden="true" />
          </Button>
          <span
            className="min-w-0 truncate font-mono text-xs text-muted-foreground"
            title={selectedPath}
          >
            {selectedPath}
          </span>
        </div>
        <div className="board-scrollbar min-h-0 flex-1 overflow-auto">
          {selected ? (
            <ArtifactFileView
              key={selectedPath}
              artifact={selected}
              session={session}
              onOpenPreview={onOpenPreview}
            />
          ) : (
            <PanelMessage>{t("files.artifacts.missing")}</PanelMessage>
          )}
        </div>
      </div>
    );
  }
  const filtered = artifacts.filter((artifact) =>
    artifact.path.toLowerCase().includes(filter.toLowerCase()),
  );
  return (
    <div className="board-scrollbar min-h-0 flex-1 overflow-auto p-1">
      {filtered.length === 0 ? (
        <PanelMessage>
          {t(
            artifacts.length
              ? "files.artifacts.noMatches"
              : "files.artifacts.empty",
          )}
        </PanelMessage>
      ) : (
        filtered.map((artifact) => (
          <Button
            key={artifact.path}
            variant="ghost"
            className="h-8 w-full justify-start gap-2 px-2 text-xs font-normal"
            onClick={() => setSelectedArtifactPath(sessionKey, artifact.path)}
            title={artifact.path}
          >
            <WorkspaceEntryIcon
              kind="file"
              name={artifact.name}
              className="size-icon-sm shrink-0"
            />
            <span className="truncate">{artifact.path}</span>
          </Button>
        ))
      )}
    </div>
  );
}

function ArtifactFileView({
  artifact,
  session,
  onOpenPreview,
}: {
  artifact: SessionArtifact;
  session: Ref;
  onOpenPreview?: (url: string) => void;
}) {
  const { t } = useTranslation();
  const rawUrl = localArtifactUrl(session, artifact.rawUrl);
  const previewUrl = localArtifactUrl(session, artifact.previewUrl);
  const tooLarge = artifact.size > MAX_ARTIFACT_TEXT_BYTES;
  const query = useQuery({
    queryKey: [
      "session-artifact",
      refKey(session),
      artifact.path,
      rawUrl,
      artifact.updatedAt,
      artifact.size,
    ],
    queryFn: ({ signal }) => readArtifactText(rawUrl!, signal),
    enabled: artifact.kind !== "html" && Boolean(rawUrl) && !tooLarge,
    retry: false,
  });
  if (artifact.kind === "html") {
    return previewUrl && onOpenPreview ? (
      <div className="p-3">
        <Button
          size="sm"
          variant="outline"
          onClick={() => onOpenPreview(previewUrl)}
        >
          <ExternalLink className="size-icon-sm" aria-hidden="true" />
          {t("files.artifacts.openBrowser")}
        </Button>
      </div>
    ) : (
      <PanelMessage>{t("files.artifacts.noUrl")}</PanelMessage>
    );
  }
  if (!rawUrl) return <PanelMessage>{t("files.artifacts.noUrl")}</PanelMessage>;
  if (tooLarge || query.error?.message === "too-large")
    return <PanelMessage>{t("files.artifacts.tooLarge")}</PanelMessage>;
  if (query.isPending)
    return <PanelMessage>{t("files.artifacts.loading")}</PanelMessage>;
  if (query.error?.message === "binary")
    return <PanelMessage>{t("files.binaryUnavailable")}</PanelMessage>;
  if (query.error)
    return (
      <PanelMessage
        action={<RetryButton onClick={() => void query.refetch()} />}
      >
        {t("files.artifacts.loadError", { reason: query.error.message })}
      </PanelMessage>
    );
  return artifact.kind === "markdown" ? (
    <MarkdownFileView
      session={session}
      filePath={artifact.path}
      content={query.data ?? ""}
      truncated={false}
      version={Date.parse(artifact.updatedAt)}
      artifactUrl={rawUrl}
    />
  ) : (
    <HighlightedContent content={query.data ?? ""} path={artifact.path} />
  );
}
