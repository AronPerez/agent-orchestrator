import { describe, expect, it } from "vitest";
import { decisionLink, parseDecisionLink } from "./decision-links";

describe("parseDecisionLink", () => {
	it("reads the one call id of a canonical decision link", () => {
		expect(parseDecisionLink("ao://decisions/dec_1")).toEqual({ callId: "dec_1" });
		expect(parseDecisionLink("ao://decisions/dec%5F1")).toEqual({ callId: "dec_1" });
		expect(parseDecisionLink("ao://decisions/dec_6f1c-0a9e")).toEqual({ callId: "dec_6f1c-0a9e" });
	});

	it("rejects anything that is not exactly one safe id segment", () => {
		for (const value of [
			"ao://decisions/",
			"ao://decisions",
			"ao://decisions/dec_1/",
			"ao://decisions/dec_1/history",
			"ao://decisions/dec_1?raw",
			"ao://decisions/dec_1#top",
			"ao://decisions/.",
			"ao://decisions/..",
			"ao://decisions/%2E%2E",
			"ao://decisions/a%2Fb",
			"ao://decisions/a%5Cb",
			"ao://decisions/a\\b",
			"ao://decisions/dec%ZZ",
			"ao://decisions/dec 1",
			"ao://decisions/dec%201",
			"ao://sessions/project/session",
			"https://example.com/ao://decisions/dec_1",
			"AO://decisions/dec_1",
		]) {
			expect(parseDecisionLink(value), value).toBeUndefined();
		}
	});
});

describe("decisionLink", () => {
	it("builds the canonical link that parses back", () => {
		expect(decisionLink("dec_1")).toBe("ao://decisions/dec_1");
		expect(parseDecisionLink(decisionLink("dec_1"))).toEqual({ callId: "dec_1" });
	});
});
