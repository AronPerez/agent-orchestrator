import { afterEach, describe, expect, it, vi } from "vitest";
import {
  localArtifactUrl,
  MAX_ARTIFACT_TEXT_BYTES,
  readArtifactText,
} from "./session-artifacts";
const url =
  "http://ao-preview-artifact.onxxe3df.localhost:3000/report.md?raw=true";
afterEach(() => vi.unstubAllGlobals());
describe("artifact boundary", () => {
  it("only accepts local isolated artifact URLs", () => {
    expect(localArtifactUrl({ host: "local", id: "one" }, url)).toBe(url);
    const chunked = url.replace("onxxe3df", "a".repeat(50) + ".onxxe3df");
    expect(localArtifactUrl({ host: "local", id: "one" }, chunked)).toBe(
      chunked,
    );
    for (const bad of [
      "javascript:alert(1)",
      "http://example.com/report.md",
      "file:///report.md",
      url.replace("artifact", "workspace"),
      url.replace("http://", "http://user:secret@"),
    ]) {
      expect(
        localArtifactUrl({ host: "local", id: "one" }, bad),
      ).toBeUndefined();
    }
    expect(
      localArtifactUrl({ host: "remote", id: "one" }, url),
    ).toBeUndefined();
  });
  it("bounds actual bytes even when metadata is stale and cancels the reader", async () => {
    const cancel = vi.fn();
    const stream = new ReadableStream({
      start(controller) {
        controller.enqueue(new Uint8Array(MAX_ARTIFACT_TEXT_BYTES + 1));
      },
      cancel,
    });
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(stream)));
    await expect(
      readArtifactText(url, new AbortController().signal),
    ).rejects.toThrow("too-large");
    expect(cancel).toHaveBeenCalled();
  });
  it("supports empty UTF-8 files and rejects invalid UTF-8", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(new Response(""))
        .mockResolvedValueOnce(new Response(new Uint8Array([255]))),
    );
    await expect(
      readArtifactText(url, new AbortController().signal),
    ).resolves.toBe("");
    await expect(
      readArtifactText(url, new AbortController().signal),
    ).rejects.toThrow("binary");
  });
});
