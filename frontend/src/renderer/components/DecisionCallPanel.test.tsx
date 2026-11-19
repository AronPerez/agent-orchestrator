import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DecisionCall, DecisionHistoryEntry } from "../lib/decision-inbox";

const { getMock, openExternalMock } = vi.hoisted(() => ({ getMock: vi.fn(), openExternalMock: vi.fn() }));
vi.mock("../lib/host-clients", () => ({ clientForSessionHost: () => ({ GET: getMock }) }));
vi.mock("../lib/external-link-policy", () => ({ openLinkInSystemBrowser: openExternalMock }));

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
	openExternalMock.mockReset();
});

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

	it("lists the options with the recommendation in words and the standing pick", async () => {
		renderPanel();
		const options = await screen.findByRole("list", { name: "Options" });
		const items = within(options).getAllByRole("listitem");
		expect(items[0]).toHaveTextContent("OAuth client credentials");
		expect(items[0]).toHaveTextContent("Recommended");
		expect(items[0]).toHaveTextContent("Chosen");
		expect(items[1]).not.toHaveTextContent("Chosen");
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
});
