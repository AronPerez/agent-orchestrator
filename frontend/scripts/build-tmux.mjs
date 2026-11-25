import { spawnSync } from "node:child_process";
import { cpus, tmpdir } from "node:os";
import { chmodSync, copyFileSync, existsSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
	buildEnvironment,
	compilerIdentity,
	ensureCachedBuild,
	fetchVerified,
	sourceDownloadsDir,
	writableDownloadsDir,
	tmuxCacheKey,
	tmuxCacheLocation,
} from "./tmux-build-cache.mjs";
import { BUNDLED_TMUX_SHA256, BUNDLED_TMUX_VERSION } from "./tmux-version.mjs";

const TMUX = {
	name: "tmux",
	version: BUNDLED_TMUX_VERSION,
	url: `https://github.com/tmux/tmux/releases/download/${BUNDLED_TMUX_VERSION}/tmux-${BUNDLED_TMUX_VERSION}.tar.gz`,
	sha256: BUNDLED_TMUX_SHA256,
	directory: `tmux-${BUNDLED_TMUX_VERSION}`,
	license: "COPYING",
};
const LIBEVENT = {
	name: "libevent",
	version: "2.1.12-stable",
	url: "https://github.com/libevent/libevent/releases/download/release-2.1.12-stable/libevent-2.1.12-stable.tar.gz",
	sha256: "92e6de1be9ec176428fd2367677e61ceffc2ee1cb119035037a27d346b0403bb",
	directory: "libevent-2.1.12-stable",
	license: "LICENSE",
};
const NCURSES = {
	name: "ncurses",
	version: "6.5",
	url: "https://invisible-mirror.net/archives/ncurses/ncurses-6.5.tar.gz",
	sha256: "136d91bc269a9a5785e5f9e980bc76ab57428f604ce3e5a5a90cebc767971cc6",
	directory: "ncurses-6.5",
	license: "COPYING",
};
const UTF8PROC = {
	name: "utf8proc",
	version: "2.10.0",
	url: "https://github.com/JuliaStrings/utf8proc/archive/refs/tags/v2.10.0.tar.gz",
	sha256: "6f4f1b639daa6dca9f80bc5db1233e9cbaa31a67790887106160b33ef743f136",
	directory: "utf8proc-2.10.0",
	license: "LICENSE.md",
};
const SOURCES = [TMUX, LIBEVENT, NCURSES, UTF8PROC];
// Linux intentionally links glibc dynamically. A fully static glibc tmux can
// enter __libc_early_init with an unset page size and SIGFPE on new-session
// even though `tmux -V` succeeds (#4616).
const BUILD_REVISION = 5;

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(scriptsDir, "..");
const outputRoot = join(frontendRoot, "tmux");
const outputBinary = join(outputRoot, "bin", "tmux");

if (process.platform !== "darwin" && process.platform !== "linux") {
	rmSync(outputRoot, { recursive: true, force: true });
	console.log("Bundled tmux is only built for macOS and Linux.");
	process.exit(0);
}

// Shared by every checkout on this machine unless the build environment is
// customised (see tmux-build-cache.mjs). The key covers the sources, compiler,
// build-relevant environment and this file's build() recipe, so editing the
// recipe or upgrading Xcode builds afresh instead of reusing a stale binary.
const cacheInputs = {
	platform: process.platform,
	arch: process.arch,
	buildRevision: BUILD_REVISION,
	sources: SOURCES,
	compiler: compilerIdentity(),
	environment: buildEnvironment(),
	recipe: build.toString(),
};
const { shared, root: cacheRoot } = tmuxCacheLocation({ repoRoot: resolve(frontendRoot, "..") });
if (!shared) {
	console.log(`Custom build environment (${Object.keys(cacheInputs.environment).join(", ")}): keeping the tmux build in this checkout.`);
}
// Resolved on the first download only: a hit, even from a read-only cache,
// never needs a writable sources directory.
let downloadsRoot;

// The entry holds the binary and the licenses that ship beside it. The sources
// are sha256-pinned and the environment and recipe are fixed for this process,
// so only the compiler can change under a running build: re-probe it after.
const { recipe: _recipe, ...metaInputs } = cacheInputs;
await ensureCachedBuild({
	root: cacheRoot,
	key: tmuxCacheKey(cacheInputs),
	currentKey: () => tmuxCacheKey({ ...cacheInputs, compiler: compilerIdentity() }),
	meta: { inputs: metaInputs },
	build,
	isComplete: (built) =>
		existsSync(join(built, "tmux")) && SOURCES.every((source) => existsSync(join(built, "licenses", `${source.name}.txt`))),
	use: (built) => {
		rmSync(outputRoot, { recursive: true, force: true });
		mkdirSync(join(outputRoot, "bin"), { recursive: true });
		// File by file into a fresh directory: copying the directory would carry
		// over a read-only cache's permissions and block the next rebuild.
		mkdirSync(join(outputRoot, "licenses"));
		for (const source of SOURCES) {
			copyFileSync(join(built, "licenses", `${source.name}.txt`), join(outputRoot, "licenses", `${source.name}.txt`));
		}
		copyFileSync(join(built, "tmux"), outputBinary);
		chmodSync(outputBinary, 0o755);
	},
});
writeFileSync(
	join(outputRoot, "VERSIONS.json"),
	JSON.stringify(
		{
			tmux: TMUX.version,
			libevent: LIBEVENT.version,
			ncurses: NCURSES.version,
			utf8proc: UTF8PROC.version,
			platform: process.platform,
			arch: process.arch,
		},
		null,
		2,
	) + "\n",
);

const version = run(outputBinary, ["-V"], { capture: true }).stdout.trim();
if (version !== `tmux ${TMUX.version}`) {
	fail(`bundled tmux reported ${JSON.stringify(version)}, expected ${JSON.stringify(`tmux ${TMUX.version}`)}`);
}
verifyTerminfoAttach(outputBinary);
console.log(`Bundled ${version} at ${outputBinary}`);

// Builds in a private work directory and writes what ships (the binary and
// each source's license) to `output`, which ensureCachedBuild then publishes.
async function build(buildDir, output) {
	const sourceRoot = join(buildDir, "src");
	const prefix = join(buildDir, "prefix");
	for (const source of SOURCES) await download(source);

	rmSync(sourceRoot, { recursive: true, force: true });
	rmSync(prefix, { recursive: true, force: true });
	mkdirSync(sourceRoot, { recursive: true });
	mkdirSync(prefix, { recursive: true });
	for (const source of SOURCES) {
		run("tar", ["-xzf", archivePath(source), "-C", sourceRoot]);
	}

	const commonEnv = {
		...process.env,
		...(process.platform === "darwin"
			? { MACOSX_DEPLOYMENT_TARGET: process.arch === "arm64" ? "11.0" : "10.15" }
			: {}),
		CPPFLAGS: `-I${join(prefix, "include")} -I${join(prefix, "include", "ncursesw")}`,
		LDFLAGS: `-L${join(prefix, "lib")}`,
		PKG_CONFIG_PATH: [join(prefix, "lib", "pkgconfig"), join(prefix, "lib64", "pkgconfig")].join(":"),
	};
	const jobs = String(Math.max(2, Math.min(cpus().length, 8)));

	const ncursesDir = join(sourceRoot, NCURSES.directory);
	run(
		"./configure",
		[
			`--prefix=${prefix}`,
			"--without-shared",
			"--with-normal",
			"--without-debug",
			"--without-ada",
			"--without-cxx",
			"--without-cxx-binding",
			"--without-tests",
			"--without-manpages",
			"--enable-widec",
			"--with-termlib",
			"--enable-pc-files",
			`--with-pkg-config-libdir=${join(prefix, "lib", "pkgconfig")}`,
		],
		{ cwd: ncursesDir, env: commonEnv },
	);
	run("make", ["-j", jobs], { cwd: ncursesDir, env: commonEnv });
	// Runtime.Attach always presents xterm-256color. Generate its fallback
	// with the matching pinned tic/infocmp we just built: macOS's older host
	// tools cannot compile the ncurses 6.5 entry. Rebuilding embeds the entry
	// into libtinfo so the shipped binary needs no host terminfo database.
	const ncursesBuildDir = join(ncursesDir, "ncurses");
	const fallback = run(
		"sh",
		[
			"-e",
			"./tinfo/MKfallback.sh",
			join(prefix, "share", "terminfo"),
			"../misc/terminfo.src",
			"../progs/tic",
			"../progs/infocmp",
			"xterm-256color",
		],
		{ cwd: ncursesBuildDir, env: commonEnv },
	);
	writeFileSync(join(ncursesBuildDir, "fallback.c"), fallback.stdout);
	run("make", ["-j", jobs], { cwd: ncursesDir, env: commonEnv });
	run("make", ["install.libs", "install.includes"], { cwd: ncursesDir, env: commonEnv });

	const libeventDir = join(sourceRoot, LIBEVENT.directory);
	run(
		"./configure",
		[
			`--prefix=${prefix}`,
			"--disable-shared",
			"--enable-static",
			"--disable-openssl",
			"--disable-samples",
			"--disable-tests",
			"--disable-regress",
			"--disable-benchmarks",
		],
		{ cwd: libeventDir, env: commonEnv },
	);
	run("make", ["-j", jobs], { cwd: libeventDir, env: commonEnv });
	run("make", ["install"], { cwd: libeventDir, env: commonEnv });

	const utf8procDir = join(sourceRoot, UTF8PROC.directory);
	run("make", ["libutf8proc.a", "libutf8proc.pc", `prefix=${prefix}`], { cwd: utf8procDir, env: commonEnv });
	copyFileSync(join(utf8procDir, "libutf8proc.a"), join(prefix, "lib", "libutf8proc.a"));
	copyFileSync(join(utf8procDir, "utf8proc.h"), join(prefix, "include", "utf8proc.h"));
	copyFileSync(join(utf8procDir, "libutf8proc.pc"), join(prefix, "lib", "pkgconfig", "libutf8proc.pc"));

	const tmuxDir = join(sourceRoot, TMUX.directory);
	const tmuxConfigureArgs = ["--enable-utf8proc"];
	const tmuxEnv = {
		...commonEnv,
		LIBTINFO_CFLAGS: `-I${join(prefix, "include", "ncursesw")} -I${join(prefix, "include")}`,
		LIBTINFO_LIBS: join(prefix, "lib", "libtinfow.a"),
		LIBUTF8PROC_CFLAGS: `-I${join(prefix, "include")}`,
		LIBUTF8PROC_LIBS: join(prefix, "lib", "libutf8proc.a"),
	};
	run("./configure", tmuxConfigureArgs, { cwd: tmuxDir, env: tmuxEnv });
	run("make", ["-j", jobs], { cwd: tmuxDir, env: tmuxEnv });

	const builtBinary = join(tmuxDir, "tmux");
	verifyPortableLinkage(builtBinary, prefix);
	const binary = join(output, "tmux");
	copyFileSync(builtBinary, binary);
	chmodSync(binary, 0o755);
	mkdirSync(join(output, "licenses"));
	for (const source of SOURCES) {
		copyFileSync(join(sourceRoot, source.directory, source.license), join(output, "licenses", `${source.name}.txt`));
	}
}

async function download(source) {
	try {
		await fetchVerified({ url: source.url, sha256: source.sha256, archive: archivePath(source), label: `${source.name} ${source.version}` });
	} catch (error) {
		fail(error instanceof Error ? error.message : String(error));
	}
}

function archivePath(source) {
	downloadsRoot ??= writableDownloadsDir(sourceDownloadsDir(), join(resolve(frontendRoot, ".."), ".cache", "bundled-tmux-sources"));
	return join(downloadsRoot, `${source.name}-${source.version}.tar.gz`);
}

function verifyPortableLinkage(binary, buildPrefix) {
	if (process.platform === "darwin") {
		const output = run("otool", ["-L", binary], { capture: true }).stdout;
		const external = output
			.split("\n")
			.slice(1)
			.map((line) => line.trim().split(" ")[0])
			.filter(Boolean)
			.filter((library) => !library.startsWith("/usr/lib/") && !library.startsWith("/System/Library/"));
		if (external.length > 0 || output.includes(buildPrefix)) {
			fail(`bundled tmux has non-system dylib dependencies:\n${output}`);
		}
		return;
	}
	const output = run("ldd", [binary], { capture: true, allowFailure: true });
	const linkage = `${output.stdout}\n${output.stderr}`;
	if (/libevent|libncurses|libtinfo|libutf8proc|not found/.test(linkage) || linkage.includes(buildPrefix)) {
		fail(`bundled tmux has unbundled runtime dependencies:\n${linkage}`);
	}
}

function verifyTerminfoAttach(binary) {
	const identity = `ao-tmux-smoke-${process.pid}`;
	const socket = join(tmpdir(), `${identity}.sock`);
	const session = "terminfo";
	const missingTerminfo = join(tmpdir(), identity, "missing-terminfo");
	const attachEnvironment = {
		...process.env,
		HOME: join(tmpdir(), identity, "missing-home"),
		TERM: "xterm-256color",
		TERMINFO: missingTerminfo,
		TERMINFO_DIRS: missingTerminfo,
	};
	run(binary, ["-S", socket, "-f", "/dev/null", "new-session", "-d", "-s", session, "sleep 10"]);
	try {
		// Detach as soon as the client is up: terminfo is resolved while
		// attaching, so a missing database still fails here. Without the detach,
		// attach-session waited out the session's `sleep 10` on every build.
		const tmuxArgs = [binary, "-S", socket, "-u", "-T", "RGB", "attach-session", "-t", session, ";", "detach-client"];
		const result =
			process.platform === "darwin"
				? run("script", ["-q", "/dev/null", ...tmuxArgs], {
						capture: true,
						allowFailure: true,
						ignoreStdin: true,
						env: attachEnvironment,
					})
				: run("script", ["-q", "-e", "-c", tmuxArgs.map(shellQuote).join(" "), "/dev/null"], {
						capture: true,
						allowFailure: true,
						ignoreStdin: true,
						env: attachEnvironment,
					});
		const output = `${result.stdout}\n${result.stderr}`;
		if (result.status !== 0 || /terminfo database/i.test(output)) {
			fail(`bundled tmux could not attach with TERM=xterm-256color:\n${output}`);
		}
	} finally {
		run(binary, ["-S", socket, "kill-server"], { capture: true, allowFailure: true });
		rmSync(socket, { force: true });
	}
}

function shellQuote(value) {
	return `'${value.replaceAll("'", `'\\''`)}'`;
}

function run(command, args, options = {}) {
	const capture = options.capture || process.env.AO_VERBOSE_NATIVE_BUILD !== "1";
	const result = spawnSync(command, args, {
		cwd: options.cwd,
		env: options.env,
		encoding: capture ? "utf8" : undefined,
		maxBuffer: 64 * 1024 * 1024,
		stdio: capture ? [options.ignoreStdin ? "ignore" : "pipe", "pipe", "pipe"] : "inherit",
		windowsHide: true,
	});
	if (result.error) fail(`failed to start ${command}: ${result.error.message}`);
	if (!options.allowFailure && result.status !== 0) {
		if (capture) {
			if (result.stdout) process.stderr.write(result.stdout);
			if (result.stderr) process.stderr.write(result.stderr);
		}
		fail(`${command} exited with status ${result.status}`);
	}
	return { status: result.status, stdout: result.stdout ?? "", stderr: result.stderr ?? "" };
}

function fail(message) {
	console.error(message);
	process.exit(1);
}
