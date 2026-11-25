import { expect, test } from "@playwright/test";
import { installFakeBridge } from "./support/fake-bridge";

// Renderer evidence with synthetic data, not proof of live fleet relationships.
test("project fleet shows hierarchy, work and recorded communication @T0", async ({ page }, testInfo) => {
	await page.setViewportSize({ width: 1280, height: 900 });
	await page.emulateMedia({ reducedMotion: "reduce", colorScheme: "dark" });
	await installFakeBridge(page);
	await page.goto("/#/projects/ao-demo");
	await page.getByRole("button", { name: "Fleet", exact: true }).click();
	await expect(page.getByRole("region", { name: "Session fleet" })).toBeVisible();
	await expect(page.getByText("Preview data · Synthetic sessions, not the live fleet.")).toBeVisible();
	// The project tab is the shared fleet tree, scoped to this project and starting at its machine.
	await expect(page.getByRole("treeitem", { name: /^This computer/ })).toHaveAttribute("aria-level", "1");
	await page.getByRole("treeitem", { name: /^Build screenshot-ready dashboard data, Manager/ }).click();
	const details = page.getByRole("group", { name: "Selected session" });
	await expect(details.getByText(/Worker PRs are in review/)).toBeVisible();
	await expect(details.getByText(/Recorded or acknowledged does not mean acted on/)).toBeVisible();
	await page.screenshot({ path: testInfo.outputPath("fleet-dark.png"), fullPage: true });
	await page.getByRole("button", { name: "Collapse Build screenshot-ready dashboard data" }).click();
	await expect(page.getByRole("treeitem", { name: /^Resolve reviewer feedback on terminal polish, Worker/ })).toHaveCount(0);
	await page.getByRole("button", { name: "Expand Build screenshot-ready dashboard data" }).click();
	await page.getByRole("treeitem", { name: /^Resolve reviewer feedback on terminal polish, Worker/ }).click();
	// Picking another session moves the one details block under it.
	await expect(page.getByRole("group", { name: "Selected session" })).toHaveCount(1);
	await expect(details.getByText(/CI:/)).toBeVisible();
	await page.setViewportSize({ width: 640, height: 900 });
	await page.emulateMedia({ colorScheme: "light" });
	await expect(page.getByRole("button", { name: "Open session" })).toBeVisible();
	await page.screenshot({ path: testInfo.outputPath("fleet-narrow.png"), fullPage: true });
	await page.getByRole("button", { name: "Open session" }).click();
	await expect(page).toHaveURL(/sessions\/demo-needs-input/);
});
