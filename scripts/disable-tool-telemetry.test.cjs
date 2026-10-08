const assert = require("node:assert/strict");
const { test } = require("node:test");

test("tool telemetry is disabled even when the inherited environment allows it", () => {
	process.env.NEXT_TELEMETRY_DISABLED = "0";
	process.env.EXPO_NO_TELEMETRY = "0";
	require("./disable-tool-telemetry.cjs");
	assert.equal(process.env.NEXT_TELEMETRY_DISABLED, "1");
	assert.equal(process.env.EXPO_NO_TELEMETRY, "1");
});
