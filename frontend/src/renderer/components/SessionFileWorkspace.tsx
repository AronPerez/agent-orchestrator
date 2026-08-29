import type { Ref } from "../lib/hosts";
import { FileContentPane } from "./FileContentPane";
import {
  FileAnnotationComposer,
  type FileAnnotationModel,
} from "./WorkspaceDiffView";

export function SessionFileWorkspace({
  annotation,
  path,
  session,
}: {
  annotation: FileAnnotationModel;
  path: string;
  session: Ref;
}) {
  const fileFeedbackActive =
    annotation.target?.path === path && annotation.target.side === "file";
  return (
    <section
      className="flex h-full min-h-0 flex-col bg-background"
      data-testid="session-file-workspace"
    >
      {fileFeedbackActive ? (
        <FileAnnotationComposer annotation={annotation} />
      ) : null}
      <div className="board-scrollbar min-h-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain">
        <FileContentPane
          annotation={annotation}
          path={path}
          session={session}
          split={false}
          wrap
        />
      </div>
    </section>
  );
}
