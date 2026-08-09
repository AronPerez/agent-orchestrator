import { readFile, stat, writeFile } from "node:fs/promises";

// The CLI's saved-remote store, shared verbatim so the UI and `ao --url` agree
// on which hosts exist and never hold two copies of a connection password.
// Format and the 0600 requirement come from backend/internal/cli/remote.go:32-47.
export type RemoteEntry = {
	label: string;
	url: string;
	password: string;
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
	if (mode & 0o077) throw new RemotesFilePermissionError(path, mode);

	const parsed = JSON.parse(await readFile(path, "utf8")) as { remotes?: RemoteEntry[] };
	return parsed.remotes ?? [];
}

export async function addRemote(path: string, entry: RemoteEntry): Promise<void> {
	const existing = await readRemotes(path);
	const remotes = [...existing.filter((candidate) => candidate.url !== entry.url), entry];
	// mode on writeFile only applies at creation; chmod-on-write would race, and
	// readRemotes refuses anything looser on the next read regardless.
	await writeFile(path, `${JSON.stringify({ remotes }, null, 2)}\n`, { encoding: "utf8", mode: 0o600 });
}
