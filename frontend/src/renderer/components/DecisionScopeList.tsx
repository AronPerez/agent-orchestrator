import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { ChevronRight, Folder } from "@/components/app-icons";
import { readFoldPreferences, rememberDecisionFold, writeFoldPreferences } from "../lib/decision-folds";
import {
	DECISION_LIST_FOLD_STORAGE_KEY,
	type DecisionScope,
	type DecisionScopeProject,
	type DecisionScopeStream,
	type DecisionScopeTree,
} from "../lib/decision-scope";
import { cn } from "../lib/utils";
import { DecisionStateDot, decisionStateLabel } from "./DecisionStateLabel";
import { Button } from "./ui/button";
import { Pressable } from "./ui/pressable";

export type DecisionScopeListProps = {
	tree: DecisionScopeTree;
	/** The scope the board is narrowed to; empty for every project. */
	scope: DecisionScope;
	onChoose: (scope: DecisionScope) => void;
	className?: string;
};

type Row =
	| { kind: "all"; id: string }
	| { kind: "project"; id: string; project: DecisionScopeProject; expanded: boolean }
	| { kind: "stream"; id: string; project: DecisionScopeProject; stream: DecisionScopeStream };

const ALL_ROW = "all";
const projectRowId = (projectKey: string) => JSON.stringify(["project", projectKey]);
const streamRowId = (projectKey: string, streamKey: string) => JSON.stringify(["stream", projectKey, streamKey]);

function scopeOf(row: Row): DecisionScope {
	if (row.kind === "all") return {};
	if (row.kind === "project") return { project: row.project.key };
	return { project: row.project.key, stream: row.stream.key };
}

/**
 * The list beside the Decisions board: every project, then each project with
 * its streams. Choosing a row narrows the board to it. Each row says how many
 * of its calls need the person, or otherwise how many are still open.
 *
 * It is a tree with one tab stop, moved with the arrow keys as the Fleet tree
 * is. A project's streams start folded away only while nothing in it is open,
 * and the person's own folds are saved.
 */
export function DecisionScopeList({ tree, scope, onChoose, className }: DecisionScopeListProps) {
	const { t } = useTranslation();
	const [folds, setFolds] = useState(() => readFoldPreferences(DECISION_LIST_FOLD_STORAGE_KEY));
	const loaded = useRef(folds);
	useEffect(() => {
		if (folds !== loaded.current) writeFoldPreferences(DECISION_LIST_FOLD_STORAGE_KEY, folds);
	}, [folds]);

	const rows = useMemo(() => {
		const list: Row[] = [{ kind: "all", id: ALL_ROW }];
		for (const project of tree.projects) {
			// A saved fold wins. Otherwise a project is open while it has open calls, or holds the chosen stream.
			const holdsChosenStream = scope.project === project.key && scope.stream !== undefined;
			const expanded = folds[project.key] === undefined ? project.open > 0 || holdsChosenStream : !folds[project.key];
			list.push({ kind: "project", id: projectRowId(project.key), project, expanded });
			if (!expanded) continue;
			for (const stream of project.streams) list.push({ kind: "stream", id: streamRowId(project.key, stream.key), project, stream });
		}
		return list;
	}, [folds, scope.project, scope.stream, tree.projects]);
	const setExpanded = (project: DecisionScopeProject, expanded: boolean) => setFolds((current) => rememberDecisionFold(current, project.key, !expanded));

	const selectedId = scope.project === undefined
		? ALL_ROW
		: scope.stream === undefined ? projectRowId(scope.project) : streamRowId(scope.project, scope.stream);

	// Roving focus: one row is the tab stop; arrows move it.
	const treeRef = useRef<HTMLDivElement>(null);
	const [focusedId, setFocusedId] = useState<string | undefined>(undefined);
	const has = (id: string | undefined) => id !== undefined && rows.some((row) => row.id === id);
	const tabStop = has(focusedId) ? focusedId : has(selectedId) ? selectedId : ALL_ROW;
	const focusRow = (row: Row | undefined) => {
		if (!row) return;
		setFocusedId(row.id);
		treeRef.current?.querySelector<HTMLElement>(`[data-decision-scope-row="${CSS.escape(row.id)}"]`)?.focus();
	};
	const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
		const id = (event.target as HTMLElement).getAttribute?.("data-decision-scope-row");
		const index = rows.findIndex((row) => row.id === id);
		const row = rows[index];
		if (!row) return;
		switch (event.key) {
			case "ArrowDown": focusRow(rows[index + 1]); break;
			case "ArrowUp": focusRow(rows[index - 1]); break;
			case "Home": focusRow(rows[0]); break;
			case "End": focusRow(rows[rows.length - 1]); break;
			case "ArrowRight":
				if (row.kind !== "project") return;
				if (row.expanded) focusRow(rows[index + 1]?.kind === "stream" ? rows[index + 1] : undefined);
				else setExpanded(row.project, true);
				break;
			case "ArrowLeft":
				if (row.kind === "project" && row.expanded) setExpanded(row.project, false);
				else if (row.kind === "stream") focusRow(rows.find((item) => item.id === projectRowId(row.project.key)));
				else return;
				break;
			case "Enter":
			case " ":
				onChoose(scopeOf(row));
				break;
			default: return;
		}
		event.preventDefault();
	};

	const rowName = (row: Row): string => {
		if (row.kind === "all") return t("decisions.scope.all");
		if (row.kind === "project") return row.project.name ?? t("decisions.noProject");
		if (row.stream.projectWide) return t("decisions.projectWide");
		return [row.stream.name ?? "", row.stream.machine].filter(Boolean).join(" · ");
	};
	const needsYouLabel = decisionStateLabel("needs_you", t);
	const countLabel = (needsYou: number, open: number) => needsYou > 0
		? t("decisions.countFor", { state: needsYouLabel, count: needsYou })
		: open > 0 ? t("decisions.scope.open", { count: open }) : "";

	return (
		<nav aria-label={t("decisions.scope.label")} className={cn("flex min-h-0 w-58 shrink-0 flex-col overflow-y-auto overscroll-contain border-r border-border p-1.5", className)}>
			<div
				ref={treeRef}
				role="tree"
				aria-label={t("decisions.scope.label")}
				className="flex flex-col gap-px"
				onKeyDown={onKeyDown}
				onFocus={(event) => {
					const id = (event.target as HTMLElement).getAttribute?.("data-decision-scope-row");
					if (id) setFocusedId(id);
				}}
			>
				{rows.map((row) => {
					const counts = row.kind === "all" ? tree : row.kind === "project" ? row.project : row.stream;
					const name = rowName(row);
					const selected = row.id === selectedId;
					return (
						<Pressable asChild key={row.id}>
							<div
								role="treeitem"
								aria-level={row.kind === "stream" ? 2 : 1}
								aria-selected={selected}
								aria-expanded={row.kind === "project" ? row.expanded : undefined}
								aria-label={[name, countLabel(counts.needsYou, counts.open)].filter(Boolean).join(", ")}
								data-decision-scope-row={row.id}
								tabIndex={row.id === tabStop ? 0 : -1}
								onClick={() => onChoose(scopeOf(row))}
								className={cn(
									"flex w-full min-w-0 cursor-default items-center gap-1.5 rounded-md pr-2 text-muted-foreground hover:bg-interactive-hover hover:text-foreground focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring",
									row.kind === "stream" ? "min-h-7 pl-8 text-xs" : "min-h-8 text-control font-medium text-foreground",
									row.kind === "all" && "pl-2",
									row.kind === "project" && "pl-1",
									row.kind === "project" && row.project.name === undefined && "font-normal text-muted-foreground",
									selected && "bg-interactive-active text-foreground hover:bg-interactive-active",
								)}
							>
								{row.kind === "project" ? (
									<>
										<Button
											aria-label={t(row.expanded ? "decisions.scope.collapse" : "decisions.scope.expand", { project: name })}
											size="icon-xs"
											tabIndex={-1}
											variant="quiet"
											onClick={(event) => {
												// Folding the streams away is not choosing the project.
												event.stopPropagation();
												setExpanded(row.project, !row.expanded);
											}}
										>
											<ChevronRight aria-hidden="true" className={cn("size-3 transition-transform duration-(--duration-fast) motion-reduce:transition-none", row.expanded && "rotate-90")} />
										</Button>
										<Folder aria-hidden="true" className="size-icon-sm shrink-0 text-muted-foreground" />
									</>
								) : null}
								<span className="min-w-0 flex-1 truncate" title={name}>{name}</span>
								{/* Decoration: the row's name already says the count. The dot marks needs-you by shape as well as colour. */}
								<span aria-hidden="true" className="flex shrink-0 items-center gap-1 pl-1 text-xs font-normal tabular-nums">
									{counts.needsYou > 0 ? (
										<>
											<span data-slot="decision-scope-dot" className="inline-flex"><DecisionStateDot state="needs_you" /></span>
											<span className="font-medium text-status-needs-you">{counts.needsYou}</span>
										</>
									) : counts.open > 0 ? <span>{counts.open}</span> : null}
								</span>
							</div>
						</Pressable>
					);
				})}
			</div>
		</nav>
	);
}
