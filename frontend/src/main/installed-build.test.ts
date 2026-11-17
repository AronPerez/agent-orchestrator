// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import * as fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { createPackage } from "@electron/asar";
import type { components } from "../api/schema";
import { createArchiveIdentityReader, InstalledBuildGuard, sessionsAreIdle, sessionsAndReviewsAreIdle, watchInstalledBuild } from "./installed-build";

function guardHarness() {
	let identity: string | null = "running-asar";
	let idle = true;
	const onChange = vi.fn();
	const relaunch = vi.fn();
	const isIdle = vi.fn(async () => idle);
	const assertRestartSafe = vi.fn<() => void>();
	const onReplacement = vi.fn();
	const guard = new InstalledBuildGuard({ readIdentity: async () => identity, isIdle, onChange, onReplacement, assertRestartSafe, relaunch });
	return { guard, onChange, onReplacement, relaunch, isIdle, assertRestartSafe, replace: (next: string | null) => { identity = next; }, busy: () => { idle = false; }, idle: () => { idle = true; } };
}

describe("installed desktop build", () => {
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
		const restarted = new InstalledBuildGuard({ readIdentity: async () => "new-asar", isIdle: async () => true, onChange, onReplacement: () => undefined, assertRestartSafe: () => undefined, relaunch: vi.fn() });
		await restarted.check();
		await restarted.check();
		expect(restarted.status).toBeUndefined();
		expect(onChange).not.toHaveBeenCalled();
	});
});

describe("idle evidence", () => {
	const idleSession = {
		id: "worker-1", isTerminated: false, status: "idle", statusReadiness: "ready",
		activity: { state: "idle", lastActivityAt: "2026-11-16T20:00:00Z" },
	} satisfies Pick<components["schemas"]["ControllersSessionView"], "id" | "isTerminated" | "status" | "statusReadiness" | "activity">;
	it.each([{ status: "no_signal" }, { runtimeUnreachable: true }])("rejects stored idle activity with unavailable live facts: %j", (facts) => {
		expect(sessionsAreIdle({ sessions: [{ ...idleSession, ...facts }] })).toBe(false);
	});
	it.each(["Approved", "Mergeable"])("checks all running review passes even when the display says %s", async (displayStatus) => {
		const readReviews = vi.fn(async (_sessionId: string) => ({ runs: [{ status: "complete", prUrl: "pr-1" }, { status: "running", prUrl: "pr-2" }] }));
		expect(await sessionsAndReviewsAreIdle({ sessions: [{ ...idleSession, displayStatus, status: displayStatus === "Approved" ? "approved" : "mergeable" }] }, readReviews)).toBe(false);
		expect(readReviews).toHaveBeenCalledWith("worker-1");
	});
	it("requires available review facts for every live session and ignores terminated sessions", async () => {
		const body = { sessions: [idleSession, { ...idleSession, id: "worker-2" }, { isTerminated: true }] };
		const readReviews = vi.fn(async (_sessionId: string) => ({ runs: [] }));
		expect(await sessionsAndReviewsAreIdle(body, readReviews)).toBe(true);
		expect(readReviews.mock.calls.map(([id]) => id)).toEqual(["worker-1", "worker-2"]);
		readReviews.mockRejectedValueOnce(new Error("unavailable"));
		expect(await sessionsAndReviewsAreIdle(body, readReviews)).toBe(false);
		readReviews.mockResolvedValueOnce({} as { runs: never[] });
		expect(await sessionsAndReviewsAreIdle(body, readReviews)).toBe(false);
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
