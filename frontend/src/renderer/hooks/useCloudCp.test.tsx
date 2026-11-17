import { renderHook } from "@testing-library/react";
import { expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({
	cloudEnabled: true,
	loading: false,
	status: "authenticated" as "loading" | "authenticated" | "unauthenticated",
}));

vi.mock("./useSettings", () => ({
	useSettings: () => ({ settings: { cloudControlPlaneUrl: "https://cp.example.com" } }),
}));
vi.mock("./useCloudGate", () => ({ useCloudGate: () => state }));
vi.mock("../lib/cloud-session", () => ({
	useCloudSession: () => ({ session: null, status: state.status }),
}));
vi.mock("../lib/cloud-cp/renderer-client", () => ({
	createRendererCloudCpClient: () => ({}),
	cloudCpFetch: vi.fn(),
}));

import { useCloudCp } from "./useCloudCp";

it.each([
	{ name: "configuration pending", cloudEnabled: false, loading: true, status: "authenticated", wantLoading: true, wantReady: false },
	{ name: "account pending", cloudEnabled: true, loading: false, status: "loading", wantLoading: true, wantReady: false },
	{ name: "signed out", cloudEnabled: true, loading: false, status: "unauthenticated", wantLoading: false, wantReady: false },
	{ name: "Cloud disabled", cloudEnabled: false, loading: false, status: "loading", wantLoading: false, wantReady: false },
	{ name: "ready", cloudEnabled: true, loading: false, status: "authenticated", wantLoading: false, wantReady: true },
] as const)("reports initial Cloud readiness: $name", (test) => {
	Object.assign(state, test);
	const { result } = renderHook(() => useCloudCp());
	expect(result.current.loading).toBe(test.wantLoading);
	expect(result.current.ready).toBe(test.wantReady);
});
