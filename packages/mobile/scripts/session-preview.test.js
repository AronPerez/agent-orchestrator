// Regression check: detecting a preview must not switch a session away from its terminal.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const source = fs.readFileSync(path.join(__dirname, "../app/session/[id].tsx"), "utf8");
const previewPollStart = source.indexOf("// Poll the daemon's on-demand preview detector");
const previewPollEnd = source.indexOf("// The WebView reports the phone's NATURAL fit");
assert.notEqual(previewPollStart, -1, "preview poll start marker should exist");
assert.notEqual(previewPollEnd, -1, "preview poll end marker should exist");
const previewPoll = source.slice(previewPollStart, previewPollEnd);

assert.match(previewPoll, /setPreview\(p\)/, "preview discovery should keep the browser button up to date");
assert.doesNotMatch(previewPoll, /setBrowserOpen\(true\)/, "preview discovery must not open the browser");

console.log("ok - preview discovery leaves the terminal visible");
