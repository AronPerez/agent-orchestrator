import { execSync } from "node:child_process";
import { defineConfig } from "vite";

// The app's VCS build stamp, injected into the main process as __AO_BUILD_IDENTITY__.
// It must match the daemon's daemonmeta.BuildIdentity() format exactly (full git
// revision, "-dirty" suffix when the working tree is modified) so the app can
// recognize a same-build daemon running from a different path (e.g. an
// ao-svc/launchd daemon under ~/.ao/bin). Empty when git is unavailable → the
// app falls back to the executable-path identity check.
function appBuildIdentity(): string {
  try {
    const revision = execSync("git rev-parse HEAD", {
      encoding: "utf8",
    }).trim();
    if (!revision) return "";
    let modified = false;
    try {
      modified =
        execSync("git status --porcelain", { encoding: "utf8" }).trim() !== "";
    } catch {
      // couldn't determine dirtiness; treat as clean
    }
    return modified ? `${revision}-dirty` : revision;
  } catch {
    return "";
  }
}

// better-sqlite3 is a native module rebuilt for Electron by Forge. Keep it out
// of Vite's bundle so the packaged app loads the rebuilt binary from node_modules
// (the auto-unpack plugin moves that binary outside app.asar).
export default defineConfig({
  define: {
    __AO_BUILD_IDENTITY__: JSON.stringify(appBuildIdentity()),
  },
  build: {
    rollupOptions: {
      external: ["better-sqlite3"],
    },
  },
});
