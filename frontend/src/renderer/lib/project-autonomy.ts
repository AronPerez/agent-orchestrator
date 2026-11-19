import type { components } from "../../api/schema";

export type AutonomyConfig = components["schemas"]["AutonomyConfig"];
export type AutonomyMode = "off" | "observe";

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
		autonomyMode: config?.mode === "observe" ? "observe" : "off",
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
