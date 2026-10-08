import { expect, test } from "@playwright/test";
import { agentReadiness } from "../src/renderer/test/agent-readiness-fixtures";
import { installFakeTerminalMux } from "./support/fake-terminal-mux";
import { installFakeAgent } from "./support/fake-bridge";

// Renderer evidence only: local uses the snapshot seam; remote uses mocked DTOs.
test("standalone agents open by host without project controls @T0", async ({
	page,
}, testInfo) => {
	test.setTimeout(90_000);
	await page.setViewportSize({ width: 1360, height: 900 });
	await page.emulateMedia({ reducedMotion: "reduce", colorScheme: "dark" });
	await installFakeAgent(page, {
		standalone: true,
		projectId: "@standalone",
		projectName: "Standalone agents",
		workers: [
			{ id: "same", title: "Local research notes", mode: "chat" },
			{
				id: "terminal",
				title: "Inspect a long-running standalone task without a repository",
				mode: "tui",
			},
		],
	});
	await installFakeTerminalMux(page, {
		"terminal/terminal_0": "Standalone terminal fixture\r\n",
		"remote-terminal": "Remote terminal fixture\r\n",
	});
	const projectRequests: string[] = [];
	await page.route("**/api/v1/**", async (route) => {
		const url = new URL(route.request().url());
		const path = url.pathname;
		if (
			path.endsWith("/agents/readiness") ||
			path.endsWith("/agents/readiness/ensure")
		)
			return route.fulfill({
				json: { agents: [agentReadiness("codex", "Codex")] },
			});
		if (path.includes("/projects/") || url.searchParams.has("projectId"))
			projectRequests.push(route.request().url());
		if (path.endsWith("/projects"))
			return route.fulfill({ json: { projects: [] } });
		if (path.endsWith("/sessions"))
			return route.fulfill({
				json: {
					sessions:
						url.searchParams.get("active") === "false"
							? []
							: [
									{
										id: "same",
										displayName: "Remote investigation",
										harness: "codex",
										kind: "worker",
										mode: "tui",
										terminalHandleId: "remote-terminal",
										status: "working",
										isTerminated: false,
										activity: { state: "idle" },
										updatedAt: "2026-10-08T12:00:00Z",
									},
								],
				},
			});
		if (path.endsWith("/conversation"))
			return route.fulfill({
				json: {
					conversationId: "local-chat",
					sessionId: "same",
					harness: "codex",
					mode: "chat",
					controller: "ready",
					latestSequence: 0,
					oldestSequence: 0,
					hasMoreBefore: false,
					turns: [],
					messages: [],
					activities: [],
					settings: {},
				},
			});
		if (path.endsWith("/conversation/models"))
			return route.fulfill({ json: { models: [], selected: {} } });
		if (path.endsWith("/conversation/skills"))
			return route.fulfill({ json: { skills: [] } });
		if (path.endsWith("/workspace/files"))
			return route.fulfill({ json: { files: [], truncated: false } });
		return route.fulfill({ json: { status: "ok" } });
	});
	await page.goto("/#/host/local/session/same");
	await expect(
		page.getByRole("region", { name: "Chat", exact: true }),
	).toBeVisible({ timeout: 45_000 });
	await expect(
		page.getByRole("button", { name: "Standalone agents", exact: true }),
	).toBeVisible();
	await expect(
		page.getByRole("button", { name: "Open orchestrator", exact: true }),
	).toHaveCount(0);
	await page.screenshot({ path: testInfo.outputPath("standalone-dark.png") });

	await page
		.getByRole("button", {
			name: "Open Inspect a long-running standalone task without a repository",
			exact: true,
		})
		.focus();
	await page.keyboard.press("Enter");
	await expect(page).toHaveURL(/host\/local\/session\/terminal/);
	await expect
		.poll(() =>
			page.evaluate(
				() =>
					window.__aoFakeTerminalMux!.stats().opens["terminal/terminal_0"] ?? 0,
			),
		)
		.toBeGreaterThan(0);
	await expect(
		page.getByRole("region", { name: "Chat", exact: true }),
	).toHaveCount(0);

	await page.evaluate(async () => {
		const modulePath = "/src/renderer/lib/host-clients.ts";
		const { registerHostBase } = await import(modulePath);
		registerHostBase("remote", `${location.origin}/remote`, "Workbox");
	});
	await page
		.getByRole("button", { name: "Open Remote investigation", exact: true })
		.click();
	await expect(page).toHaveURL(/host\/remote\/session\/same/);
	await expect
		.poll(() =>
			page.evaluate(
				() => window.__aoFakeTerminalMux!.stats().opens["remote-terminal"] ?? 0,
			),
		)
		.toBeGreaterThan(0);
	await expect(
		page.getByRole("region", { name: "Chat", exact: true }),
	).toHaveCount(0);
	await expect(
		page.getByRole("button", { name: "Open orchestrator", exact: true }),
	).toHaveCount(0);
	await page.emulateMedia({ colorScheme: "light" });
	await expect(page.getByTestId("terminal-replay-cover")).toHaveCount(0);
	await page.screenshot({
		path: testInfo.outputPath("standalone-light-remote.png"),
	});
	await page.setViewportSize({ width: 1100, height: 800 });
	const localGroup = page.getByRole("button", {
		name: "Standalone agents",
		exact: true,
	});
	await expect(localGroup).toHaveAttribute("aria-expanded", "false");
	await localGroup.click();
	await page
		.getByRole("button", { name: "Open Local research notes", exact: true })
		.click();
	await expect(page).toHaveURL(/host\/local\/session\/same/);
	await expect(
		page.getByRole("region", { name: "Chat", exact: true }),
	).toBeVisible();
	expect(projectRequests).toEqual([]);
});
