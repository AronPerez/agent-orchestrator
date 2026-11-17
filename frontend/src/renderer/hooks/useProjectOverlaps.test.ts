import { describe, expect, it } from "vitest";
import type { components } from "../../api/schema";
import {
	isProjectOverlapsResponse,
	projectOverlapForSession,
	projectOverlapScanState,
	projectOverlapSessionFingerprint,
} from "./useProjectOverlaps";

type ProjectOverlapsResponse = components["schemas"]["ProjectOverlapsResponse"];

describe("projectOverlapForSession", () => {
	it("groups exact paths by peer and carries incomplete-read facts", () => {
		const response: ProjectOverlapsResponse = {
			projectId: "demo",
			overlaps: [
				{ sessionA: "one", sessionB: "two", paths: ["frontend/App.tsx", "README.md"] },
				{ sessionA: "three", sessionB: "one", paths: ["backend/main.go", "README.md"] },
			],
			truncatedSessionIds: ["three"],
			unresolvedBaseSessionIds: ["one"],
			unavailableSessionIds: [],
		};

		expect(projectOverlapForSession(response, "one")).toEqual({
			peers: [
				{ sessionId: "three", paths: ["README.md", "backend/main.go"] },
				{ sessionId: "two", paths: ["README.md", "frontend/App.tsx"] },
			],
			paths: ["README.md", "backend/main.go", "frontend/App.tsx"],
			truncated: true,
			unresolvedBase: true,
			unavailable: false,
		});
	});

	it("returns an incomplete model without claiming an overlap", () => {
		const response: ProjectOverlapsResponse = {
			projectId: "demo",
			overlaps: [],
			truncatedSessionIds: [],
			unresolvedBaseSessionIds: [],
			unavailableSessionIds: ["one"],
		};
		expect(projectOverlapForSession(response, "one")).toEqual({
			peers: [], paths: [], truncated: false, unresolvedBase: false, unavailable: true,
		});
		expect(projectOverlapForSession(response, "two")).toEqual({
			peers: [], paths: [], truncated: false, unresolvedBase: false, unavailable: true,
		});
	});
});

describe("projectOverlapSessionFingerprint", () => {
	it("changes when a provisioned workspace becomes available to watch", () => {
		const pending = projectOverlapSessionFingerprint([
			{ id: "two", workspacePublished: false },
			{ id: "one" },
		]);
		const published = projectOverlapSessionFingerprint([
			{ id: "one" },
			{ id: "two", workspacePublished: true },
		]);

		expect(pending).toBe(JSON.stringify([["one", true], ["two", false]]));
		expect(published).toBe(JSON.stringify([["one", true], ["two", true]]));
		expect(published).not.toBe(pending);
	});
});

describe("projectOverlapScanState", () => {
	const clean: ProjectOverlapsResponse = {
		projectId: "demo",
		overlaps: [],
		truncatedSessionIds: [],
		unresolvedBaseSessionIds: [],
		unavailableSessionIds: [],
	};

	const cases: Array<[
		string,
		Parameters<typeof projectOverlapScanState>[0],
		ReturnType<typeof projectOverlapScanState>,
	]> = [
		["idle", { data: undefined, isError: false }, undefined],
		["initial error", { data: undefined, isError: true }, "unavailable"],
		["failed refresh", { data: clean, isError: true }, "stale"],
		["incomplete read", { data: { ...clean, unavailableSessionIds: ["one"] }, isError: false }, "incomplete"],
		["clean read", { data: clean, isError: false }, undefined],
	];
	it.each(cases)("classifies a %s", (_name, query, expected) => {
		expect(projectOverlapScanState(query)).toBe(expected);
	});
});

describe("isProjectOverlapsResponse", () => {
	it("accepts the generated overlap response shape", () => {
		expect(isProjectOverlapsResponse({
			projectId: "project-1",
			overlaps: [],
			truncatedSessionIds: [],
			unresolvedBaseSessionIds: [],
			unavailableSessionIds: [],
		})).toBe(true);
	});

	it("rejects a successful response that omits collision arrays", () => {
		expect(isProjectOverlapsResponse({ projectId: "project-1" })).toBe(false);
		expect(isProjectOverlapsResponse({})).toBe(false);
	});
});
