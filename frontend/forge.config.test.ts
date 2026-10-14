import { afterEach, describe, expect, it, vi } from "vitest";
import fs from "node:fs";
import type { ForgeHookFn, ResolvedForgeConfig } from "@electron-forge/shared-types";
import config, { extraResourcesForPlatform } from "./forge.config";

describe("native runtime resources", () => {
	it.each(["darwin", "linux"] as const)("bundles tmux on %s", (platform) => {
		expect(extraResourcesForPlatform(platform)).toContain("tmux");
	});

	it("does not bundle tmux on Windows", () => {
		expect(extraResourcesForPlatform("win32")).not.toContain("tmux");
	});
});

describe("packaged authentication callback registration", () => {
	it("declares ao-app in the macOS bundle and Linux package metadata", () => {
		expect(config.packagerConfig?.protocols).toEqual([
			{
				name: "Agent Orchestrator authentication callback",
				schemes: ["ao-app"],
			},
		]);

		const makers = config.makers as Array<{
			name?: string;
			config?: { options?: { mimeType?: string[] } };
		}>;
		for (const name of [
			"@electron-forge/maker-deb",
			"@electron-forge/maker-rpm",
		]) {
			const maker = makers.find((candidate) => candidate.name === name);
			expect(maker?.config?.options?.mimeType).toEqual([
				"x-scheme-handler/ao-app",
			]);
		}
	});
});

describe("desktop release feed", () => {
	afterEach(() => {
		vi.unstubAllEnvs();
		vi.restoreAllMocks();
	});

	it.each([
		[undefined, "AronPerez", "agent-orchestrator"],
		["", "AronPerez", "agent-orchestrator"],
		["invalid", "AronPerez", "agent-orchestrator"],
		["custom-owner/custom-repo", "custom-owner", "custom-repo"],
	])("uses AO_RELEASE_REPO=%s for the updater and publisher", async (repo, owner, name) => {
		vi.stubEnv("AO_RELEASE_REPO", repo);
		vi.resetModules();
		const writeFile = vi.spyOn(fs, "writeFileSync").mockImplementation(() => {});
		const { default: releaseConfig } = await import("./forge.config");
		const prePackage = releaseConfig.hooks!.prePackage as ForgeHookFn<"prePackage">;
		await prePackage(releaseConfig as ResolvedForgeConfig, "darwin", "arm64");

		expect(writeFile).toHaveBeenCalledWith("app-update.yml", [
			"provider: github",
			`owner: ${owner}`,
			`repo: ${name}`,
			"updaterCacheDirName: agent-orchestrator-updater",
			"",
		].join("\n"));
		expect(releaseConfig.publishers).toContainEqual(expect.objectContaining({
			name: "@electron-forge/publisher-github",
			config: expect.objectContaining({ repository: { owner, name } }),
		}));
	});
});
