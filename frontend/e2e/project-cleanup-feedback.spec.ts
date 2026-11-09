import { expect, test } from "@playwright/test";
import { installFakeAgent } from "./support/fake-bridge";

test("project cleanup feedback stays readable with many sessions @P0", async ({ page }) => {
	await installFakeAgent(page, {
		projectId: "cleanup-project",
		projectName: "Mini",
		workers: Array.from({ length: 24 }, (_, index) => ({
			id: `cleanup-${index}`,
			title: `Archived session ${index + 1}`,
			status: "terminated",
			activity: "exited",
			isTerminated: true,
			workspaceCleanup: index < 5 ? "preserved_dirty" : index < 12 ? "pending" : "failed",
		})),
	});
	await page.goto("/#/projects/cleanup-project");
	const feedback = page.getByLabel("Session termination status");
	await expect(feedback.getByRole("alert")).toHaveCount(17);
	await expect(feedback.getByRole("status")).toHaveCount(7);
	const error = feedback.getByRole("alert").first();
	await expect.poll(async () => (await error.boundingBox())?.width ?? 0).toBeGreaterThan(100);
	const pending = feedback.getByRole("status").first();
	await expect.poll(async () => (await pending.boundingBox())?.width ?? 0).toBeGreaterThan(100);
	await expect(error).toHaveAttribute("title", /Archived session 1: Worktree has uncommitted changes/);

	await feedback.focus();
	await expect(feedback).toBeFocused();
	await page.keyboard.press("ArrowRight");
	await expect.poll(() => feedback.evaluate((element) => element.scrollLeft)).toBeGreaterThan(0);
	await feedback.getByRole("alert").last().scrollIntoViewIfNeeded();
	await expect(feedback.getByRole("alert").last()).toBeInViewport();
});
