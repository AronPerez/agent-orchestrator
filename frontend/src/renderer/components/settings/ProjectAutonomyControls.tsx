import { ProjectSettingsSection } from "@aoagents/product-ui";
import { useTranslation } from "react-i18next";
import { effectiveReserved, isDefaultReserved, toggleReserved, type AutonomyDraft, type AutonomyMode, type DecisionKindOption } from "../../lib/project-autonomy";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import { SettingsOptionMenu } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";

/**
 * The project's autonomy mode and, in observe mode, the decision kinds the
 * person keeps. Kind labels are translated by id; a kind this renderer does
 * not know yet falls back to the daemon's own label.
 */
export function ProjectAutonomyControls({ draft, kinds, onChange }: {
	draft: AutonomyDraft;
	kinds: DecisionKindOption[];
	onChange: (next: AutonomyDraft) => void;
}) {
	const { t } = useTranslation();
	const reserved = effectiveReserved(draft.reservedKinds, kinds);
	const modes: { value: AutonomyMode; label: string }[] = [
		{ value: "off", label: t("settings.project.autonomyOff") },
		{ value: "observe", label: t("settings.project.autonomyObserve") },
		// Mode on is chosen from the command line for now; show it only so a
		// project already in it reads truthfully and is not downgraded on save.
		...(draft.autonomyMode === "on" ? [{ value: "on" as const, label: t("settings.project.autonomyOn") }] : []),
	];
	return <>
		<ProjectSettingsSection title={t("settings.project.decisions")} grouped>
			<SettingsRow label={t("settings.project.autonomy")} description={t("settings.project.autonomyDescription")}>
				<SettingsOptionMenu
					aria-label={t("settings.project.autonomy")}
					value={draft.autonomyMode}
					options={modes}
					triggerClassName="w-fit"
					onChange={(autonomyMode) => onChange({ ...draft, autonomyMode })}
				/>
			</SettingsRow>
		</ProjectSettingsSection>
		{draft.autonomyMode !== "off" && <ProjectSettingsSection title={t("settings.project.reservedKinds")} grouped>
			{kinds.map((kind) => {
				const label = t(`settings.project.decisionKind.${kind.id}`, { defaultValue: kind.label });
				return <SettingsRow key={kind.id} label={label}>
					<Switch
						aria-label={label}
						id={`project-reserved-${kind.id}`}
						checked={reserved.has(kind.id)}
						onCheckedChange={(on) => onChange({ ...draft, reservedKinds: toggleReserved(draft.reservedKinds, kinds, kind.id, on) })}
					/>
				</SettingsRow>;
			})}
			{!isDefaultReserved(draft.reservedKinds, kinds) && <div className="settings-row-bar flex justify-end">
				<Button type="button" variant="ghost" size="sm" onClick={() => onChange({ ...draft, reservedKinds: undefined })}>
					{t("settings.project.reservedKindsReset")}
				</Button>
			</div>}
		</ProjectSettingsSection>}
	</>;
}
