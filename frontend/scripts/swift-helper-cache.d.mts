// Type declarations for swift-helper-cache.mjs, a plain ESM module that
// forge.config.ts imports. Without this it fails typecheck (TS7016).
export declare function buildSwiftHelper(options: {
	name: "update-helper" | "wake-helper" | "dictation-helper";
	arch: string;
	build: () => void;
	root?: string;
	cacheRoot?: string;
	env?: NodeJS.ProcessEnv;
	log?: (line: string) => void;
}): "built" | "restored";
