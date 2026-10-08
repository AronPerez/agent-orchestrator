import { expect, test } from "@playwright/test";
import { codexAccountsFixture } from "../src/renderer/test/codex-accounts-fixture";
import { installFakeBridge } from "./support/fake-bridge";

for (const theme of ["dark", "light"] as const) {
	test(`Accounts overview is local, keyboard accessible and retains stale rows (${theme}) @P0`, async ({ page }, testInfo) => {
		await page.setViewportSize({ width: theme === "dark" ? 1200 : 860, height: 820 });
		await page.emulateMedia({ colorScheme: theme, reducedMotion: "reduce" });
		await installFakeBridge(page);
		// No real daemon or account credentials are involved in this renderer test.
		await page.route("http://127.0.0.1:8080/**", (route) => route.fulfill({ status: 503, json: {} }));
		const data = codexAccountsFixture();
		data.accounts.push({
			...data.accounts[0], id: "work", active: false,
			label: "Work account for the infrastructure and developer experience team",
			accountEmail: "infrastructure-team@example.test",
			authentication: { ...data.accounts[0].authentication, state: "unknown", freshness: "stale" },
			capacity: { ...data.accounts[0].capacity, state: "unknown", remainingPercent: null, freshness: "checking", plan: null },
		});
		let status = 200;
		let release!: () => void;
		const firstResponse = new Promise<void>((resolve) => { release = resolve; });
		const requests: string[] = [];
		await page.route("**/api/v1/agents/codex/accounts**", async (route) => {
			requests.push(`${route.request().method()} ${route.request().url()}`);
			await firstResponse;
			return route.fulfill({ status, json: status === 200 ? data : { message: "private diagnostic" } });
		});
		await page.goto("/#/settings");
		const nav = page.getByRole("button", { name: "Accounts", exact: true });
		await nav.focus();
		await page.keyboard.press("Enter");
		await expect(nav).toHaveAttribute("aria-current", "page");
		await expect(page.getByText("Loading Codex accounts…")).toBeVisible();
		release();
		await expect(page.getByText("engineer@example.test")).toBeVisible();
		await expect(page.getByText("In use", { exact: true })).toBeVisible();
		await expect(page.getByText("Authentication unknown", { exact: false })).toBeVisible();
		const content = page.locator('[data-section="accounts"]');
		expect(await content.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
		await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
		const dialog = await page.getByRole("dialog").boundingBox();
		expect(dialog!.x).toBeGreaterThanOrEqual(0);
		expect(dialog!.x + dialog!.width).toBeLessThanOrEqual(page.viewportSize()!.width);
		await page.screenshot({ path: testInfo.outputPath(`accounts-${theme}.png`) });

		status = 503;
		await page.getByRole("button", { name: "Refresh", exact: true }).click();
		await expect(page.getByRole("alert")).toContainText("Showing previously loaded accounts");
		await expect(page.getByText("engineer@example.test")).toBeVisible();
		await expect(content).not.toContainText("private diagnostic");
		await page.screenshot({ path: testInfo.outputPath(`accounts-${theme}-stale.png`) });
		expect(requests.length).toBeGreaterThanOrEqual(2);
		expect(requests.every((request) => request === "GET http://127.0.0.1:8080/api/v1/agents/codex/accounts")).toBe(true);

		status = 200;
		data.accounts = [];
		await page.getByRole("button", { name: "Refresh", exact: true }).click();
		await expect(page.getByText("No Codex accounts found.")).toBeVisible();
		await expect(page.getByRole("alert")).toBeHidden();
		status = 401;
		await page.getByRole("button", { name: "Refresh", exact: true }).click();
		await expect(page.getByRole("alert")).toContainText("Access to local Codex accounts was denied.");

		await page.keyboard.press("Escape");
		await expect(page.getByRole("dialog")).toBeHidden();
	});
}
