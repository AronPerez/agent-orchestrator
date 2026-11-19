import { describe, expect, it } from "vitest";
import { buildAutonomyConfig, effectiveReserved, initialAutonomyDraft, isDefaultReserved, toggleReserved, type DecisionKindOption } from "./project-autonomy";

const kinds: DecisionKindOption[] = [
	{ id: "production", label: "Production changes and deploys", reservedByDefault: true },
	{ id: "design", label: "Design and product behaviour", reservedByDefault: true },
	{ id: "pr_hygiene", label: "Pull request hygiene", reservedByDefault: false },
	{ id: "other", label: "Anything else", reservedByDefault: true },
];

describe("project autonomy", () => {
	it("reads the defaults when the project has no list", () => {
		expect([...effectiveReserved(undefined, kinds)]).toEqual(["production", "design", "other"]);
		expect([...effectiveReserved(["pr_hygiene"], kinds)]).toEqual(["pr_hygiene"]);
	});

	it("toggles a kind into an explicit list in vocabulary order", () => {
		expect(toggleReserved(undefined, kinds, "pr_hygiene", true)).toEqual(["production", "design", "pr_hygiene", "other"]);
		expect(toggleReserved(undefined, kinds, "design", false)).toEqual(["production", "other"]);
		expect(toggleReserved(["pr_hygiene"], kinds, "production", true)).toEqual(["production", "pr_hygiene"]);
	});

	it("knows when the list matches the defaults", () => {
		expect(isDefaultReserved(undefined, kinds)).toBe(true);
		expect(isDefaultReserved(["other", "production", "design"], kinds)).toBe(true);
		expect(isDefaultReserved(["production"], kinds)).toBe(false);
	});

	it("saves nothing for a project that stays off with the defaults", () => {
		expect(buildAutonomyConfig("off", undefined)).toBeUndefined();
		expect(buildAutonomyConfig("observe", undefined)).toEqual({ mode: "observe" });
		expect(buildAutonomyConfig("off", ["design"])).toEqual({ reserved: ["design"] });
		expect(buildAutonomyConfig("observe", ["design"])).toEqual({ mode: "observe", reserved: ["design"] });
	});

	it("starts the draft from the stored config", () => {
		expect(initialAutonomyDraft(undefined)).toEqual({ autonomyMode: "off", reservedKinds: undefined });
		expect(initialAutonomyDraft({ mode: "observe", reserved: [] })).toEqual({ autonomyMode: "observe", reservedKinds: undefined });
		expect(initialAutonomyDraft({ reserved: ["design"] })).toEqual({ autonomyMode: "off", reservedKinds: ["design"] });
	});
});
