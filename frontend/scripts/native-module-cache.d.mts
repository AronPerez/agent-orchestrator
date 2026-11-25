// Type declarations for native-module-cache.mjs, a plain ESM module that
// forge.config.ts imports. Without this it fails typecheck (TS7016).
export declare function prepareBetterSqlite3(options: {
	moduleDir: string;
	electronDir: string;
	cacheRoot?: string;
	platform: string;
	arch: string;
	electronVersion: string;
	rebuild: () => Promise<void>;
	env?: NodeJS.ProcessEnv;
	log?: (line: string) => void;
}): Promise<"cached" | "rebuilt">;
