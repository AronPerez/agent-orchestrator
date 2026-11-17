import { execFileSync } from "node:child_process";
import { version } from "./package.json";

export function desktopBuildIdentity(): string {
	return `${version}:${appBuildIdentity()}:${process.env.AO_DESKTOP_BUILD_ID ?? "web"}`;
}

/** Same VCS stamp as daemonmeta.BuildIdentity(), shared by main and renderer. */
export function appBuildIdentity(): string {
	try {
		const revision = execFileSync("git", ["rev-parse", "HEAD"], { encoding: "utf8" }).trim();
		if (!revision) return "";
		let modified = false;
		try { modified = execFileSync("git", ["status", "--porcelain"], { encoding: "utf8" }).trim() !== ""; } catch { /* Unknown dirtiness retains the revision. */ }
		return modified ? `${revision}-dirty` : revision;
	} catch { return ""; }
}
