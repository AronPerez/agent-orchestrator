// Packaging-time cache for the macOS Swift helpers (update, wake, dictation).
// prePackage recompiled all three with `swiftc -O` on every package run (about
// 5 s warm) although they rarely change. Each helper's output folder is now
// stored in the machine-wide build cache (dev-cache.mjs; AO_BUILD_CACHE_DIR or
// ~/.ao/build-cache/outputs), keyed by everything the build reads, and cloned
// back into place on a hit. A miss runs the existing build script unchanged.
import { spawnSync } from "node:child_process";
import { chmodSync, existsSync, lstatSync, readdirSync, rmSync, statSync } from "node:fs";
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
	quarantineEntry,
	storeEntry,
} from "./dev-cache.mjs";

const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

// What each helper build reads (its script and sources, relative to frontend/),
// the files its output folder must contain, and an optional check that the
// output is sound (the dictation bundle must still carry a valid seal).
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
		sentinels: [
			"Orchestrator.app/Contents/MacOS/Orchestrator",
			"Orchestrator.app/Contents/Info.plist",
			"Orchestrator.app/Contents/Resources/icon.icns",
			"Orchestrator.app/Contents/_CodeSignature/CodeResources",
		],
		verify: (outDir, run) => run("codesign", ["--verify", path.join(outDir, "Orchestrator.app")], { encoding: "utf8" }).status === 0,
	},
};

function helperIsSound(helper, outDir, run) {
	return Boolean(outputFingerprint(outDir, helper.sentinels)) && (!helper.verify || helper.verify(outDir, run));
}

// A clone keeps the cache's directory modes. From a read-only cache that would
// leave read-only folders in the checkout, and the next package run could not
// remove them. Files keep their modes; only folders need to be writable.
function makeFoldersWritable(dir) {
	const stat = lstatSync(dir);
	if (!stat.isDirectory()) return;
	chmodSync(dir, stat.mode | 0o700);
	for (const entry of readdirSync(dir)) makeFoldersWritable(path.join(dir, entry));
}

// Remove a helper output folder, including one an earlier restore left
// read-only.
function removeTree(dir) {
	if (!existsSync(dir)) return;
	makeFoldersWritable(dir);
	rmSync(dir, { recursive: true, force: true });
}

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
		removeTree(outDir);
		try {
			cloneTree(cached, outDir);
			makeFoldersWritable(outDir);
			if (helperIsSound(helper, outDir, run)) {
				log(`${name}: reused the cached ${arch} build (${key.slice(0, 12)}).`);
				return "restored";
			}
			// A damaged entry would be reused forever: set it aside so this build
			// can replace it. Optional, like the rest of the cache.
			log(`${name}: the cached build is incomplete or its signature is invalid; rebuilding.`);
			try {
				quarantineEntry(cacheRoot, entryName, key, (dir) => !helperIsSound(helper, path.join(dir, "output"), run));
			} catch (error) {
				log(`${name}: warning: could not set the bad cache entry aside (${error.message}).`);
			}
		} catch (error) {
			log(`${name}: could not restore the cached build (${error.message}); building.`);
		}
		removeTree(outDir);
	}
	build();
	if (!helperIsSound(helper, outDir, run)) return "built";
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
