import { expect, test } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

test.use({
	userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/130.0.0.0 Safari/537.36",
	viewport: { width: 1280, height: 800 },
});

for (const theme of ["light", "dark"]) {
	for (const fullscreen of [false, true]) {
		test(`history controls stay visible in ${theme}, fullscreen=${fullscreen} @T0`, async ({ page }) => {
			await page.emulateMedia({ reducedMotion: "reduce" });
			await installFakeAgent(page, {
				platform: "MacIntel",
				projectId: "chrome-project",
				projectName: "A long project name that should never obscure navigation",
			});
			await page.addInitScript(({ theme, fullscreen }) => {
				localStorage.setItem("ao.theme", theme);
				localStorage.setItem("ao-sidebar-w", "200");
				window.ao!.window.isFullScreen = async () => fullscreen;
			}, { theme, fullscreen });
			await page.goto("/#/projects/chrome-project");
			const nav = page.locator('[data-slot="titlebar-nav"]');
			await expect(nav).toBeVisible();
			await expect(nav.locator('[data-slot="titlebar-brand"], [data-sidebar-brand], img')).toHaveCount(0);
			await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
			const initial = (await nav.boundingBox())!;
			expect(initial.x).toBe(fullscreen ? 8 : 84);
			expect(initial.y + initial.height / 2).toBe(fullscreen ? 18 : 25);

			// Both the expanded 200px sidebar and the off-canvas shell must leave
			// room for the cluster, including at a narrow desktop width.
			for (const width of [1280, 900]) {
				await page.setViewportSize({ width, height: 800 });
				for (const toggle of ["Collapse sidebar", "Expand sidebar"]) {
					for (const name of ["Go back", "Go forward"]) {
						const arrow = nav.getByRole("button", { name, exact: true });
						await expect(arrow).toBeVisible();
						await expect(arrow).toBeDisabled();
						await expect(arrow).toBeInViewport();
						expect(Number(await arrow.evaluate((el) => getComputedStyle(el).opacity))).toBeGreaterThan(0);
						expect(await arrow.evaluate((el) => {
							const box = el.getBoundingClientRect();
							// Disabled buttons pass hit testing to their tooltip wrapper.
							return el.parentElement!.contains(document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2));
						})).toBe(true);
					}
					const box = (await nav.boundingBox())!;
					expect(box).toEqual(initial);
					const lead = page.locator(".center-panel-titlebar > div").first();
					await expect.poll(async () => (await lead.boundingBox())!.x).toBeGreaterThanOrEqual(box.x + box.width);
					await nav.getByRole("button", { name: toggle, exact: true }).click();
				}
			}
		});
	}
}

test("navigation stays clear of the title at 50% native zoom @T0", async ({ page }) => {
	await installFakeAgent(page, { platform: "MacIntel", projectId: "zoom-project" });
	await page.addInitScript(() => {
		localStorage.setItem("ao-sidebar-w", "200");
		window.ao!.window.getZoomFactor = async () => 0.5;
	});
	await page.goto("/#/projects/zoom-project");
	const nav = page.locator('[data-slot="titlebar-nav"]');
	await expect(nav).toHaveCSS("left", "168px");
	const box = (await nav.boundingBox())!;
	const lead = page.locator(".center-panel-titlebar > div").first();
	await expect.poll(async () => (await lead.boundingBox())!.x).toBeGreaterThanOrEqual(box.x + box.width + 12);
	await nav.getByRole("button", { name: "Collapse sidebar", exact: true }).click();
	await expect.poll(async () => (await lead.boundingBox())!.x).toBeGreaterThanOrEqual(box.x + box.width + 12);
});
