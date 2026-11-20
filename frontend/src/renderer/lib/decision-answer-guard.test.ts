import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { decisionAnswerGuard, decisionViewMeta, detailView, guardedDecisionRead, needsYouView } from "./decision-answer-guard";

const localBell = needsYouView("local");
const remoteBell = needsYouView("remote-a");
const panel = detailView("dec_1");

type Guard = ReturnType<typeof decisionAnswerGuard>;

const waiting = (id = "dec_1") => [{ id, state: "needs_you" }];
const detail = (state: string) => ({ call: { id: "dec_1", state }, history: [] });

function viewQuery(guard: Guard, view: string, read: () => Promise<unknown>) {
	return { queryKey: ["decisions", "test", view], queryFn: () => guardedDecisionRead(guard, view, read), meta: decisionViewMeta(view), retry: false as const };
}

/** Applies one read of `view` through React Query, as the app does. */
async function read(client: QueryClient, guard: Guard, view: string, data: unknown) {
	await client.fetchQuery(viewQuery(guard, view, async () => data));
}

function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((res) => { resolve = res; });
	return { promise, resolve };
}

function lostAnswer(source = "local") {
	const client = new QueryClient();
	const guard = decisionAnswerGuard(client);
	guard.begin(source, "dec_1");
	guard.settle("dec_1", false);
	return { client, guard };
}

describe("decision answer guard", () => {
	it("is shared by everything using one query client and separate across clients", () => {
		const client = new QueryClient();
		expect(decisionAnswerGuard(client)).toBe(decisionAnswerGuard(client));
		expect(decisionAnswerGuard(new QueryClient())).not.toBe(decisionAnswerGuard(client));
	});

	it("holds a call in every view while its answer is in flight and frees it when the daemon confirms", async () => {
		const client = new QueryClient();
		const guard = decisionAnswerGuard(client);
		guard.begin("local", "dec_1");
		expect(guard.state("dec_1", localBell)).toBe("sending");
		expect(guard.state("dec_1", panel)).toBe("sending");
		await read(client, guard, localBell, waiting());
		expect(guard.state("dec_1", localBell)).toBe("sending");
		guard.settle("dec_1", true);
		expect(guard.state("dec_1", localBell)).toBeUndefined();
		expect(guard.state("dec_1", panel)).toBeUndefined();
	});

	it("releases every view once the source shows the lost answer was not recorded", async () => {
		const { client, guard } = lostAnswer();
		expect(guard.state("dec_1", panel)).toBe("unconfirmed");
		await read(client, guard, localBell, waiting());
		expect(guard.state("dec_1", localBell)).toBeUndefined();
		expect(guard.state("dec_1", panel)).toBeUndefined();
	});

	// The board can show the answer while the panel's own read fails: the panel
	// still shows its cached waiting copy, so it must stay held.
	it("keeps a view held when the source shows the answer recorded but that view has not seen it", async () => {
		const { client, guard } = lostAnswer();
		await read(client, guard, localBell, []);
		expect(guard.state("dec_1", localBell)).toBeUndefined();
		expect(guard.state("dec_1", panel)).toBe("unconfirmed");
		await read(client, guard, panel, detail("in_progress"));
		expect(guard.state("dec_1", panel)).toBeUndefined();
	});

	// Decision sync puts one call on several machines under the same id.
	it("holds a synced local copy while an answer to the remote copy is unconfirmed", async () => {
		const { client, guard } = lostAnswer("remote-a");
		// The bell now lists this computer's copy; a fresh local read is not the source.
		await read(client, guard, localBell, waiting());
		expect(guard.state("dec_1", localBell)).toBe("unconfirmed");
		expect(guard.state("dec_1", panel)).toBe("unconfirmed");

		// The source shows the answer recorded; local copies wait until sync shows it too.
		await read(client, guard, remoteBell, []);
		expect(guard.state("dec_1", remoteBell)).toBeUndefined();
		await read(client, guard, localBell, waiting());
		expect(guard.state("dec_1", localBell)).toBe("unconfirmed");
		await read(client, guard, localBell, []);
		expect(guard.state("dec_1", localBell)).toBeUndefined();
	});

	it("releases a synced local copy once the remote source shows the answer was not recorded", async () => {
		const { client, guard } = lostAnswer("remote-a");
		await read(client, guard, remoteBell, waiting());
		expect(guard.state("dec_1", localBell)).toBeUndefined();
		expect(guard.state("dec_1", panel)).toBeUndefined();
	});

	// A live refresh can read the call while the answer is still being sent;
	// it being applied after the answer fails proves nothing about the answer.
	it("ignores a read that was already in flight when the answer failed", async () => {
		const client = new QueryClient();
		const guard = decisionAnswerGuard(client);
		const late = deferred<unknown>();
		guard.begin("local", "dec_1");
		const fetching = client.fetchQuery(viewQuery(guard, localBell, () => late.promise));
		await Promise.resolve();
		guard.settle("dec_1", false);
		late.resolve(waiting());
		await fetching;
		expect(guard.state("dec_1", localBell)).toBe("unconfirmed");
	});

	// A refresh cancelled by an unmount or another invalidation may still get
	// its response; React Query discards it, so the view has learned nothing.
	it("keeps a hold when a cancelled refresh completes late and the next refresh fails", async () => {
		const { client, guard } = lostAnswer();
		const late = deferred<unknown>();
		const cancelled = client.fetchQuery(viewQuery(guard, localBell, () => late.promise)).catch(() => undefined);
		await Promise.resolve();
		await client.cancelQueries({ queryKey: ["decisions", "test", localBell] });
		late.resolve(waiting());
		await cancelled;
		await client.fetchQuery(viewQuery(guard, localBell, () => Promise.reject(new Error("offline")))).catch(() => undefined);
		expect(guard.state("dec_1", localBell)).toBe("unconfirmed");

		await read(client, guard, localBell, waiting());
		expect(guard.state("dec_1", localBell)).toBeUndefined();
	});

	it("does not treat a cache write as a read of the daemon", async () => {
		const { client, guard } = lostAnswer();
		client.setQueryData(["decisions", "test", localBell], waiting());
		expect(guard.state("dec_1", localBell)).toBe("unconfirmed");
	});

	it("keeps calls apart", async () => {
		const { client, guard } = lostAnswer();
		expect(guard.state("dec_2", localBell)).toBeUndefined();
		await read(client, guard, localBell, waiting("dec_2"));
		// dec_1 is absent from the list: recorded (or closed) at its source.
		expect(guard.state("dec_1", panel)).toBe("unconfirmed");
	});

	it("tells subscribers when a hold starts, settles, or a read is observed", async () => {
		const client = new QueryClient();
		const guard = decisionAnswerGuard(client);
		let changes = 0;
		const unsubscribe = guard.subscribe(() => { changes += 1; });
		guard.begin("local", "dec_1");
		guard.settle("dec_1", false);
		await read(client, guard, localBell, waiting());
		unsubscribe();
		guard.begin("local", "dec_2");
		expect(changes).toBe(3);
	});
});
