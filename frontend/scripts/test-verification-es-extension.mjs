import { spawnSync } from "node:child_process";
import { mkdirSync } from "node:fs";
import { homedir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = path.join(frontend, "native", "verification-es-extension");
const dataDir = process.env.AO_DATA_DIR?.trim() || path.join(homedir(), ".ao");
const output = path.join(dataDir, "build-cache", "verification-es-extension", "tests");
const executable = path.join(output, "state-tests");
const moduleCache = path.join(output, "module-cache");

if (process.platform !== "darwin") throw new Error("The macOS verification extension tests must run on macOS");
mkdirSync(moduleCache, { recursive: true });
const compile = spawnSync("xcrun", [
	"swiftc",
	"-swift-version", "6",
	"-warnings-as-errors",
	"-target", `${process.arch === "x64" ? "x86_64" : "arm64"}-apple-macosx13.0`,
	...[
		"Protocol.swift",
		"WireCodec.swift",
		"PeerRequirement.swift",
		"ObservationTracker.swift",
		"XPCService.swift",
		"EndpointSecuritySource.swift",
		"StateTests.swift",
	].map((file) => path.join(source, file)),
	"-lEndpointSecurity",
	"-lbsm",
	"-framework", "Security",
	"-module-cache-path", moduleCache,
	"-o", executable,
], { stdio: "inherit" });
if (compile.error) throw compile.error;
if (compile.status !== 0) throw new Error(`macOS verification extension tests failed to compile (${compile.status ?? compile.signal})`);
const test = spawnSync(executable, [], { stdio: "inherit" });
if (test.error) throw test.error;
if (test.status !== 0) throw new Error(`macOS verification extension tests failed (${test.status ?? test.signal})`);
