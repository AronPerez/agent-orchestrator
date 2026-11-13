import { describe, expect, it } from "vitest";
import { sessionInterfaceTransition } from "../test/interface-transition-fixtures";
import type { WorkspaceSession } from "../types/workspace";
import { canResumeAgent, resumeAgentError, resumeAgentErrorMessage, resumeOpenElsewhere } from "./useCanResumeAgent";

const exitedSession: WorkspaceSession = {
	id: "session-1",
	workspaceId: "project-1",
	workspaceName: "Project One",
	title: "Exited agent",
	provider: "codex",
	kind: "worker",
	branch: "ao/session-1",
	status: "exited",
	activity: { state: "exited", lastActivityAt: "2026-09-21T00:00:00Z" },
	updatedAt: "2026-09-21T00:00:00Z",
	prs: [],
};

describe("canResumeAgent", () => {
	const unavailableSession: WorkspaceSession = {
		...exitedSession,
		status: "unknown",
		statusReadiness: "unavailable",
		activity: undefined,
	};

	it("allows explicit recovery when runtime verification failed without an exit fact", () => {
		expect(canResumeAgent(unavailableSession)).toBe(true);
	});

	it.each([
		{ isTerminated: true },
		{ status: "terminated" as const },
		{ statusReadiness: "checking" as const },
		{ statusReadiness: "ready" as const },
		{ cloud: { orgId: "org-1" } },
		{ activeAgentSwitch: { id: "switch-1" } as WorkspaceSession["activeAgentSwitch"] },
	])("does not recover an unavailable session with %j", (override) => {
		expect(canResumeAgent({ ...unavailableSession, ...override })).toBe(false);
	});

	it("blocks unavailable-session recovery during an interface transition", () => {
		expect(canResumeAgent(unavailableSession, sessionInterfaceTransition(unavailableSession.id))).toBe(false);
	});

	it("blocks resume while an interface transition is active", () => {
		expect(canResumeAgent(exitedSession, sessionInterfaceTransition(exitedSession.id))).toBe(false);
	});

	it("allows resume after an interface transition completes", () => {
		expect(canResumeAgent(exitedSession, sessionInterfaceTransition(exitedSession.id, "completed"))).toBe(true);
	});
});

describe("resumeAgentErrorMessage", () => {
	it.each([
		["AGENT_NOT_EXITED", "already running"],
		["AGENT_RESUME_IN_PROGRESS", "already resuming"],
		["CHAT_RESUME_FAILED", "saved conversation"],
		["CHAT_AUTH_REQUIRED", "sign-in"],
	])("explains %s with a next step", (code, fragment) => {
		expect(resumeAgentErrorMessage({ code, message: "conflict" }, 409)).toContain(fragment);
	});
	it("preserves an unknown server message", () => {
		expect(resumeAgentErrorMessage({ code: "OTHER_CONFLICT", message: "Another operation owns the session" }, 409)).toBe("Another operation owns the session");
	});
});

describe("resumeOpenElsewhere", () => {
	it("reads the error code, not the message text", () => {
		expect(resumeOpenElsewhere(resumeAgentError({ code: "SESSION_OPEN_ELSEWHERE", message: "Reworded" }, 409))).toBe(true);
		expect(resumeOpenElsewhere({ code: "AGENT_RESUME_FAILED", message: "this is open elsewhere" })).toBe(false);
		expect(resumeOpenElsewhere(null)).toBe(false);
	});
});
