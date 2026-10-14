import { mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import { meetsMinimumVersion, parseGoVersion, parseMinimumGoVersion } from "./go-version.mjs";

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(scriptsDir, "..");
const repoRoot = resolve(frontendRoot, "..");
const backendRoot = join(repoRoot, "backend");
const outDir = join(frontendRoot, "daemon");
const outPath = join(outDir, process.platform === "win32" ? "ao.exe" : "ao");
const isWindowsDev = process.platform === "win32" && process.argv.includes("--dev");
const windowsDevOutDir = join(outDir, `dev-${Date.now()}-${process.pid}`);
const buildOutPath = isWindowsDev ? join(windowsDevOutDir, "ao.exe") : outPath;
const windowsDevManifestPath = join(outDir, "dev-daemon.json");
const minimumGoVersion = parseMinimumGoVersion(readFileSync(join(backendRoot, "go.mod"), "utf8"));

if (!minimumGoVersion) {
	console.error("Could not determine the required Go version from backend/go.mod.");
	process.exit(1);
}

const versionResult = spawnSync("go", ["version"], { encoding: "utf8", windowsHide: true });
if (versionResult.error) {
	console.error(
		`Go ${minimumGoVersion.join(".")}+ is required, but Go could not be started: ${versionResult.error.message}`,
	);
	process.exit(1);
}
const actualGoVersion = parseGoVersion(versionResult.stdout);
if (versionResult.status !== 0 || !actualGoVersion || !meetsMinimumVersion(actualGoVersion, minimumGoVersion)) {
	const found = actualGoVersion ? actualGoVersion.join(".") : versionResult.stdout.trim() || "unknown";
	console.error(`Go ${minimumGoVersion.join(".")}+ required, found ${found} — upgrade at https://go.dev/dl/`);
	process.exit(1);
}

if (isWindowsDev) {
	mkdirSync(windowsDevOutDir, { recursive: true });
} else if (process.platform === "win32") {
	// A running dev daemon may still hold an older dev-* binary open. Keep the
	// output directory in place and remove only the files that the packaged
	// build owns; locked dev folders can remain without affecting the bundled
	// daemon path.
	mkdirSync(outDir, { recursive: true });
	rmSync(outPath, { force: true });
	rmSync(windowsDevManifestPath, { force: true });
	cleanupOldWindowsDevDaemons(undefined);
} else {
	rmSync(outDir, { recursive: true, force: true });
	mkdirSync(outDir, { recursive: true });
}

// Link-time build stamp. Go's own VCS stamping is silently absent when the build
// runs inside a linked git worktree — even with -buildvcs=true, which exits 0 and
// stamps nothing — and the app-bundled daemon shipped with no build identity for
// exactly that reason. `git rev-parse` works in a worktree, so ask git directly.
//
// DO NOT "simplify" this back to -buildvcs. It is a silent no-op in a worktree:
// the build succeeds, the app ships, and /healthz reports source "unknown"
// forever — which is precisely the bug this replaced.
const stampPkg = "github.com/aoagents/agent-orchestrator/backend/internal/daemonmeta";
const gitOutput = (args) => {
	const r = spawnSync("git", args, { cwd: repoRoot, encoding: "utf8" });
	return r.status === 0 ? r.stdout.trim() : "";
};
const head = gitOutput(["rev-parse", "HEAD"]);
const dirty = head && spawnSync("git", ["diff", "--quiet", "HEAD"], { cwd: repoRoot }).status !== 0;
const buildStamp = head ? `${head}${dirty ? "-dirty" : ""}` : "";

const result = spawnSync("go", ["build", "-ldflags", `-X ${stampPkg}.buildStamp=${buildStamp}`, "-o", buildOutPath, "./cmd/ao"], {
	cwd: backendRoot,
	stdio: "inherit",
	windowsHide: true,
});

if (result.error) {
	console.error(`failed to start go build: ${result.error.message}`);
	process.exit(1);
}

if (result.status !== 0) {
	process.exit(result.status ?? 1);
}

if (isWindowsDev) {
	writeFileSync(windowsDevManifestPath, `${JSON.stringify({ path: buildOutPath }, null, 2)}\n`);
	cleanupOldWindowsDevDaemons(buildOutPath);
}

function cleanupOldWindowsDevDaemons(activePath) {
	const activeDir = activePath ? dirname(activePath) : "";
	let entries;
	try {
		entries = readdirSync(outDir, { withFileTypes: true })
			.filter((entry) => entry.isDirectory() && entry.name.startsWith("dev-"))
			.map((entry) => {
				const dir = join(outDir, entry.name);
				return { dir, mtimeMs: statSync(dir).mtimeMs };
			})
			.sort((a, b) => b.mtimeMs - a.mtimeMs);
	} catch {
		return;
	}
	for (const entry of entries.slice(activePath ? 5 : 0)) {
		if (entry.dir === activeDir) continue;
		try {
			rmSync(entry.dir, { recursive: true, force: true });
		} catch {
			// Old session processes can keep their exe locked. They will be cleaned
			// up by a later dev build after the process exits.
		}
	}
}
