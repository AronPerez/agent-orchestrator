import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

vi.hoisted(() => vi.stubEnv("VITE_AO_WEB", "1"));

import { isDaemonServedWeb } from "../lib/preview-mode";
import { SessionArtifactExplorer } from "./SessionArtifactExplorer";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});

it("does not expose local artifact URLs in the daemon-served web build", () => {
  expect(isDaemonServedWeb).toBe(true);
  vi.stubGlobal("ao", undefined);
  const fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
  render(
    <QueryClientProvider client={new QueryClient()}>
      <SessionArtifactExplorer
        session={{ host: "local", id: "web-session" }}
        artifacts={[{
          name: "report.md", path: "report.md", kind: "markdown", size: 20,
          updatedAt: "2026-10-08T00:00:00Z",
          rawUrl: "http://ao-preview-artifact.onxxe3df.localhost:3000/report.md?raw=true",
        }]}
        filter=""
      />
    </QueryClientProvider>,
  );
  expect(screen.getByText("Artifacts are unavailable in the web app.")).toBeVisible();
  expect(screen.queryByRole("button")).not.toBeInTheDocument();
  expect(fetchMock).not.toHaveBeenCalled();
});
