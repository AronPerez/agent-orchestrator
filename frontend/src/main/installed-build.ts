import { createHash } from "node:crypto";
import type * as fs from "node:fs";
import type { InstalledBuildStatus } from "./update-settings";

export type InstalledBuildRestart = "now" | "when-idle" | "cancel";

// Read the ASAR's length-delimited header through raw fs, avoiding Electron's
// cached archive index. A paused copy must not become a restart target.
function asarIsComplete(file: string, disk: typeof fs): boolean {
	const fd = disk.openSync(file, "r");
	try {
		const prefix = Buffer.alloc(16);
		if (disk.readSync(fd, prefix, 0, 16, 0) !== 16 || prefix.readUInt32LE(0) !== 4) return false;
		const headerSize = prefix.readUInt32LE(4);
		const jsonSize = prefix.readUInt32LE(12);
		const payloadSize = disk.fstatSync(fd).size - 8 - headerSize;
		if (headerSize < 8 || headerSize > 64 * 1024 * 1024 || jsonSize > headerSize - 8 || payloadSize < 0) return false;
		const json = Buffer.alloc(jsonSize);
		if (disk.readSync(fd, json, 0, jsonSize, 16) !== jsonSize) return false;
		type Entry = { files?: Record<string, Entry>; offset?: string; size?: number; unpacked?: boolean; link?: string };
		const header = JSON.parse(json.toString("utf8")) as Entry;
		if (!header.files?.["package.json"]) return false;
		const entries = Object.values(header.files);
		let end = 0;
		for (let index = 0; index < entries.length; index += 1) {
			if (index > 1_000_000) return false;
			const entry = entries[index];
			if (entry.files) entries.push(...Object.values(entry.files));
			else if (!entry.unpacked && !entry.link) {
				const offset = Number(entry.offset);
				if (!Number.isSafeInteger(offset) || offset < 0 || !Number.isSafeInteger(entry.size) || entry.size! < 0) return false;
				end = Math.max(end, offset + entry.size!);
			}
		}
		return end === payloadSize;
	} finally { disk.closeSync(fd); }
}

/** Uses original-fs in Electron: its patched fs can cache a replaced ASAR. */
export function createArchiveIdentityReader(file: string, disk: typeof fs): () => string | null {
	let cached: { stamp: string; identity: string } | undefined;
	const stamp = () => {
		const st = disk.statSync(file);
		return st.isFile() && st.size > 0 ? `${st.dev}:${st.ino}:${st.size}:${st.mtimeMs}:${st.ctimeMs}` : null;
	};
	return () => {
		try {
			const before = stamp();
			if (!before) return null;
			if (cached?.stamp === before) return cached.identity;
			if (file.endsWith(".asar") && !asarIsComplete(file, disk)) return null;
			const hash = createHash("sha256");
			// MacUpdater's automatic handoff cannot await an event listener.
			const fd = disk.openSync(file, "r");
			try {
				const chunk = Buffer.alloc(1024 * 1024);
				let size: number;
				while ((size = disk.readSync(fd, chunk, 0, chunk.length, null)) > 0) hash.update(chunk.subarray(0, size));
			} finally { disk.closeSync(fd); }
			if (stamp() !== before) return null;
			cached = { stamp: before, identity: hash.digest("hex") };
			return cached.identity;
		} catch {
			return null; // Missing/in-progress installations are not a new identity.
		}
	};
}

/** Unknown, provisioning and approval states must never count as idle. */
export function sessionsAreIdle(body: unknown): boolean {
	if (!body || typeof body !== "object" || !("sessions" in body) || !Array.isArray(body.sessions)) return false;
	return body.sessions.every((session: unknown) => {
		if (!session || typeof session !== "object") return false;
		const s = session as { isTerminated?: boolean; status?: string; displayStatus?: string; statusReadiness?: string; provisionState?: string; runtimeUnreachable?: boolean; activity?: { state?: string } };
		if (s.isTerminated === true) return true;
		return s.isTerminated === false && s.statusReadiness === "ready" && s.runtimeUnreachable !== true && s.provisionState !== "provisioning" &&
			(s.activity?.state === "idle" || s.activity?.state === "exited") &&
			!["Reviewing", "Review scheduled", "Retrying review"].includes(s.displayStatus ?? "") &&
			["idle", "exited", "pr_open", "draft", "ci_failed", "review_pending", "changes_requested", "commented", "approved", "mergeable", "merged"].includes(s.status ?? "");
	});
}

/** Display labels can hide another PR's running reviewer. Read all live sessions. */
export async function sessionsAndReviewsAreIdle(body: unknown, readReviews: (sessionId: string) => Promise<unknown>, readReviewerConversation: (reviewId: string) => Promise<unknown>): Promise<boolean> {
	if (!sessionsAreIdle(body)) return false;
	try {
		for (const session of (body as { sessions: { id?: unknown; isTerminated?: boolean }[] }).sessions) {
			if (session.isTerminated === true) continue;
			if (typeof session.id !== "string" || !session.id) return false;
			const result = await readReviews(session.id) as { autoReviewPending?: boolean; chatReviewerIds?: unknown; runs?: unknown; reviewerActivityState?: string; reviewerHandleId?: string; reviewerSurface?: { reviewId?: string }; activeReviewers?: unknown } | null;
			if (!result || !Array.isArray(result.runs) || !result.runs.every((run: unknown) =>
				run && typeof run === "object" && "status" in run &&
				["completed", "superseded", "complete", "delivered", "failed", "cancelled"].includes(String(run.status)))) return false;
			if (result.autoReviewPending !== false) return false;
			if (!Array.isArray(result.activeReviewers) || !Array.isArray(result.chatReviewerIds)) return false;
			if (result.reviewerHandleId && result.activeReviewers.length === 0 && result.reviewerActivityState !== "exited") return false;
			const chatIDs = new Set<unknown>(result.chatReviewerIds);
			for (const reviewer of result.activeReviewers as { reviewId?: unknown; mode?: unknown }[]) {
				if (!reviewer || typeof reviewer.reviewId !== "string" || !reviewer.reviewId) return false;
				if (reviewer.mode === "chat") chatIDs.add(reviewer.reviewId);
				else if (reviewer.mode !== "tui" || reviewer.reviewId !== result.reviewerSurface?.reviewId || !["idle", "exited"].includes(result.reviewerActivityState ?? "")) return false;
			}
			for (const reviewId of chatIDs) {
				if (typeof reviewId !== "string" || !reviewId) return false;
				const live = await readReviewerConversation(reviewId) as { mode?: string; controller?: string; turns?: unknown } | null;
				if (!live || live.mode !== "chat" || !["ready", "stopped", "hibernated"].includes(live.controller ?? "") ||
					!Array.isArray(live.turns) || !live.turns.every((turn: unknown) => turn && typeof turn === "object" && "state" in turn &&
						["completed", "recovered", "interrupted", "failed", "cancelled"].includes(String(turn.state)))) return false;
			}
		}
		return true;
	} catch { return false; }
}

/** One launch owns one immutable baseline; no restart/dismissal state is persisted. */
export class InstalledBuildGuard {
	private readonly running: string | null;
	private candidate: string | null = null;
	private rendererIsMismatched = false;
	private checking: Promise<void> | undefined;
	private restarting = false;
	private restartRevision = 0;
	status: InstalledBuildStatus | undefined;
	get restartRequested(): boolean { return this.restarting; }

	cancelRestart(): void {
		this.restartRevision += 1;
		this.restarting = false;
		if (this.status) this.publish({ ...this.status, restartWhenIdle: false });
	}

	constructor(private readonly deps: {
		readIdentity(): string | null;
		isIdle(): Promise<boolean | { openSideChats: string[]; sideChatsToClose?: InstalledBuildStatus["sideChatsToClose"] }>;
		onChange(status: InstalledBuildStatus | undefined): void;
		onReplacement(replaced: boolean): void;
		assertRestartSafe(): void;
		relaunch(): void;
	}) {
		this.running = deps.readIdentity();
	}

	private publish(status: InstalledBuildStatus | undefined): void {
		if (JSON.stringify(this.status) === JSON.stringify(status)) return;
		this.status = status;
		this.deps.onChange(status);
	}

	private async idleState(): Promise<Pick<InstalledBuildStatus, "idle" | "openSideChats" | "sideChatsToClose">> {
		const result = await this.deps.isIdle().catch(() => false);
		return { idle: result === true, openSideChats: typeof result === "object" ? result.openSideChats : undefined, sideChatsToClose: typeof result === "object" ? result.sideChatsToClose : undefined };
	}

	private assertRestartSafe(): void {
		try { this.deps.assertRestartSafe(); }
		catch (error) { this.cancelRestart(); throw error; }
	}

	isRunningBuildInstalled(): boolean {
		const current = this.running !== null && this.deps.readIdentity() === this.running;
		this.deps.onReplacement(!current);
		return current;
	}

	async rendererMismatch(): Promise<void> {
		this.rendererIsMismatched = true;
		await this.check();
	}

	check(): Promise<void> {
		return this.checking ??= this.checkInstalledBuild().finally(() => { this.checking = undefined; });
	}

	private async checkInstalledBuild(): Promise<void> {
		const running = this.running;
		const installed = this.deps.readIdentity();
		// Suspend installers before target settling or any asynchronous idle read.
		this.deps.onReplacement(!running || !installed || running !== installed || this.rendererIsMismatched);
		if (!running || !installed) {
			this.candidate = null;
			if (this.status) this.publish({ ...this.status, idle: false });
			return;
		}
		if (running === installed && !this.rendererIsMismatched) {
			this.candidate = null;
			this.publish(undefined);
			return;
		}
		if (this.candidate !== installed && !this.rendererIsMismatched) {
			this.candidate = installed;
			if (this.status) this.publish({ ...this.status, idle: false, restartWhenIdle: false });
			return;
		}
		const readiness = await this.idleState();
		const { idle } = readiness;
		this.publish({ identity: installed, ...readiness, restartWhenIdle: this.status?.identity === installed && this.status.restartWhenIdle });
		if (idle && this.status?.restartWhenIdle) await this.requestRestart("now");
	}

	async requestRestart(mode: InstalledBuildRestart): Promise<void> {
		const revision = ++this.restartRevision;
		if (mode === "cancel") {
			this.cancelRestart();
			return;
		}
		const target = this.status;
		if (!target || this.deps.readIdentity() !== target.identity) {
			throw new Error("The installed build is still changing. Wait for installation to finish and try again.");
		}
		const readiness = await this.idleState();
		const { idle, openSideChats } = readiness;
		if (revision !== this.restartRevision || this.status?.identity !== target.identity) return;
		this.assertRestartSafe();
		if (mode === "when-idle") {
			this.publish({ ...target, ...readiness, restartWhenIdle: true });
		} else if (!idle) {
			this.publish({ ...target, ...readiness });
			if (openSideChats?.length) throw new Error(`Read or save and close these side chats before restarting: ${openSideChats.join(", ")}.`);
			throw new Error("Current tasks are not confirmed idle. Choose Restart when idle or try again after they finish.");
		}
		if (!idle || this.restarting) return;
		// Recheck the target after the asynchronous worker probe as well.
		if (this.deps.readIdentity() !== target.identity || revision !== this.restartRevision) return;
		this.assertRestartSafe();
		this.restarting = true;
		try { this.deps.relaunch(); } catch (error) { this.restarting = false; throw error; }
	}
}

/** Watches are hints; polling also reattaches them after directory replacement. */
export function watchInstalledBuild(guard: Pick<InstalledBuildGuard, "check">, disk: typeof fs, directories: string[]): { check(): void; dispose(): void } {
	let watchers: fs.FSWatcher[] = [];
	let settled: ReturnType<typeof setTimeout> | undefined;
	let disposed = false;
	const run = () => guard.check().catch((error) => console.warn("Installed build check failed:", error));
	const check = () => {
		if (disposed) return;
		void run();
		clearTimeout(settled);
		settled = setTimeout(() => { if (!disposed) void run(); }, 2_000);
		settled.unref();
	};
	const attach = () => {
		for (const watcher of watchers) watcher.close();
		watchers = [];
		for (const directory of new Set(directories)) {
			try { watchers.push(disk.watch(directory, check).on("error", () => undefined)); } catch { /* Polling remains available. */ }
		}
	};
	attach();
	check();
	const timer = setInterval(() => { attach(); check(); }, 30_000);
	timer.unref();
	return {
		check,
		dispose: () => {
			disposed = true;
			clearInterval(timer);
			clearTimeout(settled);
			for (const watcher of watchers) watcher.close();
		},
	};
}
