// @vitest-environment node
import { once } from "node:events";
import { createServer, request as httpRequest } from "node:http";
import type { AddressInfo } from "node:net";
import { describe, expect, it, onTestFinished } from "vitest";
import { createServer as createViteServer } from "vite";
import rendererConfig, { contentSecurityPolicy } from "./vite.renderer.config";

describe("packaged renderer CSP", () => {
	it("frames agent renders from the loopback daemon and nothing else", () => {
		const frameSrc = contentSecurityPolicy("build")
			.split("; ")
			.filter((directive) => directive.startsWith("frame-src"));
		expect(frameSrc).toEqual(["frame-src 'self' http://127.0.0.1:* http://*.localhost:*"]);
	});
});

describe("dev:web API proxy", () => {
	it("identifies originless same-origin reads without laundering hostile origins", async () => {
		const daemon = createServer((request, response) => {
			response.setHeader("X-Proxied-Origin", request.headers.origin ?? "missing");
			response.end();
		});
		onTestFinished(() => new Promise<void>((resolve) => daemon.close(() => resolve())));
		daemon.listen(0, "127.0.0.1");
		await once(daemon, "listening");
		const proxy = rendererConfig.server?.proxy?.["/api"];
		if (!proxy || typeof proxy === "string") throw new Error("API proxy options missing");
		const vite = await createViteServer({
			configFile: false,
			logLevel: "silent",
			appType: "custom",
			optimizeDeps: { noDiscovery: true, include: [] },
			server: {
				host: "127.0.0.1", port: 0, hmr: false,
				// Reach the proxy with a hostile host; it must not gain a trusted Origin.
				allowedHosts: ["evil.example"],
				proxy: { "/api": { ...proxy, target: `http://127.0.0.1:${(daemon.address() as AddressInfo).port}` } },
			},
		});
		onTestFinished(() => vite.close());
		await vite.listen();
		const origin = vite.resolvedUrls?.local[0]?.replace(/\/$/, "");
		if (!origin) throw new Error("Vite did not bind a local URL");
		for (const test of [
			{ name: "same-origin GET", method: "GET", site: "same-origin", want: origin },
			{ name: "same-origin HEAD", method: "HEAD", site: "same-origin", want: origin },
			{ name: "originless POST", method: "POST", site: "same-origin", want: "missing" },
			{ name: "cross-site image", site: "cross-site", want: "missing" },
			{ name: "same-site request", site: "same-site", want: "missing" },
			{ name: "navigation", site: "none", want: "missing" },
			{ name: "native client", want: "missing" },
			{ name: "explicit hostile origin", site: "same-origin", origin: "http://evil.example", want: "http://evil.example" },
			{ name: "opaque origin", site: "same-origin", origin: "null", want: "null" },
			{ name: "explicit empty origin", site: "same-origin", origin: "", want: "" },
			{ name: "rebinding host", site: "same-origin", host: "evil.example", want: "http://evil.example" },
		]) {
			const headers: Record<string, string> = {};
			if (test.site) headers["Sec-Fetch-Site"] = test.site;
			if (test.origin !== undefined) headers.Origin = test.origin;
			if (test.host) headers.Host = test.host;
			const forwardedOrigin = await new Promise<string | string[] | undefined>((resolve, reject) => {
				const request = httpRequest(`${origin}/api/v1/sessions`, { method: test.method ?? "GET", headers }, (response) => {
					response.resume();
					response.on("end", () => resolve(response.headers["x-proxied-origin"]));
				});
				request.on("error", reject);
				request.end();
			});
			expect(forwardedOrigin, test.name).toBe(test.want);
		}
	});
});
