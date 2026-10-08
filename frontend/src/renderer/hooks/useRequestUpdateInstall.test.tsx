import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RestartToUpdateDialog } from "../components/RestartToUpdateDialog";
import {
	cancelUpdateInstall, confirmUpdateInstall, requestUpdateInstall, useUpdateInstallStore,
} from "./useRequestUpdateInstall";

const { get, install, clientFor, baseUrlFor } = vi.hoisted(() => ({
	get: vi.fn(), install: vi.fn(), clientFor: vi.fn(), baseUrlFor: vi.fn(),
}));
vi.mock("../lib/host-clients", () => ({ clientFor, baseUrlFor }));
vi.mock("../lib/bridge", () => ({ aoBridge: { updates: { install } } }));

const active = { id: "same-id", mode: "chat", chatProviderPreserved: false, isTerminated: false, activity: { state: "active" } };
const response = (sessions: unknown[] = []) => ({ data: { sessions } });
const phase = () => useUpdateInstallStore.getState().phase;

beforeEach(() => {
	vi.resetAllMocks();
	clientFor.mockReturnValue({ GET: get });
	baseUrlFor.mockReturnValue("http://localhost:3001");
	get.mockResolvedValue(response([active]));
	install.mockResolvedValue(undefined);
	useUpdateInstallStore.setState({ phase: "idle", promptOpen: false, risk: { count: 0, unknown: true }, failed: false });
});
afterEach(() => { cleanup(); vi.useRealTimers(); });

describe("shared restart-to-update guard", () => {
	it("warns for active local Chat; Cancel and Escape never call install", async () => {
		render(<RestartToUpdateDialog />);
		act(requestUpdateInstall);
		expect(await screen.findByText("1 local Chat session may lose its current turn")).toBeInTheDocument();
		expect(screen.getByText(/Remote sessions are not affected/)).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
		expect(phase()).toBe("idle");
		act(requestUpdateInstall);
		await screen.findByRole("dialog");
		fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
		expect(phase()).toBe("idle");
		expect(install).not.toHaveBeenCalled();
	});

	it.each([[], [{ ...active, chatProviderPreserved: true }], [{ ...active, mode: "tui" }]].map((sessions) => ({ sessions })))("installs a safe snapshot without prompting: %j", async ({ sessions }) => {
		get.mockResolvedValue(response(sessions));
		requestUpdateInstall();
		await waitFor(() => expect(install).toHaveBeenCalledTimes(1));
		expect(useUpdateInstallStore.getState().promptOpen).toBe(false);
	});

	it("fetches a new local-only snapshot on every request and on confirmation, never reuses cached safe data", async () => {
		get.mockResolvedValueOnce(response());
		requestUpdateInstall();
		await waitFor(() => expect(phase()).toBe("idle"));
		requestUpdateInstall();
		await waitFor(() => expect(phase()).toBe("confirm"));
		expect(install).toHaveBeenCalledTimes(1);
		confirmUpdateInstall();
		await waitFor(() => expect(install).toHaveBeenCalledTimes(2));
		expect(get).toHaveBeenCalledTimes(3);
		for (const [host] of clientFor.mock.calls) expect(host).toBe("local");
		expect(get).toHaveBeenCalledWith("/api/v1/sessions", expect.objectContaining({
			params: { query: { active: true } }, cache: "no-store", signal: expect.any(AbortSignal),
		}));
	});

	it("coalesces clicks across entry points while checking, prompting, and installing", async () => {
		let finish!: () => void;
		install.mockReturnValue(new Promise<void>((resolve) => { finish = resolve; }));
		requestUpdateInstall(); requestUpdateInstall();
		await waitFor(() => expect(phase()).toBe("confirm"));
		requestUpdateInstall();
		expect(get).toHaveBeenCalledTimes(1);
		confirmUpdateInstall(); confirmUpdateInstall(); requestUpdateInstall();
		await waitFor(() => expect(install).toHaveBeenCalledTimes(1));
		confirmUpdateInstall(); requestUpdateInstall(); cancelUpdateInstall();
		expect(phase()).toBe("installing");
		finish();
		await waitFor(() => expect(phase()).toBe("idle"));
	});

	it.each(["network", "api", "malformed", "disconnect"])("requires confirmation after %s failure", async (failure) => {
		if (failure === "network") get.mockRejectedValue(new Error("offline"));
		if (failure === "api") get.mockResolvedValue({ error: { message: "failed" } });
		if (failure === "malformed") get.mockResolvedValue({ data: {} });
		if (failure === "disconnect") clientFor.mockImplementation(() => { throw new Error("disconnected"); });
		requestUpdateInstall();
		await waitFor(() => expect(phase()).toBe("confirm"));
		expect(useUpdateInstallStore.getState().risk.unknown).toBe(true);
		expect(install).not.toHaveBeenCalled();
	});

	it("rejects an old daemon response after its base changes", async () => {
		baseUrlFor.mockReturnValueOnce("http://localhost:3001").mockReturnValue("http://localhost:3002");
		get.mockResolvedValue(response());
		requestUpdateInstall();
		await waitFor(() => expect(phase()).toBe("confirm"));
		expect(install).not.toHaveBeenCalled();
	});

	it("bounds a hanging snapshot, aborts it, and ignores its late safe response", async () => {
		vi.useFakeTimers();
		let finish!: (value: ReturnType<typeof response>) => void;
		get.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
		requestUpdateInstall();
		await vi.advanceTimersByTimeAsync(5_000);
		expect(phase()).toBe("confirm");
		expect(get.mock.calls[0][1].signal.aborted).toBe(true);
		cancelUpdateInstall();
		finish(response());
		await vi.advanceTimersByTimeAsync(0);
		expect(phase()).toBe("idle");
		expect(install).not.toHaveBeenCalled();
	});

	it("surfaces install rejection, permits cancellation and a later retry", async () => {
		get.mockResolvedValue(response());
		install.mockRejectedValueOnce(new Error("IPC failed"));
		render(<RestartToUpdateDialog />);
		act(requestUpdateInstall);
		expect(await screen.findByRole("alert")).toHaveTextContent("AO could not prepare the update");
		fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
		act(requestUpdateInstall);
		await waitFor(() => expect(install).toHaveBeenCalledTimes(2));
	});
});
