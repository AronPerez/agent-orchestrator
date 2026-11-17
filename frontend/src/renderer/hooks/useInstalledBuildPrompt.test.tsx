import { renderHook } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import type { UpdateStatus } from "../../main/update-settings";
import { useInstalledBuildPrompt } from "./useInstalledBuildPrompt";

const { state, openPrompt } = vi.hoisted(() => ({ state: { status: { state: "idle" } as UpdateStatus }, openPrompt: vi.fn() }));
vi.mock("../stores/ui-store", () => ({ useUiStore: (select: (store: { openUpdateInstallPrompt: () => void }) => unknown) => select({ openUpdateInstallPrompt: openPrompt }) }));
beforeEach(() => { state.status = { state: "idle" }; openPrompt.mockReset(); });

it("prompts once for an installed build after workers become idle", () => {
	state.status = { state: "downloaded", installed: { identity: "new", idle: false, restartWhenIdle: false } };
	const { rerender } = renderHook(() => useInstalledBuildPrompt(state.status));
	expect(openPrompt).not.toHaveBeenCalled();
	state.status = { ...state.status, installed: { identity: "new", idle: true, restartWhenIdle: false } };
	rerender();
	expect(openPrompt).toHaveBeenCalledTimes(1);
	rerender();
	expect(openPrompt).toHaveBeenCalledTimes(1);
});

it("does not open a modal for a matching build or a restart already queued by the user", () => {
	const { rerender } = renderHook(() => useInstalledBuildPrompt(state.status));
	state.status = { state: "downloaded", installed: { identity: "new", idle: true, restartWhenIdle: true } };
	rerender();
	expect(openPrompt).not.toHaveBeenCalled();
});
