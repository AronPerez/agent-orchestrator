// The bundled tmux build cache, shared by every checkout and worktree on the
// machine. A tmux build takes about a minute, and a per-checkout cache made
// every new worktree pay it again.
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, readdirSync, renameSync, rmSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

// Under ~/.ao, beside the Swift helpers' module caches. Never inside frontend/:
// Electron Packager treats that as the app root and would copy hundreds of
// megabytes of intermediate objects into app.asar.
export function tmuxBuildCacheRoot(home = homedir()) {
	return join(home, ".ao", "build-cache", "bundled-tmux");
}

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

// Share only default builds. A customised environment (say CFLAGS=-march=native)
// can produce a binary that is wrong for other machines, so it keeps its build
// in this checkout, as before. The key covers the environment either way.
export function tmuxCacheLocation({ env = process.env, home = homedir(), repoRoot }) {
	const shared = Object.keys(buildEnvironment(env)).length === 0;
	return { shared, root: shared ? tmuxBuildCacheRoot(home) : join(repoRoot, ".cache", "bundled-tmux") };
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

// Builds run in <key>.building-<pid> and are renamed into place only once
// complete, so two checkouts building the same key never see each other's
// half-finished tree. The loser of the race keeps the winner's build; a
// damaged directory already at the key (no completion stamp) is replaced.
export function buildDirFor(finalDir, pid = process.pid) {
	return `${finalDir}.building-${pid}`;
}

export function publishBuild(buildDir, finalDir, isComplete) {
	try {
		renameSync(buildDir, finalDir);
		return true;
	} catch (error) {
		if (error?.code !== "EEXIST" && error?.code !== "ENOTEMPTY") throw error;
		if (isComplete(finalDir)) {
			rmSync(buildDir, { recursive: true, force: true });
			return false;
		}
		rmSync(finalDir, { recursive: true, force: true });
		renameSync(buildDir, finalDir);
		return true;
	}
}

// Remove build directories left by processes that died mid-build.
export function pruneAbandonedBuilds(root, isAlive = processIsAlive) {
	if (!existsSync(root)) return;
	for (const entry of readdirSync(root)) {
		const match = entry.match(/\.building-(\d+)$/);
		if (match && !isAlive(Number(match[1]))) rmSync(join(root, entry), { recursive: true, force: true });
	}
}

function processIsAlive(pid) {
	try {
		process.kill(pid, 0);
		return true;
	} catch (error) {
		return error?.code === "EPERM";
	}
}
