// Regression check: PR visibility follows the PR lifecycle, not its session's lifecycle.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const source = fs.readFileSync(path.join(__dirname, "../app/(tabs)/prs.tsx"), "utf8");
const grouping = source.slice(source.indexOf("function groupPRs"), source.indexOf("function passesFilter"));

assert.doesNotMatch(grouping, /isTerminalStatus\(item\.session\.status\)/, "terminal sessions must not hide their PRs");
assert.match(grouping, /if \(pr\.state === "closed"\) return "dead"/, "closed PRs need a visible passive section");

console.log("ok - PR grouping keeps counted PRs visible");
