import { beforeEach, describe, expect, it, vi } from "vitest";
import {
	isUnauthorized,
	login,
	LoginFailedError,
	reportUnauthorized,
	subscribeUnauthorized,
} from "./auth-gate";

describe("auth gate", () => {
	beforeEach(async () => {
		// Clear the module-level flag between cases via a successful login.
		vi.stubGlobal(
			"fetch",
			vi.fn(async () => new Response(null, { status: 204 })),
		);
		await login("x").catch(() => undefined);
	});

	it("raises once for a storm of 401s and notifies subscribers", () => {
		const listener = vi.fn();
		const unsubscribe = subscribeUnauthorized(listener);
		expect(isUnauthorized()).toBe(false);

		reportUnauthorized();
		reportUnauthorized();
		reportUnauthorized();

		expect(isUnauthorized()).toBe(true);
		expect(listener).toHaveBeenCalledTimes(1);
		unsubscribe();
	});

	it("posts the password to the login route and clears the gate on 204", async () => {
		const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
		vi.stubGlobal("fetch", fetchMock);
		reportUnauthorized();

		await login("WpI3r0aI");

		expect(isUnauthorized()).toBe(false);
		const [url, init] = fetchMock.mock.calls[0] as unknown as [
			string,
			RequestInit,
		];
		// Relative URL and same-origin credentials are what let the browser apply
		// the Set-Cookie without any CORS credential dance.
		expect(url).toBe("/api/v1/auth/login");
		expect(init.method).toBe("POST");
		expect(init.credentials).toBe("same-origin");
		expect(init.body).toBe(JSON.stringify({ password: "WpI3r0aI" }));
	});

	it("keeps the gate up and reports the status when the password is wrong", async () => {
		vi.stubGlobal(
			"fetch",
			vi.fn(async () => new Response(null, { status: 401 })),
		);
		reportUnauthorized();

		await expect(login("nope")).rejects.toBeInstanceOf(LoginFailedError);
		expect(isUnauthorized()).toBe(true);
		await login("nope").catch((error: unknown) => {
			expect((error as LoginFailedError).status).toBe(401);
		});
	});
});
