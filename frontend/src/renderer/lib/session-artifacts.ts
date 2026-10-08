import { isLocal, type Ref } from "./hosts";

export const MAX_ARTIFACT_TEXT_BYTES = 1024 * 1024;

// Only the daemon's isolated artifact origin, never a workspace/API URL or a
// remote daemon's loopback URL. Do not forward renderer credentials here.
export function localArtifactUrl(
  session: Ref,
  value?: string,
): string | undefined {
  if (!isLocal(session.host) || !value) return undefined;
  try {
    const url = new URL(value);
    return url.protocol === "http:" &&
      /^ao-preview-artifact\.(?:[a-z2-7]{1,63}\.)+localhost$/.test(
        url.hostname,
      ) &&
      url.hostname.length <= 253 &&
      !url.username &&
      !url.password
      ? value
      : undefined;
  } catch {
    return undefined;
  }
}

export async function readArtifactText(
  url: string,
  signal: AbortSignal,
): Promise<string> {
  const response = await fetch(url, {
    signal,
    credentials: "omit",
    redirect: "error",
  });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  const mediaType = response.headers.get("content-type")?.split(";")[0].trim();
  if (
    mediaType &&
    !mediaType.startsWith("text/") &&
    ![
      "application/json",
      "application/xml",
      "application/octet-stream",
    ].includes(mediaType)
  ) {
    await response.body?.cancel();
    throw new Error("binary");
  }
  if (!response.body) throw new Error("empty-response");
  const reader = response.body.getReader();
  const decoder = new TextDecoder("utf-8", { fatal: true });
  let size = 0;
  let text = "";
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > MAX_ARTIFACT_TEXT_BYTES) throw new Error("too-large");
      try {
        text += decoder.decode(value, { stream: true });
      } catch {
        throw new Error("binary");
      }
    }
    try {
      text += decoder.decode();
    } catch {
      throw new Error("binary");
    }
    if (/[\x00-\x08\x0e-\x1f]/.test(text)) throw new Error("binary");
    return text;
  } finally {
    await reader.cancel();
    reader.releaseLock();
  }
}
