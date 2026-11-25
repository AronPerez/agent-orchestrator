// @vitest-environment node
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { buildSwiftHelper, helperCacheKey } from "./swift-helper-cache.mjs";

let root;
let cacheRoot;

beforeEach(() => {
	const dir = mkdtempSync(join(tmpdir(), "swift-helper-cache-"));
	root = join(dir, "frontend");
	cacheRoot = join(dir, "cache");
	mkdirSync(join(root, "scripts"), { recursive: true });
	mkdirSync(join(root, "native", "wake-helper"), { recursive: true });
	writeFileSync(join(root, "scripts", "build-wake-helper.mjs"), "// build script");
	writeFileSync(join(root, "native", "wake-helper", "main.swift"), "print(1)");
	writeFileSync(join(root, "native", "wake-helper", "THIRD_PARTY_NOTICES.md"), "notices");
});

afterEach(() => {
	rmSync(join(root, ".."), { recursive: true, force: true });
});

const swift = (version = "Apple Swift version 6.3") => vi.fn(() => ({ status: 0, stdout: `${version}\n` }));
let builds = 0;
const build = vi.fn(() => {
	builds += 1;
	const out = join(root, "wake-helper");
	mkdirSync(out, { recursive: true });
	writeFileSync(join(out, "ao-wake"), `binary-${builds}`);
	writeFileSync(join(out, "THIRD_PARTY_NOTICES.md"), "notices");
});

const prepare = (overrides = {}) =>
	buildSwiftHelper({ name: "wake-helper", arch: "arm64", build, root, cacheRoot, env: {}, run: swift(), log: () => undefined, ...overrides });
const entries = () => {
	const dir = join(cacheRoot, "package-wake-helper");
	return existsSync(dir) ? readdirSync(dir).filter((name) => !name.startsWith(".")) : [];
};

describe("Swift helper cache", () => {
	beforeEach(() => {
		builds = 0;
		build.mockClear();
	});

	it("builds and stores on a miss, then restores the same output without compiling", () => {
		expect(prepare()).toBe("built");
		expect(entries()).toHaveLength(1);
		rmSync(join(root, "wake-helper"), { recursive: true });

		expect(prepare()).toBe("restored");
		expect(build).toHaveBeenCalledTimes(1);
		expect(readFileSync(join(root, "wake-helper", "ao-wake"), "utf8")).toBe("binary-1");
		expect(readFileSync(join(root, "wake-helper", "THIRD_PARTY_NOTICES.md"), "utf8")).toBe("notices");
	});

	it.each([
		["a Swift source", () => writeFileSync(join(root, "native", "wake-helper", "main.swift"), "print(2)")],
		["a new Swift source", () => writeFileSync(join(root, "native", "wake-helper", "Extra.swift"), "")],
		["the build script", () => writeFileSync(join(root, "scripts", "build-wake-helper.mjs"), "// changed")],
	])("rebuilds when %s changes", (_name, change) => {
		prepare();
		change();
		expect(prepare()).toBe("built");
		expect(build).toHaveBeenCalledTimes(2);
	});

	it.each([
		["another arch", { arch: "x64" }],
		["another Swift compiler", { run: swift("Apple Swift version 6.4") }],
		["another SDK", { env: { SDKROOT: "/Library/Developer/CommandLineTools/SDKs/MacOSX15.sdk" } }],
	])("rebuilds for %s", (_name, overrides) => {
		prepare();
		expect(prepare(overrides)).toBe("built");
		expect(build).toHaveBeenCalledTimes(2);
	});

	it("ignores files the helper does not read", () => {
		const key = helperCacheKey({ name: "wake-helper", arch: "arm64", swift: "s", env: {}, root });
		writeFileSync(join(root, "scripts", "unrelated.mjs"), "");
		expect(helperCacheKey({ name: "wake-helper", arch: "arm64", swift: "s", env: {}, root })).toBe(key);
	});

	it("rebuilds when the cached output is missing a required file", () => {
		prepare();
		const [entry] = entries();
		rmSync(join(cacheRoot, "package-wake-helper", entry, "output", "ao-wake"));
		expect(prepare()).toBe("built");
		expect(readFileSync(join(root, "wake-helper", "ao-wake"), "utf8")).toBe("binary-2");
	});

	it("does not store an output that is missing a required file", () => {
		const incomplete = vi.fn(() => mkdirSync(join(root, "wake-helper"), { recursive: true }));
		prepare({ build: incomplete });
		expect(entries()).toHaveLength(0);
	});

	it("does not store a build whose sources changed while it compiled", () => {
		const lines = [];
		const editingBuild = vi.fn(() => {
			build();
			writeFileSync(join(root, "native", "wake-helper", "main.swift"), "print(\"edited mid-build\")");
		});
		expect(prepare({ build: editingBuild, log: (line) => lines.push(line) })).toBe("built");
		expect(entries()).toHaveLength(0);
		expect(lines).toEqual(["wake-helper: inputs changed during the build; not caching it."]);
	});

	it("keeps a successful build when the cache cannot be written", () => {
		const lines = [];
		writeFileSync(cacheRoot, "not a directory");
		expect(prepare({ log: (line) => lines.push(line) })).toBe("built");
		expect(readFileSync(join(root, "wake-helper", "ao-wake"), "utf8")).toBe("binary-1");
		expect(lines.some((line) => line.startsWith("wake-helper: warning: could not store the build in the cache"))).toBe(true);
	});

	it("restores from a read-only cache on every package run, not just the first", () => {
		prepare();
		const [entry] = entries();
		const entryDir = join(cacheRoot, "package-wake-helper", entry);
		const setMode = (dir, mode) => {
			chmodSync(dir, mode);
			for (const name of readdirSync(dir)) {
				const child = join(dir, name);
				if (statSync(child).isDirectory()) setMode(child, mode);
			}
		};
		setMode(entryDir, 0o555);
		try {
			expect(prepare()).toBe("restored");
			expect(statSync(join(root, "wake-helper")).mode & 0o200).toBe(0o200);
			expect(prepare()).toBe("restored");
			expect(build).toHaveBeenCalledTimes(1);
		} finally {
			setMode(entryDir, 0o755);
		}
	});

	it("removes an output folder an earlier restore left read-only", () => {
		prepare();
		chmodSync(join(root, "wake-helper"), 0o555);
		try {
			expect(prepare()).toBe("restored");
		} finally {
			chmodSync(join(root, "wake-helper"), 0o755);
		}
	});

	describe("dictation bundle", () => {
		const bundle = () => join(root, "dictation-helper", "Orchestrator.app", "Contents");
		const dictationBuild = vi.fn(() => {
			mkdirSync(join(bundle(), "MacOS"), { recursive: true });
			mkdirSync(join(bundle(), "Resources"), { recursive: true });
			mkdirSync(join(bundle(), "_CodeSignature"), { recursive: true });
			writeFileSync(join(bundle(), "MacOS", "Orchestrator"), `binary-${dictationBuild.mock.calls.length}`);
			writeFileSync(join(bundle(), "Info.plist"), "plist");
			writeFileSync(join(bundle(), "Resources", "icon.icns"), "icon");
			writeFileSync(join(bundle(), "_CodeSignature", "CodeResources"), "seal");
		});
		// xcrun answers the toolchain probe; codesign answers per `sealValid`.
		const tools = (sealValid) =>
			vi.fn((command) => (command === "codesign" ? { status: sealValid() ? 0 : 1, stdout: "" } : { status: 0, stdout: "Apple Swift version 6.3\n" }));
		const prepareDictation = (overrides = {}) =>
			buildSwiftHelper({
				name: "dictation-helper",
				arch: "arm64",
				build: dictationBuild,
				root,
				cacheRoot,
				env: {},
				run: tools(() => true),
				log: () => undefined,
				...overrides,
			});
		const cachedOutput = () => {
			const dir = join(cacheRoot, "package-dictation-helper");
			const [entry] = readdirSync(dir).filter((name) => !name.startsWith("."));
			return join(dir, entry, "output", "Orchestrator.app", "Contents");
		};

		beforeEach(() => {
			dictationBuild.mockClear();
			mkdirSync(join(root, "native", "dictation-helper"), { recursive: true });
			mkdirSync(join(root, "assets"), { recursive: true });
			writeFileSync(join(root, "native", "dictation-helper", "main.swift"), "print(1)");
			writeFileSync(join(root, "native", "dictation-helper", "Info.plist"), "plist");
			writeFileSync(join(root, "assets", "icon.icns"), "icon");
			writeFileSync(join(root, "scripts", "build-dictation-helper.mjs"), "// build script");
		});

		it("reuses a complete bundle whose seal verifies", () => {
			expect(prepareDictation()).toBe("built");
			expect(prepareDictation()).toBe("restored");
			expect(dictationBuild).toHaveBeenCalledTimes(1);
		});

		it.each([
			["its icon", "Resources/icon.icns"],
			["its code signature", "_CodeSignature/CodeResources"],
		])("rebuilds and replaces a cached bundle that lost %s", (_name, rel) => {
			prepareDictation();
			rmSync(join(cachedOutput(), rel));
			expect(prepareDictation()).toBe("built");
			expect(dictationBuild).toHaveBeenCalledTimes(2);
			expect(existsSync(join(cachedOutput(), rel))).toBe(true);
			expect(prepareDictation()).toBe("restored");
		});

		it("rebuilds when the cached bundle's seal does not verify", () => {
			prepareDictation();
			let verifications = 0;
			// The restored copy fails codesign once; the rebuilt one passes.
			const run = tools(() => ++verifications > 2);
			expect(prepareDictation({ run })).toBe("built");
			expect(dictationBuild).toHaveBeenCalledTimes(2);
		});
	});

	it("builds without the cache when swiftc is unavailable", () => {
		expect(prepare({ run: vi.fn(() => ({ status: 1, stdout: "" })) })).toBe("built");
		expect(prepare({ run: vi.fn(() => ({ status: 1, stdout: "" })) })).toBe("built");
		expect(build).toHaveBeenCalledTimes(2);
		expect(entries()).toHaveLength(0);
	});
});
