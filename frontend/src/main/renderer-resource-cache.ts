import { copyFile, mkdir, readdir, rm } from "node:fs/promises";
import { rmSync } from "node:fs";
import path from "node:path";

/** Capture before showing a window; later lazy loads must never read the replacement. */
export async function pinRendererResources(source: string, cache: string, isCurrent: () => Promise<boolean>): Promise<{ root: string; dispose(): void }> {
	// This private cache belongs to the process holding Electron's instance lock.
	await rm(cache, { recursive: true, force: true });
	try {
		if (!await isCurrent()) throw new Error("The desktop build changed before renderer capture.");
		// Electron's bundled cp rejects ASAR directories; copyFile supports their entries.
		const copy = async (from: string, to: string): Promise<void> => {
			await mkdir(to, { recursive: true });
			for (const entry of await readdir(from, { withFileTypes: true })) {
				const src = path.join(from, entry.name), dst = path.join(to, entry.name);
				if (entry.isDirectory()) await copy(src, dst);
				else if (entry.isFile()) await copyFile(src, dst);
				else throw new Error("Unsupported link in packaged renderer resources.");
			}
		};
		await copy(source, cache);
		if (!await isCurrent()) throw new Error("The desktop build changed during renderer capture.");
		return { root: cache, dispose: () => rmSync(cache, { recursive: true, force: true }) };
	} catch (error) {
		await rm(cache, { recursive: true, force: true });
		throw error;
	}
}
