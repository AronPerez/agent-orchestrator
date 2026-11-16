import { expect, test } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

test.use({
	userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/130.0.0.0 Safari/537.36",
});

test("titlebar arrows traverse home history and discard forward entries on push @T0", async ({ page }) => {
	await installFakeAgent(page, { platform: "MacIntel", projectId: "history-project", projectName: "History project" });
	await page.goto("/");
	const nav = page.locator('[data-slot="titlebar-nav"]');
	const back = nav.getByRole("button", { name: "Go back", exact: true });
	const forward = nav.getByRole("button", { name: "Go forward", exact: true });
	await expect(back).toBeVisible();
	await expect(forward).toBeVisible();
	await expect(back).toBeDisabled();
	await expect(forward).toBeDisabled();

	await page.locator('[data-sidebar="menu-button"]').filter({ hasText: "History project" }).first().click();
	await expect(page).toHaveURL(/projects\/history-project$/);
	await expect(forward).toBeDisabled();
	await expect(back).toBeEnabled();

	await back.focus();
	await expect(back).toBeFocused();
	await page.keyboard.press("Enter");
	await expect(page).not.toHaveURL(/projects\/history-project/);
	await expect(back).toBeDisabled();
	await expect(forward).toBeEnabled();
	await forward.click();
	await expect(page).toHaveURL(/projects\/history-project$/);
	await expect(forward).toBeDisabled();

	await back.click();
	await expect(forward).toBeEnabled();
	await page.getByRole("button", { name: "Automations", exact: true }).click();
	await expect(page).toHaveURL(/automations$/);
	await expect(forward).toBeDisabled();
});
