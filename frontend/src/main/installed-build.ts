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
export function createArchiveIdentityReader(file: string, disk: typeof fs): () => Promise<string | null> {
	let cached: { stamp: string; identity: string } | undefined;
	let pending: Promise<string | null> | undefined;
	const stamp = () => {
		const st = disk.statSync(file);
		return st.isFile() && st.size > 0 ? `${st.dev}:${st.ino}:${st.size}:${st.mtimeMs}:${st.ctimeMs}` : null;
	};
	const read = async () => {
		try {
			const before = stamp();
			if (!before) return null;
			if (cached?.stamp === before) return cached.identity;
			if (file.endsWith(".asar") && !asarIsComplete(file, disk)) return null;
			const hash = createHash("sha256");
			for await (const chunk of disk.createReadStream(file)) hash.update(chunk);
			if (stamp() !== before) return null;
			cached = { stamp: before, identity: hash.digest("hex") };
			return cached.identity;
		} catch {
			return null; // Missing/in-progress installations are not a new identity.
		}
	};
	return () => pending ??= read().finally(() => { pending = undefined; });
}

/** Unknown, provisioning and approval states must never count as idle. */
export function sessionsAreIdle(body: unknown): boolean {
	if (!body || typeof body !== "object" || !("sessions" in body) || !Array.isArray(body.sessions)) return false;
	return body.sessions.every((session: unknown) => {
		if (!session || typeof session !== "object") return false;
		const s = session as { isTerminated?: boolean; statusReadiness?: string; provisionState?: string; activity?: { state?: string }; displayStatus?: string };
		if (s.isTerminated === true) return true;
		return s.statusReadiness === "ready" && s.provisionState !== "provisioning" &&
			(s.activity?.state === "idle" || s.activity?.state === "exited") &&
			!["Reviewing", "Review scheduled", "Retrying review"].includes(s.displayStatus ?? "");
	});
}

/** One launch owns one immutable baseline; no restart/dismissal state is persisted. */
export class InstalledBuildGuard {
	private readonly running: Promise<string | null>;
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
		readIdentity(): Promise<string | null>;
		isIdle(): Promise<boolean>;
		onChange(status: InstalledBuildStatus | undefined): void;
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

	private assertRestartSafe(): void {
		try { this.deps.assertRestartSafe(); }
		catch (error) { this.cancelRestart(); throw error; }
	}

	async canLoadRenderer(): Promise<boolean> {
		const running = await this.running;
		return running !== null && await this.deps.readIdentity() === running;
	}

	async rendererMismatch(): Promise<void> {
		this.rendererIsMismatched = true;
		await this.check();
	}

	check(): Promise<void> {
		return this.checking ??= this.checkInstalledBuild().finally(() => { this.checking = undefined; });
	}

	private async checkInstalledBuild(): Promise<void> {
		const running = await this.running;
		const installed = await this.deps.readIdentity();
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
		const idle = await this.deps.isIdle().catch(() => false);
		this.publish({ identity: installed, idle, restartWhenIdle: this.status?.identity === installed && this.status.restartWhenIdle });
		if (idle && this.status?.restartWhenIdle) await this.requestRestart("now");
	}

	async requestRestart(mode: InstalledBuildRestart): Promise<void> {
		const revision = ++this.restartRevision;
		if (mode === "cancel") {
			this.cancelRestart();
			return;
		}
		const target = this.status;
		if (!target || await this.deps.readIdentity() !== target.identity) {
			throw new Error("The installed build is still changing. Wait for installation to finish and try again.");
		}
		const idle = await this.deps.isIdle().catch(() => false);
		if (revision !== this.restartRevision || this.status?.identity !== target.identity) return;
		this.assertRestartSafe();
		if (mode === "when-idle") {
			this.publish({ ...target, idle, restartWhenIdle: true });
		} else if (!idle) {
			this.publish({ ...target, idle });
			throw new Error("Current tasks are not confirmed idle. Choose Restart when idle or try again after they finish.");
		}
		if (!idle || this.restarting) return;
		// Recheck the target after the asynchronous worker probe as well.
		if (await this.deps.readIdentity() !== target.identity || revision !== this.restartRevision) return;
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
