// @vitest-environment node
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import {
	closeSync,
	existsSync,
	mkdirSync,
	mkdtempSync,
	openSync,
	readdirSync,
	readFileSync,
	readSync,
	rmSync,
	writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
	CACHE_NAME,
	buildEnvironment,
	compilerIdentity,
	ensureCachedBuild,
	fetchVerified,
	tmuxCacheKey,
	tmuxCacheLocation,
	writableDownloadsDir,
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
	recipe: "async function build(buildDir, output) { /* … */ }",
};

let dir;
beforeEach(() => {
	dir = mkdtempSync(join(tmpdir(), "tmux-build-cache-"));
});
afterEach(() => {
	rmSync(dir, { recursive: true, force: true });
});

describe("tmux cache key", () => {
	it("keeps the same key for the same inputs", () => {
		expect(tmuxCacheKey(inputs)).toBe(tmuxCacheKey(structuredClone(inputs)));
		expect(tmuxCacheKey(inputs)).toMatch(/^darwin-arm64-r5-3\.5a-2\.1\.12-stable-[0-9a-f]{16}$/);
	});

	it.each([
		["a source checksum", { sources: [{ ...inputs.sources[0], sha256: "changed" }, inputs.sources[1]] }],
		["the compiler", { compiler: { ...inputs.compiler, cc: "Apple clang version 22.0.0" } }],
		["the SDK", { compiler: { ...inputs.compiler, sdk: "28.0" } }],
		["the build environment", { environment: { CFLAGS: "-O3" } }],
		["the build recipe", { recipe: `${inputs.recipe} // new flag` }],
		["the build revision", { buildRevision: 6 }],
		["the architecture", { arch: "x64" }],
	])("never reuses a build when %s changes", (_name, change) => {
		expect(tmuxCacheKey({ ...inputs, ...change })).not.toBe(tmuxCacheKey(inputs));
	});
});

describe("build environment", () => {
	const keyFor = (env) => tmuxCacheKey({ ...inputs, environment: buildEnvironment(env) });

	it("reuses one shared build across checkouts with the default environment", () => {
		const env = { AO_BUILD_CACHE_DIR: "/cache", PATH: "/usr/bin", PKG_CONFIG_PATH: "/opt/homebrew/lib/pkgconfig" };
		const first = tmuxCacheLocation({ env, repoRoot: "/src/checkout-a" });
		const second = tmuxCacheLocation({ env, repoRoot: "/src/checkout-b" });
		expect(first).toEqual({ shared: true, root: "/cache" });
		expect(second).toEqual(first);
		expect(keyFor(env)).toBe(keyFor({}));
	});

	it("never shares or reuses a build made with a custom flag", () => {
		const env = { AO_BUILD_CACHE_DIR: "/cache", CFLAGS: "-O3 -march=native" };
		expect(tmuxCacheLocation({ env, repoRoot: "/src/checkout-a" })).toEqual({ shared: false, root: "/src/checkout-a/.cache" });
		expect(keyFor(env)).not.toBe(keyFor({}));
	});

	it.each(["CC", "CFLAGS", "CPPFLAGS", "LDFLAGS", "MACOSX_DEPLOYMENT_TARGET", "SDKROOT"])(
		"treats %s as customising the build",
		(name) => {
			expect(buildEnvironment({ [name]: "x" })).toEqual({ [name]: "x" });
			expect(tmuxCacheLocation({ env: { [name]: "x" }, repoRoot: "/src/a" }).shared).toBe(false);
		},
	);

	it("ignores unrelated and empty variables", () => {
		expect(buildEnvironment({ PATH: "/usr/bin", CFLAGS: "", HOME: "/Users/me" })).toEqual({});
	});

	it("identifies the compiler named by $CC", () => {
		const run = vi.fn(() => ({ status: 0, stdout: "gcc (GCC) 14.2.0\n" }));
		expect(compilerIdentity(run, { CC: "gcc-14 -std=c11" }).cc).toBe("gcc (GCC) 14.2.0");
		expect(run).toHaveBeenCalledWith("gcc-14", ["--version"], expect.anything());
	});

	it("falls back when the compiler is unavailable", () => {
		const identity = compilerIdentity(() => ({ status: 1, stdout: "" }), {});
		expect(identity.cc).toBe("cc unavailable");
	});
});

describe("cached builds", () => {
	const fakeBuild = vi.fn(async (_work, output) => {
		writeFileSync(join(output, "tmux"), "binary");
		mkdirSync(join(output, "licenses"));
		writeFileSync(join(output, "licenses", "tmux.txt"), "ISC");
	});

	beforeEach(() => {
		fakeBuild.mockClear();
	});

	it("builds on a miss and reuses the entry afterwards", async () => {
		const root = join(dir, "cache");
		const first = await ensureCachedBuild({ root, key: "k", meta: {}, build: fakeBuild, use: (built) => built, workRoot: dir });
		expect(readFileSync(join(first, "tmux"), "utf8")).toBe("binary");
		expect(readFileSync(join(first, "licenses", "tmux.txt"), "utf8")).toBe("ISC");
		const second = await ensureCachedBuild({ root, key: "k", meta: {}, build: fakeBuild, use: (built) => built, workRoot: dir });
		expect(second).toBe(first);
		expect(fakeBuild).toHaveBeenCalledTimes(1);
		// The private work directory is gone either way.
		expect(readdirSync(dir).filter((name) => name.startsWith(`${CACHE_NAME}-build-`))).toEqual([]);
	});

	it("replaces a cached entry that lost its binary instead of failing every checkout", async () => {
		const root = join(dir, "cache");
		const final = join(root, CACHE_NAME, "k");
		mkdirSync(join(final, "output", "licenses"), { recursive: true });
		writeFileSync(join(final, "meta.json"), "{}");
		const isComplete = (built) => existsSync(join(built, "tmux"));
		const read = await ensureCachedBuild({
			root,
			key: "k",
			meta: {},
			build: fakeBuild,
			isComplete,
			use: (built) => readFileSync(join(built, "tmux"), "utf8"),
			workRoot: dir,
			log: () => undefined,
		});
		expect(read).toBe("binary");
		expect(fakeBuild).toHaveBeenCalledTimes(1);
		expect(readFileSync(join(final, "output", "tmux"), "utf8")).toBe("binary");
		expect(readdirSync(join(root, CACHE_NAME)).filter((name) => name.startsWith(".tmp-stale-k-"))).toHaveLength(1);
	});

	it("builds when a cached entry disappears while it is being copied", async () => {
		const root = join(dir, "cache");
		await ensureCachedBuild({ root, key: "k", meta: {}, build: fakeBuild, use: () => undefined, workRoot: dir });
		let copies = 0;
		const read = await ensureCachedBuild({
			root,
			key: "k",
			meta: {},
			build: fakeBuild,
			use: (built) => {
				copies += 1;
				if (copies === 1) throw Object.assign(new Error(`ENOENT: no such file, open '${built}/tmux'`), { code: "ENOENT" });
				return readFileSync(join(built, "tmux"), "utf8");
			},
			workRoot: dir,
			log: () => undefined,
		});
		expect(read).toBe("binary");
		expect(fakeBuild).toHaveBeenCalledTimes(2);
	});

	it("uses but never stores a build whose inputs changed while it ran", async () => {
		const root = join(dir, "cache");
		const lines = [];
		const read = await ensureCachedBuild({
			root,
			key: "k",
			currentKey: () => "k-after-an-xcode-update",
			meta: {},
			build: fakeBuild,
			use: (built) => readFileSync(join(built, "tmux"), "utf8"),
			workRoot: dir,
			log: (line) => lines.push(line),
		});
		expect(read).toBe("binary");
		expect(existsSync(join(root, CACHE_NAME, "k"))).toBe(false);
		expect(lines).toEqual(["bundled tmux: inputs changed during the build; not caching it."]);
	});

	it("keeps a successful build when the cache cannot be written", async () => {
		const root = join(dir, "read-only-cache");
		writeFileSync(root, "not a directory");
		const lines = [];
		const read = await ensureCachedBuild({
			root,
			key: "k",
			meta: {},
			build: fakeBuild,
			use: (built) => readFileSync(join(built, "licenses", "tmux.txt"), "utf8"),
			workRoot: dir,
			log: (line) => lines.push(line),
		});
		expect(read).toBe("ISC");
		expect(lines.some((line) => line.startsWith("bundled tmux: warning: could not store the build in the cache"))).toBe(true);
		expect(readdirSync(dir).filter((name) => name.startsWith(`${CACHE_NAME}-build-`))).toEqual([]);
	});

	it("falls back to this checkout's downloads directory when the shared one cannot be written", () => {
		const blocked = join(dir, "blocked");
		writeFileSync(blocked, "a file, not a directory");
		const fallback = join(dir, "checkout", ".cache", "bundled-tmux-sources");
		expect(writableDownloadsDir(join(blocked, "sources"), fallback)).toBe(fallback);
		expect(existsSync(fallback)).toBe(true);
		expect(writableDownloadsDir(join(dir, "shared"), fallback)).toBe(join(dir, "shared"));
	});

	// Several workers spawn worktrees at once, so builders race for real here:
	// separate processes, released together, each with a slow build. A watcher
	// asserts that the entry, once complete, never disappears or shows partial
	// content, and every builder must end with the complete entry.
	it.each([
		["an empty cache", false],
		["a damaged leftover entry", true],
	])("publishes one complete entry when builders race from %s", async (_name, damaged) => {
		const root = join(dir, "cache");
		const key = "racing-key";
		const final = join(root, CACHE_NAME, key);
		// Large enough that a half-written copy would show, generated in each
		// builder: Linux caps a single argument at 128 KB, so it cannot ride in -e.
		const payload = "x".repeat(256 * 1024);
		if (damaged) {
			mkdirSync(join(final, "output"), { recursive: true });
			writeFileSync(join(final, "output", "tmux"), "partial");
		}
		const barrier = join(dir, "go");
		const moduleUrl = new URL("./tmux-build-cache.mjs", import.meta.url).href;
		const builder = `
			import { existsSync, mkdirSync, writeFileSync } from "node:fs";
			import { join } from "node:path";
			const { ensureCachedBuild } = await import(${JSON.stringify(moduleUrl)});
			while (!existsSync(${JSON.stringify(barrier)})) await new Promise((r) => setTimeout(r, 2));
			const output = await ensureCachedBuild({
				root: ${JSON.stringify(root)},
				key: ${JSON.stringify(key)},
				meta: {},
				use: (built) => built,
				workRoot: ${JSON.stringify(dir)},
				build: async (_work, out) => {
					await new Promise((r) => setTimeout(r, Math.random() * 40));
					writeFileSync(join(out, "tmux"), "x".repeat(${payload.length}));
					mkdirSync(join(out, "licenses"));
					writeFileSync(join(out, "licenses", "tmux.txt"), "ISC");
				},
			});
			process.stdout.write(output);
		`;
		const children = Array.from({ length: 4 }, () =>
			spawn(process.execPath, ["--input-type=module", "-e", builder], { stdio: ["ignore", "pipe", "pipe"] }),
		);
		const results = children.map(
			(child) =>
				new Promise((resolve) => {
					let stdout = "";
					let stderr = "";
					child.stdout.on("data", (chunk) => (stdout += chunk));
					child.stderr.on("data", (chunk) => (stderr += chunk));
					child.on("exit", (code) => resolve({ code, stdout, stderr }));
				}),
		);

		const violations = [];
		let seenComplete = false;
		const watcher = setInterval(() => {
			const complete = existsSync(join(final, "meta.json"));
			if (seenComplete && !complete) violations.push("complete entry disappeared");
			if (complete) {
				seenComplete = true;
				try {
					const binary = readFileSync(join(final, "output", "tmux"), "utf8");
					if (binary !== payload) violations.push(`partial binary (${binary.length} bytes)`);
				} catch (error) {
					violations.push(`unreadable complete entry: ${error.code}`);
				}
			}
		}, 1);
		writeFileSync(barrier, "");
		const finished = await Promise.all(results);
		clearInterval(watcher);

		for (const { code, stderr } of finished) expect({ code, stderr }).toEqual({ code: 0, stderr: "" });
		for (const { stdout } of finished) {
			expect(stdout).toBe(join(final, "output"));
			expect(readFileSync(join(stdout, "tmux"), "utf8")).toBe(payload);
		}
		expect(violations).toEqual([]);
		expect(readdirSync(join(root, CACHE_NAME)).filter((name) => !name.startsWith(".tmp-stale-"))).toEqual([key]);
	}, 30_000);
});

describe("source downloads", () => {
	const archiveBytes = Buffer.from("tmux-3.5a.tar.gz contents");
	const sha256 = createHash("sha256").update(archiveBytes).digest("hex");
	const slowFetch = (delayMs) =>
		vi.fn(async () => {
			await new Promise((r) => setTimeout(r, delayMs));
			return { ok: true, arrayBuffer: async () => archiveBytes };
		});

	it("keeps the archive present and valid while two checkouts download it at once", async () => {
		const archive = join(dir, "tmux.tar.gz");
		const violations = [];
		let seen = false;
		const watcher = setInterval(() => {
			if (!existsSync(archive)) {
				if (seen) violations.push("archive disappeared");
				return;
			}
			seen = true;
			if (!readFileSync(archive).equals(archiveBytes)) violations.push("archive partial or wrong");
		}, 1);
		await Promise.all([
			fetchVerified({ url: "u", sha256, archive, fetchImpl: slowFetch(5) }),
			fetchVerified({ url: "u", sha256, archive, fetchImpl: slowFetch(25) }),
		]);
		clearInterval(watcher);
		expect(violations).toEqual([]);
		expect(readFileSync(archive).equals(archiveBytes)).toBe(true);
	});

	it("replaces a bad archive by rename, so a reader of the old one keeps a whole file", async () => {
		const archive = join(dir, "tmux.tar.gz");
		writeFileSync(archive, "stale bytes");
		const reader = openSync(archive, "r");
		try {
			await fetchVerified({ url: "u", sha256, archive, fetchImpl: slowFetch(0) });
			const buffer = Buffer.alloc(32);
			const read = readSync(reader, buffer, 0, buffer.length, 0);
			expect(buffer.subarray(0, read).toString()).toBe("stale bytes");
		} finally {
			closeSync(reader);
		}
		expect(readFileSync(archive).equals(archiveBytes)).toBe(true);
	});

	it("says what it downloads, and only when it downloads", async () => {
		const archive = join(dir, "tmux.tar.gz");
		const lines = [];
		await fetchVerified({ url: "u", sha256, archive, label: "tmux 3.5a", fetchImpl: slowFetch(0), log: (line) => lines.push(line) });
		await fetchVerified({ url: "u", sha256, archive, label: "tmux 3.5a", fetchImpl: slowFetch(0), log: (line) => lines.push(line) });
		expect(lines).toEqual(["Downloading tmux 3.5a..."]);
	});

	it("does not fetch when a verified archive is already there", async () => {
		const archive = join(dir, "tmux.tar.gz");
		writeFileSync(archive, archiveBytes);
		const fetchImpl = slowFetch(0);
		await fetchVerified({ url: "u", sha256, archive, fetchImpl });
		expect(fetchImpl).not.toHaveBeenCalled();
	});

	it("leaves the published archive alone when a download fails", async () => {
		const archive = join(dir, "tmux.tar.gz");
		writeFileSync(archive, "someone else's copy");
		const failing = vi.fn(async () => ({ ok: false, status: 503 }));
		await expect(
			fetchVerified({ url: "https://example.invalid/t.tgz", sha256, archive, fetchImpl: failing, attempts: 2, retryDelayMs: 1 }),
		).rejects.toThrow(/HTTP 503/);
		expect(readFileSync(archive, "utf8")).toBe("someone else's copy");
		expect(readdirSync(dir).filter((name) => name.includes(".part-"))).toEqual([]);
	});
});

