// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import * as fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { createPackage } from "@electron/asar";
import { createArchiveIdentityReader, InstalledBuildGuard, sessionsAreIdle, watchInstalledBuild } from "./installed-build";

function guardHarness() {
	let identity: string | null = "running-asar";
	let idle = true;
	const onChange = vi.fn();
	const relaunch = vi.fn();
	const isIdle = vi.fn(async () => idle);
	const guard = new InstalledBuildGuard({ readIdentity: async () => identity, isIdle, onChange, relaunch });
	return { guard, onChange, relaunch, isIdle, replace: (next: string | null) => { identity = next; }, busy: () => { idle = false; }, idle: () => { idle = true; } };
}

describe("installed desktop build", () => {
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

	it("blocks renderer loads immediately on replacement, before the prompt settles", async () => {
		const h = guardHarness();
		expect(await h.guard.canLoadRenderer()).toBe(true);
		h.replace("new-asar");
		expect(await h.guard.canLoadRenderer()).toBe(false);
		h.replace(null);
		expect(await h.guard.canLoadRenderer()).toBe(false);
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
		const restarted = new InstalledBuildGuard({ readIdentity: async () => "new-asar", isIdle: async () => true, onChange, relaunch: vi.fn() });
		await restarted.check();
		await restarted.check();
		expect(restarted.status).toBeUndefined();
		expect(onChange).not.toHaveBeenCalled();
	});
});

describe("idle evidence", () => {
	it.each(["active", "waiting_input", "blocked", "unknown"])("waits for %s sessions", (state) => {
		expect(sessionsAreIdle({ sessions: [{ activity: { state }, statusReadiness: "ready" }] })).toBe(false);
	});
	it("accepts idle, exited and terminated sessions, including an empty board", () => {
		expect(sessionsAreIdle({ sessions: [] })).toBe(true);
		expect(sessionsAreIdle({ sessions: [{ activity: { state: "idle" }, statusReadiness: "ready" }, { activity: { state: "exited" }, statusReadiness: "ready" }, { isTerminated: true }] })).toBe(true);
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
	expect(await read()).not.toBeNull();
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
