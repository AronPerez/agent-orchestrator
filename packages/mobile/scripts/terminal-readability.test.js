// The phone renders the daemon's grid scaled to fit, and at pure fit-to-width the
// on-screen text size is contW/cols - it does NOT depend on the xterm font size.
// So a wide grid (a co-viewing desktop drives it) used to render an unreadable
// ~4px TUI that the zoom buttons could not rescue. Guards the readability floor
// that keeps the resting text legible, and that the injected script still parses.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const source = fs.readFileSync(path.join(__dirname, "../app/session/[id].tsx"), "utf8");

// 1. The injected payload must be valid JS - it runs in the WebView, where a
//    syntax error is silent (no scrolling, no zoom, no dims ever reported).
const script = source.match(/const TERMINAL_ENHANCE_JS = `([\s\S]*?)\n`;/);
assert.ok(script, "TERMINAL_ENHANCE_JS should be a template literal");
new Function(`var IS_ANDROID=false;\n${script[1]}`);

// 2. Panning and cursor-follow must be gated on the content overflowing, not on
//    Z.zoomed - at rest the floor holds the grid oversized without "zooming".
assert.match(script[1], /function overflows\(b\)/, "overflow predicate should exist");
assert.doesNotMatch(script[1], /if \(Z\.zoomed\) \{\n\s+var bz = box\(\)/, "pan must not be gated on Z.zoomed");

// 3. The floor itself, mirrored from restScale() in the injected script.
const MIN_TEXT_PX = Number(script[1].match(/var MIN_TEXT_PX = (\d+)/)[1]);
const restScale = (contW, natW, fontSize) => Math.min(1, Math.max(Math.min(1, contW / natW), MIN_TEXT_PX / fontSize));

const PHONE_W = 390;
const cellWidth = (fontSize) => 0.6 * fontSize; // xterm monospace cell
for (const cols of [40, 80, 120, 200]) {
	for (const fontSize of [10, 12, 20]) {
		const scale = restScale(PHONE_W, cols * cellWidth(fontSize), fontSize);
		assert.ok(scale <= 1, `${cols}x@${fontSize}: never upscales past 1:1`);
		assert.ok(fontSize * scale >= MIN_TEXT_PX - 1e-9, `${cols}x@${fontSize}: text stays >= ${MIN_TEXT_PX}px`);
	}
}
// A narrow grid still fits whole - the floor only stops the shrinking, it never
// forces a pan that fit-to-width did not need.
assert.equal(restScale(PHONE_W, 40 * cellWidth(12), 12), 1, "a 40-col grid fits at 1:1");

console.log(`ok - resting terminal text never shrinks below ${MIN_TEXT_PX}px`);
