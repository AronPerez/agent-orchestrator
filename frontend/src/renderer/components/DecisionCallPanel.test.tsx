import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DecisionCall, DecisionHistoryEntry } from "../lib/decision-inbox";

const { getMock, postMock, openExternalMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn(), openExternalMock: vi.fn() }));
vi.mock("../lib/host-clients", () => ({ clientForSessionHost: () => ({ GET: getMock, POST: postMock }) }));
vi.mock("../lib/external-link-policy", () => ({ openLinkInSystemBrowser: openExternalMock }));
const { navigateMock, sessionTarget } = vi.hoisted(() => ({
	navigateMock: vi.fn(),
	sessionTarget: { current: { kind: "checking" } as import("../lib/decision-session-target").DecisionSessionTarget },
}));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigateMock }));
vi.mock("../hooks/useDecisionSessionTarget", () => ({ useDecisionSessionTarget: () => sessionTarget.current }));

import { DecisionCallPanel } from "./DecisionCallPanel";

const call: DecisionCall = {
	id: "dec_1",
	link: "ao://decisions/dec_1",
	state: "in_progress",
	stateLabel: "In progress",
	title: "Which auth scheme for the Nango proxy?",
	context: "The Power BI connector needs to call Nango for each tenant.",
	options: [
		{ label: "OAuth client credentials", consequence: "One app registration per tenant.", recommended: true },
		{ label: "Per-tenant API keys", consequence: "Customers rotate keys by hand." },
	],
	links: [
		{ label: "Nango docs", url: "https://docs.nango.dev/auth" },
		{ label: "Local file", url: "file:///etc/passwd" },
	],
	projectId: "cloud",
	projectName: "skyvern-cloud",
	streamSessionId: "nango",
	streamName: "Power BI and Nango",
	hostId: "h_a",
	machineLabel: "mac-mini",
	ownerSessionId: "w-5512",
	ownerSessionName: "worker 5512",
	raisedAt: "2026-11-18T09:00:00Z",
	choice: "option",
	option: 1,
	chosenLabel: "OAuth client credentials",
	delivery: "sent",
	deliveredToName: "worker 5512",
	retired: false,
	changedAfterClose: true,
};

const at = (minute: number) => `2026-11-18T10:${String(minute).padStart(2, "0")}:00Z`;
const entry = (overrides: Partial<DecisionHistoryEntry> & { id: string; kind: string }): DecisionHistoryEntry => ({
	writtenAt: at(0),
	hostId: "h_a",
	authorKind: "person",
	...overrides,
});

const history: DecisionHistoryEntry[] = [
	entry({ id: "e1", kind: "raised", authorKind: "session", authorSession: "w-5512", writtenAt: at(1) }),
	entry({ id: "e2", kind: "answered", choice: "option", option: 2, authorSurface: "mobile", writtenAt: at(2) }),
	entry({ id: "e3", kind: "answered", choice: "other", note: "Use the vault instead", authorSurface: "desktop", writtenAt: at(3) }),
	entry({ id: "e4", kind: "dismissed", note: "Not needed", authorSurface: "cli", writtenAt: at(4) }),
	entry({ id: "e5", kind: "delivered", authorKind: "system", status: "sent", sessionName: "worker 5512", writtenAt: at(5) }),
	entry({ id: "e6", kind: "delivered", authorKind: "system", status: "sent", sessionName: "orchestrator", fallback: true, writtenAt: at(6) }),
	entry({ id: "e7", kind: "delivered", authorKind: "system", status: "no_session", writtenAt: at(7) }),
	entry({ id: "e8", kind: "outcome", authorKind: "session", authorSession: "w-5512", text: "Shipped", link: "https://github.com/acme/app/pull/412", writtenAt: at(8) }),
	entry({ id: "e9", kind: "withdrawn", authorKind: "session", authorSession: "w-5512", writtenAt: at(9) }),
	entry({ id: "e10", kind: "retired", retired: true, authorSurface: "desktop", writtenAt: at(10) }),
	entry({ id: "e11", kind: "retired", retired: false, authorSurface: "desktop", writtenAt: at(11) }),
	entry({ id: "e12", kind: "asked", authorSurface: "desktop", writtenAt: at(12) }),
];

function renderPanel(callId = "dec_1", onClose = vi.fn()) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const view = render(<QueryClientProvider client={client}><DecisionCallPanel callId={callId} onClose={onClose} /></QueryClientProvider>);
	return { ...view, onClose, client };
}

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({ data: { call, history }, response: { status: 200 } });
	postMock.mockReset().mockResolvedValue({ data: call });
	navigateMock.mockReset();
	sessionTarget.current = { kind: "checking" };
	openExternalMock.mockReset();
});

function serve(next: DecisionCall, entries: DecisionHistoryEntry[] = history) {
	getMock.mockResolvedValue({ data: { call: next, history: entries }, response: { status: 200 } });
}

describe("DecisionCallPanel", () => {
	it("names the project, work stream, machine and session before the call itself", async () => {
		renderPanel();
		const title = await screen.findByRole("heading", { level: 2, name: call.title });
		const place = screen.getByText("skyvern-cloud · Power BI and Nango");
		const owner = screen.getByText("mac-mini · worker 5512");
		expect(place.compareDocumentPosition(title) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(owner.compareDocumentPosition(title) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(screen.getByText("In progress")).toBeInTheDocument();
		expect(screen.getByText("Changed after it closed")).toBeInTheDocument();
		expect(screen.getByText(call.context)).toBeInTheDocument();
		expect(screen.getByRole("complementary", { name: "Decision" })).toBeInTheDocument();
	});

	it("offers the options with the recommendation in words and the standing pick selected", async () => {
		renderPanel();
		const group = await screen.findByRole("radiogroup", { name: "Your decision" });
		const radios = within(group).getAllByRole("radio");
		expect(radios.map((radio) => radio.getAttribute("aria-checked"))).toEqual(["true", "false", "false"]);
		expect(within(group).getByText("Recommended")).toBeInTheDocument();
		expect(within(group).getByRole("radio", { name: /Something else/ })).toBeInTheDocument();
	});

	it("opens only web links, through the system browser", async () => {
		renderPanel();
		await userEvent.click(await screen.findByRole("link", { name: "Nango docs" }));
		expect(openExternalMock).toHaveBeenCalledWith("https://docs.nango.dev/auth");
		expect(screen.queryByRole("link", { name: "Local file" })).not.toBeInTheDocument();
		expect(screen.getByText("Local file")).toBeInTheDocument();
	});

	it("tells the whole history: who did what, from where, and when", async () => {
		renderPanel();
		const list = await screen.findByRole("list", { name: "History" });
		const lines = within(list).getAllByRole("listitem");
		expect(lines.map((line) => line.querySelector("[data-slot='history-what']")?.textContent)).toEqual([
			"Raised",
			"Answered: Per-tenant API keys",
			"Answered: Something else",
			"Dismissed",
			"Sent to worker 5512",
			"Sent to orchestrator instead",
			"No session to carry this out",
			"Outcome: Shipped",
			"Withdrawn",
			"Marked no longer applies",
			"Marked applies again",
			"asked recorded",
		]);
		expect(lines[0]).toHaveTextContent("worker 5512");
		expect(lines[1]).toHaveTextContent("You · Phone");
		expect(lines[2]).toHaveTextContent("Use the vault instead");
		expect(lines[3]).toHaveTextContent("You · Command line");
		expect(lines[4]).toHaveTextContent("Orchestrator");
		expect(within(lines[7]).getByRole("link", { name: "https://github.com/acme/app/pull/412" })).toBeInTheDocument();
		expect(lines[1].querySelector("time")).toHaveAttribute("dateTime", at(2));
	});

	it("closes from its close button and from Escape", async () => {
		const { onClose } = renderPanel();
		await screen.findByRole("heading", { level: 2, name: call.title });
		await userEvent.click(screen.getByRole("button", { name: "Close decision" }));
		expect(onClose).toHaveBeenCalledTimes(1);
		screen.getByRole("button", { name: "Close decision" }).focus();
		await userEvent.keyboard("{Escape}");
		expect(onClose).toHaveBeenCalledTimes(2);
	});

	it("says a call that no longer exists is unavailable and can be closed", async () => {
		getMock.mockResolvedValue({ error: { code: "DECISION_NOT_FOUND", message: "Unknown decision" }, response: { status: 404 } });
		const { onClose } = renderPanel("dec_gone");
		expect(await screen.findByText("This decision is no longer available")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Close decision" }));
		expect(onClose).toHaveBeenCalledOnce();
	});

	it("shows loading, then a failure it can retry", async () => {
		getMock.mockReturnValue(new Promise(() => {}));
		const { unmount } = renderPanel();
		expect(screen.getByRole("status", { name: "Loading decision" })).toBeInTheDocument();
		unmount();

		getMock.mockResolvedValue({ error: { code: "DECISION_GET_FAILED", message: "Unable to get decision", requestId: "req-7" }, response: { status: 500 } });
		renderPanel();
		const alert = await screen.findByRole("alert", undefined, { timeout: 4_000 });
		expect(alert).toHaveTextContent("Unable to get decision");
		expect(alert).toHaveTextContent("Request req-7");
		getMock.mockResolvedValue({ data: { call, history }, response: { status: 200 } });
		await userEvent.click(within(alert).getByRole("button", { name: "Retry" }));
		expect(await screen.findByRole("heading", { level: 2, name: call.title })).toBeInTheDocument();
	});

	it("keeps showing the call while it refreshes after a live change", async () => {
		const { client } = renderPanel();
		await screen.findByRole("heading", { level: 2, name: call.title });
		let resolve!: (value: unknown) => void;
		getMock.mockReturnValue(new Promise((done) => { resolve = done; }));
		void client.invalidateQueries();
		expect(await screen.findByRole("heading", { level: 2, name: call.title })).toBeInTheDocument();
		resolve({ data: { call: { ...call, state: "done", stateLabel: "Done", outcome: "Shipped" }, history }, response: { status: 200 } });
		expect(await screen.findByText("Done")).toBeInTheDocument();
	});

	describe("actions", () => {
		const needsYou: DecisionCall = { ...call, state: "needs_you", stateLabel: "Needs you", choice: undefined, option: undefined, chosenLabel: undefined, delivery: undefined, changedAfterClose: false, standingEntryId: undefined };
		const inProgress: DecisionCall = { ...call, changedAfterClose: false, standingEntryId: "dent_answer" };
		const done: DecisionCall = { ...call, state: "done", stateLabel: "Done", outcome: "Shipped", changedAfterClose: false };

		it("sends the picked option, with an optional note, through the shared answer request", async () => {
			serve(needsYou);
			renderPanel();
			const send = await screen.findByRole("button", { name: "Send decision" });
			expect(send).toBeDisabled();
			await userEvent.click(screen.getByRole("radio", { name: /Per-tenant API keys/ }));
			await userEvent.type(screen.getByLabelText("Note for the session"), "Rotate monthly");
			await userEvent.click(send);
			expect(postMock).toHaveBeenCalledTimes(1);
			expect(postMock).toHaveBeenCalledWith("/api/v1/decisions/{id}/answer", {
				params: { path: { id: "dec_1" } },
				body: { choice: "option", option: 2, note: "Rotate monthly" },
			});
		});

		it("requires a note for Something else and says so beside the field", async () => {
			serve(needsYou);
			renderPanel();
			await userEvent.click(await screen.findByRole("radio", { name: /Something else/ }));
			await userEvent.click(screen.getByRole("button", { name: "Send decision" }));
			expect(postMock).not.toHaveBeenCalled();
			const note = screen.getByLabelText("Note for the session");
			expect(note).toHaveAttribute("aria-invalid", "true");
			expect(note).toHaveAccessibleDescription("Say what to do instead.");
			await userEvent.type(note, "Use the shared vault");
			await userEvent.click(screen.getByRole("button", { name: "Send decision" }));
			expect(postMock).toHaveBeenCalledWith("/api/v1/decisions/{id}/answer", {
				params: { path: { id: "dec_1" } },
				body: { choice: "other", note: "Use the shared vault" },
			});
		});

		it("changes a standing pick and explains it reaches the session as an update", async () => {
			serve(inProgress);
			renderPanel();
			expect(await screen.findByText("Sending a different pick delivers it to the session as an update.")).toBeInTheDocument();
			await userEvent.click(screen.getByRole("radio", { name: /Per-tenant API keys/ }));
			await userEvent.click(screen.getByRole("button", { name: "Send decision" }));
			expect(postMock).toHaveBeenCalledWith("/api/v1/decisions/{id}/answer", expect.objectContaining({ body: { choice: "option", option: 2 } }));
		});

		it("records an outcome for the standing answer, checking the text and link first", async () => {
			serve(inProgress);
			renderPanel();
			const record = await screen.findByRole("button", { name: "Record outcome" });
			await userEvent.click(record);
			expect(screen.getByLabelText("What happened")).toHaveAccessibleDescription("Say what happened, in one line.");
			await userEvent.type(screen.getByLabelText("What happened"), "Shipped in PR 412");
			await userEvent.type(screen.getByLabelText("Link (optional)"), "ftp://nope");
			await userEvent.click(record);
			expect(screen.getByLabelText("Link (optional)")).toHaveAccessibleDescription("Use an http or https link.");
			expect(postMock).not.toHaveBeenCalled();
			await userEvent.clear(screen.getByLabelText("Link (optional)"));
			await userEvent.type(screen.getByLabelText("Link (optional)"), "https://github.com/acme/app/pull/412");
			await userEvent.click(record);
			expect(postMock).toHaveBeenCalledWith("/api/v1/decisions/{id}/outcome", {
				params: { path: { id: "dec_1" } },
				body: { forEntry: "dent_answer", text: "Shipped in PR 412", link: "https://github.com/acme/app/pull/412" },
			});
		});

		it("dismisses only after a confirmation that takes an optional note", async () => {
			serve(needsYou);
			renderPanel();
			await userEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
			expect(postMock).not.toHaveBeenCalled();
			await userEvent.type(screen.getByLabelText("Why (optional)"), "Not needed");
			await userEvent.click(screen.getByRole("button", { name: "Dismiss call" }));
			expect(postMock).toHaveBeenCalledWith("/api/v1/decisions/{id}/dismiss", { params: { path: { id: "dec_1" } }, body: { note: "Not needed" } });
		});

		it("marks a finished call as no longer applying, and back", async () => {
			serve(done);
			const { unmount } = renderPanel();
			await userEvent.click(await screen.findByRole("button", { name: "Mark no longer applies" }));
			expect(postMock).toHaveBeenLastCalledWith("/api/v1/decisions/{id}/retire", { params: { path: { id: "dec_1" } }, body: { retired: true } });
			expect(screen.queryByRole("radiogroup")).not.toBeInTheDocument();
			unmount();
			serve({ ...done, retired: true });
			renderPanel();
			await userEvent.click(await screen.findByRole("button", { name: "Mark applies again" }));
			expect(postMock).toHaveBeenLastCalledWith("/api/v1/decisions/{id}/retire", { params: { path: { id: "dec_1" } }, body: { retired: false } });
		});

		it("lets a dismissed call be answered to reopen it, and leaves a withdrawn call read-only", async () => {
			serve({ ...needsYou, state: "dismissed", stateLabel: "Dismissed" });
			const { unmount } = renderPanel();
			expect(await screen.findByText("Answering reopens this dismissed call.")).toBeInTheDocument();
			expect(screen.getByRole("radiogroup", { name: "Your decision" })).toBeInTheDocument();
			expect(screen.queryByRole("button", { name: "Dismiss" })).not.toBeInTheDocument();
			unmount();
			serve({ ...needsYou, state: "withdrawn", stateLabel: "Withdrawn" });
			renderPanel();
			await screen.findByRole("heading", { level: 2, name: call.title });
			expect(screen.queryByRole("radiogroup")).not.toBeInTheDocument();
			expect(screen.queryByRole("button", { name: /Send decision|Dismiss|Record outcome|Mark/ })).not.toBeInTheDocument();
		});

		it("allows one action at a time and shows the daemon's refusal with its request id", async () => {
			serve(inProgress);
			let finish!: (value: unknown) => void;
			postMock.mockReturnValue(new Promise((done) => { finish = done; }));
			renderPanel();
			await userEvent.click(await screen.findByRole("radio", { name: /Per-tenant API keys/ }));
			await userEvent.click(screen.getByRole("button", { name: "Send decision" }));
			expect(screen.getByRole("button", { name: "Send decision" })).toBeDisabled();
			expect(screen.getByRole("button", { name: "Record outcome" })).toBeDisabled();
			const reads = getMock.mock.calls.length;
			finish({ error: { code: "DECISION_WRONG_STATE", message: "This call is closed. Its state is \"Done\".", requestId: "req-9" } });
			const alert = await screen.findByRole("alert");
			expect(alert).toHaveTextContent("This call is closed.");
			expect(alert).toHaveTextContent("Request req-9");
			expect(postMock).toHaveBeenCalledTimes(1);
			await vi.waitFor(() => expect(getMock.mock.calls.length).toBeGreaterThan(reads));
		});
	});

	describe("open session", () => {
		it("opens exactly the owning session on its machine", async () => {
			sessionTarget.current = { kind: "open", host: "h_laptop", projectId: "shop", sessionId: "w-5512", terminated: false };
			renderPanel();
			await userEvent.click(await screen.findByRole("button", { name: "Open session" }));
			expect(navigateMock).toHaveBeenCalledWith({
				to: "/host/$hostId/project/$projectId/session/$sessionId",
				params: { hostId: "h_laptop", projectId: "shop", sessionId: "w-5512" },
			});
		});

		it("opens a finished call's ended session without restarting it", async () => {
			serve({ ...call, state: "done", stateLabel: "Done", outcome: "Shipped" });
			sessionTarget.current = { kind: "open", host: "local", projectId: "shop", sessionId: "w-5512", terminated: true };
			renderPanel();
			await userEvent.click(await screen.findByRole("button", { name: "Open session" }));
			expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "shop", sessionId: "w-5512" } });
			expect(postMock).not.toHaveBeenCalled();
		});

		it.each([
			[{ kind: "checking" } as const, "Checking session…"],
			[{ kind: "offline", machine: "laptop" } as const, "Connect to laptop to open this session."],
			[{ kind: "missing" } as const, "This session is no longer available."],
			[{ kind: "error" } as const, "Could not check this session."],
		])("is disabled and says why: %o", async (target, reason) => {
			sessionTarget.current = target;
			renderPanel();
			const open = await screen.findByRole("button", { name: "Open session" });
			expect(open).toBeDisabled();
			expect(open).toHaveAccessibleDescription(reason);
			expect(screen.getByText(reason)).toBeInTheDocument();
		});
	});

	it("keeps the call and reports a failed refresh with a retry", async () => {
		const { client } = renderPanel();
		await screen.findByRole("heading", { level: 2, name: call.title });
		getMock.mockResolvedValue({ error: { code: "DECISION_GET_FAILED", message: "Unable to get decision" }, response: { status: 500 } });
		await act(() => client.invalidateQueries());
		const alert = await screen.findByRole("alert", undefined, { timeout: 4_000 });
		expect(alert).toHaveTextContent("Unable to get decision");
		expect(within(alert).getByRole("button", { name: "Retry" })).toBeInTheDocument();
		expect(screen.getByRole("heading", { level: 2, name: call.title })).toBeInTheDocument();
	}, 10_000);
});
