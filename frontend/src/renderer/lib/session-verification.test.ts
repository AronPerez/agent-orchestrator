import { describe, expect, it } from "vitest";

import type { WorkspaceSession } from "../types/workspace";
import { sessionVerificationPresentation } from "./session-verification";

function session(overrides: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id: "session-1",
		workspaceId: "project-1",
		workspaceName: "Project",
		title: "Ship it",
		provider: "codex",
		status: "idle",
		updatedAt: "2026-11-17T12:00:00Z",
		prs: [],
		...overrides,
	};
}

describe("sessionVerificationPresentation", () => {
	it("gives suspected tampering precedence and aggregates approved signals", () => {
		const result = sessionVerificationPresentation(session({
			latestDoneReport: {
				id: "done-1",
				verification: {
					state: "passed",
					evidence: "verification/done-1.txt",
					startedAt: "2026-11-17T12:00:00Z",
					completedAt: "2026-11-17T12:01:00Z",
				},
			},
			prs: [
				{
					url: "pr-1", number: 1, state: "open", ci: "passing", review: "approved", mergeability: "mergeable",
					reviewComments: false, updatedAt: "2026-11-17T12:00:00Z",
					tamper: {
						headSha: "head-1", testFilesDeleted: 1, testCasesRemoved: 3, testCasesAdded: 1,
						skipMarkersAdded: 2, ciWorkflowEdited: true, assessedAt: "2026-11-17T12:00:00Z",
					},
				},
			],
		}));

		expect(result).toEqual({
			state: "tampering_suspected",
			evidence: "verification/done-1.txt",
			reasons: [
				{ kind: "test_files_deleted", count: 1 },
				{ kind: "test_cases_removed", removed: 3, added: 1 },
				{ kind: "skip_markers_added", count: 2 },
				{ kind: "ci_workflow_edited" },
			],
		});
	});

	it("derives verified and unverified only after a done report", () => {
		expect(sessionVerificationPresentation(session())).toBeUndefined();
		expect(sessionVerificationPresentation(session({ latestDoneReport: { id: "done-1" } }))).toEqual({
			state: "unverified", reasons: [],
		});
		expect(sessionVerificationPresentation(session({
			latestDoneReport: {
				id: "done-1",
				verification: {
					state: "passed", evidence: "verification/done-1.txt",
					startedAt: "2026-11-17T12:00:00Z", completedAt: "2026-11-17T12:01:00Z",
				},
			},
		}))).toEqual({ state: "verified", evidence: "verification/done-1.txt", reasons: [] });
		expect(sessionVerificationPresentation(session({
			latestDoneReport: {
				id: "done-1",
				verification: {
					state: "failed", evidence: "verification/done-1.txt",
					startedAt: "2026-11-17T12:00:00Z", completedAt: "2026-11-17T12:01:00Z",
				},
			},
		}))).toEqual({ state: "unverified", evidence: "verification/done-1.txt", reasons: [] });
	});

	it("does not flag a clean tamper assessment", () => {
		expect(sessionVerificationPresentation(session({ prs: [{
			url: "pr-1", number: 1, state: "open", ci: "passing", review: "approved", mergeability: "mergeable",
			reviewComments: false, updatedAt: "2026-11-17T12:00:00Z",
			tamper: { headSha: "head-1", testCasesRemoved: 1, testCasesAdded: 1, assessedAt: "2026-11-17T12:00:00Z" },
		}] }))).toBeUndefined();
	});
});
