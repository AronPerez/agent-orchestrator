import { defineConfig } from "vitest/config";

// The app's unit tests live under lib/. scripts/*.test.js are standalone node
// assert scripts (`node scripts/<name>.test.js`), not vitest suites — vitest's
// default glob picks them up and fails them for having no describe/it.
export default defineConfig({
	test: { include: ["lib/**/*.test.ts"] },
});
