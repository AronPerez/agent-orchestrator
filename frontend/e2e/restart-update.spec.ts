import { expect, test, type Page } from "@playwright/test";
import type { AoBridge } from "../src/preload";
import type { UpdateStatus } from "../src/main/update-settings";
import { installFakeBridge } from "./support/fake-bridge";

const activeChat = {
	id: "local-chat", mode: "chat", isTerminated: false,
	chatProviderPreserved: false, activity: { state: "active" },
};

async function setup(page: Page, feature = false) {
	await installFakeBridge(page, {
		updateStatus: { state: "downloaded", version: "0.13.1-test" },
		updateSettings: { enabled: false, channel: "latest", nightlyAck: false, feature: feature ? { pr: 123 } : null },
	});
	await page.route("http://127.0.0.1:8080/**", (route) => route.abort());
	await page.route("**/api/v1/sessions?active=true", (route) => route.fulfill({ json: { sessions: [activeChat] } }));
	await page.goto("/");
	await expect(page.getByRole("button", { name: /Restart to install update/ }).filter({ visible: true })).toBeVisible({ timeout: 15_000 });
	await page.evaluate(() => {
		const updates = (window as unknown as { ao: AoBridge }).ao.updates;
		const listeners = new Set<(status: UpdateStatus) => void>();
		document.body.dataset.installCalls = "0";
		updates.install = async () => { document.body.dataset.installCalls = String(Number(document.body.dataset.installCalls) + 1); };
		updates.onStatus = (listener) => { listeners.add(listener); return () => { listeners.delete(listener); }; };
		updates.returnHome = async (requestId) => {
			for (const listener of listeners) listener({ state: "downloaded", requestId, version: "0.13.1-test" });
		};
	});
}

for (const entry of ["sidebar", "compact sidebar", "settings", "feature auto-progress"] as const) {
	test(`@P0 restart update guard: ${entry}`, async ({ page }, testInfo) => {
		await page.setViewportSize({ width: 1100, height: 760 });
		await page.emulateMedia({ colorScheme: "dark", reducedMotion: "reduce" });
		await setup(page, entry === "feature auto-progress");
		if (entry === "compact sidebar") {
			await page.evaluate(() => { window.location.hash = "/host/local/session/demo-working"; });
			await page.setViewportSize({ width: 1000, height: 760 });
			await expect(page.locator('[data-slot="sidebar"]')).toHaveAttribute("data-state", "collapsed");
		}
		if (entry === "settings" || entry === "feature auto-progress") {
			await page.evaluate(() => { window.location.hash = "/settings"; });
			await page.getByRole("button", { name: "Updates", exact: true }).click();
			await page.getByRole("button", { name: entry === "settings" ? "Restart & install" : "Return to Stable", exact: true }).click();
		} else {
			await page.getByRole("button", { name: /Restart to install update/ }).filter({ visible: true }).click();
		}
		const dialog = page.getByRole("dialog", { name: "Restart to update" });
		await expect(dialog).toContainText("1 local Chat session may lose its current turn");
		await expect(dialog).toContainText("Remote sessions are not affected");
		await expect(page.locator("body")).toHaveAttribute("data-install-calls", "0");
		if (entry === "settings") {
			await page.screenshot({ path: testInfo.outputPath("restart-update-dark.png") });
		}
		await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
		await expect(dialog).not.toBeVisible();
		await expect(page.locator("body")).toHaveAttribute("data-install-calls", "0");
		if (entry !== "feature auto-progress") {
			const restart = entry === "settings"
				? page.getByRole("button", { name: "Restart & install", exact: true })
				: page.getByRole("button", { name: /Restart to install update/ }).filter({ visible: true });
			await restart.click();
			await dialog.getByRole("button", { name: "Install and restart", exact: true }).click();
			await expect(page.locator("body")).toHaveAttribute("data-install-calls", "1");
		}
	});
}

test("@P0 unknown local state uses a conservative keyboard-dismissable warning", async ({ page }, testInfo) => {
	await page.setViewportSize({ width: 1100, height: 680 });
	await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
	await setup(page);
	await page.route("**/api/v1/sessions?active=true", (route) => route.fulfill({ status: 503, json: { error: "Unavailable" } }));
	await page.getByRole("button", { name: /Restart to install update/ }).filter({ visible: true }).click();
	const dialog = page.getByRole("dialog", { name: "Restart to update" });
	await expect(dialog).toContainText("Current local session state could not be confirmed");
	await expect(dialog).toContainText("Remote sessions are not affected");
	await page.setViewportSize({ width: 740, height: 680 });
	await page.screenshot({ path: testInfo.outputPath("restart-update-light-unknown.png") });
	await page.keyboard.press("Tab");
	await expect(dialog.getByRole("button", { name: "Cancel", exact: true })).toBeFocused();
	await page.keyboard.press("Escape");
	await expect(dialog).not.toBeVisible();
	await expect(page.locator("body")).toHaveAttribute("data-install-calls", "0");
	// The narrow layout removes the original opener. Reopen at desktop width
	// to verify focus restoration while the initiating control is still mounted.
	await page.setViewportSize({ width: 1100, height: 680 });
	const restart = page.getByRole("button", { name: /Restart to install update/ }).filter({ visible: true });
	await restart.click();
	await expect(dialog).toBeVisible();
	await page.keyboard.press("Escape");
	await expect(restart).toBeFocused();
});
