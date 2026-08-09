import { describe, expect, it } from "vitest";
import { chmod, mkdtemp, readFile, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { addRemote, readRemotes, RemotesFilePermissionError } from "./remotes-store";

async function tempFile(contents?: string, mode = 0o600): Promise<string> {
	const dir = await mkdtemp(join(tmpdir(), "ao-remotes-"));
	const path = join(dir, "remotes.json");
	if (contents !== undefined) {
		await writeFile(path, contents, "utf8");
		await chmod(path, mode);
	}
	return path;
}

describe("readRemotes", () => {
	it("returns an empty list when the file does not exist", async () => {
		const path = await tempFile();
		await expect(readRemotes(path)).resolves.toEqual([]);
	});

	it("reads entries from a 0600 file", async () => {
		const path = await tempFile('{"remotes":[{"label":"workbox","url":"http://192.0.2.1:3011","password":"pw"}]}');
		await expect(readRemotes(path)).resolves.toEqual([
			{ label: "workbox", url: "http://192.0.2.1:3011", password: "pw" },
		]);
	});

	it("refuses a file readable by others, naming the fix", async () => {
		const path = await tempFile('{"remotes":[]}', 0o644);
		await expect(readRemotes(path)).rejects.toBeInstanceOf(RemotesFilePermissionError);
		await expect(readRemotes(path)).rejects.toThrow(/chmod 600/);
	});
});

describe("addRemote", () => {
	it("creates the file 0600 when absent", async () => {
		const path = await tempFile();
		await addRemote(path, { label: "workbox", url: "http://192.0.2.1:3011", password: "pw" });
		expect((await stat(path)).mode & 0o777).toBe(0o600);
		expect(JSON.parse(await readFile(path, "utf8")).remotes).toHaveLength(1);
	});

	it("appends without dropping existing entries", async () => {
		const path = await tempFile('{"remotes":[{"label":"a","url":"http://192.0.2.1:1","password":"x"}]}');
		await addRemote(path, { label: "b", url: "http://192.0.2.2:2", password: "y" });
		const labels = JSON.parse(await readFile(path, "utf8")).remotes.map((r: { label: string }) => r.label);
		expect(labels).toEqual(["a", "b"]);
	});

	it("replaces an entry with the same url rather than duplicating it", async () => {
		const path = await tempFile('{"remotes":[{"label":"old","url":"http://192.0.2.1:1","password":"x"}]}');
		await addRemote(path, { label: "new", url: "http://192.0.2.1:1", password: "z" });
		const remotes = JSON.parse(await readFile(path, "utf8")).remotes;
		expect(remotes).toEqual([{ label: "new", url: "http://192.0.2.1:1", password: "z" }]);
	});
});
