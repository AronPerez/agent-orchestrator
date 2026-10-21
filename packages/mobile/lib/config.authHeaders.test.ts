import { expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: {} }));
vi.mock("expo-secure-store", () => ({}));

import { authHeaders, DEFAULT_CONFIG } from "./config";

it("pins requests to a known daemon while preserving legacy unpinned configs", () => {
	expect(authHeaders({ ...DEFAULT_CONFIG, password: "token" })).toEqual({ Authorization: "Bearer token" });
	expect(authHeaders({ ...DEFAULT_CONFIG, password: "token", hostId: "alpha" })).toEqual({
		Authorization: "Bearer token", "X-AO-Expected-Host-ID": "alpha",
	});
	expect(authHeaders(DEFAULT_CONFIG)).toEqual({});
});
