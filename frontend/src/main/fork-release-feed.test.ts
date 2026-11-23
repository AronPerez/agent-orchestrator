import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

// Fork releases ship from AronPerez/agent-orchestrator. Every default the app
// falls back to (update feed, feature-build list, version floor, download link)
// must name the fork, or a fork install would follow upstream's releases or be
// force-quit by upstream's version floor.
const FORK = "AronPerez/agent-orchestrator";
const UPSTREAM_OWNERS = ["Untrivial-ai", "OrchestratorInc", "AgentWrapper", "aoagents/agent-orchestrator"];

const frontend = path.resolve(import.meta.dirname, "..", "..");
const read = (relative: string) => readFileSync(path.resolve(frontend, relative), "utf8");

describe("fork release defaults", () => {
	it("bakes the fork into the update feed", () => {
		expect(read("forge.config.ts")).toContain(`const DEFAULT_RELEASE_REPO = "${FORK}";`);
	});

	it("falls back to the fork for the feature-build list", () => {
		const [owner, repo] = FORK.split("/");
		expect(read("src/main/feature-builds.ts")).toContain(
			`const DEFAULT_REPO = { owner: "${owner}", repo: "${repo}" } as const;`,
		);
	});

	it("reads the version floor from the fork's develop branch", () => {
		const floor = read("src/main/desktop-version-floor.ts");
		expect(floor).toContain(`https://raw.githubusercontent.com/${FORK}/develop/desktop-version-floor.json`);
		expect(floor).toContain(`https://github.com/${FORK}/releases/latest`);
		for (const owner of UPSTREAM_OWNERS) expect(floor).not.toContain(owner);
	});

	it("links the shipped floor file to the fork's releases", () => {
		const floor = JSON.parse(read("../desktop-version-floor.json")) as { downloadUrl?: string };
		expect(floor.downloadUrl).toBe(`https://github.com/${FORK}/releases/latest`);
	});
});
