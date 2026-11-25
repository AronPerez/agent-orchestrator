// Packaging-time cache for the macOS Swift helpers (update, wake, dictation).
// prePackage recompiled all three with `swiftc -O` on every package run (about
// 5 s warm) although they rarely change. Each helper's output folder is now
// stored in the machine-wide build cache (dev-cache.mjs; AO_BUILD_CACHE_DIR or
// ~/.ao/build-cache/outputs), keyed by everything the build reads, and cloned
// back into place on a hit. A miss runs the existing build script unchanged.
import { spawnSync } from "node:child_process";
import { readdirSync, rmSync, statSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
	buildCacheRoot,
	cacheKey,
	cloneTree,
	hashFiles,
	lookupEntry,
	outputFingerprint,
	pruneEntries,
	storeEntry,
} from "./dev-cache.mjs";

const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

// What each helper build reads (its script and sources, relative to frontend/)
// and the files its output folder must contain.
export const SWIFT_HELPERS = {
	"update-helper": {
		outDir: "update-helper",
		inputs: ["scripts/build-update-helper.mjs", "native/update-helper"],
		sentinels: ["ao-update-progress"],
	},
	"wake-helper": {
		outDir: "wake-helper",
		inputs: ["scripts/build-wake-helper.mjs", "native/wake-helper"],
		sentinels: ["ao-wake", "THIRD_PARTY_NOTICES.md"],
	},
	"dictation-helper": {
		outDir: "dictation-helper",
		inputs: ["scripts/build-dictation-helper.mjs", "native/dictation-helper", "assets/icon.icns"],
		sentinels: ["Orchestrator.app/Contents/MacOS/Orchestrator", "Orchestrator.app/Contents/Info.plist"],
	},
};

// Every file under each input (a file or a directory), relative to `root`.
function inputFiles(root, inputs) {
	const files = [];
	const walk = (rel) => {
		const full = path.join(root, rel);
		if (!statSync(full).isDirectory()) return void files.push(rel);
		for (const entry of readdirSync(full)) walk(path.join(rel, entry));
	};
	for (const rel of inputs) walk(rel);
	return files;
}

export function swiftToolchain(run = spawnSync) {
	const result = run("xcrun", ["swiftc", "--version"], { encoding: "utf8" });
	return result.status === 0 ? String(result.stdout).trim() : null;
}

// The helper, target arch, Swift compiler, SDK selection and every input file.
export function helperCacheKey({ name, arch, swift, env = process.env, root = frontend }) {
	return cacheKey({
		helper: name,
		arch,
		swift,
		sdk: { SDKROOT: env.SDKROOT ?? null, DEVELOPER_DIR: env.DEVELOPER_DIR ?? null },
		files: hashFiles(root, inputFiles(root, SWIFT_HELPERS[name].inputs)),
	});
}

export function buildSwiftHelper({
	name,
	arch,
	build,
	root = frontend,
	cacheRoot = buildCacheRoot(),
	env = process.env,
	run = spawnSync,
	log = (line) => console.log(line),
}) {
	const helper = SWIFT_HELPERS[name];
	const outDir = path.join(root, helper.outDir);
	const swift = swiftToolchain(run);
	if (!swift) {
		build();
		return "built";
	}
	const key = helperCacheKey({ name, arch, swift, env, root });
	const entryName = `package-${name}`;
	const cached = lookupEntry(cacheRoot, entryName, key);
	if (cached) {
		rmSync(outDir, { recursive: true, force: true });
		try {
			cloneTree(cached, outDir);
			if (outputFingerprint(outDir, helper.sentinels)) {
				log(`${name}: reused the cached ${arch} build (${key.slice(0, 12)}).`);
				return "restored";
			}
		} catch (error) {
			log(`${name}: could not restore the cached build (${error.message}); building.`);
		}
		rmSync(outDir, { recursive: true, force: true });
	}
	build();
	if (!outputFingerprint(outDir, helper.sentinels)) return "built";
	// A source edited while swiftc ran would be stored under the old key and
	// handed to a checkout that still has the old sources: recheck first.
	if (helperCacheKey({ name, arch, swift, env, root }) !== key) {
		log(`${name}: inputs changed during the build; not caching it.`);
		return "built";
	}
	// The cache is optional: a failure to store or prune (say a read-only
	// AO_BUILD_CACHE_DIR) is a warning, never a failed package.
	try {
		storeEntry(cacheRoot, entryName, key, outDir, { arch });
		pruneEntries(cacheRoot, entryName);
	} catch (error) {
		log(`${name}: warning: could not store the build in the cache (${error.message}); continuing without it.`);
	}
	return "built";
}
