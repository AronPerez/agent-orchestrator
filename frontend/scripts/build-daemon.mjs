import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
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

rmSync(outDir, { recursive: true, force: true });
mkdirSync(outDir, { recursive: true });

const result = spawnSync("go", ["build", "-o", outPath, "./cmd/ao"], {
	cwd: backendRoot,
	stdio: "inherit",
});

if (result.error) {
	console.error(`failed to start go build: ${result.error.message}`);
	process.exit(1);
}

if (result.status !== 0) {
	process.exit(result.status ?? 1);
}
