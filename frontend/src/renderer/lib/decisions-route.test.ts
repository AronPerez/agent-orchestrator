import { describe, expect, it } from "vitest";
import { DECISIONS_CALL_MAX, DECISIONS_QUERY_MAX, DECISIONS_SCOPE_MAX, formatDecisionCount, validateDecisionsSearch } from "./decisions-route";

describe("validateDecisionsSearch", () => {
	it("keeps a selected call and search words", () => {
		expect(validateDecisionsSearch({ call: "dec_1", q: "auth scheme" })).toEqual({ call: "dec_1", q: "auth scheme" });
	});

	it("treats a missing or blank value as absent", () => {
		expect(validateDecisionsSearch({})).toEqual({});
		expect(validateDecisionsSearch({ call: "", q: "   " })).toEqual({});
	});

	it("drops arrays, objects and oversized values", () => {
		expect(validateDecisionsSearch({ call: ["dec_1"], q: { text: "x" } })).toEqual({});
		expect(validateDecisionsSearch({ call: "d".repeat(DECISIONS_CALL_MAX + 1) })).toEqual({});
		expect(validateDecisionsSearch({ q: "q".repeat(DECISIONS_QUERY_MAX + 1) })).toEqual({});
	});

	it("drops a call id that is not one path-safe segment", () => {
		for (const call of ["dec/1", "dec\\1", ".", "..", "dec 1", "dec%2F1", "dec?x", "dec#x"]) {
			expect(validateDecisionsSearch({ call }), call).toEqual({});
		}
	});

	it("keeps numeric-looking search words the router parsed as numbers", () => {
		expect(validateDecisionsSearch({ q: 2026 })).toEqual({ q: "2026" });
		expect(validateDecisionsSearch({ call: 42 })).toEqual({});
	});
});

describe("validateDecisionsSearch scope", () => {
	it("keeps a chosen project, and a stream only together with its project", () => {
		expect(validateDecisionsSearch({ project: "cloud" })).toEqual({ project: "cloud" });
		expect(validateDecisionsSearch({ project: "cloud", stream: "h_a:nango" })).toEqual({ project: "cloud", stream: "h_a:nango" });
		expect(validateDecisionsSearch({ stream: "h_a:nango" })).toEqual({});
		expect(validateDecisionsSearch({ project: "@no-project", stream: "@project", call: "dec_1" })).toEqual({ project: "@no-project", stream: "@project", call: "dec_1" });
	});

	it("keeps a project id the router parsed as a number, and drops oversized or non-text values", () => {
		expect(validateDecisionsSearch({ project: 42 })).toEqual({ project: "42" });
		expect(validateDecisionsSearch({ project: "p".repeat(DECISIONS_SCOPE_MAX + 1) })).toEqual({});
		expect(validateDecisionsSearch({ project: "cloud", stream: ["x"] })).toEqual({ project: "cloud" });
		expect(validateDecisionsSearch({ project: { id: "cloud" } })).toEqual({});
	});
});

describe("validateDecisionsSearch tab", () => {
	it("keeps the active tab when it is a call id, beside the other fields", () => {
		expect(validateDecisionsSearch({ tab: "dec_1" })).toEqual({ tab: "dec_1" });
		expect(validateDecisionsSearch({ tab: "dec_1", call: "dec_2", project: "cloud" })).toEqual({ tab: "dec_1", call: "dec_2", project: "cloud" });
	});

	it("drops a tab that is not one path-safe call id", () => {
		for (const tab of ["dec/1", "", "dec 1", 42, ["dec_1"]]) {
			expect(validateDecisionsSearch({ tab }), String(tab)).toEqual({});
		}
	});
});

describe("formatDecisionCount", () => {
	it("caps the visible count while keeping zero hidden", () => {
		expect(formatDecisionCount(0)).toBeUndefined();
		expect(formatDecisionCount(3)).toBe("3");
		expect(formatDecisionCount(99)).toBe("99");
		expect(formatDecisionCount(120)).toBe("99+");
	});
});
