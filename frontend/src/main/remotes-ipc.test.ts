import { describe, expect, it } from "vitest";
import { toHostViews } from "./remotes-ipc";

describe("toHostViews", () => {
	it("strips the password before anything crosses to the renderer", () => {
		const views = toHostViews([{ label: "workbox", url: "http://192.0.2.1:3011", password: "supersecret" }]);
		expect(views).toEqual([{ label: "workbox", url: "http://192.0.2.1:3011" }]);
		expect(JSON.stringify(views)).not.toContain("supersecret");
	});
});
