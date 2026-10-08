import { expect, type Locator, type Page, test } from "@playwright/test";
import { agentReadiness } from "../src/renderer/test/agent-readiness-fixtures";
import { installFakeAgent } from "./support/fake-bridge";
import { openSwitchAgentMenu } from "./support/open-switch-agent-menu";

const projectId = "switch-agent-dialog";

for (const colorScheme of ["dark", "light"] as const) {
	test(`renderer: switch-agent submenu is keyboard accessible in ${colorScheme} theme @T0`, async ({ page }) => {
		await page.setViewportSize({ width: 960, height: 720 });
		await page.emulateMedia({ colorScheme, reducedMotion: "reduce" });
		await installFakeAgent(page, {
			projectId,
			projectName: "Long project name for session agent switching",
			workers: [{ id: "switch-worker", provider: "claude-code", title: "Verify same-session agent switching without losing work" }],
		});
		await page.route("http://127.0.0.1:8080/api/v1/**", (route) => route.fulfill({
			json: { status: "ok", agents: [agentReadiness("claude-code", "Claude Code"), agentReadiness("codex", "Codex")] },
		}));
		await page.goto("/#/host/local/session/switch-worker");
		const trigger = page.getByRole("button", { name: "Session actions", exact: true });
		await trigger.focus();
		await page.keyboard.press("Enter");
		await expect(page.getByRole("menuitem", { name: "Switch agent", exact: true })).toBeFocused();
		await page.keyboard.press("ArrowRight");
		const submenu = page.getByRole("menu", { name: "Switch agent", exact: true });
		await expect(submenu.getByRole("menuitem", { name: "Claude Code Current" })).toBeDisabled();
		await expect(submenu.getByRole("menuitem", { name: "Codex", exact: true })).toBeFocused();
		const bounds = await submenu.boundingBox();
		expect(bounds).not.toBeNull();
		expect(bounds!.x).toBeGreaterThanOrEqual(0);
		expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(960);
		if (process.env.AO_SWITCH_MENU_SCREENSHOT_DIR) {
			await page.screenshot({ path: `${process.env.AO_SWITCH_MENU_SCREENSHOT_DIR}/switch-agent-${colorScheme}.png` });
		}
		await page.keyboard.press("Escape");
		await expect(submenu).not.toBeVisible();
		await expect(trigger).toBeFocused();
	});
}

async function setupSwitchAgentDialogTest(page: Page): Promise<{
	dialog: Locator;
	terminalPanel: Locator;
}> {
	await page.emulateMedia({ reducedMotion: "reduce" });
	await installFakeAgent(page, {
		projectId,
		projectName: projectId,
		workers: [{ id: "switch-worker", provider: "claude-code", title: "Switch worker" }],
	});
	await page.route("http://127.0.0.1:8080/api/v1/**", async (route) => {
		const pathname = new URL(route.request().url()).pathname;
		if (pathname === "/api/v1/sessions/switch-worker/switch-agent") {
			await route.fulfill({ status: 409, json: { message: "Codex is not authorized" } });
			return;
		}
		if (pathname === "/api/v1/agents/readiness" || pathname === "/api/v1/agents/readiness/ensure") {
			await route.fulfill({
				json: {
					agents: [agentReadiness("claude-code", "Claude Code"), agentReadiness("codex", "Codex")],
				},
			});
			return;
		}
		if (pathname === `/api/v1/projects/${projectId}`) {
			await route.fulfill({
				json: {
					status: "ok",
					project: {
						id: projectId,
						agent: "claude-code",
						config: { worker: { agent: "claude-code" } },
					},
				},
			});
			return;
		}
		if (pathname === "/api/v1/agents/codex/models") {
			await route.fulfill({
				json: {
					agentId: "codex",
					allowCustom: false,
					fetchedAt: "2026-08-15T00:00:00Z",
					models: [{ id: "gpt-5.4", label: "GPT-5.4", isDefault: true }],
					selectionMode: "catalog",
					source: "test",
					stale: false,
				},
			});
			return;
		}
		await route.fulfill({ json: { status: "ok" } });
	});

	await page.goto("/#/host/local/session/switch-worker");
	const primaryTerminalTab = page.locator('[data-terminal-role="primary"]');
	const primaryTabBox = await primaryTerminalTab.boundingBox();
	const terminalRegionBox = await page.getByTestId("session-terminal-region").boundingBox();
	expect(primaryTabBox).not.toBeNull();
	expect(terminalRegionBox).not.toBeNull();
	expect(primaryTabBox!.x + primaryTabBox!.width).toBeLessThanOrEqual(
		terminalRegionBox!.x + terminalRegionBox!.width,
	);
	const menu = await openSwitchAgentMenu(page);
	await expect(menu.getByRole("menuitem", { name: "Claude Code Current" })).toBeDisabled();
	await menu.getByRole("menuitem", { name: "Codex", exact: true }).click();
	const dialog = page.getByRole("dialog", { name: "Switch agent" });
	await expect(dialog).toBeVisible();
	await expect(dialog.getByRole("alert")).toHaveText("Codex is not authorized");
	return {
		dialog,
		terminalPanel: page.getByRole("tabpanel", { name: "Switch worker terminal" }),
	};
}

test("renderer: switch-agent selector remains compact inside a wide terminal @T0", async ({ page }) => {
	const { dialog, terminalPanel } = await setupSwitchAgentDialogTest(page);
	await expect(dialog).toHaveCSS("width", "420px");
	await expect
		.poll(async () => (await terminalPanel.boundingBox())?.width ?? 0)
		.toBeGreaterThan(420);
});

test("renderer: switch-agent selector stays inside a narrow terminal @T0", async ({ page }) => {
	await page.setViewportSize({ width: 960, height: 720 });
	const { dialog, terminalPanel } = await setupSwitchAgentDialogTest(page);
	const dialogBox = await dialog.boundingBox();
	const terminalBox = await terminalPanel.boundingBox();

	expect(dialogBox).not.toBeNull();
	expect(terminalBox).not.toBeNull();
	expect(dialogBox!.x).toBeGreaterThanOrEqual(terminalBox!.x + 16);
	expect(dialogBox!.x + dialogBox!.width).toBeLessThanOrEqual(
		terminalBox!.x + terminalBox!.width - 16,
	);
});
