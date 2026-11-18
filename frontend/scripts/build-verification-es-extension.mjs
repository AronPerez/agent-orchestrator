import { spawnSync } from "node:child_process";
import { copyFileSync, mkdirSync, rmSync } from "node:fs";
import { homedir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
export const VERIFICATION_ES_DEV_FLAG = "AO_MACOS_ES_EXTENSION_DEV";
export const VERIFICATION_ES_EXECUTABLE = "ao-verification-endpoint-security";
export const VERIFICATION_ES_BUNDLE = "AgentOrchestratorVerification.systemextension";

const sources = [
	"Protocol.swift",
	"WireCodec.swift",
	"PeerRequirement.swift",
	"ObservationTracker.swift",
	"EndpointSecuritySource.swift",
	"XPCService.swift",
	"main.swift",
];

export function verificationExtensionBuildOptions(arch, root = frontend, dataDir = path.join(homedir(), ".ao")) {
	const cpu = arch === "arm64" ? "arm64" : arch === "x64" ? "x86_64" : undefined;
	if (!cpu) throw new Error(`Unsupported macOS verification extension architecture: ${arch}`);
	const source = path.join(root, "native", "verification-es-extension");
	const outputRoot = path.join(dataDir, "build-cache", "verification-es-extension", arch);
	const bundle = path.join(outputRoot, VERIFICATION_ES_BUNDLE);
	const executable = path.join(bundle, "Contents", "MacOS", VERIFICATION_ES_EXECUTABLE);
	return {
		source,
		outputRoot,
		bundle,
		executable,
		moduleCache: path.join(outputRoot, "module-cache"),
		args: [
			"swiftc",
			"-swift-version", "6",
			"-warnings-as-errors",
			"-target", `${cpu}-apple-macosx13.0`,
			"-O",
			"-D", "AO_MACOS_ES_EXTENSION_DEV",
			...sources.map((file) => path.join(source, file)),
			"-lEndpointSecurity",
			"-lbsm",
			"-framework", "Security",
		],
	};
}

export function buildVerificationExtension({
	arch = process.arch,
	platform = process.platform,
	env = process.env,
	run = spawnSync,
} = {}) {
	if (env[VERIFICATION_ES_DEV_FLAG] !== "1") return null;
	if (platform !== "darwin") throw new Error("The macOS verification extension must be built on macOS");
	const dataDir = env.AO_DATA_DIR?.trim() || path.join(homedir(), ".ao");
	const options = verificationExtensionBuildOptions(arch, frontend, dataDir);
	rmSync(options.outputRoot, { recursive: true, force: true });
	mkdirSync(path.dirname(options.executable), { recursive: true });
	mkdirSync(options.moduleCache, { recursive: true });
	copyFileSync(path.join(options.source, "Info.plist"), path.join(options.bundle, "Contents", "Info.plist"));
	const result = run("xcrun", [...options.args, "-module-cache-path", options.moduleCache, "-o", options.executable], { stdio: "inherit" });
	if (result.error) throw result.error;
	if (result.status !== 0) throw new Error(`macOS verification extension compilation failed (${result.status ?? result.signal})`);
	return options.bundle;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
	try {
		const args = process.argv.slice(2);
		const ifEnabled = args[0] === "--if-enabled";
		const remaining = ifEnabled ? args.slice(1) : args;
		if (remaining.length !== 0 && (remaining.length !== 2 || remaining[0] !== "--arch")) {
			throw new Error("Usage: build-verification-es-extension.mjs [--if-enabled] [--arch arm64|x64]");
		}
		const output = buildVerificationExtension({ arch: remaining[1] ?? process.arch });
		if (!output && !ifEnabled) throw new Error(`${VERIFICATION_ES_DEV_FLAG}=1 is required for this development-only target`);
	} catch (error) {
		console.error(error.message);
		process.exitCode = 1;
	}
}
