// Regression check: detecting a preview must not switch a session away from its terminal.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const source = fs.readFileSync(path.join(__dirname, "../app/session/[id].tsx"), "utf8");
const previewPoll = source.slice(
	source.indexOf("// Poll for a generated preview"),
	source.indexOf("// The WebView's FitAddon"),
);

assert.match(previewPoll, /setPreview\(p\)/, "preview discovery should keep the browser button up to date");
assert.doesNotMatch(previewPoll, /setBrowserOpen\(true\)/, "preview discovery must not open the browser");

console.log("ok - preview discovery leaves the terminal visible");
