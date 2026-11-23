import { beforeEach, describe, expect, it } from "vitest";
import { closeDecisionTab, DECISION_TABS_MAX, DECISION_TABS_STORAGE_KEY, keepDecisionTab, readDecisionTabs, writeDecisionTabs } from "./decision-tabs";

describe("kept decision tabs", () => {
	beforeEach(() => window.localStorage.clear());

	it("adds a decision once, at the end", () => {
		const one = keepDecisionTab([], "dec_1");
		expect(one).toEqual(["dec_1"]);
		expect(keepDecisionTab(one, "dec_2")).toEqual(["dec_1", "dec_2"]);
		expect(keepDecisionTab(one, "dec_1")).toBe(one);
	});

	it("refuses one more than the cap and anything that is not a call id", () => {
		const full = Array.from({ length: DECISION_TABS_MAX }, (_, index) => `dec_${index}`);
		expect(keepDecisionTab(full, "dec_extra")).toBe(full);
		const some = ["dec_1"];
		expect(keepDecisionTab(some, "not/an id")).toBe(some);
	});

	it("closes one and leaves the rest in order", () => {
		const ids = ["dec_1", "dec_2", "dec_3"];
		expect(closeDecisionTab(ids, "dec_2")).toEqual(["dec_1", "dec_3"]);
		expect(closeDecisionTab(ids, "dec_9")).toBe(ids);
	});

	it("round-trips through local storage", () => {
		writeDecisionTabs(["dec_1", "dec_2"]);
		expect(JSON.parse(window.localStorage.getItem(DECISION_TABS_STORAGE_KEY) ?? "[]")).toEqual(["dec_1", "dec_2"]);
		expect(readDecisionTabs()).toEqual(["dec_1", "dec_2"]);
		writeDecisionTabs([]);
		expect(window.localStorage.getItem(DECISION_TABS_STORAGE_KEY)).toBeNull();
	});

	it("reads damaged, duplicated or oversized storage safely", () => {
		expect(readDecisionTabs()).toEqual([]);
		window.localStorage.setItem(DECISION_TABS_STORAGE_KEY, "{not json");
		expect(readDecisionTabs()).toEqual([]);
		window.localStorage.setItem(DECISION_TABS_STORAGE_KEY, JSON.stringify({ a: 1 }));
		expect(readDecisionTabs()).toEqual([]);
		window.localStorage.setItem(DECISION_TABS_STORAGE_KEY, JSON.stringify(["dec_1", "dec_1", 7, "bad/id", "dec_2"]));
		expect(readDecisionTabs()).toEqual(["dec_1", "dec_2"]);
		window.localStorage.setItem(DECISION_TABS_STORAGE_KEY, JSON.stringify(Array.from({ length: DECISION_TABS_MAX + 5 }, (_, index) => `dec_${index}`)));
		expect(readDecisionTabs()).toHaveLength(DECISION_TABS_MAX);
	});
});
