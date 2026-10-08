import { expect, test } from "@playwright/test";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { homedir } from "node:os";
import { join } from "node:path";
import { installFakeAgent } from "./support/fake-bridge";
import type { SessionArtifact } from "../src/renderer/types/workspace";

// Renderer/CSP coverage with disposable AO artifact files, not workspace docs.
// Native Browser navigation is asserted at the bridge; this is not Electron QA.
let fixtureDir: string;
const origin = "http://ao-preview-artifact.onxxe3df.localhost:8080";
const names = [
  "report.md",
  "report.html",
  "run.log",
  "missing.txt",
  "archive.zip",
];
const artifacts: SessionArtifact[] = names.map((name) => ({
  name,
  path: name,
  kind: name.endsWith(".md")
    ? "markdown"
    : name.endsWith(".html")
      ? "html"
      : "file",
  size: 120,
  updatedAt: "2026-10-08T00:00:00Z",
  rawUrl: `${origin}/${name}?raw=true`,
  ...(name.endsWith(".html") ? { previewUrl: `${origin}/${name}` } : {}),
}));

test.beforeAll(async () => {
  const root = join(homedir(), ".ao", "data", "artifacts");
  await mkdir(root, { recursive: true });
  fixtureDir = await mkdtemp(join(root, "s03-test-"));
  await writeFile(
    join(fixtureDir, "report.md"),
    "# Delivery report\n\nA disposable **session artifact**, outside the worktree.\n\n| Check | Result |\n| --- | --- |\n| Local artifact URLs | Passed |\n| Workspace isolation | Passed |\n\n![Delivery chart](./chart.svg)\n\n## Notes\n\nFiles are read-only. HTML opens in Browser.\n",
  );
  await writeFile(
    join(fixtureDir, "report.html"),
    "<!doctype html><title>Artifact preview</title><h1>Delivery report</h1>",
  );
  await writeFile(
    join(fixtureDir, "run.log"),
    "Verification completed. No workspace files were read.",
  );
  await writeFile(join(fixtureDir, "archive.zip"), new Uint8Array([0, 1, 2]));
  await writeFile(
    join(fixtureDir, "chart.svg"),
    '<svg xmlns="http://www.w3.org/2000/svg" width="240" height="50"><rect width="240" height="50" fill="#222"/><text x="16" y="31" fill="white">Disposable artifact asset</text></svg>',
  );
});
test.afterAll(async () => {
  if (fixtureDir) await rm(fixtureDir, { recursive: true, force: true });
});

for (const theme of ["dark", "light"] as const) {
  test(`local artifacts: Markdown, text, missing, binary and HTML in ${theme} @P0`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 860 });
    await page.emulateMedia({ colorScheme: theme, reducedMotion: "reduce" });
    await installFakeAgent(page, {
      projectName: "Artifact delivery verification",
      workers: [
        {
          id: "artifact-session",
          title: "Review delivered reports without searching the worktree",
          artifactFiles: artifacts,
          status: theme === "light" ? "merged" : "working",
          isTerminated: theme === "light",
          activity: theme === "light" ? "exited" : "active",
          previewUrl: "http://127.0.0.1:8080/previous-preview",
          previewRevision: 1,
        },
      ],
    });
    await page.addInitScript((theme) => {
      localStorage.setItem("ao.theme", theme);
    }, theme);
    const rawRequests: string[] = [];
    await page.route(`${origin}/**`, async (route) => {
      const url = new URL(route.request().url());
      if (url.searchParams.has("raw")) rawRequests.push(url.href);
      const name = url.pathname.slice(1);
      if (![...names, "chart.svg"].includes(name) || name === "missing.txt") {
        await route.fulfill({
          status: 404,
          body: "Missing",
          headers: { "access-control-allow-origin": "*" },
        });
        return;
      }
      await route.fulfill({
        body: await readFile(join(fixtureDir, name)),
        contentType: name.endsWith("svg") ? "image/svg+xml" : "text/plain",
        headers: { "access-control-allow-origin": "*" },
      });
    });
    await page.route("http://127.0.0.1:8080/api/**", (route) =>
      route.fulfill({
        status: 404,
        json: { error: { code: "NOT_FOUND", message: "Unused mock endpoint" } },
      }),
    );
    await page.route("**/api/v1/projects/fake-proj", (route) =>
      route.fulfill({ json: { project: { id: "fake-proj", config: {} } } }),
    );
    await page.route("**/api/v1/sessions/*/workspace/files", (route) =>
      route.fulfill({ json: { files: [], truncated: false } }),
    );
    await page.route("**/api/v1/sessions/*/workspace/tree*", (route) =>
      route.fulfill({ json: { entries: [], truncated: false } }),
    );
    await page.goto("/#/host/local/session/artifact-session");
    await page.getByRole("tab", { name: "Files", exact: true }).click();
    await page.getByRole("tab", { name: "Artifacts", exact: true }).click();
    if (process.env.S03_SCREENSHOT_DIR && theme === "dark") {
      await mkdir(process.env.S03_SCREENSHOT_DIR, { recursive: true });
      await page.screenshot({
        path: join(process.env.S03_SCREENSHOT_DIR, "s03-artifacts-list.png"),
      });
    }
    const report = page.getByRole("button", { name: "report.md", exact: true });
    await report.focus();
    await page.keyboard.press("Enter");
    await expect(
      page.getByRole("heading", { name: "Delivery report" }),
    ).toBeVisible();
    await expect(
      page.getByRole("img", { name: "Delivery chart" }),
    ).toHaveJSProperty("naturalWidth", 240);
    expect([...new Set(rawRequests)]).toEqual([`${origin}/report.md?raw=true`]);
    if (process.env.S03_SCREENSHOT_DIR) {
      await mkdir(process.env.S03_SCREENSHOT_DIR, { recursive: true });
      await page.screenshot({
        path: join(
          process.env.S03_SCREENSHOT_DIR,
          `s03-artifacts-${theme}.png`,
        ),
      });
    }
    await page
      .getByRole("button", { name: "Expand sidebar", exact: true })
      .first()
      .click();
    await page.setViewportSize({ width: 1024, height: 760 });
    await expect(
      page.getByRole("heading", { name: "Delivery report" }),
    ).toBeVisible();
    await page
      .getByRole("button", { name: "Collapse sidebar", exact: true })
      .first()
      .click();
    await page.setViewportSize({ width: 1280, height: 860 });
    await page.getByRole("button", { name: "Back to file tree" }).click();
    await page.getByRole("button", { name: "run.log", exact: true }).click();
    await expect(
      page.getByText("Verification completed. No workspace files were read."),
    ).toBeVisible();
    await page.getByRole("button", { name: "Back to file tree" }).click();
    await page
      .getByRole("button", { name: "missing.txt", exact: true })
      .click();
    await expect(
      page.getByText("Unable to load artifact (HTTP 404)."),
    ).toBeVisible();
    await page.getByRole("button", { name: "Back to file tree" }).click();
    await page
      .getByRole("button", { name: "archive.zip", exact: true })
      .click();
    await expect(
      page.getByText("Binary file preview is not available."),
    ).toBeVisible();
    await page.getByRole("button", { name: "Back to file tree" }).click();
    await page
      .getByRole("button", { name: "report.html", exact: true })
      .click();
    await page.getByRole("button", { name: "Open in Browser" }).click();
    await expect(
      page.getByRole("tab", { name: "Browser", exact: true }),
    ).toHaveAttribute("aria-selected", "true");
    await expect(page.getByPlaceholder("localhost:5173")).toHaveValue(
      new URL(origin).host,
    );
    await page.getByPlaceholder("localhost:5173").focus();
    await expect(page.getByPlaceholder("localhost:5173")).toHaveValue(
      `${origin}/report.html`,
    );
  });
}
