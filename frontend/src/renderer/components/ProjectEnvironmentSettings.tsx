import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Eye, EyeOff, FileCode2, Plus, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { projectQueryKey, useProjectSettingsQuery } from "../hooks/useProjectSettingsQuery";
import { clientForHost } from "../lib/host-clients";
import { LOCAL_HOST, refKey } from "../lib/hosts";
import type { ProjectSettingsSaveState } from "./ProjectSettingsForm";
import { Button } from "./ui/button";
import { ProjectSettingsSection } from "@aoagents/product-ui";

type Row = { name: string; value: string; visible: boolean };
type Project = components["schemas"]["Project"];

const rowsFromEnv = (env?: Record<string, string>): Row[] =>
	Object.entries(env ?? {}).map(([name, value]) => ({ name, value, visible: false }));

function parsePastedEnv(text: string): { rows: Row[]; invalidLine?: number } {
	const rows: Row[] = [];
	const names = new Set<string>();
	for (const [index, source] of text.split(/\r?\n/).entries()) {
		const line = source.trim();
		if (!line || line.startsWith("#")) continue;
		const match = /^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$/.exec(line);
		if (!match) return { rows: [], invalidLine: index + 1 };
		const name = match[1];
		const folded = name.toUpperCase();
		if (folded.startsWith("AO_") || names.has(folded)) return { rows: [], invalidLine: index + 1 };
		names.add(folded);
		let value = match[2].trim();
		if (value.startsWith('"') || value.startsWith("'")) {
			const quoted = /^(['"])(.*)\1(?:\s*#.*)?$/.exec(value);
			if (!quoted) return { rows: [], invalidLine: index + 1 };
			value = quoted[2];
			if (quoted[1] === '"') value = value.replace(/\\n/g, "\n").replace(/\\r/g, "\r");
		} else {
			value = value.replace(/\s+#.*$/, "").trimEnd();
		}
		if (value.includes("\0")) return { rows: [], invalidLine: index + 1 };
		rows.push({ name, value, visible: false });
	}
	return { rows };
}

export function ProjectEnvironmentSettings({ projectId, hostId, onSaveState }: { projectId: string; hostId?: string; onSaveState?: (state: ProjectSettingsSaveState) => void }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const { query, hostConnected } = useProjectSettingsQuery(projectId, hostId);
	useEffect(() => {
		if (!hostConnected) onSaveState?.({ phase: "idle" });
		else if (query.isError) onSaveState?.({ phase: "failed", error: apiErrorMessage(query.error) });
	}, [hostConnected, onSaveState, query.isError, query.error]);
	const offline = <p role="alert" className="text-pretty text-sm text-error">{t("remote.hostOffline")}</p>;
	if (!hostConnected && !query.data) return offline;
	if (query.isLoading) return <div className="project-settings-form" aria-busy="true" aria-label={t("settings.project.loading")}><section><div className="settings-grouped-rows flex w-full flex-col"><div className="settings-row-bar" /></div></section></div>;
	if (!query.data) return <div className="flex flex-col items-start gap-3"><p role="alert" className="text-sm text-error">{query.error instanceof Error ? query.error.message : t("settings.project.loadFailed")}</p><Button type="button" variant="outline" size="sm" onClick={() => void query.refetch()}>{t("settings.project.retry")}</Button></div>;
	return <>
		{!hostConnected ? offline : query.isError ? <div className="flex flex-col items-start gap-3"><p role="alert" className="text-sm text-error">{query.error instanceof Error ? query.error.message : t("settings.project.loadFailed")}</p><Button type="button" variant="outline" size="sm" onClick={() => void query.refetch()}>{t("settings.project.retry")}</Button></div> : null}
		<fieldset className="min-h-full min-w-0" hidden={!hostConnected} disabled={!hostConnected || query.isError}>
			<VariablesEditor
				key={refKey({ host: hostId ?? LOCAL_HOST, id: projectId })}
				hostId={hostId}
				hostConnected={hostConnected && !query.isError}
				projectId={projectId}
				initial={query.data.config?.env}
				onSaveState={onSaveState}
				onSaved={() => {
					void queryClient.invalidateQueries({ queryKey: projectQueryKey(projectId, hostId) });
					void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
				}}
			/>
		</fieldset>
	</>;
}

function VariablesEditor({ projectId, hostId, hostConnected, initial, onSaveState, onSaved }: {
	projectId: string;
	hostId?: string;
	hostConnected: boolean;
	initial?: Record<string, string>;
	onSaveState?: (state: ProjectSettingsSaveState) => void;
	onSaved: () => void;
}) {
	const { t } = useTranslation();
	const attempted = useRef<string | null>(null);
	const [rows, setRows] = useState(() => rowsFromEnv(initial));
	const [saved, setSaved] = useState(() => JSON.stringify(initial ?? {}));
	const [error, setError] = useState<string | null>(null);
	const [savedAt, setSavedAt] = useState(false);
	const [activeTab, setActiveTab] = useState<"variables" | "paste">("variables");
	const [pasteText, setPasteText] = useState("");
	const [importedCount, setImportedCount] = useState<number | null>(null);
	const filled = rows.filter(({ name, value }) => name !== "" || value !== "");
	const dirty = JSON.stringify(Object.fromEntries(filled.map(({ name, value }) => [name, value]))) !== saved;
	// The env map this draft would save, or null while a name is invalid or duplicated.
	const envDraft = (() => {
		const env: Record<string, string> = {};
		const names = new Set<string>();
		for (const row of filled) {
			const folded = row.name.toUpperCase();
			if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(row.name) || names.has(folded)) return null;
			names.add(folded);
			env[row.name] = row.value;
		}
		return env;
	})();
	const unsaveable = dirty && envDraft === null;
	const mutation = useMutation({
		mutationFn: async (env: Record<string, string>) => {
			if (!hostConnected) throw new Error(t("remote.hostOffline"));
			// Refresh before the whole-config PUT so this page cannot erase changes made elsewhere.
			const current = await (hostId ? clientForHost(hostId) : apiClient).GET("/api/v1/projects/{id}", { params: { path: { id: projectId } } });
			if (current.error) throw new Error(apiErrorMessage(current.error));
			if (current.data?.status !== "ok" || !current.data.project) throw new Error(t("settings.project.degraded"));
			const project = current.data.project as Project;
			const { error: updateError } = await (hostId ? clientForHost(hostId) : apiClient).PUT("/api/v1/projects/{id}", {
				params: { path: { id: projectId } },
				body: { displayName: project.name, config: { ...project.config, env } },
			});
			if (updateError) throw new Error(apiErrorMessage(updateError));
			return env;
		},
		onSuccess: (env) => {
			setSaved(JSON.stringify(env));
			setSavedAt(true);
			onSaved();
		},
	});
	useEffect(() => {
		if (!hostConnected) return;
		onSaveState?.({
			phase: mutation.isError ? "failed" : mutation.isPending ? "saving" : dirty ? "pending" : savedAt ? "saved" : "idle",
			dirty,
			unsaveable,
			requestPending: mutation.isPending,
			error: mutation.error instanceof Error ? mutation.error.message : undefined,
		});
	}, [hostConnected, dirty, mutation.error, mutation.isError, mutation.isPending, onSaveState, savedAt, unsaveable]);
	const showEmpty = rows.length === 0 && activeTab === "variables";
	const update = (next: Row[]) => { attempted.current = null; setRows(next); setError(null); setSavedAt(false); setImportedCount(null); };
	const importPasted = () => {
		const parsed = parsePastedEnv(pasteText);
		if (parsed.invalidLine || parsed.rows.length === 0) {
			setError(t("settings.project.envPasteInvalid", { line: parsed.invalidLine ?? 1 }));
			return;
		}
		const next = [...rows];
		for (const row of parsed.rows) {
			const existing = next.findIndex((item) => item.name.toUpperCase() === row.name.toUpperCase());
			if (existing < 0) next.push(row);
			else next[existing] = { ...row, name: next[existing].name };
		}
		update(next);
		setImportedCount(parsed.rows.length);
		setPasteText("");
		setActiveTab("variables");
	};
	// A draft that cannot be saved must still tell the dialog, or a pending close waits forever.
	const reject = (message: string) => {
		setError(message);
		onSaveState?.({ phase: "failed", dirty, requestPending: false, error: message });
	};
	const save = () => {
		if (activeTab === "paste" && pasteText.trim()) {
			reject(t("settings.project.envPastePending"));
			return;
		}
		const env: Record<string, string> = {};
		const names = new Set<string>();
		for (const row of filled) {
			const name = row.name;
			const folded = name.toUpperCase();
			if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name) || names.has(folded)) {
				reject(t("settings.project.envInvalid"));
				return;
			}
			names.add(folded);
			env[name] = row.value;
		}
		setError(null);
		mutation.mutate(env);
	};
	// Autosave: a draft with valid, unique names saves shortly after the last edit.
	useEffect(() => {
		if (!hostConnected || !dirty || mutation.isPending || activeTab !== "variables" || envDraft === null) return;
		const key = JSON.stringify(envDraft);
		if (attempted.current === key) return;
		const timer = window.setTimeout(() => { attempted.current = key; mutation.mutate(envDraft); }, 700);
		return () => window.clearTimeout(timer);
	}, [hostConnected, activeTab, dirty, mutation, rows]); // eslint-disable-line react-hooks/exhaustive-deps
	return <form id="project-settings-form" className="project-settings-form flex min-h-full flex-col gap-(--size-settings-section-inner-gap)" onSubmit={(event) => { event.preventDefault(); save(); }}>
		<p className="text-sm leading-5 text-settings-muted">{t("settings.project.environmentDescription")}</p>
		{showEmpty ? <ProjectSettingsSection title={t("settings.project.environmentVariables")} titleHidden grouped>
			<div className="settings-row-bar">
				<span className="text-sm leading-5 text-settings-label">{t("settings.project.environmentEmptyTitle")}</span>
				<div className="flex items-center gap-2">
					<Button onClick={() => { setActiveTab("paste"); setError(null); }} type="button" variant="ghost"><FileCode2 aria-hidden="true" />{t("settings.project.pasteVariables")}</Button>
					<Button onClick={() => update([...rows, { name: "", value: "", visible: false }])} type="button" variant="ghost"><Plus aria-hidden="true" />{t("settings.project.addVariable")}</Button>
				</div>
			</div>
		</ProjectSettingsSection> : activeTab === "variables" ? <><ProjectSettingsSection title={t("settings.project.environmentVariables")} titleHidden grouped>
			{rows.map((row, index) => <div className="settings-row-bar gap-2" key={index}>
				<input aria-label={`${t("settings.project.envName")} ${index + 1}`} className="settings-field-control h-(--size-settings-action-height) min-w-0 flex-1 rounded-md!" placeholder={t("settings.project.envName")} value={row.name} onChange={(event) => update(rows.map((item, i) => i === index ? { ...item, name: event.target.value } : item))} />
				<input aria-label={`${t("settings.project.envValue")} ${index + 1}`} autoComplete="off" className="settings-field-control h-(--size-settings-action-height) min-w-0 flex-1 rounded-md!" placeholder={t("settings.project.envValue")} type={row.visible ? "text" : "password"} value={row.value} onChange={(event) => update(rows.map((item, i) => i === index ? { ...item, value: event.target.value } : item))} />
				<Button aria-label={row.visible ? t("settings.project.hideVariable") : t("settings.project.showVariable")} className="size-7 shrink-0 p-0 text-settings-muted hover:text-foreground" onClick={() => update(rows.map((item, i) => i === index ? { ...item, visible: !item.visible } : item))} size="icon-sm" title={row.visible ? t("settings.project.hideVariable") : t("settings.project.showVariable")} type="button" variant="ghost">{row.visible ? <EyeOff className="size-3.5" aria-hidden="true" /> : <Eye className="size-3.5" aria-hidden="true" />}</Button>
				<Button aria-label={t("settings.project.removeVariable", { name: row.name || index + 1 })} className="size-7 shrink-0 p-0 text-settings-muted hover:text-destructive" onClick={() => update(rows.filter((_, i) => i !== index))} size="icon-sm" title={t("settings.project.removeVariable", { name: row.name || index + 1 })} type="button" variant="ghost"><Trash2 className="size-3.5" aria-hidden="true" /></Button>
			</div>)}
		</ProjectSettingsSection>
		<div className="flex items-center justify-end gap-2">
				<Button onClick={() => { setActiveTab("paste"); setError(null); }} type="button" variant="outline"><FileCode2 aria-hidden="true" />{t("settings.project.pasteVariables")}</Button>
				<Button onClick={() => update([...rows, { name: "", value: "", visible: false }])} type="button"><Plus aria-hidden="true" />{t("settings.project.addVariable")}</Button>
			</div>
		</> : <ProjectSettingsSection title={t("settings.project.pasteVariables")}>
			<p className="text-sm leading-5 text-settings-muted">{t("settings.project.envPasteHint")}</p>
			<textarea aria-label={t("settings.project.pasteVariables")} autoComplete="off" className="settings-field-control min-h-(--size-textarea-min) min-w-0 resize-none rounded-md! py-2.5 font-mono" id="project-env-paste" onChange={(event) => setPasteText(event.target.value)} spellCheck={false} value={pasteText} />
			<div className="flex justify-end gap-2">
				<Button onClick={() => { setActiveTab("variables"); setPasteText(""); setError(null); }} type="button" variant="outline">{t("settings.project.envPasteCancel")}</Button>
				<Button disabled={!pasteText.trim()} onClick={importPasted} type="button">{t("settings.project.importVariables")}</Button>
			</div>
		</ProjectSettingsSection>}
		{importedCount !== null && <p role="status" className="text-sm text-settings-muted">{t("settings.project.envImported", { count: importedCount })}</p>}
		{error && <p role="alert" className="text-sm text-error">{error}</p>}
		{mutation.isError && <p role="alert" className="text-sm text-error">{mutation.error instanceof Error ? mutation.error.message : t("settings.project.saveFailed")}</p>}
	</form>;
}
