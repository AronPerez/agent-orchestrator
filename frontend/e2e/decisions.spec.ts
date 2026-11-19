import { expect, test, type Page } from "@playwright/test";
import type { components } from "../src/api/schema";
import { installFakeAgent, type FakeAgentController } from "./support/fake-bridge";

// Renderer integration for the Decisions page: the real decision API client,
// hooks, event transport, board, panel and search, against a small in-memory
// ledger behind the daemon's /api/v1/decisions routes. Ledger rules (state
// reduction, person-only writes, FTS ranking) are the daemon's and are covered
// by its own tests; this ledger only mirrors the transitions the page shows.

type Call = components["schemas"]["DecisionCallResponse"];
type Entry = components["schemas"]["DecisionEntryResponse"];

const HOST = "h_e2e";
const hour = 60 * 60 * 1000;
const iso = (msAgo: number) => new Date(Date.now() - msAgo).toISOString();

function call(id: string, title: string, overrides: Partial<Call> = {}): Call {
	return {
		id, link: `ao://decisions/${id}`, state: "needs_you", stateLabel: "Needs you", title,
		context: "Pick one so the session can continue.",
		options: [
			{ label: "Ship it", consequence: "Release today.", recommended: true },
			{ label: "Wait", consequence: "Release next week." },
		],
		links: [], projectId: "shop", projectName: "Shop", hostId: HOST, machineLabel: "mac-mini",
		ownerSessionId: `s-${id}`, ownerSessionName: `worker ${id}`, raisedAt: iso(3 * hour), retired: false,
		...overrides,
	};
}

class Ledger {
	calls = new Map<string, Call>();
	history = new Map<string, Entry[]>();
	failBoard = false;
	constructor(calls: Call[]) {
		for (const item of calls) {
			this.calls.set(item.id, item);
			const kind = item.state === "decided" ? "decided" : "raised";
			this.history.set(item.id, [{ id: `${item.id}-${kind}`, kind, text: kind === "decided" ? item.title : undefined, writtenAt: item.raisedAt, hostId: HOST, authorKind: "session", authorSession: item.ownerSessionId }]);
		}
	}
	board() {
		const doneSince = Date.now() - 7 * 24 * hour;
		const calls = [...this.calls.values()].filter((item) =>
			item.state === "needs_you" || item.state === "in_progress" || item.state === "decided" ||
			(item.state === "done" && !item.retired && Date.parse(item.outcomeAt ?? "") >= doneSince));
		return { calls, needsYouCount: calls.filter((item) => item.state === "needs_you").length, servingHostId: HOST, doneSince: new Date(doneSince).toISOString() };
	}
	search(q: string) {
		const words = q.toLowerCase();
		return [...this.calls.values()].filter((item) => item.title.toLowerCase().includes(words));
	}
	record(id: string, entry: Omit<Entry, "id" | "writtenAt" | "hostId">) {
		const entries = this.history.get(id)!;
		entries.push({ id: `${id}-${entries.length}`, writtenAt: new Date().toISOString(), hostId: HOST, ...entry });
	}
	answer(id: string, body: { choice: "option" | "other"; option?: number; note?: string }) {
		const item = this.calls.get(id)!;
		const chosenLabel = body.choice === "other" ? "Something else" : item.options[(body.option ?? 1) - 1]!.label;
		this.record(id, { kind: "answered", authorKind: "person", authorSurface: "desktop", choice: body.choice, option: body.option, note: body.note });
		Object.assign(item, { state: "in_progress", stateLabel: "In progress", choice: body.choice, option: body.option, chosenLabel, delivery: "pending", answeredAt: new Date().toISOString(), standingEntryId: `${id}-answer` });
		return item;
	}
	overrule(id: string, note: string) {
		const item = this.calls.get(id)!;
		this.record(id, { kind: "overruled", authorKind: "person", authorSurface: "desktop", note });
		Object.assign(item, { state: "in_progress", stateLabel: "In progress", note, delivery: "pending", answeredAt: new Date().toISOString(), standingEntryId: `${id}-overrule`, standingKind: "overruled" });
		return item;
	}
}

async function openDecisions(page: Page, ledger: Ledger, path = "/#/decisions") {
	await installFakeAgent(page, { projectId: "shop", projectName: "Shop", workers: [] });
	await page.route("http://127.0.0.1:8080/api/v1/decisions**", async (route) => {
		const url = new URL(route.request().url());
		const method = route.request().method();
		const segments = url.pathname.replace("/api/v1/decisions", "").split("/").filter(Boolean);
		if (method === "GET" && segments[0] === "board") {
			if (ledger.failBoard) {
				await route.fulfill({ status: 500, json: { error: "internal", code: "DECISION_BOARD_FAILED", message: "Unable to load decisions", requestId: "e2e-board" } });
				return;
			}
			await route.fulfill({ json: ledger.board() });
			return;
		}
		if (method === "GET" && segments.length === 0) {
			const q = url.searchParams.get("q");
			const calls = q ? ledger.search(q) : [...ledger.calls.values()].filter((item) => item.state === "needs_you");
			await route.fulfill({ json: { calls } });
			return;
		}
		const id = decodeURIComponent(segments[0] ?? "");
		const item = ledger.calls.get(id);
		if (!item) {
			await route.fulfill({ status: 404, json: { error: "not_found", code: "DECISION_NOT_FOUND", message: "Unknown call", requestId: "e2e-404" } });
			return;
		}
		if (method === "GET") {
			await route.fulfill({ json: { call: item, history: ledger.history.get(id) } });
			return;
		}
		const body = route.request().postDataJSON() ?? {};
		if (segments[1] === "answer") await route.fulfill({ json: ledger.answer(id, body) });
		else if (segments[1] === "overrule") await route.fulfill({ json: ledger.overrule(id, body.note) });
		else if (segments[1] === "dismiss") {
			ledger.record(id, { kind: "dismissed", authorKind: "person", authorSurface: "desktop", note: body.note });
			await route.fulfill({ json: Object.assign(item, { state: "dismissed", stateLabel: "Dismissed" }) });
		} else await route.fulfill({ status: 400, json: { error: "bad_request", code: "UNSUPPORTED", message: "Not in this fake" } });
	});
	await page.goto(path);
}

const emitDecision = (page: Page, callId: string) =>
	page.evaluate((id) => (window as unknown as { __aoFakeAgent: FakeAgentController }).__aoFakeAgent.emitCdc("decision_updated", { callId: id }), callId);

test("decisions: the sidebar count opens the board, an answer moves the card, and live events refresh it @T0", async ({ page }) => {
	const ledger = new Ledger([
		call("dec_auth", "Which auth scheme for the proxy?", { streamSessionId: "nango", streamName: "Power BI and Nango" }),
		call("dec_node", "Drop Node 18?", { projectId: "orch", projectName: "agent-orchestrator", raisedAt: iso(20 * 60 * 1000) }),
		call("dec_retry", "Retry policy for webhooks", { state: "in_progress", stateLabel: "In progress", chosenLabel: "Ship it", delivery: "sent", deliveredToName: "worker 7", answeredAt: iso(hour) }),
		call("dec_done", "Where to store credentials?", { state: "done", stateLabel: "Done", chosenLabel: "Ship it", outcome: "Migrated to vault", outcomeAt: iso(5 * hour) }),
	]);
	await openDecisions(page, ledger, "/#/");

	const nav = page.getByRole("button", { name: "Decisions, needs you: 2" });
	await expect(nav).toContainText("2");
	await nav.click();
	await expect(page).toHaveURL(/#\/decisions/);
	const states = page.getByRole("list", { name: "Decision states" });
	await expect(states.getByRole("listitem")).toHaveText([/Needs you\s*2/, /In progress\s*1/, /Done\s*1/]);
	await expect(page.getByRole("region").first()).toHaveAttribute("aria-label", "agent-orchestrator");

	await page.getByRole("button", { name: /^Which auth scheme/ }).click();
	await expect(page).toHaveURL(/call=dec_auth/);
	const panel = page.getByRole("complementary", { name: "Decision" });
	await expect(panel.getByRole("heading", { level: 2 })).toHaveText("Which auth scheme for the proxy?");
	await panel.getByRole("radio", { name: /Wait/ }).click();
	await panel.getByRole("button", { name: "Send decision" }).click();
	await expect(page.getByRole("button", { name: "Decisions, needs you: 1" })).toBeVisible();
	await expect(states.getByRole("listitem")).toHaveText([/Needs you\s*1/, /In progress\s*2/, /Done\s*1/]);
	await expect(panel.getByText("Sending a different pick delivers it to the session as an update.")).toBeVisible();
	await expect(panel.getByRole("list", { name: "History" }).getByText("Answered: Wait")).toBeVisible();

	// Another surface delivers the answer; the page learns from the event alone.
	Object.assign(ledger.calls.get("dec_auth")!, { delivery: "sent", deliveredToName: "worker dec_auth" });
	ledger.record("dec_auth", { kind: "delivered", authorKind: "system", status: "sent", sessionName: "worker dec_auth" });
	await emitDecision(page, "dec_auth");
	await expect(page.getByRole("button", { name: /^Which auth scheme/ })).toContainText("Sent to worker dec_auth");
	await expect(panel.getByRole("list", { name: "History" }).getByText("Sent to worker dec_auth")).toBeVisible();
});

test("decisions: search covers closed and old calls and opens them in the panel", async ({ page }) => {
	const ledger = new Ledger([
		call("dec_live", "Auth retries"),
		call("dec_dismissed", "Auth vendor choice", { state: "dismissed", stateLabel: "Dismissed" }),
		call("dec_old", "Auth library", { state: "done", stateLabel: "Done", outcome: "Picked slog", outcomeAt: iso(30 * 24 * hour) }),
	]);
	await openDecisions(page, ledger);
	await expect(page.getByRole("button", { name: /^Auth retries/ })).toBeVisible();
	await expect(page.getByRole("button", { name: /^Auth library/ })).toHaveCount(0);

	await page.getByRole("button", { name: "Search all decisions" }).click();
	await page.getByRole("searchbox", { name: "Search all decisions" }).fill("auth");
	await expect(page).toHaveURL(/q=auth/);
	const results = page.getByRole("list", { name: "Search results" });
	await expect(results.getByRole("button")).toHaveCount(3);
	await results.getByRole("button", { name: /Auth library/ }).click();
	await expect(page.getByRole("complementary", { name: "Decision" }).getByRole("heading", { level: 2 })).toHaveText("Auth library");
	await expect(page).toHaveURL(/call=dec_old/);

	await page.getByRole("searchbox", { name: "Search all decisions" }).fill("zzz");
	await expect(page.getByText("No decisions match “zzz”")).toBeVisible();
	await page.getByRole("button", { name: "Clear search" }).click();
	await expect(page.getByRole("list", { name: "Decision states" })).toBeVisible();
});

test("decisions: keyboard alone moves between cards, opens one and closes it", async ({ page }) => {
	const ledger = new Ledger([
		call("dec_a", "First question", { raisedAt: iso(5 * hour) }),
		call("dec_b", "Second question", { raisedAt: iso(2 * hour) }),
		call("dec_c", "In flight", { state: "in_progress", stateLabel: "In progress", chosenLabel: "Ship it", delivery: "pending", answeredAt: iso(hour) }),
	]);
	await openDecisions(page, ledger);
	const first = page.getByRole("button", { name: /^First question/ });
	await first.focus();
	await page.keyboard.press("ArrowDown");
	await expect(page.getByRole("button", { name: /^Second question/ })).toBeFocused();
	await page.keyboard.press("ArrowUp");
	await page.keyboard.press("ArrowRight");
	await expect(page.getByRole("button", { name: /^In flight/ })).toBeFocused();
	await page.keyboard.press("Enter");
	await expect(page).toHaveURL(/call=dec_c/);
	await page.keyboard.press("Escape");
	await expect(page).not.toHaveURL(/call=/);
	await expect(page.getByRole("complementary", { name: "Decision" })).toHaveCount(0);
});

test("decisions: a failed board load explains itself and recovers on retry", async ({ page }) => {
	const ledger = new Ledger([call("dec_a", "Recovered question")]);
	ledger.failBoard = true;
	await openDecisions(page, ledger);
	const alert = page.getByRole("alert").filter({ hasText: "Unable to load decisions" });
	await expect(alert).toContainText("Request e2e-board", { timeout: 10_000 });
	ledger.failBoard = false;
	await alert.getByRole("button", { name: "Retry" }).click();
	await expect(page.getByRole("button", { name: /^Recovered question/ })).toBeVisible();
});

test("decisions: a narrow window puts each stream's name above its row", async ({ page }) => {
	await page.setViewportSize({ width: 820, height: 800 });
	const ledger = new Ledger([call("dec_a", "Narrow question", { streamSessionId: "epic", streamName: "Checkout epic" })]);
	await openDecisions(page, ledger);
	const name = page.locator("[data-slot='decision-row-name']").filter({ hasText: "Checkout epic" });
	const card = page.getByRole("button", { name: /^Narrow question/ });
	await expect(card).toBeVisible();
	const nameBox = (await name.boundingBox())!;
	const cardBox = (await card.boundingBox())!;
	expect(nameBox.y + nameBox.height).toBeLessThanOrEqual(cardBox.y + 1);
});

test("decisions: a decision made for the person has its own lane and can be overruled with a note", async ({ page }) => {
	const decided = (id: string, title: string, overrides: Partial<Call> = {}): Call => call(id, title, {
		state: "decided", stateLabel: "Decided for you", options: [], context: "It repeats #11, which is further along.",
		decided: { key: "pr_hygiene", undo: "Reopen #12; its branch is kept.", verify: "One open PR remains for the ticket.", carriedOut: false },
		...overrides,
	});
	const ledger = new Ledger([
		decided("dec_close", "Closed the duplicate pull request #12"),
		decided("dec_label", "Labelled stale issues", { decided: { undo: "Remove the stale label.", carriedOut: true, carriedOutAt: iso(hour), outsidePolicy: "kind_reserved" } }),
	]);
	await openDecisions(page, ledger);
	await expect(page.getByRole("heading", { name: "No decisions yet" })).toHaveCount(0);
	const states = page.getByRole("list", { name: "Decision states" });
	await expect(states.getByRole("listitem")).toHaveText([/Needs you\s*0/, /Decided for you\s*2/, /In progress\s*0/, /Done\s*0/]);
	await expect(page.getByRole("button", { name: /^Labelled stale issues/ })).toContainText("Carried out");

	await page.getByRole("button", { name: /^Closed the duplicate/ }).click();
	const panel = page.getByRole("complementary", { name: "Decision" });
	await expect(panel.getByText("Reopen #12; its branch is kept.")).toBeVisible();
	await expect(panel.getByText("One open PR remains for the ticket.")).toBeVisible();
	await panel.getByRole("button", { name: "Overrule" }).click();
	await expect(panel.getByText("Say what to do instead.")).toBeVisible();
	await panel.getByRole("textbox", { name: "What to do instead" }).fill("Keep both open.");
	await panel.getByRole("button", { name: "Overrule" }).click();
	await expect(states.getByRole("listitem")).toHaveText([/Needs you\s*0/, /Decided for you\s*1/, /In progress\s*1/, /Done\s*0/]);
	await expect(panel.getByRole("list", { name: "History" }).getByText(/Overruled/)).toBeVisible();
	await expect(panel.getByRole("button", { name: "Record outcome" })).toBeVisible();
	await expect(panel.getByRole("button", { name: "Send decision" })).toHaveCount(0);
});
