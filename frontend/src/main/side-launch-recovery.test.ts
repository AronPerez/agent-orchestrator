import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { closeMismatchedSide, SideLaunchRecovery, type SideLaunchAttachment } from "./side-launch-recovery";
import type { SideLaunchSnapshot } from "./side-close";
import { SideCloseRecovery } from "./side-close";
import { resetMainErrorCaptureForTests, setMainErrorSink } from "./main-error-capture";

const sentryCapture = vi.fn();

const attachment: SideLaunchAttachment = { generation: 1, pid: 11, port: 3002, appRunId: "run" };
const controllers: SideLaunchRecovery[] = [];
const ok = (state: unknown = { appRunId: "run", sides: [] }) => ({ ok: true, status: 200, json: async () => state }) as Response;
const status = (code: number) => ({ ok: false, status: code }) as Response;
const hung = (signal: AbortSignal) => new Promise<Response>((_resolve, reject) => signal.addEventListener("abort", () => reject(signal.reason), { once: true }));

function setup(snapshot?: SideLaunchSnapshot) {
	const fetcher = vi.fn<typeof fetch>().mockResolvedValue(ok());
	const options = { fetch: fetcher, snapshot: vi.fn(() => snapshot), onRestore: vi.fn(), onReady: vi.fn(), onSnapshot: vi.fn(), onError: vi.fn() };
	const recovery = new SideLaunchRecovery(options);
	controllers.push(recovery);
	return { recovery, fetcher, ...options };
}
beforeEach(() => { vi.useFakeTimers(); resetMainErrorCaptureForTests(); sentryCapture.mockClear(); setMainErrorSink(sentryCapture); });
afterEach(() => { for (const controller of controllers.splice(0)) controller.setAttachment(undefined); resetMainErrorCaptureForTests(); vi.useRealTimers(); });

function mismatchClose() {
	const record = { side: { id: "side", sessionId: "worker-1", mainConversationId: "main", state: "ready", label: "Notes", launchConfig: "private side config" }, providerHostId: "btw-run-side", providerForkId: "fork", harness: "codex", draft: "private draft", turns: [{ state: "queued" }], launchConfig: "private launch config" };
	const other = { ...record, side: { ...record.side, id: "other" }, providerHostId: "btw-run-other" };
	const recovery = new SideCloseRecovery(); recovery.remember({ appRunId: "run", sides: [record, other] });
	const fetcher = vi.fn<typeof fetch>();
	const isCurrent = vi.fn<() => boolean | Promise<boolean>>(() => true), onChange = vi.fn();
	const close = () => closeMismatchedSide({ attachment, sideId: "side", recovery, fetch: fetcher, isCurrent, onChange });
	return { record, other, recovery, fetcher, isCurrent, onChange, close };
}
const noContent = () => ({ ok: true, status: 204 }) as Response;

it("closes an owned side through the registered route while preserving other retained sides", async () => {
	const h = mismatchClose();
	h.fetcher.mockResolvedValueOnce(ok({ appRunId: "run", sides: [h.record] })).mockResolvedValueOnce(noContent()).mockResolvedValueOnce(ok());
	await h.close();
	expect(h.fetcher.mock.calls.map(([url, init]) => [url, init?.method ?? "GET"])).toEqual([
		["http://127.0.0.1:3002/api/v1/side-chats/launch/state?appRunId=run", "GET"],
		["http://127.0.0.1:3002/api/v1/sessions/worker-1/conversation/side-chats/side", "DELETE"],
		["http://127.0.0.1:3002/api/v1/side-chats/launch/state?appRunId=run", "GET"],
	]);
	expect(h.recovery.state?.sides).toEqual([h.other]);
});
it("recovers only confirmed cleanup into an unclaimed mismatched daemon", async () => {
	const h = mismatchClose();
	h.fetcher.mockResolvedValueOnce({ ok: false, status: 409, json: async () => ({ code: "CHAT_SIDE_UNAVAILABLE" }) } as Response)
		.mockResolvedValueOnce(noContent()).mockResolvedValueOnce(noContent()).mockResolvedValueOnce(ok());
	await h.close();
	expect(h.fetcher.mock.calls[1][0]).toBe("http://127.0.0.1:3002/api/v1/side-chats/launch/claim");
	expect(h.fetcher.mock.calls[2][0]).toBe("http://127.0.0.1:3002/api/v1/side-chats/launch/state");
	const payload = JSON.parse(String(h.fetcher.mock.calls[2][1]?.body));
	expect(payload).toMatchObject({ appRunId: "run", sides: [{ cleanupOnly: true, side: { id: "side" }, providerHostId: "btw-run-side" }] });
	expect(payload.sides).toHaveLength(1);
	expect(JSON.stringify(payload)).not.toMatch(/private|turns|draft|launchConfig/);
	expect(h.recovery.state?.sides).toEqual([h.other]);
});
it("leaves unselected retained contents unchanged when a mismatched snapshot contains them", async () => {
	const h = mismatchClose();
	const staleOther = { ...h.other, draft: "older daemon draft" };
	h.fetcher.mockResolvedValueOnce(ok({ appRunId: "run", sides: [h.record, staleOther] })).mockResolvedValueOnce(noContent())
		.mockResolvedValueOnce(ok({ appRunId: "run", sides: [staleOther] }));
	await h.close();
	expect(h.recovery.state?.sides).toEqual([h.other]);
});
it("can pin a just-created owned side without overwriting other retained state", async () => {
	const h = mismatchClose(); h.recovery.remember({ appRunId: "run", sides: [h.other] });
	h.fetcher.mockResolvedValueOnce(ok({ appRunId: "run", sides: [h.record] })).mockResolvedValueOnce(noContent()).mockResolvedValueOnce(ok());
	await h.close();
	expect(h.recovery.state?.sides).toEqual([h.other]);
});
it.each(["unavailable", "stale snapshot", "changed attachment", "foreign host", "malformed record"])("keeps close ownership on %s without sending a mutation", async condition => {
	const h = mismatchClose();
	if (condition === "foreign host") h.recovery.remember({ appRunId: "run", sides: [{ ...h.record, providerHostId: "someone-else" }] });
	h.fetcher.mockImplementationOnce(async () => {
		if (condition === "changed attachment") h.isCurrent.mockReturnValue(false);
		return condition === "unavailable" ? { ok: false, status: 503, json: async () => ({}) } as Response : ok({ appRunId: condition === "stale snapshot" ? "other-launch" : "run", sides: condition === "malformed record" ? [{}] : [] });
	});
	await expect(h.close()).rejects.toThrow();
	expect(h.fetcher).toHaveBeenCalledTimes(1);
	expect(h.recovery.state?.sides).toContainEqual(expect.objectContaining({ cleanupOnly: true }));
});
it("retains cleanup until the daemon proves host and fork cleanup finished", async () => {
	const h = mismatchClose();
	h.fetcher.mockResolvedValueOnce(ok({ appRunId: "run", sides: [h.record] })).mockResolvedValueOnce(noContent())
		.mockResolvedValueOnce(ok({ appRunId: "run", sides: [{ ...h.record, cleanupOnly: true }] }));
	await expect(h.close()).rejects.toThrow(/still running/);
	expect(h.recovery.state?.sides).toContainEqual(expect.objectContaining({ cleanupOnly: true }));
});
it("finishes a later close retry without recreating cleanup on the same older daemon", async () => {
	const h = mismatchClose();
	h.fetcher.mockResolvedValueOnce(ok({ appRunId: "run", sides: [h.record] })).mockResolvedValueOnce(noContent())
		.mockResolvedValueOnce(ok({ appRunId: "run", sides: [{ ...h.record, cleanupOnly: true }] }));
	await expect(h.close()).rejects.toThrow(/still running/);
	h.fetcher.mockResolvedValueOnce(ok());
	await h.close();
	expect(h.fetcher).toHaveBeenCalledTimes(4);
	expect(h.recovery.state?.sides).toEqual([h.other]);
});
it("replays acknowledged cleanup after the physical daemon changes", async () => {
	const h = mismatchClose();
	h.fetcher.mockResolvedValueOnce(ok({ appRunId: "run", sides: [h.record] })).mockResolvedValueOnce(noContent())
		.mockResolvedValueOnce(ok({ appRunId: "run", sides: [{ ...h.record, cleanupOnly: true }] }));
	await expect(h.close()).rejects.toThrow(/still running/);
	h.fetcher.mockResolvedValueOnce(ok()).mockResolvedValueOnce(noContent()).mockResolvedValueOnce(noContent()).mockResolvedValueOnce(ok());
	await closeMismatchedSide({ attachment: { ...attachment, generation: 2, pid: 12 }, sideId: "side", recovery: h.recovery, fetch: h.fetcher, isCurrent: h.isCurrent, onChange: h.onChange });
	expect(h.fetcher).toHaveBeenCalledTimes(7);
	expect(h.fetcher.mock.calls[5][0]).toBe("http://127.0.0.1:3002/api/v1/side-chats/launch/state");
});
it("keeps cleanup if the daemon changes after accepting DELETE", async () => {
	const h = mismatchClose();
	h.fetcher.mockResolvedValueOnce(ok({ appRunId: "run", sides: [h.record] })).mockImplementationOnce(async () => {
		h.isCurrent.mockReturnValue(false); return noContent();
	});
	await expect(h.close()).rejects.toThrow(/attachment changed/);
	expect(h.recovery.state?.sides).toContainEqual(expect.objectContaining({ cleanupOnly: true }));
});

it.each(["headers", "body"])("rejects empty cleanup proof from a same-port replacement without a status notification, phase=%s", async phase => {
	const h = mismatchClose();
	let livePid = attachment.pid;
	h.isCurrent.mockImplementation(async () => livePid === attachment.pid);
	h.fetcher.mockResolvedValueOnce(ok()).mockResolvedValueOnce(noContent()).mockResolvedValueOnce(noContent())
		.mockImplementationOnce(async () => {
			if (phase === "headers") livePid = 12;
			return { ...ok(), json: async () => { livePid = 12; return { appRunId: "run", sides: [] }; } } as Response;
		});
	await expect(h.close()).rejects.toThrow(/attachment changed/);
	expect(h.recovery.state?.sides).toContainEqual(expect.objectContaining({ side: expect.objectContaining({ id: "side" }), cleanupOnly: true }));
	expect(h.recovery.cleanupAcknowledged("side", JSON.stringify(attachment))).toBe(false);
});
it("does not dispatch cleanup after an asynchronous physical identity check fails", async () => {
	const h = mismatchClose(); h.isCurrent.mockResolvedValue(false);
	await expect(h.close()).rejects.toThrow(/attachment changed/);
	expect(h.fetcher).not.toHaveBeenCalled();
	expect(h.recovery.state?.sides).toHaveLength(2);
});

it("reports one restore fault when recovery and background waiters observe the same error", async () => {
	const { recovery, onRestore } = setup({ appRunId: "run", sides: [{ sessionId: "private-session", sideId: "private-side" }] } as unknown as SideLaunchSnapshot);
	const error = new TypeError("private restore failure");
	onRestore.mockImplementation(() => { throw error; });
	recovery.setAttachment(attachment);
	recovery.captureInBackground(); recovery.captureInBackground();
	await vi.advanceTimersByTimeAsync(0);
	expect(sentryCapture).toHaveBeenCalledExactlyOnceWith({ category: "side_chat", reason: "recovery", error });
});

it("reports a background snapshot decoding fault after recovery completed", async () => {
	const { recovery, fetcher } = setup();
	recovery.setAttachment(attachment); await recovery.ensureReady();
	const error = new SyntaxError("private snapshot body");
	fetcher.mockResolvedValue({ ...ok(), json: async () => { throw error; } } as Response);
	recovery.captureInBackground();
	await vi.advanceTimersByTimeAsync(0);
	expect(sentryCapture).toHaveBeenCalledExactlyOnceWith({ category: "side_chat", reason: "recovery", error });
});

it.each([401, 403, 404, 408, 429, 503, 504])("keeps expected side-chat HTTP %i state out of Sentry", async code => {
	const { recovery, fetcher } = setup(); fetcher.mockResolvedValue(status(code));
	recovery.setAttachment(attachment); recovery.captureInBackground();
	await vi.advanceTimersByTimeAsync(0);
	expect(sentryCapture).not.toHaveBeenCalled();
});

it("keeps network loss, timeout and attachment replacement out of Sentry", async () => {
	const { recovery, fetcher } = setup(); fetcher.mockRejectedValueOnce(new TypeError("Failed to fetch"));
	recovery.setAttachment(attachment); recovery.captureInBackground();
	await vi.advanceTimersByTimeAsync(0);
	fetcher.mockImplementationOnce((_url, init) => hung(init!.signal!));
	await vi.advanceTimersByTimeAsync(6_000);
	recovery.setAttachment(undefined);
	await vi.advanceTimersByTimeAsync(0);
	expect(sentryCapture).not.toHaveBeenCalled();
});

it("recovers once and adds no requests or timers while healthy", async () => {
	const { recovery, fetcher, onReady } = setup();
	recovery.setAttachment(attachment);
	await recovery.ensureReady();
	for (let i = 0; i < 10; i++) recovery.setAttachment({ ...attachment });
	await vi.advanceTimersByTimeAsync(300_000);
	expect(fetcher).toHaveBeenCalledTimes(1);
	expect(onReady).toHaveBeenCalledOnce();
	expect(vi.getTimerCount()).toBe(0);
});

it("releases unused HTTP bodies so retrying does not strand loopback connections", async () => {
	const { recovery, fetcher } = setup();
	const cancel = vi.fn().mockResolvedValue(undefined);
	fetcher.mockResolvedValueOnce({ ...status(503), body: { cancel } } as unknown as Response);
	recovery.setAttachment(attachment);
	await vi.advanceTimersByTimeAsync(0);
	expect(cancel).toHaveBeenCalledOnce();
	const ready = recovery.ensureReady();
	await vi.advanceTimersByTimeAsync(1000);
	await ready;
});

it.each([408, 429, 500, 503])("backs off transient HTTP %i failures while the daemon stays ready", async code => {
	const { recovery, fetcher } = setup();
	fetcher.mockResolvedValue(status(code));
	recovery.setAttachment(attachment);
	await vi.advanceTimersByTimeAsync(0);
	for (const [i, delay] of [1000, 2000, 4000, 8000, 16000, 30000, 30000].entries()) {
		await vi.advanceTimersByTimeAsync(delay - 1);
		expect(fetcher).toHaveBeenCalledTimes(i + 1);
		await vi.advanceTimersByTimeAsync(1);
		expect(fetcher).toHaveBeenCalledTimes(i + 2);
	}
	fetcher.mockResolvedValue(ok());
	const ready = recovery.ensureReady();
	await vi.advanceTimersByTimeAsync(30_000);
	await ready;
	expect(vi.getTimerCount()).toBe(0);
});

it("retries a claim timeout at five seconds without overlapping attempts", async () => {
	const { recovery, fetcher } = setup();
	fetcher.mockImplementationOnce((_url, init) => hung(init!.signal!));
	recovery.setAttachment(attachment);
	const first = recovery.ensureReady().catch(error => error);
	const alsoFirst = recovery.capture().catch(error => error);
	await vi.advanceTimersByTimeAsync(5000);
	expect((await first).message).toContain("timed out");
	expect(await alsoFirst).toBe(await first);
	expect(fetcher).toHaveBeenCalledTimes(1);
	const next = recovery.ensureReady();
	await vi.advanceTimersByTimeAsync(1000);
	await next;
	expect(fetcher).toHaveBeenCalledTimes(2);
});

it("releases serialized snapshot batches when transport ignores abort and ignores its late result", async () => {
	const { recovery, fetcher, onSnapshot } = setup();
	recovery.setAttachment(attachment); await recovery.ensureReady();
	let finish!: (response: Response) => void;
	fetcher.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
	const first = recovery.captureForClose().catch(error => error);
	await vi.advanceTimersByTimeAsync(0);
	const next = recovery.captureForClose();
	await vi.advanceTimersByTimeAsync(5_000);
	expect((await first).message).toContain("timed out");
	await next;
	expect(onSnapshot).toHaveBeenCalledOnce();
	const cancel = vi.fn().mockResolvedValue(undefined);
	finish({ ...ok({ appRunId: "run", sides: [{ stale: true }] }), body: { cancel } } as unknown as Response);
	await vi.advanceTimersByTimeAsync(0);
	expect(onSnapshot).toHaveBeenCalledOnce();
	expect(cancel).toHaveBeenCalledOnce();
});

it("retries the exact frozen restore after sixty seconds and releases the payload on success", async () => {
	const snapshot = { appRunId: "run", sides: [{ id: "original" }], numbers: { session: 4 } };
	const { recovery, fetcher, snapshot: getSnapshot, onRestore } = setup(snapshot);
	fetcher.mockResolvedValueOnce(ok()).mockImplementationOnce((_url, init) => hung(init!.signal!));
	recovery.setAttachment(attachment);
	const first = recovery.ensureReady().catch(error => error);
	await vi.advanceTimersByTimeAsync(15_001);
	expect(fetcher).toHaveBeenCalledTimes(2);
	snapshot.sides.push({ id: "newer local state" });
	await vi.advanceTimersByTimeAsync(44_999);
	expect((await first).message).toContain("timed out");
	const retry = recovery.ensureReady();
	await vi.advanceTimersByTimeAsync(1000);
	await retry;
	expect(fetcher).toHaveBeenCalledTimes(3); // Do not reclaim after acknowledged claim.
	expect(fetcher.mock.calls[2][1]?.body).toBe(fetcher.mock.calls[1][1]?.body);
	expect(String(fetcher.mock.calls[2][1]?.body)).not.toContain("newer local state");
	expect(getSnapshot).toHaveBeenCalledOnce();
	expect(onRestore).toHaveBeenCalledTimes(2);
	await vi.advanceTimersByTimeAsync(120_000);
	expect(fetcher).toHaveBeenCalledTimes(3);
});

it.each([400, 401, 403, 409, 422])("does not spin on permanent rejection %i", async code => {
	const { recovery, fetcher } = setup();
	fetcher.mockResolvedValue(status(code));
	recovery.setAttachment(attachment);
	await expect(recovery.ensureReady()).rejects.toThrow(`HTTP ${code}`);
	await vi.advanceTimersByTimeAsync(120_000);
	expect(fetcher).toHaveBeenCalledOnce();
	expect(vi.getTimerCount()).toBe(0);
});

it("coalesces concurrent captures into one read plus a follow-up for later mutations", async () => {
	const { recovery, fetcher, onSnapshot } = setup();
	recovery.setAttachment(attachment);
	await recovery.ensureReady();
	let finish!: (response: Response) => void;
	fetcher.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
	const first = recovery.capture();
	await vi.advanceTimersByTimeAsync(0);
	const later = Array.from({ length: 100 }, () => recovery.capture());
	await vi.advanceTimersByTimeAsync(0);
	expect(fetcher).toHaveBeenCalledTimes(2); // Claim and the first read.
	finish(ok());
	await Promise.all([first, ...later]);
	expect(fetcher).toHaveBeenCalledTimes(3);
	expect(onSnapshot).toHaveBeenCalledTimes(2);
});

it("rejects snapshots from the wrong app launch", async () => {
	const { recovery, fetcher, onSnapshot } = setup();
	recovery.setAttachment(attachment);
	await recovery.ensureReady();
	fetcher.mockResolvedValueOnce(ok({ appRunId: "foreign", sides: [] }));
	await expect(recovery.capture()).rejects.toThrow("Stale");
	expect(onSnapshot).not.toHaveBeenCalled();
});

it("aborts obsolete requests and ignores late replies on daemon replacement", async () => {
	const { recovery, fetcher, onReady } = setup();
	let finish!: (response: Response) => void;
	fetcher.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
	recovery.setAttachment(attachment);
	const old = recovery.ensureReady().catch(error => error);
	const oldSignal = fetcher.mock.calls[0][1]!.signal!;
	recovery.setAttachment({ ...attachment, generation: 2, port: 3003 });
	expect(oldSignal.aborted).toBe(true);
	await recovery.ensureReady();
	finish(ok());
	await vi.advanceTimersByTimeAsync(0);
	expect((await old).message).toContain("attachment changed");
	expect(onReady).toHaveBeenCalledOnce();
	expect(fetcher).toHaveBeenCalledTimes(2);
});

it("disposes scheduled retries when the daemon leaves", async () => {
	const { recovery, fetcher } = setup();
	fetcher.mockRejectedValue(new TypeError("network unavailable"));
	recovery.setAttachment(attachment);
	await vi.advanceTimersByTimeAsync(0);
	const waiting = recovery.capture().catch(error => error);
	recovery.setAttachment(undefined);
	await waiting;
	await vi.advanceTimersByTimeAsync(300_000);
	expect(fetcher).toHaveBeenCalledOnce();
	expect(vi.getTimerCount()).toBe(0);
});

it("does not apply a stale snapshot or resolve old waiters into a replacement daemon", async () => {
	const { recovery, fetcher, onSnapshot } = setup();
	recovery.setAttachment(attachment);
	await recovery.ensureReady();
	let finish!: (response: Response) => void;
	fetcher.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
	const old = recovery.capture().catch(error => error);
	await vi.advanceTimersByTimeAsync(0);
	const queued = recovery.capture().catch(error => error);
	await vi.advanceTimersByTimeAsync(0);
	recovery.setAttachment({ ...attachment, generation: 2 });
	await recovery.ensureReady();
	finish(ok({ appRunId: "run", sides: [{ stale: true }] }));
	await vi.advanceTimersByTimeAsync(0);
	expect(await old).toBeInstanceOf(Error);
	expect(await queued).toBeInstanceOf(Error);
	expect(onSnapshot).not.toHaveBeenCalled();
	await recovery.capture();
	expect(onSnapshot).toHaveBeenCalledOnce();
});

it("bounds capture time and permits a fresh follow-up without reclaiming", async () => {
	const { recovery, fetcher } = setup();
	recovery.setAttachment(attachment);
	await recovery.ensureReady();
	fetcher.mockImplementationOnce((_url, init) => hung(init!.signal!));
	const slow = recovery.capture().catch(error => error);
	await vi.advanceTimersByTimeAsync(0);
	const fresh = recovery.capture();
	await vi.advanceTimersByTimeAsync(5000);
	expect((await slow).message).toContain("timed out");
	await fresh;
	expect(fetcher.mock.calls.filter(([url]) => String(url).endsWith("/claim"))).toHaveLength(1);
	expect(fetcher).toHaveBeenCalledTimes(3);
});

it("retains accepted close ownership when the background snapshot fails", async () => {
	const ledger = new SideCloseRecovery();
	ledger.remember({ appRunId: "run", sides: [{ side: { id: "side", sessionId: "session" }, providerHostId: "host", providerForkId: "fork" }] });
	ledger.begin("side");
	const { recovery, fetcher, onError } = setup();
	recovery.setAttachment(attachment);
	await recovery.ensureReady();
	ledger.confirm("side"); // The daemon's DELETE already succeeded.
	fetcher.mockRejectedValueOnce(new Error("snapshot transport failed"));
	expect(recovery.captureInBackground()).toBeUndefined();
	expect(ledger.pending()).toEqual([]);
	await vi.advanceTimersByTimeAsync(0);
	expect(onError).toHaveBeenCalledOnce();
	expect(ledger.state?.sides).toMatchObject([{ cleanupOnly: true, providerForkId: "fork" }]);
	await recovery.capture(); // A later capture still works without another claim.
	expect(fetcher.mock.calls.filter(([url]) => String(url).endsWith("/claim"))).toHaveLength(1);
});

it("permits claimed close capture after an unrelated permanent restore rejection", async () => {
	const { recovery, fetcher } = setup({ appRunId: "run", sides: [{ side: { id: "bad", sessionId: "terminated" } }] });
	fetcher.mockResolvedValueOnce(ok()).mockResolvedValueOnce(status(409)).mockResolvedValueOnce(ok({ appRunId: "run", sides: [] }));
	recovery.setAttachment(attachment);
	await recovery.ensureClaimed();
	await expect(recovery.ensureReady()).rejects.toThrow("409");
	expect((await recovery.captureForClose()).coverage).toEqual({ ids: [], ready: false });
	expect(fetcher).toHaveBeenCalledTimes(3);
});

it("captures a verified open side mutation after restore rejection without losing another private original", async () => {
	const blocked = { side: { id: "blocked", sessionId: "terminated" }, providerHostId: "private-host", providerForkId: "private-fork", draft: { text: "original" } };
	const open = { side: { id: "open", sessionId: "usable" }, providerHostId: "open-host", draft: { text: "before" } };
	const ledger = new SideCloseRecovery(); ledger.remember({ appRunId: "run", sides: [blocked, open] });
	const { recovery, fetcher, snapshot: getSnapshot, onSnapshot } = setup(ledger.state);
	getSnapshot.mockImplementation(() => ledger.state);
	onSnapshot.mockImplementation((state, coverage) => ledger.remember(state, coverage));
	fetcher.mockResolvedValueOnce(ok()).mockResolvedValueOnce(status(409));
	recovery.setAttachment(attachment);
	await expect(recovery.ensureReady()).rejects.toThrow("409");
	const updated = { ...open, draft: { text: "latest draft and delivery receipt", pendingDelivery: { id: "receipt" } } };
	fetcher.mockResolvedValueOnce(ok({ appRunId: "run", sides: [updated] }));
	const capture = await recovery.captureForClose();
	expect(capture.coverage).toEqual({ ids: [], ready: false });
	expect(ledger.state?.sides).toEqual([updated, blocked]);
	// A second crash still offers both the latest mutation and the private
	// original which the first replacement daemon could not restore.
	recovery.setAttachment({ ...attachment, generation: 2, pid: 22 });
	await recovery.ensureReady();
	const offered = JSON.parse(String(fetcher.mock.calls.findLast(([url, init]) => String(url).endsWith("/state") && init?.method === "POST")?.[1]?.body));
	expect(offered.sides).toEqual([updated, blocked]);
});

it("keeps a pre-ack close capture partial and requires a fresh post-ack read", async () => {
	const record = { side: { id: "side", sessionId: "session", mainConversationId: "main" }, providerHostId: "host", providerForkId: "fork" };
	const ledger = new SideCloseRecovery(); ledger.remember({ appRunId: "run", sides: [record] }); ledger.begin("side");
	const offered = ledger.state!;
	const { recovery, fetcher, onSnapshot } = setup(offered);
	onSnapshot.mockImplementation((state, coverage) => ledger.remember(state, coverage));
	let acknowledge!: (value: Response) => void, finishRead!: (value: Response) => void;
	fetcher.mockResolvedValueOnce(ok()).mockReturnValueOnce(new Promise(yes => { acknowledge = yes; }));
	recovery.setAttachment(attachment); await recovery.ensureClaimed();
	fetcher.mockReturnValueOnce(new Promise(yes => { finishRead = yes; }));
	const early = recovery.captureForClose(); await vi.advanceTimersByTimeAsync(0);
	acknowledge(ok()); await recovery.ensureReady(); ledger.markRestoreAccepted(offered);
	finishRead(ok());
	expect((await early).coverage).toEqual({ ids: [], ready: false });
	expect(ledger.pending()[0].phase).toBe("cleanup_pending");
	const later = await recovery.captureForClose();
	expect(later.coverage).toEqual({ ids: ["side"], ready: true });
	expect(ledger.pending()).toEqual([]);
});

it("does not grant cleanup absence proof for an acknowledged OPEN payload", async () => {
	const record = { side: { id: "side", sessionId: "session", mainConversationId: "main" }, providerHostId: "host", providerForkId: "fork" };
	const { recovery } = setup({ appRunId: "run", sides: [record] });
	recovery.setAttachment(attachment); await recovery.ensureReady();
	expect((await recovery.captureForClose()).coverage).toEqual({ ids: [], ready: true });
});
