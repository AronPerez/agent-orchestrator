import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { SessionArtifact } from "../types/workspace";
import { SessionArtifactExplorer } from "./SessionArtifactExplorer";

const session = { host: "local", id: "sess-1" };
const rawUrl =
  "http://ao-preview-artifact.onxxe3df.localhost:3000/reports/report.md?raw=true";
const artifact: SessionArtifact = {
  path: "reports/report.md",
  name: "report.md",
  kind: "markdown",
  size: 42,
  updatedAt: "2026-10-08T00:00:00Z",
  rawUrl,
};
const fetchMock = vi.fn();
afterEach(() => {
  vi.unstubAllGlobals();
  fetchMock.mockReset();
});
function setup(files: SessionArtifact[] = [artifact], host = "local") {
  vi.stubGlobal("fetch", fetchMock);
  const onOpenPreview = vi.fn();
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const props = {
    session: { ...session, host },
    artifacts: files,
    filter: "",
    onOpenPreview,
  };
  const ui = (next = props) => (
    <QueryClientProvider client={client}>
      <SessionArtifactExplorer {...next} />
    </QueryClientProvider>
  );
  return { ...render(ui()), onOpenPreview, client, props, ui };
}
function select(name = "reports/report.md") {
  fireEvent.click(screen.getByRole("button", { name }));
}

describe("SessionArtifactExplorer", () => {
  it("loads Markdown from rawUrl and resolves images only against the artifact origin", async () => {
    fetchMock.mockResolvedValue(
      new Response("# Delivered report\n![Chart](./chart.png)"),
    );
    setup();
    select();
    expect(
      await screen.findByRole("heading", { name: "Delivered report" }),
    ).toBeVisible();
    expect(fetchMock).toHaveBeenCalledWith(
      rawUrl,
      expect.objectContaining({
        credentials: "omit",
        signal: expect.any(AbortSignal),
      }),
    );
    expect(screen.getByRole("img", { name: "Chart" })).toHaveAttribute(
      "src",
      "http://ao-preview-artifact.onxxe3df.localhost:3000/reports/chart.png?v=1791417600000",
    );
  });

  it("opens HTML with previewUrl without fetching or injecting raw HTML", () => {
    const html = {
      ...artifact,
      path: "report.html",
      name: "report.html",
      kind: "html" as const,
      previewUrl: rawUrl.replace("report.md?raw=true", "report.html"),
    };
    const { onOpenPreview } = setup([html]);
    select("report.html");
    fireEvent.click(screen.getByRole("button", { name: "Open in Browser" }));
    expect(onOpenPreview).toHaveBeenCalledWith(html.previewUrl);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("renders plain UTF-8 files read-only", async () => {
    fetchMock.mockResolvedValue(new Response("plain log entry"));
    setup([{ ...artifact, kind: "file", path: "run.log" }]);
    select("run.log");
    expect(await screen.findByText("plain log entry")).toBeVisible();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  });

  it("shows loading, aborts a departing session read, and never displays its late response", async () => {
    let resolveFirst!: (response: Response) => void;
    fetchMock
      .mockImplementationOnce(
        () =>
          new Promise<Response>((resolve) => {
            resolveFirst = resolve;
          }),
      )
      .mockResolvedValueOnce(new Response("# Current session"));
    const view = setup();
    select();
    expect(screen.getByText("Loading artifact…")).toBeVisible();
    const signal = fetchMock.mock.calls[0][1].signal as AbortSignal;
    view.rerender(
      view.ui({ ...view.props, session: { host: "local", id: "next" } }),
    );
    expect(signal.aborted).toBe(true);
    select();
    expect(
      await screen.findByRole("heading", { name: "Current session" }),
    ).toBeVisible();
    resolveFirst(new Response("# Previous session"));
    await waitFor(() =>
      expect(
        screen.queryByRole("heading", { name: "Previous session" }),
      ).not.toBeInTheDocument(),
    );
  });

  it("shows a removed artifact instead of retained cached content", async () => {
    fetchMock.mockResolvedValue(new Response("# Old report"));
    const view = setup();
    select();
    expect(
      await screen.findByRole("heading", { name: "Old report" }),
    ).toBeVisible();
    view.rerender(view.ui({ ...view.props, artifacts: [] }));
    expect(screen.getByText("Artifact is no longer available.")).toBeVisible();
    expect(
      screen.queryByRole("heading", { name: "Old report" }),
    ).not.toBeInTheDocument();
  });

  it("filters the artifact list without fetching file content", () => {
    const view = setup();
    view.rerender(view.ui({ ...view.props, filter: "does-not-match" }));
    expect(screen.getByText("No matching artifacts.")).toBeVisible();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("shows empty and missing-file states with retry", async () => {
    const view = setup([]);
    expect(screen.getByText("No artifacts yet.")).toBeVisible();
    view.rerender(view.ui({ ...view.props, artifacts: [artifact] }));
    fetchMock
      .mockResolvedValueOnce(new Response(null, { status: 404 }))
      .mockResolvedValueOnce(new Response("# Recovered"));
    select();
    expect(
      await screen.findByText("Unable to load artifact (HTTP 404)."),
    ).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(
      await screen.findByRole("heading", { name: "Recovered" }),
    ).toBeVisible();
  });

  it.each([
    undefined,
    "file:///private/report.md",
    "http://127.0.0.1:3000/api/v1/config",
  ])("does not fetch unavailable or non-artifact URLs: %s", async (url) => {
    setup([{ ...artifact, rawUrl: url }]);
    select();
    expect(
      await screen.findByText("Artifact URL is unavailable."),
    ).toBeVisible();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("never reads a remote artifact's localhost URL and clears the local selection on host change", async () => {
    fetchMock.mockResolvedValue(new Response("# Local report"));
    const view = setup();
    select();
    expect(
      await screen.findByRole("heading", { name: "Local report" }),
    ).toBeVisible();
    view.rerender(
      view.ui({ ...view.props, session: { host: "remote", id: session.id } }),
    );
    expect(
      screen.getByText("Artifacts are available on this computer only."),
    ).toBeVisible();
    expect(
      screen.queryByRole("heading", { name: "Local report" }),
    ).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("shows unsupported binary content and bounds text reads", async () => {
    fetchMock.mockResolvedValue(new Response(new Uint8Array([0, 1, 2])));
    const view = setup([{ ...artifact, kind: "file" }]);
    select();
    expect(
      await screen.findByText("Binary file preview is not available."),
    ).toBeVisible();
    view.rerender(
      view.ui({ ...view.props, artifacts: [{ ...artifact, size: 2_000_000 }] }),
    );
    expect(
      await screen.findByText("Artifact is too large to preview (limit 1 MB)."),
    ).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("does not reuse content for another session with the same artifact path", async () => {
    fetchMock
      .mockResolvedValueOnce(new Response("# First"))
      .mockResolvedValueOnce(new Response("# Second"));
    const view = setup();
    select();
    expect(await screen.findByRole("heading", { name: "First" })).toBeVisible();
    view.rerender(
      view.ui({ ...view.props, session: { host: "local", id: "sess-2" } }),
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("heading", { name: "First" }),
      ).not.toBeInTheDocument(),
    );
    select();
    expect(
      await screen.findByRole("heading", { name: "Second" }),
    ).toBeVisible();
  });
});
