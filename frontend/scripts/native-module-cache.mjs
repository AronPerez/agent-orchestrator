// A machine-wide cache of better-sqlite3 built for Electron. No prebuilt
// binary exists for current Electron releases, so every package run compiled
// it from source (about 14 s warm, 40 s or more in a new checkout).
//
// Entries live in the shared build cache (scripts/dev-cache.mjs: written once
// through a temporary directory and an atomic rename, the 3 most recent kept).
// A cached binary is used only when its key matches and it actually loads
// under the target Electron; anything else falls back to a full rebuild.
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, relative } from "node:path";
import { buildCacheRoot, entryDir, lookupEntry, pruneEntries, storeEntry } from "./dev-cache.mjs";

export const CACHE_NAME = "better-sqlite3";

// The compiled addon, and @electron/rebuild's "<arch>--<ABI>" marker. Forge's
// packaging-time rebuild reads the marker to skip recompiling the copied module.
export const CACHED_FILES = ["better_sqlite3.node", ".forge-meta"];

// Environment node-gyp inherits that can change the binary. A customised one
// (say CXXFLAGS=-march=native) can produce a binary that is wrong for other
// machines, so it bypasses the cache and rebuilds as before.
export const BUILD_ENVIRONMENT = [
	"CC",
	"CXX",
	"CFLAGS",
	"CXXFLAGS",
	"CPPFLAGS",
	"LDFLAGS",
	"LIBS",
	"MACOSX_DEPLOYMENT_TARGET",
	"SDKROOT",
	"DEVELOPER_DIR",
	"CPATH",
	"LIBRARY_PATH",
	"GYP_DEFINES",
];

export function customBuildEnvironment(env = process.env) {
	return BUILD_ENVIRONMENT.filter((name) => env[name]);
}

// Everything node-gyp compiles: the manifest, binding.gyp, src/ and the
// vendored SQLite in deps/.
export function moduleSourceDigest(moduleDir) {
	const hash = createHash("sha256");
	const add = (file) => {
		hash.update(relative(moduleDir, file)).update("\0").update(readFileSync(file)).update("\0");
	};
	const walk = (dir) => {
		for (const entry of readdirSync(dir, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
			const file = join(dir, entry.name);
			if (entry.isDirectory()) walk(file);
			else if (entry.isFile()) add(file);
		}
	};
	for (const name of ["package.json", "binding.gyp"]) add(join(moduleDir, name));
	for (const name of ["src", "deps"]) if (existsSync(join(moduleDir, name))) walk(join(moduleDir, name));
	return hash.digest("hex");
}

export function nativeModuleCacheKey({ platform, arch, electronVersion, abi, moduleVersion, sourceDigest }) {
	return `${platform}-${arch}-electron${electronVersion}-abi${abi}-better-sqlite3@${moduleVersion}-${sourceDigest.slice(0, 16)}`;
}

// The dev Electron binary in node_modules. Electron 44 installs it lazily on
// first require; run its installer with a hard timeout instead, since the
// installer's download has none. It validates with the bundled checksums and
// reuses the same zip cache the packager fills, so it adds no download when
// packaging would have downloaded anyway. Returns null when unavailable.
export function devElectronBinary(electronDir, run = spawnSync, timeoutMs = 180_000) {
	const resolveBinary = () => {
		const pathFile = join(electronDir, "path.txt");
		if (!existsSync(pathFile)) return null;
		const binary = join(electronDir, "dist", readFileSync(pathFile, "utf8").trim());
		return existsSync(binary) ? binary : null;
	};
	const installed = resolveBinary();
	if (installed) return installed;
	const install = run(process.execPath, [join(electronDir, "install.js")], { stdio: "inherit", timeout: timeoutMs });
	return install.status === 0 ? resolveBinary() : null;
}

function runUnderElectron(electronBinary, args, run) {
	return run(electronBinary, args, {
		encoding: "utf8",
		timeout: 30_000,
		env: { ...process.env, ELECTRON_RUN_AS_NODE: "1" },
	});
}

export function electronAbi(electronBinary, run = spawnSync) {
	const result = runUnderElectron(electronBinary, ["-p", "process.versions.modules"], run);
	const abi = result.status === 0 ? String(result.stdout).trim() : "";
	return /^\d+$/.test(abi) ? abi : null;
}

// Load the module under the target Electron and run a query: the binary must
// match Electron's ABI and link, not merely exist.
export function loadsUnderElectron(electronBinary, moduleDir, run = spawnSync) {
	const probe =
		"const Database = require(process.argv[1]); const db = new Database(':memory:');" +
		"if (db.prepare('select 1 as ok').get().ok !== 1) process.exit(2);";
	return runUnderElectron(electronBinary, ["-e", probe, moduleDir], run).status === 0;
}

function hasBuild(dir) {
	return CACHED_FILES.every((name) => existsSync(join(dir, name)));
}

function copyBuild(fromDir, toDir) {
	mkdirSync(toDir, { recursive: true });
	for (const name of CACHED_FILES) copyFileSync(join(fromDir, name), join(toDir, name));
}

function storeBuild(cacheRoot, key, releaseDir, meta) {
	// Store only the two runtime files, not node-gyp's intermediate objects.
	const staging = mkdtempSync(join(tmpdir(), "better-sqlite3-"));
	try {
		copyBuild(releaseDir, staging);
		storeEntry(cacheRoot, CACHE_NAME, key, staging, meta);
		pruneEntries(cacheRoot, CACHE_NAME);
	} finally {
		rmSync(staging, { recursive: true, force: true });
	}
}

export async function prepareBetterSqlite3({
	moduleDir,
	electronDir,
	cacheRoot = buildCacheRoot(),
	platform,
	arch,
	electronVersion,
	rebuild,
	env = process.env,
	run = spawnSync,
	log = (line) => console.log(line),
}) {
	const rebuildWithoutCache = async (reason) => {
		if (reason) log(`better-sqlite3: ${reason}; rebuilding without the cache.`);
		await rebuild();
		return "rebuilt";
	};
	// A cross-arch or cross-platform target cannot be loaded here to verify it.
	if (platform !== process.platform || arch !== process.arch) return rebuildWithoutCache();
	const custom = customBuildEnvironment(env);
	if (custom.length > 0) return rebuildWithoutCache(`custom build environment (${custom.join(", ")})`);
	const electronBinary = devElectronBinary(electronDir, run);
	const abi = electronBinary ? electronAbi(electronBinary, run) : null;
	if (!electronBinary || !abi) return rebuildWithoutCache("target Electron unavailable to verify a cached build");
	const moduleVersion = JSON.parse(readFileSync(join(moduleDir, "package.json"), "utf8")).version;
	const key = nativeModuleCacheKey({
		platform,
		arch,
		electronVersion,
		abi,
		moduleVersion,
		sourceDigest: moduleSourceDigest(moduleDir),
	});
	const releaseDir = join(moduleDir, "build", "Release");

	const cached = lookupEntry(cacheRoot, CACHE_NAME, key);
	if (cached && hasBuild(cached)) {
		copyBuild(cached, releaseDir);
		if (loadsUnderElectron(electronBinary, moduleDir, run)) {
			log(`better-sqlite3: reused the cached build for Electron ${electronVersion} (${key}).`);
			return "cached";
		}
		log(`better-sqlite3: the cached build did not load under Electron ${electronVersion}; discarding it.`);
		rmSync(entryDir(cacheRoot, CACHE_NAME, key), { recursive: true, force: true });
	}

	await rebuild();
	if (!hasBuild(releaseDir) || !loadsUnderElectron(electronBinary, moduleDir, run)) {
		log("better-sqlite3: the fresh build did not load under the target Electron; not caching it.");
		return "rebuilt";
	}
	storeBuild(cacheRoot, key, releaseDir, { electronVersion, abi, moduleVersion });
	return "rebuilt";
}
