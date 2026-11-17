import { defineConfig } from "vite";
import { sentrySourceMaps } from "./vite.sentry";
import { appBuildIdentity, desktopBuildIdentity } from "./vite.build-identity";

const sentry = sentrySourceMaps();

// The app's VCS build stamp, injected into the main process as __AO_BUILD_IDENTITY__.
// It must match the daemon's daemonmeta.BuildIdentity() format exactly (full git
// revision, "-dirty" suffix when the working tree is modified) so the app can
// recognize a same-build daemon running from a different path (e.g. an
// ao-svc/launchd daemon under ~/.ao/bin). Empty when git is unavailable → the
// app falls back to the executable-path identity check.
// better-sqlite3 is a native module rebuilt for Electron by Forge. Keep it out
// of Vite's bundle so the packaged app loads the rebuilt binary from
// node_modules (the auto-unpack plugin moves that binary outside app.asar).
export default defineConfig({
	// Set only by the release build (build-artifacts.yml). Without it a packaged
	// copy never initializes Sentry (src/shared/sentry-config.ts).
	define: {
		__AO_BUILD_IDENTITY__: JSON.stringify(appBuildIdentity()),
		__AO_DESKTOP_BUILD_ID__: JSON.stringify(desktopBuildIdentity()),
		"process.env.AO_PUBLISHED_BUILD": JSON.stringify(process.env.AO_PUBLISHED_BUILD === "1" ? "1" : ""),
	},
	plugins: sentry.plugins,
	build: {
		sourcemap: sentry.sourcemap,
		rollupOptions: {
			external: ["better-sqlite3", "original-fs"],
		},
	},
});
