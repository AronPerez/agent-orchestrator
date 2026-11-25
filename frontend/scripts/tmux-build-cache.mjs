// The bundled tmux build cache, shared by every checkout and worktree on the
// machine. A tmux build takes about a minute, and a per-checkout cache made
// every new worktree pay it again. Several workers often create worktrees at
// once, so every step here is safe to run concurrently against one cache.
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { accessSync, constants, existsSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { buildCacheRoot, lookupEntry, pruneEntries, storeEntry } from "./dev-cache.mjs";

export const CACHE_NAME = "bundled-tmux";

// Environment the build inherits that can change the binary: the compiler,
// its flags, the SDK and toolchain selection, and library search paths. The
// build overrides CPPFLAGS, LDFLAGS and (on macOS) MACOSX_DEPLOYMENT_TARGET
// itself; they are listed anyway so a customised shell never shares.
export const BUILD_ENVIRONMENT = [
	"CC",
	"CPP",
	"CFLAGS",
	"CPPFLAGS",
	"LDFLAGS",
	"LIBS",
	"MACOSX_DEPLOYMENT_TARGET",
	"SDKROOT",
	"DEVELOPER_DIR",
	"CPATH",
	"C_INCLUDE_PATH",
	"LIBRARY_PATH",
	"PKG_CONFIG_LIBDIR",
	"AR",
	"RANLIB",
	"LD",
];

// The build-relevant variables that are actually set.
export function buildEnvironment(env = process.env) {
	return Object.fromEntries(BUILD_ENVIRONMENT.filter((name) => env[name]).map((name) => [name, env[name]]));
}

// Share only default builds, in the machine-wide build cache (dev-cache.mjs;
// AO_BUILD_CACHE_DIR overrides it). A customised environment (say
// CFLAGS=-march=native) can produce a binary that is wrong for other machines,
// so it keeps its builds in this checkout's .cache, as before. Neither is
// inside frontend/: Electron Packager treats that as the app root and would
// copy hundreds of megabytes of intermediate objects into app.asar.
export function tmuxCacheLocation({ env = process.env, repoRoot }) {
	const shared = Object.keys(buildEnvironment(env)).length === 0;
	return { shared, root: shared ? buildCacheRoot(env) : join(repoRoot, ".cache") };
}

// Source archives are verified by sha256, so one copy serves every checkout.
export function sourceDownloadsDir(env = process.env) {
	return join(buildCacheRoot(env), `${CACHE_NAME}-sources`);
}

// Everything that decides the bytes of the built binary: the build revision,
// the pinned sources (with their sha256), the compiler, the build-relevant
// environment, and the build recipe itself. Any change gives a new key, so a
// stale build is never reused.
export function tmuxCacheKey({ platform, arch, buildRevision, sources, compiler, environment = {}, recipe }) {
	const digest = createHash("sha256")
		.update(JSON.stringify({ platform, arch, buildRevision, sources, compiler, environment, recipe }))
		.digest("hex")
		.slice(0, 16);
	return `${platform}-${arch}-r${buildRevision}-${sources.map((source) => source.version).join("-")}-${digest}`;
}

// The C toolchain that compiles tmux and its libraries. An Xcode or compiler
// upgrade must not reuse objects built by the previous one.
export function compilerIdentity(run = spawnSync, env = process.env) {
	const firstLine = (command, args) => {
		const result = run(command, args, { encoding: "utf8" });
		return result.status === 0 ? String(result.stdout).split("\n")[0].trim() : `${command} unavailable`;
	};
	// configure uses $CC when set; its first word is the compiler.
	const cc = env.CC?.trim().split(/\s+/)[0] || "cc";
	const identity = { cc: firstLine(cc, ["--version"]) };
	if (process.platform === "darwin") identity.sdk = firstLine("xcrun", ["--show-sdk-version"]);
	return identity;
}

// Hands the build for `key` to `use(outputDir)` and returns what `use`
// returns, building first on a miss. Each builder works in its own temporary
// directory and publishes through dev-cache's storeEntry (write once, atomic
// rename, never delete a published entry), so concurrent builders of one key
// all end with the same complete entry.
//
// The cache is optional and must never hold a build under a key it does not
// match. If `currentKey()` differs after the build (an input changed while it
// ran), or the cache cannot be written (say a read-only AO_BUILD_CACHE_DIR),
// this build is used once and not stored.
export async function ensureCachedBuild({
	root,
	key,
	meta,
	build,
	use,
	currentKey = () => key,
	workRoot = tmpdir(),
	log = (line) => console.log(line),
}) {
	const cached = lookupEntry(root, CACHE_NAME, key);
	if (cached) return use(cached);
	const work = mkdtempSync(join(workRoot, `${CACHE_NAME}-build-`));
	try {
		const output = join(work, "output");
		mkdirSync(output);
		await build(work, output);
		if (currentKey() !== key) {
			log("bundled tmux: inputs changed during the build; not caching it.");
			return use(output);
		}
		try {
			storeEntry(root, CACHE_NAME, key, output, meta);
			pruneEntries(root, CACHE_NAME);
		} catch (error) {
			log(`bundled tmux: warning: could not store the build in the cache (${error.message}); continuing without it.`);
			return use(output);
		}
		return use(lookupEntry(root, CACHE_NAME, key) ?? output);
	} finally {
		rmSync(work, { recursive: true, force: true });
	}
}

// The shared source downloads directory, or this checkout's when the shared
// one cannot be written: the cache must never fail a build.
export function writableDownloadsDir(shared, fallback) {
	for (const dir of [shared, fallback]) {
		try {
			mkdirSync(dir, { recursive: true });
			accessSync(dir, constants.W_OK);
			return dir;
		} catch {
			// Try the next one.
		}
	}
	return fallback;
}

// Fetch `url` to `archive` unless a copy with `sha256` is already there. The
// published archive is never unlinked: a verified download replaces it with
// one atomic rename, so another checkout that is extracting it keeps reading a
// whole file.
export async function fetchVerified({ url, sha256, archive, label = url, fetchImpl = fetch, attempts = 3, retryDelayMs = 1000, log = (line) => console.log(line) }) {
	if (fileSha256(archive) === sha256) return;
	log(`Downloading ${label}...`);
	const part = `${archive}.part-${process.pid}`;
	let lastError;
	for (let attempt = 1; attempt <= attempts; attempt += 1) {
		rmSync(part, { force: true });
		try {
			const response = await fetchImpl(url, { redirect: "follow" });
			if (!response.ok) throw new Error(`HTTP ${response.status}`);
			writeFileSync(part, Buffer.from(await response.arrayBuffer()));
			if (fileSha256(part) !== sha256) throw new Error("sha256 mismatch");
			renameSync(part, archive);
			return;
		} catch (error) {
			lastError = error;
			if (attempt < attempts) await new Promise((resolveDelay) => setTimeout(resolveDelay, attempt * retryDelayMs));
		}
	}
	rmSync(part, { force: true });
	throw new Error(`failed to download ${url}: ${lastError instanceof Error ? lastError.message : String(lastError)}`);
}

function fileSha256(file) {
	return existsSync(file) ? createHash("sha256").update(readFileSync(file)).digest("hex") : null;
}
