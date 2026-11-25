// @vitest-environment node
import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
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

	it("builds without the cache when swiftc is unavailable", () => {
		expect(prepare({ run: vi.fn(() => ({ status: 1, stdout: "" })) })).toBe("built");
		expect(prepare({ run: vi.fn(() => ({ status: 1, stdout: "" })) })).toBe("built");
		expect(build).toHaveBeenCalledTimes(2);
		expect(entries()).toHaveLength(0);
	});
});
