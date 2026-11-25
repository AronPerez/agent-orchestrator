// @vitest-environment node
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
	buildDirFor,
	compilerIdentity,
	pruneAbandonedBuilds,
	publishBuild,
	tmuxBuildCacheRoot,
	tmuxCacheKey,
} from "./tmux-build-cache.mjs";

const inputs = {
	platform: "darwin",
	arch: "arm64",
	buildRevision: 5,
	sources: [
		{ name: "tmux", version: "3.5a", sha256: "aaa" },
		{ name: "libevent", version: "2.1.12-stable", sha256: "bbb" },
	],
	compiler: { cc: "Apple clang version 21.0.0", sdk: "27.2" },
	recipe: "async function build(buildDir) { /* … */ }",
};

let dir;
beforeEach(() => {
	dir = mkdtempSync(join(tmpdir(), "tmux-build-cache-"));
});
afterEach(() => {
	rmSync(dir, { recursive: true, force: true });
});

describe("tmux build cache", () => {
	it("lives under ~/.ao/build-cache, shared by every checkout", () => {
		expect(tmuxBuildCacheRoot("/Users/me")).toBe("/Users/me/.ao/build-cache/bundled-tmux");
	});

	it("keeps the same key for the same inputs", () => {
		expect(tmuxCacheKey(inputs)).toBe(tmuxCacheKey(structuredClone(inputs)));
		expect(tmuxCacheKey(inputs)).toMatch(/^darwin-arm64-r5-3\.5a-2\.1\.12-stable-[0-9a-f]{16}$/);
	});

	it.each([
		["a source checksum", { sources: [{ ...inputs.sources[0], sha256: "changed" }, inputs.sources[1]] }],
		["the compiler", { compiler: { ...inputs.compiler, cc: "Apple clang version 22.0.0" } }],
		["the SDK", { compiler: { ...inputs.compiler, sdk: "28.0" } }],
		["the build recipe", { recipe: `${inputs.recipe} // new flag` }],
		["the build revision", { buildRevision: 6 }],
		["the architecture", { arch: "x64" }],
	])("never reuses a build when %s changes", (_name, change) => {
		expect(tmuxCacheKey({ ...inputs, ...change })).not.toBe(tmuxCacheKey(inputs));
	});

	it("publishes a finished build into place", () => {
		const finalDir = join(dir, "key");
		const buildDir = buildDirFor(finalDir, 101);
		mkdirSync(buildDir);
		writeFileSync(join(buildDir, "tmux"), "mine");

		expect(publishBuild(buildDir, finalDir, () => true)).toBe(true);
		expect(readFileSync(join(finalDir, "tmux"), "utf8")).toBe("mine");
		expect(existsSync(buildDir)).toBe(false);
	});

	it("keeps the other checkout's build when it published first", () => {
		const finalDir = join(dir, "key");
		mkdirSync(finalDir);
		writeFileSync(join(finalDir, "tmux"), "theirs");
		const buildDir = buildDirFor(finalDir, 102);
		mkdirSync(buildDir);
		writeFileSync(join(buildDir, "tmux"), "mine");

		expect(publishBuild(buildDir, finalDir, () => true)).toBe(false);
		expect(readFileSync(join(finalDir, "tmux"), "utf8")).toBe("theirs");
		expect(existsSync(buildDir)).toBe(false);
	});

	it("replaces a damaged build at the key instead of reusing it", () => {
		const finalDir = join(dir, "key");
		mkdirSync(finalDir);
		writeFileSync(join(finalDir, "partial.o"), "");
		const buildDir = buildDirFor(finalDir, 103);
		mkdirSync(buildDir);
		writeFileSync(join(buildDir, "tmux"), "mine");

		expect(publishBuild(buildDir, finalDir, () => false)).toBe(true);
		expect(readFileSync(join(finalDir, "tmux"), "utf8")).toBe("mine");
		expect(existsSync(join(finalDir, "partial.o"))).toBe(false);
		expect(existsSync(buildDir)).toBe(false);
	});

	it("prunes builds abandoned by dead processes and leaves the rest", () => {
		for (const name of ["key.building-111", "key.building-222", "key", "downloads"]) mkdirSync(join(dir, name));

		pruneAbandonedBuilds(dir, (pid) => pid === 222);

		expect(existsSync(join(dir, "key.building-111"))).toBe(false);
		expect(existsSync(join(dir, "key.building-222"))).toBe(true);
		expect(existsSync(join(dir, "key"))).toBe(true);
		expect(existsSync(join(dir, "downloads"))).toBe(true);
	});

	it("tolerates a cache root that does not exist yet", () => {
		expect(() => pruneAbandonedBuilds(join(dir, "missing"))).not.toThrow();
	});

	it("identifies the compiler by its first version line", () => {
		const run = (command) =>
			command === "cc"
				? { status: 0, stdout: "Apple clang version 21.0.0 (clang-2100.0.1)\nTarget: arm64-apple-darwin\n" }
				: { status: 1, stdout: "" };
		const identity = compilerIdentity(run);
		expect(identity.cc).toBe("Apple clang version 21.0.0 (clang-2100.0.1)");
		if (process.platform === "darwin") expect(identity.sdk).toBe("xcrun unavailable");
	});
});
