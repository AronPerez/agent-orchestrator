import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
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
const minimumGoVersion = parseMinimumGoVersion(readFileSync(join(backendRoot, "go.mod"), "utf8"));

if (!minimumGoVersion) {
	console.error("Could not determine the required Go version from backend/go.mod.");
	process.exit(1);
}

const versionResult = spawnSync("go", ["version"], { encoding: "utf8" });
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

// Build the browser bundle the daemon embeds and serves at its own origin
// (backend/internal/httpd/webui), so a machine with only a browser gets the
// full UI. Generated here rather than committed: it is a megabyte of hashed
// assets that would churn on every renderer change. The directory keeps a
// tracked .gitkeep so go:embed — and therefore `go build` — works in a
// checkout that has not run this step.
const webuiDir = join(backendRoot, "internal", "httpd", "webui", "bundle");
rmSync(webuiDir, { recursive: true, force: true });

const viteCli = join(frontendRoot, "node_modules", "vite", "bin", "vite.js");
const webResult = spawnSync(
	process.execPath,
	[viteCli, "build", "--config", "vite.renderer.config.ts", "--outDir", webuiDir, "--emptyOutDir"],
	{ cwd: frontendRoot, stdio: "inherit", env: { ...process.env, VITE_AO_WEB: "1" } },
);

if (webResult.error) {
	console.error(`failed to start the web UI build: ${webResult.error.message}`);
	process.exit(1);
}
if (webResult.status !== 0) {
	process.exit(webResult.status ?? 1);
}
mkdirSync(webuiDir, { recursive: true });
writeFileSync(join(webuiDir, ".gitkeep"), "");

// Prove the bundle is really there before `go build` embeds it. go:embed is
// satisfied by the tracked .gitkeep alone, so an empty bundle directory still
// compiles, still passes CI, and still ships — producing a daemon whose UI
// answers 503 to the first person who opens the URL. That is a silent ship, and
// this is the only place that can catch it: a Go test cannot, because CI checks
// out .gitkeep and nothing else, so asserting the bundle exists there would
// fail by design.
if (!existsSync(join(webuiDir, "index.html"))) {
	console.error(
		`web UI bundle missing: ${join(webuiDir, "index.html")} does not exist after the vite build.\n` +
			"The daemon would compile and ship with no UI (503 on every page request).\n" +
			"Check the `vite build` step above — do not skip or reorder it.",
	);
	process.exit(1);
}

rmSync(outDir, { recursive: true, force: true });
mkdirSync(outDir, { recursive: true });

// Link-time build stamp. Go's own VCS stamping is silently absent when the build
// runs inside a linked git worktree — even with -buildvcs=true, which exits 0 and
// stamps nothing — and the app-bundled daemon shipped with no build identity for
// exactly that reason. `git rev-parse` works in a worktree, so ask git directly.
const stampPkg = "github.com/aoagents/agent-orchestrator/backend/internal/daemonmeta";
const gitOutput = (args) => {
	const r = spawnSync("git", args, { cwd: repoRoot, encoding: "utf8" });
	return r.status === 0 ? r.stdout.trim() : "";
};
const head = gitOutput(["rev-parse", "HEAD"]);
const dirty = head && spawnSync("git", ["diff", "--quiet", "HEAD"], { cwd: repoRoot }).status !== 0;
const buildStamp = head ? `${head}${dirty ? "-dirty" : ""}` : "";

const result = spawnSync(
	"go",
	["build", "-ldflags", `-X ${stampPkg}.buildStamp=${buildStamp}`, "-o", outPath, "./cmd/ao"],
	{ cwd: backendRoot, stdio: "inherit" },
);

if (result.error) {
	console.error(`failed to start go build: ${result.error.message}`);
	process.exit(1);
}

if (result.status !== 0) {
	process.exit(result.status ?? 1);
}
