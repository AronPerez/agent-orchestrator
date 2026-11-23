import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { resetCloudLocalAuthAnswers, useCloudLocalAuth } from "./useCloudLocalAuth";

const settingsState = vi.hoisted(() => ({
	settings: undefined as undefined | { cloudControlPlaneUrl: string },
	isLoading: true,
}));

vi.mock("./useSettings", () => ({ useSettings: () => settingsState }));

function deferred<T>() {
	let resolve!: (value: T) => void;
	let reject!: (reason: unknown) => void;
	const promise = new Promise<T>((res, rej) => {
		resolve = res;
		reject = rej;
	});
	return { promise, resolve, reject };
}

beforeEach(() => {
	resetCloudLocalAuthAnswers();
	settingsState.settings = { cloudControlPlaneUrl: "http://127.0.0.1:8080" };
	settingsState.isLoading = false;
});

afterEach(() => {
	vi.restoreAllMocks();
});

describe("useCloudLocalAuth", () => {
	it("is not checked until main answers, then reports its answer", async () => {
		const answer = deferred<boolean>();
		const ask = vi.spyOn(aoBridge.cloud, "localAuthAvailable").mockReturnValue(answer.promise);
		const { result } = renderHook(() => useCloudLocalAuth());

		expect(result.current).toMatchObject({ available: false, checked: false });
		expect(ask).toHaveBeenCalledWith("http://127.0.0.1:8080");

		await act(async () => answer.resolve(true));
		expect(result.current).toMatchObject({ available: true, checked: true });
	});

	it("answers a later mount at once from main's earlier answer", async () => {
		const ask = vi.spyOn(aoBridge.cloud, "localAuthAvailable").mockResolvedValue(true);
		const first = renderHook(() => useCloudLocalAuth());
		await waitFor(() => expect(first.result.current.checked).toBe(true));
		first.unmount();

		// Opening Settings again: no wait, no second ask.
		const second = renderHook(() => useCloudLocalAuth());
		expect(second.result.current).toMatchObject({ available: true, checked: true });
		expect(ask).toHaveBeenCalledOnce();
	});

	it("ends the wait on a failed ask without remembering the failure", async () => {
		const ask = vi.spyOn(aoBridge.cloud, "localAuthAvailable").mockRejectedValueOnce(new Error("ipc down"));
		const first = renderHook(() => useCloudLocalAuth());
		await waitFor(() => expect(first.result.current.checked).toBe(true));
		expect(first.result.current.available).toBe(false);
		first.unmount();

		ask.mockResolvedValueOnce(true);
		const second = renderHook(() => useCloudLocalAuth());
		expect(second.result.current.checked).toBe(false);
		await waitFor(() => expect(second.result.current).toMatchObject({ available: true, checked: true }));
		expect(ask).toHaveBeenCalledTimes(2);
	});

	it("waits for settings, then needs no answer without a control plane", () => {
		const ask = vi.spyOn(aoBridge.cloud, "localAuthAvailable");
		settingsState.settings = undefined;
		settingsState.isLoading = true;
		const { result, rerender } = renderHook(() => useCloudLocalAuth());
		expect(result.current.checked).toBe(false);

		settingsState.settings = { cloudControlPlaneUrl: "" };
		settingsState.isLoading = false;
		rerender();
		expect(result.current).toMatchObject({ available: false, checked: true });
		expect(ask).not.toHaveBeenCalled();
	});
});
