// @vitest-environment node
import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
	CACHE_NAME,
	customBuildEnvironment,
	devElectronBinary,
	moduleSourceDigest,
	nativeModuleCacheKey,
	prepareBetterSqlite3,
} from "./native-module-cache.mjs";

let root;
let moduleDir;
let electronDir;
let cacheRoot;
let releaseDir;

beforeEach(() => {
	root = mkdtempSync(join(tmpdir(), "native-module-cache-"));
	moduleDir = join(root, "node_modules", "better-sqlite3");
	electronDir = join(root, "node_modules", "electron");
	cacheRoot = join(root, "cache");
	releaseDir = join(moduleDir, "build", "Release");
	mkdirSync(join(moduleDir, "src"), { recursive: true });
	writeFileSync(join(moduleDir, "package.json"), JSON.stringify({ version: "12.11.1" }));
	writeFileSync(join(moduleDir, "binding.gyp"), "{}");
	writeFileSync(join(moduleDir, "src", "better_sqlite3.cpp"), "// source");
	mkdirSync(join(electronDir, "dist"), { recursive: true });
	writeFileSync(join(electronDir, "path.txt"), "Electron");
	writeFileSync(join(electronDir, "dist", "Electron"), "");
});

afterEach(() => {
	rmSync(root, { recursive: true, force: true });
});

// A stand-in for the target Electron: reports an ABI and loads the module
// according to `loads`, which sees the binary currently in build/Release.
function fakeElectron({ abi = "149", loads = () => true } = {}) {
	return vi.fn((_command, args) => {
		if (args[0] === "-p") return { status: 0, stdout: `${abi}\n` };
		if (args[0] === "-e") return { status: loads(readFileSync(join(releaseDir, "better_sqlite3.node"), "utf8")) ? 0 : 1 };
		return { status: 1 };
	});
}

let builds = 0;
const rebuild = vi.fn(async () => {
	builds += 1;
	mkdirSync(releaseDir, { recursive: true });
	writeFileSync(join(releaseDir, "better_sqlite3.node"), `build-${builds}`);
	writeFileSync(join(releaseDir, ".forge-meta"), `${process.arch}--149`);
});

function prepare(overrides = {}) {
	return prepareBetterSqlite3({
		moduleDir,
		electronDir,
		cacheRoot,
		platform: process.platform,
		arch: process.arch,
		electronVersion: "44.11.3",
		rebuild,
		env: {},
		run: fakeElectron(),
		log: () => undefined,
		...overrides,
	});
}

const cacheEntries = () => {
	const dir = join(cacheRoot, CACHE_NAME);
	return existsSync(dir) ? readdirSync(dir).filter((name) => !name.startsWith(".")) : [];
};
const cachedBinary = (entry) => readFileSync(join(cacheRoot, CACHE_NAME, entry, "output", "better_sqlite3.node"), "utf8");

describe("better-sqlite3 build cache", () => {
	beforeEach(() => {
		builds = 0;
		rebuild.mockClear();
	});

	it("builds and stores on a miss, then reuses the verified build", async () => {
		await expect(prepare()).resolves.toBe("rebuilt");
		expect(rebuild).toHaveBeenCalledTimes(1);
		expect(cacheEntries()).toHaveLength(1);

		rmSync(join(moduleDir, "build"), { recursive: true });
		await expect(prepare()).resolves.toBe("cached");
		expect(rebuild).toHaveBeenCalledTimes(1);
		expect(readFileSync(join(releaseDir, "better_sqlite3.node"), "utf8")).toBe("build-1");
		expect(readFileSync(join(releaseDir, ".forge-meta"), "utf8")).toBe(`${process.arch}--149`);
	});

	it.each([
		["another Electron version", { electronVersion: "44.11.2" }],
		["another Electron ABI", { run: fakeElectron({ abi: "150" }) }],
	])("rebuilds instead of reusing a build keyed to %s", async (_name, mismatch) => {
		await prepare();
		await expect(prepare(mismatch)).resolves.toBe("rebuilt");
		expect(rebuild).toHaveBeenCalledTimes(2);
		expect(cacheEntries()).toHaveLength(2);
	});

	it("rebuilds when the module's source changes", async () => {
		await prepare();
		writeFileSync(join(moduleDir, "src", "better_sqlite3.cpp"), "// patched source");
		await expect(prepare()).resolves.toBe("rebuilt");
		expect(rebuild).toHaveBeenCalledTimes(2);
	});

	it("discards a cached build that does not load under the target Electron", async () => {
		await prepare();
		const run = fakeElectron({ loads: (binary) => binary !== "build-1" });
		await expect(prepare({ run })).resolves.toBe("rebuilt");
		expect(rebuild).toHaveBeenCalledTimes(2);
		const [entry] = cacheEntries();
		expect(cachedBinary(entry)).toBe("build-2");
	});

	it("does not cache a fresh build that fails to load", async () => {
		await expect(prepare({ run: fakeElectron({ loads: () => false }) })).resolves.toBe("rebuilt");
		expect(cacheEntries()).toHaveLength(0);
	});

	it("never caches or reuses a build made with a custom compiler flag", async () => {
		await prepare();
		const run = fakeElectron();
		await expect(prepare({ env: { CXXFLAGS: "-O3 -march=native" }, run })).resolves.toBe("rebuilt");
		expect(rebuild).toHaveBeenCalledTimes(2);
		expect(run).not.toHaveBeenCalled();
		expect(cacheEntries()).toHaveLength(1);
		expect(cachedBinary(cacheEntries()[0])).toBe("build-1");
	});

	it("rebuilds a cross-architecture target without touching the cache or Electron", async () => {
		const run = fakeElectron();
		await prepare({ arch: process.arch === "arm64" ? "x64" : "arm64", run });
		expect(rebuild).toHaveBeenCalledTimes(1);
		expect(run).not.toHaveBeenCalled();
		expect(cacheEntries()).toHaveLength(0);
	});

	it("rebuilds without the cache when the target Electron is not available", async () => {
		rmSync(join(electronDir, "path.txt"));
		const run = vi.fn(() => ({ status: 1 }));
		await expect(prepare({ run })).resolves.toBe("rebuilt");
		expect(rebuild).toHaveBeenCalledTimes(1);
		expect(cacheEntries()).toHaveLength(0);
	});

	it("keeps only the most recent builds", async () => {
		for (const electronVersion of ["44.0.0", "44.1.0", "44.2.0", "44.3.0"]) await prepare({ electronVersion });
		expect(cacheEntries()).toHaveLength(3);
	});

	it("installs the dev Electron with a timeout when it is missing", () => {
		rmSync(join(electronDir, "path.txt"));
		const run = vi.fn(() => {
			writeFileSync(join(electronDir, "path.txt"), "Electron");
			return { status: 0 };
		});
		expect(devElectronBinary(electronDir, run, 5_000)).toBe(join(electronDir, "dist", "Electron"));
		expect(run).toHaveBeenCalledWith(process.execPath, [join(electronDir, "install.js")], expect.objectContaining({ timeout: 5_000 }));
	});

	it("keys on every input", () => {
		const inputs = { platform: "darwin", arch: "arm64", electronVersion: "44.11.3", abi: "149", moduleVersion: "12.11.1", sourceDigest: "a".repeat(64) };
		const key = nativeModuleCacheKey(inputs);
		expect(key).toBe(`darwin-arm64-electron44.11.3-abi149-better-sqlite3@12.11.1-${"a".repeat(16)}`);
		for (const change of [{ arch: "x64" }, { electronVersion: "44.11.2" }, { abi: "150" }, { moduleVersion: "12.12.0" }, { sourceDigest: "b".repeat(64) }]) {
			expect(nativeModuleCacheKey({ ...inputs, ...change })).not.toBe(key);
		}
	});

	it("treats only set compiler variables as a custom environment", () => {
		expect(customBuildEnvironment({ PATH: "/usr/bin", CXXFLAGS: "", HOME: "/Users/me" })).toEqual([]);
		expect(customBuildEnvironment({ CC: "clang-19", CXXFLAGS: "-O3" })).toEqual(["CC", "CXXFLAGS"]);
	});

	it("hashes the compiled sources, not the build output", () => {
		const before = moduleSourceDigest(moduleDir);
		mkdirSync(releaseDir, { recursive: true });
		writeFileSync(join(releaseDir, "better_sqlite3.node"), "binary");
		expect(moduleSourceDigest(moduleDir)).toBe(before);
		writeFileSync(join(moduleDir, "binding.gyp"), "{ 'changed': 1 }");
		expect(moduleSourceDigest(moduleDir)).not.toBe(before);
	});
});
