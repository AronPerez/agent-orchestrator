import { expect, type Page } from "@playwright/test";

export async function openSwitchAgentMenu(page: Page) {
	await page.getByRole("button", { name: "Session actions", exact: true }).click();
	const switchAgent = page.getByRole("menuitem", { name: "Switch agent", exact: true });
	await expect(switchAgent).toBeVisible();
	await switchAgent.click();
	const menu = page.getByRole("menu", { name: "Switch agent", exact: true });
	await expect(menu).toBeVisible();
	return menu;
}
