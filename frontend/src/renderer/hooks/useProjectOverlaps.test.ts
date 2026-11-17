import { describe, expect, it } from "vitest";
import type { components } from "../../api/schema";
import { projectOverlapForSession } from "./useProjectOverlaps";

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
			truncated: false,
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
		expect(projectOverlapForSession(response, "two")).toBeUndefined();
	});
});
