import type { components } from "../../api/schema";

export type AutonomyConfig = components["schemas"]["AutonomyConfig"];
/** "on" is set from the command line; the settings page shows it but does not offer it. */
export type AutonomyMode = "off" | "observe" | "on";

/** One decision kind as the project's daemon reports it. */
export type DecisionKindOption = { id: string; label: string; reservedByDefault: boolean };

export type AutonomyDraft = {
	autonomyMode: AutonomyMode;
	/** Undefined keeps the daemon's default reserved kinds. */
	reservedKinds?: string[];
};

export function initialAutonomyDraft(config: AutonomyConfig | undefined): AutonomyDraft {
	const reserved = config?.reserved ?? [];
	return {
		autonomyMode: config?.mode === "observe" || config?.mode === "on" ? config.mode : "off",
		reservedKinds: reserved.length > 0 ? [...reserved] : undefined,
	};
}

/** The kinds the person keeps, in vocabulary order. */
export function effectiveReserved(reserved: string[] | undefined, kinds: DecisionKindOption[]): Set<string> {
	if (reserved === undefined) return new Set(kinds.filter((kind) => kind.reservedByDefault).map((kind) => kind.id));
	const chosen = new Set(reserved);
	return new Set(kinds.filter((kind) => chosen.has(kind.id)).map((kind) => kind.id));
}

export function toggleReserved(reserved: string[] | undefined, kinds: DecisionKindOption[], id: string, on: boolean): string[] {
	const next = effectiveReserved(reserved, kinds);
	if (on) next.add(id);
	else next.delete(id);
	return kinds.filter((kind) => next.has(kind.id)).map((kind) => kind.id);
}

export function isDefaultReserved(reserved: string[] | undefined, kinds: DecisionKindOption[]): boolean {
	if (reserved === undefined) return true;
	const chosen = new Set(reserved);
	return chosen.size === reserved.length && kinds.every((kind) => chosen.has(kind.id) === kind.reservedByDefault);
}

/** The config block to save. A project that stays off with the defaults saves none. */
export function buildAutonomyConfig(mode: AutonomyMode, reserved: string[] | undefined): AutonomyConfig | undefined {
	const config: AutonomyConfig = {
		...(mode === "off" ? {} : { mode }),
		...(reserved === undefined ? {} : { reserved: reserved as AutonomyConfig["reserved"] }),
	};
	return Object.keys(config).length > 0 ? config : undefined;
}

/**
 * The decision kinds from a daemon's response, or undefined when the daemon
 * cannot back the settings controls. A daemon that serves kinds without
 * `reservedByDefault` (the first autonomy release) would make every
 * default-reserved kind look unreserved, and one switch would then save a
 * list that silently drops them, so its response is refused.
 */
export function decisionKindsFromResponse(kinds: readonly { id: string; label: string; reserved?: boolean; reservedByDefault?: boolean }[] | undefined): DecisionKindOption[] | undefined {
	if (!kinds || kinds.length === 0 || kinds.some((kind) => typeof kind.reservedByDefault !== "boolean")) return undefined;
	return kinds.map((kind) => ({ id: kind.id, label: kind.label, reservedByDefault: kind.reservedByDefault as boolean }));
}
