// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import * as fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { runInNewContext } from "node:vm";
import { ScriptTarget, transpile } from "typescript";
import { SideCloseRecovery } from "./side-close";
import { createPackage } from "@electron/asar";
import type { DaemonStatus } from "../shared/daemon-status";
import type { components } from "../api/schema";
import { createArchiveIdentityReader, InstalledBuildGuard, sessionsAreIdle, sessionsAndReviewsAreIdle, watchInstalledBuild } from "./installed-build";

function guardHarness() {
	let identity: string | null = "running-asar";
	let idle: boolean | { openSideChats: string[] } = true;
	const onChange = vi.fn();
	const relaunch = vi.fn();
	const isIdle = vi.fn(async () => idle);
	const assertRestartSafe = vi.fn<() => void>();
	const onReplacement = vi.fn();
	const guard = new InstalledBuildGuard({ readIdentity: () => identity, isIdle, onChange, onReplacement, assertRestartSafe, relaunch });
	return { guard, onChange, onReplacement, relaunch, isIdle, assertRestartSafe, replace: (next: string | null) => { identity = next; }, busy: () => { idle = false; }, idle: () => { idle = true; } };
}

describe("installed desktop build", () => {
	it("keeps named side blockers busy and clears them after the user closes the sides", async () => {
		const h = guardHarness();
		h.replace("B");
		h.isIdle.mockResolvedValue({ openSideChats: ["Design notes"] });
		await h.guard.check();
		await h.guard.check();
		expect(h.guard.status).toMatchObject({ idle: false, openSideChats: ["Design notes"] });
		await expect(h.guard.requestRestart("now")).rejects.toThrow(/close.*Design notes/);
		await h.guard.requestRestart("when-idle");
		expect(h.relaunch).not.toHaveBeenCalled();
		h.isIdle.mockResolvedValue(true);
		await h.guard.check();
		expect(h.guard.status?.openSideChats).toBeUndefined();
		expect(h.relaunch).toHaveBeenCalledOnce();
	});
	it("suspends the updater at the first mismatch before settling or probing workers", async () => {
		const h = guardHarness();
		h.replace("B");
		await h.guard.check();
		expect(h.onReplacement).toHaveBeenLastCalledWith(true);
		expect(h.guard.status).toBeUndefined();
		expect(h.isIdle).not.toHaveBeenCalled();
		let finish!: (idle: boolean) => void;
		h.isIdle.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
		const checking = h.guard.check();
		await vi.waitFor(() => expect(finish).toBeDefined());
		expect(h.onReplacement).toHaveBeenLastCalledWith(true);
		expect(h.guard.status).toBeUndefined();
		finish(true);
		await checking;
	});

	it("keeps updater handoffs suspended during an unreadable copy and restores them after a reversion", async () => {
		const h = guardHarness();
		h.replace(null);
		await h.guard.check();
		expect(h.onReplacement).toHaveBeenLastCalledWith(true);
		h.replace("running-asar");
		await h.guard.check();
		expect(h.onReplacement).toHaveBeenLastCalledWith(false);
	});

	it("does not prompt when identities match", async () => {
		const h = guardHarness();
		await h.guard.check();
		await h.guard.check();
		expect(h.guard.status).toBeUndefined();
		expect(h.onChange).not.toHaveBeenCalled();
	});

	it("notices a same-version replacement only after two stable reads", async () => {
		const h = guardHarness();
		h.replace("rebuilt-asar");
		await h.guard.check();
		expect(h.guard.status).toBeUndefined();
		await h.guard.check();
		expect(h.guard.status).toEqual({ identity: "rebuilt-asar", idle: true, restartWhenIdle: false });
		expect(h.relaunch).not.toHaveBeenCalled();
	});

	it("does not treat a missing, half-copied, or reverted archive as an installed build", async () => {
		const h = guardHarness();
		for (const identity of [null, "partial", null, "different-partial", "running-asar", "running-asar"]) {
			h.replace(identity);
			await h.guard.check();
		}
		expect(h.onChange).not.toHaveBeenCalled();
	});

	it("gates restart on fresh idle evidence and waits through unknown worker state", async () => {
		const h = guardHarness();
		h.busy();
		h.replace("new-asar");
		await h.guard.check();
		await h.guard.check();
		expect(h.guard.status?.idle).toBe(false);
		await expect(h.guard.requestRestart("now")).rejects.toThrow(/idle/);
		await h.guard.requestRestart("when-idle");
		expect(h.guard.status?.restartWhenIdle).toBe(true);
		h.isIdle.mockRejectedValueOnce(new Error("daemon unavailable"));
		await h.guard.check();
		expect(h.relaunch).not.toHaveBeenCalled();
		h.idle();
		await h.guard.check();
		await h.guard.check();
		expect(h.relaunch).toHaveBeenCalledTimes(1);
	});

	it("lets the user cancel a queued restart", async () => {
		const h = guardHarness();
		h.busy();
		h.replace("new-asar");
		await h.guard.check();
		await h.guard.check();
		await h.guard.requestRestart("when-idle");
		await h.guard.requestRestart("cancel");
		h.idle();
		await h.guard.check();
		expect(h.relaunch).not.toHaveBeenCalled();
	});

	it.each(["now", "when-idle"] as const)("refuses %s while the native installer owns an update", async (mode) => {
		const h = guardHarness();
		h.replace("B");
		await h.guard.check();
		await h.guard.check();
		h.assertRestartSafe.mockImplementation(() => { throw new Error("native update armed"); });
		await expect(h.guard.requestRestart(mode)).rejects.toThrow("native update armed");
		expect(h.guard.status?.restartWhenIdle).toBe(false);
		expect(h.relaunch).not.toHaveBeenCalled();
	});

	it("cancels a queued restart if native staging starts before workers become idle", async () => {
		const h = guardHarness();
		h.busy();
		h.replace("B");
		await h.guard.check();
		await h.guard.check();
		await h.guard.requestRestart("when-idle");
		h.assertRestartSafe.mockImplementation(() => { throw new Error("native update armed"); });
		h.idle();
		await expect(h.guard.check()).rejects.toThrow("native update armed");
		expect(h.guard.status?.restartWhenIdle).toBe(false);
		await h.guard.check();
		expect(h.relaunch).not.toHaveBeenCalled();
	});

	it("honors a cancellation while the final idle check is in flight", async () => {
		const h = guardHarness();
		h.replace("new-asar");
		await h.guard.check();
		await h.guard.check();
		let finish!: (idle: boolean) => void;
		h.isIdle.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
		const restarting = h.guard.requestRestart("now");
		await vi.waitFor(() => expect(finish).toBeDefined());
		await h.guard.requestRestart("cancel");
		finish(true);
		await restarting;
		expect(h.relaunch).not.toHaveBeenCalled();
	});

	it("rechecks worker activity after the notice said the board was idle", async () => {
		const h = guardHarness();
		h.replace("new-asar");
		await h.guard.check();
		await h.guard.check();
		h.busy();
		await expect(h.guard.requestRestart("now")).rejects.toThrow(/idle/);
		expect(h.relaunch).not.toHaveBeenCalled();
	});

	it("never relaunches into an archive that disappeared or changed again", async () => {
		const h = guardHarness();
		h.replace("new-asar");
		await h.guard.check();
		await h.guard.check();
		h.replace(null);
		await expect(h.guard.requestRestart("now")).rejects.toThrow(/install/);
		h.replace("another-asar");
		await expect(h.guard.requestRestart("now")).rejects.toThrow(/install/);
		expect(h.relaunch).not.toHaveBeenCalled();
	});

	it("checks physical identity immediately on replacement, before the prompt settles", async () => {
		const h = guardHarness();
		expect(await h.guard.isRunningBuildInstalled()).toBe(true);
		h.replace("new-asar");
		expect(await h.guard.isRunningBuildInstalled()).toBe(false);
		h.replace(null);
		expect(await h.guard.isRunningBuildInstalled()).toBe(false);
	});

	it("detects a renderer built against a different main and does not loop after relaunch", async () => {
		const h = guardHarness();
		await h.guard.rendererMismatch();
		expect(h.guard.status?.idle).toBe(true);
		await h.guard.requestRestart("now");
		expect(h.relaunch).toHaveBeenCalledTimes(1);
		const restarted = guardHarness();
		await restarted.guard.check();
		await restarted.guard.check();
		expect(restarted.guard.status).toBeUndefined();
		expect(restarted.onChange).not.toHaveBeenCalled();
	});

	it("starts with the new identity after relaunch without another prompt", async () => {
		const h = guardHarness();
		h.replace("new-asar");
		await h.guard.check();
		await h.guard.check();
		await h.guard.requestRestart("now");
		const onChange = vi.fn();
		const restarted = new InstalledBuildGuard({ readIdentity: () => "new-asar", isIdle: async () => true, onChange, onReplacement: () => undefined, assertRestartSafe: () => undefined, relaunch: vi.fn() });
		await restarted.check();
		await restarted.check();
		expect(restarted.status).toBeUndefined();
		expect(onChange).not.toHaveBeenCalled();
	});
});

describe("idle evidence", () => {
	const noLiveReviewerWork = async () => ({ mode: "chat", controller: "ready", turns: [] });
	const idleSession = {
		id: "worker-1", mode: "chat", isTerminated: false, status: "idle", statusReadiness: "ready",
		activity: { state: "idle", lastActivityAt: "2026-11-16T20:00:00Z" },
	} satisfies Pick<components["schemas"]["ControllersSessionView"], "id" | "mode" | "isTerminated" | "status" | "statusReadiness" | "activity">;
	it.each([{ status: "no_signal" }, { runtimeUnreachable: true }])("rejects stored idle activity with unavailable live facts: %j", (facts) => {
		expect(sessionsAreIdle({ sessions: [{ ...idleSession, ...facts }] })).toBe(false);
	});
	it.each(["Approved", "Mergeable"])("checks all running review passes even when the display says %s", async (displayStatus) => {
		const readReviews = vi.fn(async (_sessionId: string) => ({ chatReviewerIds: [], autoReviewPending: false, runs: [{ status: "complete", prUrl: "pr-1" }, { status: "running", prUrl: "pr-2" }] }));
		expect(await sessionsAndReviewsAreIdle({ sessions: [{ ...idleSession, displayStatus, status: displayStatus === "Approved" ? "approved" : "mergeable" }] }, readReviews, noLiveReviewerWork)).toBe(false);
		expect(readReviews).toHaveBeenCalledWith("worker-1");
	});
	it.each([
		{ status: "pr_open", displayStatus: "Review scheduled", runs: [] },
		{ status: "review_pending", displayStatus: "Retrying review", runs: [{ status: "failed" }] },
	])("keeps pending reviews busy with terminal or absent runs: $displayStatus", async ({ status, displayStatus, runs }) => {
		expect(await sessionsAndReviewsAreIdle({ sessions: [{ ...idleSession, status, displayStatus }] }, async () => ({ chatReviewerIds: [], autoReviewPending: false, runs, activeReviewers: [] }), noLiveReviewerWork)).toBe(false);
	});
	it.each(["active", "waiting_input", "blocked", "unknown", undefined])("keeps terminal passes busy with reviewer activity %s", async (reviewerActivityState) => {
		expect(await sessionsAndReviewsAreIdle({ sessions: [idleSession] }, async () => ({
			chatReviewerIds: [], autoReviewPending: false, runs: [{ status: "delivered" }], reviewerHandleId: "reviewer-1", reviewerActivityState,
			reviewerSurface: { reviewId: "review-1", mode: "tui" }, activeReviewers: [{ reviewId: "review-1", mode: "tui" }],
		}), noLiveReviewerWork)).toBe(false);
	});
	it("does not infer another live reviewer's activity from the selected idle reviewer", async () => {
		expect(await sessionsAndReviewsAreIdle({ sessions: [idleSession] }, async () => ({
			chatReviewerIds: [], autoReviewPending: false, runs: [{ status: "complete" }], reviewerHandleId: "pane-1", reviewerActivityState: "idle",
			reviewerSurface: { reviewId: "reviewer-1", mode: "tui", handleId: "pane-1" },
			activeReviewers: [{ reviewId: "reviewer-1", mode: "tui", handleId: "pane-1" }, { reviewId: "reviewer-2", mode: "tui", handleId: "pane-2" }],
		}), noLiveReviewerWork)).toBe(false);
	});
	it.each(["idle", "exited"])("accepts terminal passes with confirmed %s reviewer activity", async (reviewerActivityState) => {
		expect(await sessionsAndReviewsAreIdle({ sessions: [idleSession] }, async () => ({
			chatReviewerIds: [], autoReviewPending: false, runs: [{ status: "complete" }, { status: "delivered" }], reviewerHandleId: "pane-1", reviewerActivityState,
			reviewerSurface: { reviewId: "reviewer-1", mode: "tui", handleId: "pane-1" },
			activeReviewers: [{ reviewId: "reviewer-1", mode: "tui", handleId: "pane-1" }],
		}), noLiveReviewerWork)).toBe(true);
	});
	it("requires available review facts for every live session and ignores terminated sessions", async () => {
		const body = { sessions: [idleSession, { ...idleSession, id: "worker-2" }, { isTerminated: true }] };
		const readReviews = vi.fn<(_sessionId: string) => Promise<unknown>>(async () => ({ chatReviewerIds: [], autoReviewPending: false, runs: [], activeReviewers: [] }));
		const readLiveReviewers = vi.fn(async (_reviewId: string) => ({ mode: "chat", controller: "ready", turns: [] }));
		expect(await sessionsAndReviewsAreIdle(body, readReviews, readLiveReviewers)).toBe(true);
		expect(readReviews.mock.calls.map(([id]) => id)).toEqual(["worker-1", "worker-2"]);
		expect(readLiveReviewers).not.toHaveBeenCalled();
		readReviews.mockRejectedValueOnce(new Error("unavailable"));
		expect(await sessionsAndReviewsAreIdle(body, readReviews, readLiveReviewers)).toBe(false);
		readReviews.mockResolvedValueOnce({});
		expect(await sessionsAndReviewsAreIdle(body, readReviews, readLiveReviewers)).toBe(false);
		readReviews.mockResolvedValueOnce({ chatReviewerIds: [], autoReviewPending: false, runs: [] });
		expect(await sessionsAndReviewsAreIdle(body, readReviews, readLiveReviewers)).toBe(false);
	});
	it("does not read Chat controllers when there are no live Chat reviewers", async () => {
		const readLiveReviewers = vi.fn(async () => { throw new Error("not a chat session"); });
		expect(await sessionsAndReviewsAreIdle({ sessions: [{ ...idleSession, mode: "tui" } satisfies Pick<components["schemas"]["ControllersSessionView"], "mode">] }, async () => ({ chatReviewerIds: [], autoReviewPending: false, runs: [], activeReviewers: [] }), readLiveReviewers)).toBe(true);
		expect(readLiveReviewers).not.toHaveBeenCalled();
	});
	it.each([
		{ stored: "active", controller: "ready", turn: "completed", want: true },
		{ stored: "idle", controller: "busy", turn: "running", want: false },
	])("uses live Chat reviewer state after a pass or during follow-up: %j", async ({ stored, controller, turn, want }) => {
		const surface = { reviewId: "chat-reviewer", mode: "chat", harness: "codex" };
		const readLive = vi.fn(async () => ({ mode: "chat", controller, turns: [{ state: turn }] }));
		expect(await sessionsAndReviewsAreIdle({ sessions: [idleSession] }, async () => ({
			chatReviewerIds: [], autoReviewPending: false, runs: [{ status: "complete" }], reviewerActivityState: stored,
			reviewerSurface: surface, activeReviewers: [surface],
		}), readLive)).toBe(want);
		expect(readLive).toHaveBeenCalledWith("chat-reviewer");
	});
	it.each([
		{ status: "ci_failed", displayStatus: "CI failing" },
		{ status: "approved", displayStatus: "Approved" },
		{ status: "mergeable", displayStatus: "Mergeable" },
	])("keeps per-PR pending work busy despite the board label: $displayStatus", async (facts) => {
		expect(await sessionsAndReviewsAreIdle({ sessions: [{ ...idleSession, ...facts, autoReviewEnabled: true }] }, async () => ({
			chatReviewerIds: [], autoReviewPending: true, runs: [], activeReviewers: [],
			reviews: [{ status: "up_to_date" }, { status: "needs_review" }],
		}), noLiveReviewerWork)).toBe(false);
	});
	it.each(["ready", "busy"])("reads every Chat reviewer independently when another controller is %s", async (controller) => {
		const readLive = vi.fn(async (id: string) => ({ mode: "chat", controller: id === "other" ? controller : "ready", turns: [] }));
		expect(await sessionsAndReviewsAreIdle({ sessions: [idleSession] }, async () => ({ chatReviewerIds: [], autoReviewPending: false, runs: [],
			activeReviewers: [{ reviewId: "selected", mode: "chat" }, { reviewId: "other", mode: "chat" }],
		}), readLive)).toBe(controller === "ready");
		expect(readLive.mock.calls.map(([id]) => id)).toEqual(["selected", "other"]);
	});
	it("keeps unavailable live Chat reviewer activity busy", async () => {
		expect(await sessionsAndReviewsAreIdle({ sessions: [idleSession] }, async () => ({ chatReviewerIds: [], autoReviewPending: false, runs: [], activeReviewers: [{ reviewId: "chat", mode: "chat" }] }), async () => { throw new Error("controller unavailable"); })).toBe(false);
	});
	it("checks retained Chat controllers without advertising them as active panes", async () => {
		const readLive = vi.fn(async () => ({ mode: "chat", controller: "busy", turns: [{ state: "running" }] }));
		expect(await sessionsAndReviewsAreIdle({ sessions: [idleSession] }, async () => ({ chatReviewerIds: ["retained-chat"], autoReviewPending: false, runs: [], activeReviewers: [] }), readLive)).toBe(false);
		expect(readLive).toHaveBeenCalledWith("retained-chat");
	});
	it("accepts the daemon's exited terminal reviewer shape with its retained handle", async () => {
		expect(await sessionsAndReviewsAreIdle({ sessions: [idleSession] }, async () => ({
			autoReviewPending: false, chatReviewerIds: [], runs: [{ status: "complete" }], activeReviewers: [],
			reviewerHandleId: "exited-pane", reviewerActivityState: "exited", reviewerSurface: { mode: "tui", reviewId: "old-review" },
		}), async () => { throw new Error("not Chat"); })).toBe(true);
	});
	it.each(["completed", "failed", "cancelled", "superseded"] satisfies components["schemas"]["ReviewRun"]["status"][])("accepts the current daemon's terminal review lifecycle: %s", async (status) => {
		expect(await sessionsAndReviewsAreIdle({ sessions: [idleSession] }, async () => ({
			autoReviewPending: false, chatReviewerIds: [], runs: [{ status }], activeReviewers: [],
		}), noLiveReviewerWork)).toBe(true);
	});
	it.each(["queued", "launching", "running", "cancelling"] satisfies components["schemas"]["ReviewRun"]["status"][])("keeps the current daemon's active review lifecycle busy: %s", async (status) => {
		expect(await sessionsAndReviewsAreIdle({ sessions: [idleSession] }, async () => ({
			autoReviewPending: false, chatReviewerIds: [], runs: [{ status }], activeReviewers: [],
		}), noLiveReviewerWork)).toBe(false);
	});
	function mainIdleProbe(sides: unknown[] = []) {
		const main = fs.readFileSync(path.join(import.meta.dirname, "../main.ts"), "utf8");
		const start = main.indexOf("\tisIdle: async () => {");
		const attachment = { generation: 1, pid: 11, port: 3001, appRunId: "launch" };
		const ensureReady = vi.fn<() => Promise<void>>(async () => undefined);
		const capture = vi.fn(async () => { await ensureReady(); return { attachment, state: { appRunId: "launch", sides } }; });
		const fetch = vi.fn(async (url: string): Promise<{ ok: boolean; json: () => Promise<unknown> }> => ({ ok: true, json: async () => url.endsWith("/sessions")
			? { sessions: [idleSession] } : url.endsWith("/reviews") ? { chatReviewerIds: [], autoReviewPending: false, runs: [], activeReviewers: [] }
			: { sides: sides.map((record: any) => record.side ?? record) } }));
		const context = { daemonStatus: { port: 3001, state: "ready" } as DaemonStatus, daemonAttachmentGeneration: 1, appRunId: "launch", chatDraftRisks: [], readDaemonProbe: async () => true,
			fetch, AbortSignal, sessionsAndReviewsAreIdle, sideLaunchState: undefined as { sides: unknown[] } | undefined,
			sideLaunchRecovery: { ensureReady, capture, matches: (candidate: unknown) => candidate === attachment },
			sideCloseController: { hasPending: vi.fn(() => false) },
		};
		const { isIdle } = runInNewContext(transpile(`({${main.slice(start, main.indexOf("\n\tonChange:", start))}})`, { target: ScriptTarget.ES2022 }), context);
		return { isIdle: isIdle as () => Promise<boolean>, context, attachment, ensureReady, capture, fetch };
	}
	it.each(["empty", "chat", "tui"])("allows an idle mismatched daemon without recovery attachment, board=%s", async (mode) => {
		const h = mainIdleProbe();
		h.context.daemonStatus = { state: "error", code: "identity_mismatch", port: 3001 };
		h.capture.mockRejectedValue(new Error("no recovery attachment"));
		const fetchNormally = h.fetch.getMockImplementation()!;
		h.fetch.mockImplementation(async (url) => url.endsWith("/sessions")
			? { ok: true, json: async () => ({ sessions: mode === "empty" ? [] : [{ ...idleSession, mode }] }) }
			: fetchNormally(url));
		expect(await h.isIdle()).toBe(true);
		expect(h.capture).not.toHaveBeenCalled();
		if (mode === "chat") expect(h.fetch).toHaveBeenCalledWith("http://127.0.0.1:3001/api/v1/sessions/worker-1/conversation/side-chats", expect.any(Object));
	});
	it("keeps the physical mismatch attachment stable across identical status polls", () => {
		const main = fs.readFileSync(path.join(import.meta.dirname, "../main.ts"), "utf8");
		const start = main.indexOf("function setDaemonStatus(");
		const context = {
			daemonStatus: { state: "error", code: "identity_mismatch", port: 3001, pid: 11 }, daemonAttachmentGeneration: 1,
			reportDaemonFailure: vi.fn(), disposeBrowserRuntimeLink: vi.fn(), disposeBrowserStreamLink: vi.fn(),
			sideStateTimer: undefined, telemetryIdentity: { invalidate: vi.fn(), flush: vi.fn() },
			getShellWebContents: () => undefined, browserViewHost: undefined, daemonProcess: undefined, appRunId: "launch",
			sideLaunchRecovery: { setAttachment: vi.fn(() => false), status: "unavailable" }, sideCloseController: { setAttachment: vi.fn() }, clearInterval,
		};
		runInNewContext(transpile(main.slice(start, main.indexOf("\nconst MAX_DAEMON_OUTPUT", start)), { target: ScriptTarget.ES2022 }), context);
		const setStatus = (context as typeof context & { setDaemonStatus(status: unknown): void }).setDaemonStatus;
		setStatus({ ...context.daemonStatus }); setStatus({ ...context.daemonStatus });
		expect(context.daemonAttachmentGeneration).toBe(1);
		setStatus({ ...context.daemonStatus, pid: 12 });
		expect(context.daemonAttachmentGeneration).toBe(2);
	});
	it("can complete an explicitly confirmed retained-side close while normal recovery is detached", async () => {
		const h = retainedDiscardIpc();
		await expect(h.invoke(["side"])).resolves.toBeUndefined();
		expect(h.context.closeMismatchedSide).toHaveBeenCalledOnce();
		expect(h.context.sideLaunchState.sides).toEqual([{ side: { id: "other" } }]);
		expect(h.context.sideLaunchRecovery.captureForClose).not.toHaveBeenCalled();
		expect(h.context.sideCloseController.reconcile).toHaveBeenCalledOnce();
	});
	function retainedDiscardIpc() {
		const main = fs.readFileSync(path.join(import.meta.dirname, "../main.ts"), "utf8");
		const start = main.indexOf('ipcMain.handle("sideChats:discardRetained"');
		let handler!: (event: { sender: object }, ids: unknown) => Promise<void>;
		const shell = {};
		const context = {
			ipcMain: { handle: (_channel: string, next: typeof handler) => { handler = next; } }, getShellWebContents: () => shell,
			ExpectedIpcError: Error, daemonStatus: { state: "error", code: "identity_mismatch", port: 3001, pid: 11 }, daemonAttachmentGeneration: 1, appRunId: "launch",
			sideLaunchState: { appRunId: "launch", sides: [{ side: { id: "side" } }, { side: { id: "other" } }] },
			sideCloseRecovery: new SideCloseRecovery(), sideCloseController: { reconcile: vi.fn() },
			sideLaunchRecovery: { captureForClose: vi.fn(async () => { throw new Error("normal recovery detached"); }) },
			closeMismatchedSide: vi.fn(async (_options: { sideId: string; onChange(): void }) => undefined), installedBuildGuard: { check: vi.fn(async () => undefined) },
		};
		context.sideCloseRecovery.remember(context.sideLaunchState);
		context.closeMismatchedSide.mockImplementation(async ({ sideId, onChange }) => {
			context.sideCloseRecovery.begin(sideId);
			context.sideCloseRecovery.reconcileMissing(sideId, { appRunId: "launch", sides: [] }, { ids: [sideId] });
			onChange();
		});
		runInNewContext(transpile(main.slice(start, main.indexOf('\nipcMain.handle("editorHandoff:pathAction"', start)), { target: ScriptTarget.ES2022 }), context);
		return { context, invoke: (ids: unknown, sender = shell) => handler({ sender }, ids) };
	}
	it("discards only the retained side identities approved in the dialog", async () => {
		const h = retainedDiscardIpc();
		await h.invoke(["side"]);
		expect(h.context.closeMismatchedSide).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ sideId: "side", attachment: { generation: 1, port: 3001, pid: 11, appRunId: "launch" } }));
		expect(h.context.sideLaunchState.sides).toEqual([{ side: { id: "other" } }]);
		expect(h.context.installedBuildGuard.check).toHaveBeenCalledOnce();
	});
	it.each([{ ids: [] }, { ids: ["unknown"] }, { ids: ["side", "side"] }, { ids: [7] }, { ids: "side" }])("rejects invalid or stale discard selection: %j", async ({ ids }) => {
		const h = retainedDiscardIpc();
		await expect(h.invoke(ids)).rejects.toThrow();
		expect(h.context.closeMismatchedSide).not.toHaveBeenCalled();
	});
	it("rejects retained-discard requests after a normal daemon attachment resumes", async () => {
		const h = retainedDiscardIpc(); h.context.daemonStatus.state = "ready";
		await expect(h.invoke(["side"])).rejects.toThrow();
		expect(h.context.closeMismatchedSide).not.toHaveBeenCalled();
	});
	it("rejects discard requests from a non-shell sender", async () => {
		const h = retainedDiscardIpc();
		await expect(h.invoke(["side"], {})).rejects.toThrow();
		expect(h.context.closeMismatchedSide).not.toHaveBeenCalled();
	});
	it("stops a confirmed batch if the daemon attachment changes", async () => {
		const h = retainedDiscardIpc();
		h.context.closeMismatchedSide.mockImplementationOnce(async () => { h.context.daemonAttachmentGeneration++; });
		await expect(h.invoke(["side", "other"])).rejects.toThrow(/attachment changed/);
		expect(h.context.closeMismatchedSide).toHaveBeenCalledOnce();
		expect(h.context.installedBuildGuard.check).toHaveBeenCalledOnce();
	});
	it("still protects retained local side state when daemon identities differ", async () => {
		const h = mainIdleProbe();
		h.context.daemonStatus = { state: "error", code: "identity_mismatch", port: 3001 };
		h.context.sideLaunchState = { sides: [{ side: { id: "side-1", label: "Design notes" } }] };
		h.capture.mockRejectedValue(new Error("no recovery attachment"));
		expect(await h.isIdle()).not.toBe(true);
	});
	it.each(["unavailable", "invalid", "attachment changed", "open side"])("rejects unsafe mismatched-daemon side facts: %s", async (condition) => {
		const h = mainIdleProbe();
		h.context.daemonStatus = { state: "error", code: "identity_mismatch", port: 3001 };
		const fetchNormally = h.fetch.getMockImplementation()!;
		h.fetch.mockImplementation(async url => {
			if (!url.endsWith("/side-chats")) return fetchNormally(url);
			if (condition === "attachment changed") h.context.daemonAttachmentGeneration++;
			return { ok: condition !== "unavailable", json: async () => ({ sides: condition === "invalid" ? null : condition === "open side" ? [{ id: "remote-side", label: "Review notes" }] : [] }) };
		});
		const result = await h.isIdle();
		expect(result).not.toBe(true);
		if (condition === "open side") expect(result).toEqual({ openSideChats: ["Review notes"] });
		expect(h.capture).not.toHaveBeenCalled();
	});
	it("names an idle side that the user must close before restarting", async () => {
		const h = mainIdleProbe([{ side: { id: "side-1", label: "Design notes", state: "ready", hasWork: false } }]);
		expect(await h.isIdle()).toEqual({ openSideChats: ["Design notes"] });
	});
	it.each(["unsent draft", ""])("keeps launch-scoped idle side conversations safe, draft=%j", async (draft) => {
		const h = mainIdleProbe([{ side: { id: "side-1", state: "ready", hasWork: false }, draft, messages: [{ text: "completed response" }] }]);
		expect(await h.isIdle()).toEqual({ openSideChats: ["side-1"] });
	});
	it("waits for side recovery before reading an apparently empty daemon", async () => {
		const h = mainIdleProbe();
		let finish!: () => void;
		h.ensureReady.mockImplementationOnce(() => new Promise<void>(resolve => { finish = resolve; }));
		const probing = h.isIdle();
		await vi.waitFor(() => expect(h.ensureReady).toHaveBeenCalled());
		expect(h.fetch).not.toHaveBeenCalled();
		finish();
		expect(await probing).toBe(true);
	});
	it("rejects empty side state from a changed daemon attachment", async () => {
		const h = mainIdleProbe();
		h.capture.mockImplementationOnce(async () => {
			h.context.daemonAttachmentGeneration++;
			return { attachment: { generation: 1, pid: 11, port: 3001, appRunId: "launch" }, state: { appRunId: "launch", sides: [] } };
		});
		expect(await h.isIdle()).toBe(false);
	});
	it("keeps unavailable side recovery busy", async () => {
		const h = mainIdleProbe();
		h.capture.mockRejectedValueOnce(new Error("restore unavailable"));
		expect(await h.isIdle()).toBe(false);
		expect(h.fetch).not.toHaveBeenCalled();
	});
	it("rechecks side state after asynchronous worker probes", async () => {
		const h = mainIdleProbe();
		h.capture.mockResolvedValueOnce({ attachment: h.attachment, state: { appRunId: "launch", sides: [] } })
			.mockResolvedValueOnce({ attachment: h.attachment, state: { appRunId: "launch", sides: [{ side: { id: "new-side" } }] } });
		expect(await h.isIdle()).toEqual({ openSideChats: ["new-side"] });
	});
	it.each([false, true])("waits for a main-owned unconfirmed close even with empty snapshots, startedDuringProbe=%s", async (startedDuringProbe) => {
		const h = mainIdleProbe();
		if (startedDuringProbe) h.capture.mockImplementationOnce(async () => {
			h.context.sideCloseController.hasPending.mockReturnValue(true);
			return { attachment: h.attachment, state: { appRunId: "launch", sides: [] } };
		});
		else h.context.sideCloseController.hasPending.mockReturnValue(true);
		expect(await h.isIdle()).toBe(false);
	});
	it("retains locally owned side-close state even if the daemon list is empty", async () => {
		const h = mainIdleProbe();
		h.context.sideLaunchState = { sides: [{ side: { id: "pending-close" }, cleanupOnly: true }] };
		expect(await h.isIdle()).toEqual({ openSideChats: ["pending-close"] });
	});
	it.each(["active", "waiting_input", "blocked", "unknown"])("waits for %s sessions", (state) => {
		expect(sessionsAreIdle({ sessions: [{ activity: { state }, statusReadiness: "ready" }] })).toBe(false);
	});
	it("accepts idle, exited and terminated sessions, including an empty board", () => {
		expect(sessionsAreIdle({ sessions: [] })).toBe(true);
		expect(sessionsAreIdle({ sessions: [idleSession, { ...idleSession, activity: { ...idleSession.activity, state: "exited" }, status: "exited" }, { isTerminated: true }] })).toBe(true);
	});
	it.each([null, {}, { sessions: null }, { sessions: [{}] }, { sessions: [{ activity: { state: "idle" }, statusReadiness: "checking" }] }, { sessions: [{ activity: { state: "idle" }, statusReadiness: "ready", provisionState: "provisioning" }] }])("fails closed for missing or unsettled evidence: %j", (body) => {
		expect(sessionsAreIdle(body)).toBe(false);
	});
});

const temporary: string[] = [];
afterEach(() => { for (const dir of temporary.splice(0)) fs.rmSync(dir, { recursive: true, force: true }); });

it("hashes archive bytes rather than version, path, size, or modification time", async () => {
	const dir = fs.mkdtempSync(path.join(os.tmpdir(), "ao-installed-build-"));
	temporary.push(dir);
	const archive = path.join(dir, "desktop.AppImage");
	fs.writeFileSync(archive, "old-build");
	const read = createArchiveIdentityReader(archive, fs);
	const before = await read();
	const stamp = fs.statSync(archive);
	fs.writeFileSync(archive, "new-build");
	fs.utimesSync(archive, stamp.atime, stamp.mtime);
	expect(await read()).not.toBe(before);
	fs.unlinkSync(archive);
	expect(await read()).toBeNull();
	fs.writeFileSync(archive, "new-build");
	expect(await read()).not.toBeNull();
});

it("reads physical identity synchronously for the native handoff boundary", () => {
	const dir = fs.mkdtempSync(path.join(os.tmpdir(), "ao-installed-sync-"));
	temporary.push(dir);
	const archive = path.join(dir, "desktop.AppImage");
	fs.writeFileSync(archive, "A");
	const read = createArchiveIdentityReader(archive, fs);
	const running = read();
	expect(typeof running).toBe("string");
	fs.writeFileSync(archive, "B");
	expect(read()).not.toBe(running);
	fs.unlinkSync(archive);
	expect(read()).toBeNull();
});

it("does not identify a truncated ASAR as an installed build even when its size stops changing", async () => {
	const dir = fs.mkdtempSync(path.join(os.tmpdir(), "ao-installed-asar-"));
	temporary.push(dir);
	const source = path.join(dir, "source");
	fs.mkdirSync(source);
	fs.writeFileSync(path.join(source, "package.json"), JSON.stringify({ version: "0.14.8", main: "main.js" }));
	fs.writeFileSync(path.join(source, "main.js"), "old code");
	const archive = path.join(dir, "app.asar");
	await createPackage(source, archive);
	const read = createArchiveIdentityReader(archive, fs);
	// This packager can resolve before its output stream has finished flushing.
	await vi.waitFor(async () => expect(await read()).not.toBeNull());
	fs.truncateSync(archive, fs.statSync(archive).size - 1);
	expect(await read()).toBeNull();
	expect(await read()).toBeNull();
});

it("keeps polling when filesystem watches fail and disposes its timers", async () => {
	vi.useFakeTimers();
	try {
		const check = vi.fn(async () => undefined);
		const watching = watchInstalledBuild({ check }, { ...fs, watch: () => { throw new Error("watch unavailable"); } } as typeof fs, ["/missing"]);
		await vi.advanceTimersByTimeAsync(30_000);
		expect(check.mock.calls.length).toBeGreaterThanOrEqual(2);
		watching.dispose();
		const calls = check.mock.calls.length;
		await vi.advanceTimersByTimeAsync(60_000);
		expect(check).toHaveBeenCalledTimes(calls);
	} finally { vi.useRealTimers(); }
});
