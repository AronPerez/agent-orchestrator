import { focusManager, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApiBaseUrl } from "../../lib/api-client";
import { clientFor, forgetHost, registerHostBase } from "../../lib/host-clients";
import { codexAccountsFixture } from "../../test/codex-accounts-fixture";
import { CodexAccountsSection } from "./CodexAccountsSection";

const get = vi.hoisted(() => vi.fn());
vi.mock("../../lib/host-clients", async (importOriginal) => ({
	...await importOriginal<typeof import("../../lib/host-clients")>(),
	clientFor: vi.fn(() => ({ GET: get })),
}));

let qc: QueryClient;
function renderAccounts(active = true) {
	qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
	const view = (enabled: boolean) => (
		<QueryClientProvider client={qc}><CodexAccountsSection active={enabled} /></QueryClientProvider>
	);
	const rendered = render(view(active));
	return { ...rendered, setActive: (enabled: boolean) => rendered.rerender(view(enabled)) };
}

beforeEach(() => {
	vi.clearAllMocks();
	setApiBaseUrl("http://127.0.0.1:8080");
	get.mockResolvedValue({ data: codexAccountsFixture(), response: { status: 200 } });
});
afterEach(() => { cleanup(); qc?.clear(); setApiBaseUrl(null); focusManager.setFocused(undefined); vi.useRealTimers(); });

describe("Codex Accounts overview", () => {
	it("loads only local cached observations and exposes no mutations or diagnostic secrets", async () => {
		renderAccounts();
		expect(await screen.findByText("Personal")).toBeVisible();
		expect(screen.getByText("Signed in")).toBeVisible();
		expect(screen.getByText(/72%/)).toBeVisible();
		expect(clientFor).toHaveBeenCalledWith("local");
		expect(get).toHaveBeenCalledWith("/api/v1/agents/codex/accounts", expect.objectContaining({ signal: expect.any(AbortSignal) }));
		expect(qc.getQueryCache().getAll()[0].queryKey).toEqual(["codex-accounts", "local", "http://127.0.0.1:8080"]);
		expect(document.body).not.toHaveTextContent(/private|token=|sensitive diagnostic/);
		expect(screen.getAllByRole("button")).toHaveLength(1);
	});

	it("distinguishes loading from a successful empty result", async () => {
		let resolve!: (value: unknown) => void;
		get.mockReturnValue(new Promise((done) => { resolve = done; }));
		renderAccounts();
		expect(screen.getByText("Loading Codex accounts…")).toBeVisible();
		expect(screen.queryByText("No Codex accounts found.")).not.toBeInTheDocument();
		const data = codexAccountsFixture(); data.accounts = [];
		await act(async () => resolve({ data, response: { status: 200 } }));
		expect(await screen.findByText("No Codex accounts found.")).toBeVisible();
	});

	it.each([401, 403, 500])("distinguishes denied access from request failure (%s)", async (status) => {
		get.mockResolvedValue({ error: { message: "private /credentials token=secret" }, response: { status } });
		renderAccounts();
		expect(await screen.findByRole("alert")).toHaveTextContent(status === 500 ? "Could not load Codex accounts." : "Access to local Codex accounts was denied.");
		expect(document.body).not.toHaveTextContent("token=secret");
		expect(screen.queryByText("No Codex accounts found.")).not.toBeInTheDocument();
	});

	it("keeps rows after a failed refresh and clears the warning after recovery", async () => {
		renderAccounts();
		await screen.findByText("Personal");
		get.mockRejectedValueOnce(new Error("private diagnostic"));
		await userEvent.click(screen.getByRole("button", { name: "Refresh" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Showing previously loaded accounts");
		expect(screen.getByText("Personal")).toBeVisible();
		await userEvent.click(screen.getByRole("button", { name: "Refresh" }));
		await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
	});

	it("shows uncertain authentication, stale capacity and reconciliation failure separately", async () => {
		const data = codexAccountsFixture();
		data.accounts[0].authentication = { ...data.accounts[0].authentication, state: "unknown", freshness: "checking" };
		data.accounts[0].capacity = { ...data.accounts[0].capacity, state: "exhausted", freshness: "stale", remainingPercent: 0 };
		data.deviceReconciliation.status = "blocked";
		data.deviceReconciliation.activeAccountVerified = false;
		get.mockResolvedValue({ data, response: { status: 200 } });
		renderAccounts();
		expect(await screen.findByText("Personal")).toBeVisible();
		expect(screen.getByText(/Authentication unknown/)).toBeVisible();
		expect(screen.getByText(/Checking/)).toBeVisible();
		expect(screen.getByText(/may be outdated/)).toBeVisible();
		expect(screen.getByText(/Device account verification is blocked/)).toBeVisible();
		expect(screen.queryByText("In use")).not.toBeInTheDocument();
	});

	it.each([null, "http://192.168.1.10:8080", "https://remote.example.test"])("does not probe an unavailable or network daemon (%s)", async (base) => {
		setApiBaseUrl(base);
		renderAccounts();
		expect(screen.getByText(base ? "Accounts are available only on this device." : "Local daemon is unavailable.")).toBeVisible();
		expect(get).not.toHaveBeenCalled();
	});

	it("does not fetch while inactive, and aborts on unmount", async () => {
		get.mockImplementation(() => new Promise(() => {}));
		const { setActive, unmount } = renderAccounts(false);
		expect(get).not.toHaveBeenCalled();
		setActive(true);
		await waitFor(() => expect(get).toHaveBeenCalledTimes(1));
		const signal = get.mock.calls[0][1].signal as AbortSignal;
		unmount();
		expect(signal.aborted).toBe(true);
	});

	it("ignores another connected host", async () => {
		registerHostBase("remote", "http://127.0.0.1:9091/proxy");
		try {
			renderAccounts();
			await screen.findByText("Personal");
			expect(vi.mocked(clientFor).mock.calls.every(([host]) => host === "local")).toBe(true);
		} finally { forgetHost("remote"); }
	});

	it("stops polling when hidden, inactive or unmounted", async () => {
		vi.useFakeTimers();
		const { setActive, unmount } = renderAccounts();
		await act(() => vi.advanceTimersByTimeAsync(1));
		expect(get).toHaveBeenCalledTimes(1);
		await act(() => vi.advanceTimersByTimeAsync(15_000));
		expect(get).toHaveBeenCalledTimes(2);
		focusManager.setFocused(false);
		await act(() => vi.advanceTimersByTimeAsync(30_000));
		expect(get).toHaveBeenCalledTimes(2);
		setActive(false);
		focusManager.setFocused(true);
		await act(() => vi.advanceTimersByTimeAsync(30_000));
		expect(get).toHaveBeenCalledTimes(2);
		unmount();
		await act(() => vi.advanceTimersByTimeAsync(30_000));
		expect(get).toHaveBeenCalledTimes(2);
	});

	it("shows signed-out and broken accounts without inventing absent capacity", async () => {
		const data = codexAccountsFixture();
		data.accounts[0].status = "broken";
		data.accounts[0].authentication.state = "unauthorized";
		data.accounts[0].capacity.state = "unsupported";
		delete data.accounts[0].capacity.remainingPercent;
		get.mockResolvedValue({ data, response: { status: 200 } });
		renderAccounts();
		expect(await screen.findByText("Signed out")).toBeVisible();
		expect(screen.getByText("This account's saved details are invalid.")).toBeVisible();
		expect(screen.getByText("Usage capacity is not available for this account.")).toBeVisible();
		expect(screen.queryByText(/%/)).not.toBeInTheDocument();
	});

	it("handles a loopback-only denial without exposing its raw message", async () => {
		get.mockResolvedValue({ error: { code: "ROUTE_LOOPBACK_ONLY", message: "private path" }, response: { status: 404 } });
		renderAccounts();
		expect(await screen.findByRole("alert")).toHaveTextContent("Accounts are available only on this device.");
	});

	it("isolates requests across daemon port changes", async () => {
		let finishOld!: (value: unknown) => void;
		get.mockReturnValueOnce(new Promise((done) => { finishOld = done; }));
		renderAccounts();
		await waitFor(() => expect(get).toHaveBeenCalledTimes(1));
		act(() => setApiBaseUrl("http://127.0.0.1:9090"));
		expect(await screen.findByText("Personal")).toBeVisible();
		const old = codexAccountsFixture(); old.accounts[0].label = "Obsolete";
		await act(async () => finishOld({ data: old, response: { status: 200 } }));
		expect(screen.queryByText("Obsolete")).not.toBeInTheDocument();
	});
});
