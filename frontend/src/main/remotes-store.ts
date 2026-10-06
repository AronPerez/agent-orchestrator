import { randomUUID } from "node:crypto";
import { mkdir, readFile, rename, rm, stat, writeFile } from "node:fs/promises";
import { dirname } from "node:path";

// The CLI's saved-remote store, shared verbatim so the UI and `ao --url` agree
// on which hosts exist and never hold two copies of a connection password.
// Format and the 0600 requirement come from backend/internal/cli/remote.go:32-47.
export type RemoteEntry = {
	label: string;
	url: string;
	password: string;
	/**
	 * The daemon's own id from GET /api/v1/identity, learned when the host is
	 * added. Sent back as X-AO-Expected-Host-ID so a saved address that now
	 * answers as a different machine is refused (421) instead of obeyed. Absent
	 * on entries saved before this field existed and for a daemon too old to
	 * report one; those stay reachable, just unbound.
	 */
	hostId?: string;
	/**
	 * Optional `user@host` the desktop app uses to SSH-attach a local editor to
	 * this host's workspaces. Uses the user's own ssh config/keys — no credential
	 * is stored here. The Go CLI tolerates and ignores unknown fields in this
	 * file (verified in the AO-82 spike; cli/remote.go has no writer to drop it).
	 */
	sshDestination?: string;
};

export class RemotesFilePermissionError extends Error {
	constructor(
		readonly path: string,
		readonly mode: number,
	) {
		super(
			`${path} holds connection passwords and is readable by others (mode ${mode.toString(8).padStart(4, "0")}) — run: chmod 600 ${path}`,
		);
	}
}

function isMissing(error: unknown): boolean {
	return (error as NodeJS.ErrnoException | undefined)?.code === "ENOENT";
}

export async function readRemotes(path: string): Promise<RemoteEntry[]> {
	let mode: number;
	try {
		mode = (await stat(path)).mode & 0o777;
	} catch (error) {
		if (isMissing(error)) return [];
		throw error;
	}
	// Mirrors the CLI: a world-readable credential file is refused, not tolerated.
	// Windows is exempt for the same reason it is in the CLI (cli/remote.go:154):
	// Node reports 0o666 for every writable file there, so the check would refuse
	// every remotes.json on that platform and take saved hosts down with it.
	if (process.platform !== "win32" && mode & 0o077) throw new RemotesFilePermissionError(path, mode);

	const parsed = JSON.parse(await readFile(path, "utf8")) as { remotes?: RemoteEntry[] };
	return parsed.remotes ?? [];
}

// Every write goes through here. Write beside the existing file and rename over
// it so an interruption never truncates the only saved copy of the passwords;
// the temporary file is 0600 before its contents are written, and readRemotes —
// which each caller runs first — refuses anything looser on the next read.
async function writeRemotes(path: string, remotes: RemoteEntry[]): Promise<void> {
	await mkdir(dirname(path), { recursive: true, mode: 0o700 });
	const temporary = `${path}.${randomUUID()}.tmp`;
	let renamed = false;
	try {
		await writeFile(temporary, `${JSON.stringify({ remotes }, null, 2)}\n`, {
			encoding: "utf8",
			mode: 0o600,
			flag: "wx",
		});
		await rename(temporary, path);
		renamed = true;
	} finally {
		if (!renamed) await rm(temporary, { force: true });
	}
}

// A concurrent add/update/remove must not overwrite another call's read-modify-write.
let mutationQueue: Promise<void> = Promise.resolve();

function serializeMutation<T>(operation: () => Promise<T>): Promise<T> {
	const queued = mutationQueue.then(operation, operation);
	mutationQueue = queued.then(
		() => undefined,
		() => undefined,
	);
	return queued;
}

export async function addRemote(path: string, entry: RemoteEntry): Promise<void> {
	return serializeMutation(async () => {
		const existing = await readRemotes(path);
		const normalized = { ...entry, sshDestination: entry.sshDestination?.trim() || undefined };
		// One row per machine: the same url, or the same daemon identity reached
		// at a new address, replaces the old row rather than sitting beside it.
		await writeRemotes(path, [
			...existing.filter(
				(candidate) => candidate.url !== entry.url && (!entry.hostId || candidate.hostId !== entry.hostId),
			),
			normalized,
		]);
	});
}

/** An edit: only the fields it carries change. */
export type RemoteChanges = Partial<RemoteEntry>;

/**
 * Absent fields keep their saved value. Written with an explicit `??` per field
 * rather than a spread because Electron's structured clone preserves a key whose
 * value is undefined — `{ password: undefined }` must not wipe a working
 * password, which is exactly what "leave blank to keep it" sends.
 */
export function applyRemoteChanges(entry: RemoteEntry, changes: RemoteChanges): RemoteEntry {
	return {
		label: changes.label ?? entry.label,
		url: changes.url ?? entry.url,
		password: changes.password ?? entry.password,
		hostId: changes.hostId ?? entry.hostId,
		sshDestination: (changes.sshDestination ?? entry.sshDestination)?.trim() || undefined,
	};
}

export async function updateRemote(path: string, url: string, changes: RemoteChanges): Promise<RemoteEntry> {
	return serializeMutation(async () => {
		const existing = await readRemotes(path);
		const current = existing.find((candidate) => candidate.url === url);
		if (!current) throw new Error(`no saved host for ${url}`);
		const updated = applyRemoteChanges(current, changes);
		// Re-pointing a host MOVES its entry: the row keeps its place, and any other
		// row already sitting on the new url (or carrying the same daemon identity)
		// is absorbed rather than left as a twin.
		const remotes = existing
			.map((candidate) => (candidate === current ? updated : candidate))
			.filter(
				(candidate) =>
					candidate === updated ||
					(candidate.url !== updated.url && (!updated.hostId || candidate.hostId !== updated.hostId)),
			);
		await writeRemotes(path, remotes);
		return updated;
	});
}

export async function removeRemote(path: string, url: string): Promise<void> {
	return serializeMutation(async () => {
		const existing = await readRemotes(path);
		const remaining = existing.filter((candidate) => candidate.url !== url);
		// Removing what is not there is not an error, but it is not a write either.
		if (remaining.length === existing.length) return;
		await writeRemotes(path, remaining);
	});
}
