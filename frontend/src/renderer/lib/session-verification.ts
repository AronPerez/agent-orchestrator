import type { WorkspaceSession } from "../types/workspace";

export type SessionVerificationReason =
	| { kind: "test_files_deleted"; count: number }
	| { kind: "test_cases_removed"; removed: number; added: number }
	| { kind: "skip_markers_added"; count: number }
	| { kind: "ci_workflow_edited" };

export type SessionVerificationPresentation = {
	state: "verified" | "unverified" | "tampering_suspected";
	evidence?: string;
	reasons: SessionVerificationReason[];
};

/** Derives the advisory verification marker from durable report and PR facts. */
export function sessionVerificationPresentation(session: WorkspaceSession): SessionVerificationPresentation | undefined {
	let testFilesDeleted = 0;
	let testCasesRemoved = 0;
	let testCasesAdded = 0;
	let skipMarkersAdded = 0;
	let ciWorkflowEdited = false;

	for (const pr of session.prs) {
		const assessment = pr.tamper;
		if (!assessment) continue;
		testFilesDeleted += assessment.testFilesDeleted ?? 0;
		if ((assessment.testCasesRemoved ?? 0) > (assessment.testCasesAdded ?? 0)) {
			testCasesRemoved += assessment.testCasesRemoved ?? 0;
			testCasesAdded += assessment.testCasesAdded ?? 0;
		}
		skipMarkersAdded += assessment.skipMarkersAdded ?? 0;
		ciWorkflowEdited ||= assessment.ciWorkflowEdited ?? false;
	}

	const reasons: SessionVerificationReason[] = [];
	if (testFilesDeleted > 0) reasons.push({ kind: "test_files_deleted", count: testFilesDeleted });
	if (testCasesRemoved > testCasesAdded) reasons.push({ kind: "test_cases_removed", removed: testCasesRemoved, added: testCasesAdded });
	if (skipMarkersAdded > 0) reasons.push({ kind: "skip_markers_added", count: skipMarkersAdded });
	if (ciWorkflowEdited) reasons.push({ kind: "ci_workflow_edited" });

	const evidence = session.latestDoneReport?.verification?.evidence;
	if (reasons.length > 0) {
		return { state: "tampering_suspected", ...(evidence ? { evidence } : {}), reasons };
	}
	if (!session.latestDoneReport) return undefined;
	if (session.latestDoneReport.verification?.state === "passed") {
		return { state: "verified", ...(evidence ? { evidence } : {}), reasons };
	}
	return { state: "unverified", ...(evidence ? { evidence } : {}), reasons };
}
