import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
	buildVerificationExtension,
	VERIFICATION_ES_BUNDLE,
	VERIFICATION_ES_DEV_FLAG,
	verificationExtensionBuildOptions,
} from "./build-verification-es-extension.mjs";

describe("macOS verification Endpoint Security extension build", () => {
	for (const [arch, cpu] of [["arm64", "arm64"], ["x64", "x86_64"]]) {
		it(`compile-checks ${arch} as an unsigned development bundle`, () => {
			const options = verificationExtensionBuildOptions(arch, "/project/frontend", "/private/ao-data");
			assert.ok(options.args.includes(`${cpu}-apple-macosx13.0`));
			assert.ok(options.args.includes("AO_MACOS_ES_EXTENSION_DEV"));
			assert.ok(options.args.includes("-lEndpointSecurity"));
			assert.ok(options.args.includes("-lbsm"));
			assert.ok(options.bundle.endsWith(`/build-cache/verification-es-extension/${arch}/${VERIFICATION_ES_BUNDLE}`));
			assert.ok(options.executable.startsWith("/private/ao-data/"));
		});
	}

	it("rejects unsupported architectures", () => {
		assert.throws(() => verificationExtensionBuildOptions("universal"), /Unsupported/);
	});

	it("does nothing while the development flag is off", () => {
		const output = buildVerificationExtension({
			platform: "linux",
			env: {},
			run: () => { throw new Error("must not spawn"); },
		});
		assert.equal(output, null);
	});

	it("rejects an enabled build away from macOS", () => {
		assert.throws(
			() => buildVerificationExtension({ platform: "linux", env: { [VERIFICATION_ES_DEV_FLAG]: "1" } }),
			/must be built on macOS/,
		);
	});
});
