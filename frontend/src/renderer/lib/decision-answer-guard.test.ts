import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { decisionAnswerGuard, decisionViewMeta, guardedDecisionRead } from "./decision-answer-guard";

const bell = "needs-you:local";
const panel = "detail:dec_1";

describe("decision answer guard", () => {
	it("is shared by everything using one query client and separate across clients", () => {
		const client = new QueryClient();
		expect(decisionAnswerGuard(client)).toBe(decisionAnswerGuard(client));
		expect(decisionAnswerGuard(new QueryClient())).not.toBe(decisionAnswerGuard(client));
	});

	it("holds a call in every view while its answer is in flight and frees it when the daemon confirms", () => {
		const guard = decisionAnswerGuard(new QueryClient());
		guard.begin("local", "dec_1");
		expect(guard.state("local", "dec_1", bell)).toBe("sending");
		expect(guard.state("local", "dec_1", panel)).toBe("sending");
		// A read cannot settle an answer that has not settled.
		guard.viewRead(bell, guard.readStarted());
		expect(guard.state("local", "dec_1", bell)).toBe("sending");
		guard.settle("local", "dec_1", true);
		expect(guard.state("local", "dec_1", bell)).toBeUndefined();
		expect(guard.state("local", "dec_1", panel)).toBeUndefined();
	});

	it("keeps a lost answer held through reads that started before it failed", () => {
		const guard = decisionAnswerGuard(new QueryClient());
		guard.begin("local", "dec_1");
		const inFlight = guard.readStarted(); // a live refresh already under way
		guard.settle("local", "dec_1", false);
		expect(guard.state("local", "dec_1", panel)).toBe("unconfirmed");
		guard.viewRead(panel, inFlight);
		expect(guard.state("local", "dec_1", panel)).toBe("unconfirmed");

		guard.viewRead(panel, guard.readStarted());
		expect(guard.state("local", "dec_1", panel)).toBeUndefined();
	});

	// Each view acts on its own cached copy of the call. Another view's fresh
	// read does not refresh this one, so it must not release this one's hold.
	it("releases a view only when that view applied a read started after the failure", () => {
		const guard = decisionAnswerGuard(new QueryClient());
		guard.begin("local", "dec_1");
		guard.settle("local", "dec_1", false);
		guard.viewRead("board", guard.readStarted());
		guard.viewRead(bell, guard.readStarted());
		expect(guard.state("local", "dec_1", bell)).toBeUndefined();
		expect(guard.state("local", "dec_1", panel)).toBe("unconfirmed");
		guard.viewRead(panel, guard.readStarted());
		expect(guard.state("local", "dec_1", panel)).toBeUndefined();
	});

	it("keeps calls and hosts apart", () => {
		const guard = decisionAnswerGuard(new QueryClient());
		guard.begin("remote-a", "dec_1");
		guard.settle("remote-a", "dec_1", false);
		expect(guard.state("local", "dec_1", bell)).toBeUndefined();
		expect(guard.state("remote-a", "dec_1", "needs-you:remote-a")).toBe("unconfirmed");
		expect(guard.state("remote-a", "dec_2", "needs-you:remote-a")).toBeUndefined();
	});

	it("tells subscribers when a hold starts, settles, or a view catches up", () => {
		const guard = decisionAnswerGuard(new QueryClient());
		let changes = 0;
		const unsubscribe = guard.subscribe(() => { changes += 1; });
		guard.begin("local", "dec_1");
		guard.settle("local", "dec_1", false);
		guard.viewRead(panel, guard.readStarted());
		unsubscribe();
		guard.begin("local", "dec_2");
		expect(changes).toBe(3);
	});

	// Views advance only when React Query installs a fetched result for them.
	function deferred<T>() {
		let resolve!: (value: T) => void;
		let reject!: (error: Error) => void;
		const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
		return { promise, resolve, reject };
	}
	const viewQuery = (guard: ReturnType<typeof decisionAnswerGuard>, view: string, read: () => Promise<string[]>) => ({
		queryKey: ["decisions", "test", view],
		queryFn: () => guardedDecisionRead(guard, view, read),
		meta: decisionViewMeta(view),
		retry: false,
	});

	it("releases a view once React Query applies a read that started after the failure", async () => {
		const client = new QueryClient();
		const guard = decisionAnswerGuard(client);
		guard.begin("local", "dec_1");
		guard.settle("local", "dec_1", false);
		await client.fetchQuery(viewQuery(guard, bell, async () => ["dec_1"]));
		expect(guard.state("local", "dec_1", bell)).toBeUndefined();
		expect(guard.state("local", "dec_1", panel)).toBe("unconfirmed");
	});

	// A live refresh can read the call while the answer is still being sent;
	// it being applied after the answer fails proves nothing about the answer.
	it("keeps a lost answer held when a read that was already in flight is applied after the failure", async () => {
		const client = new QueryClient();
		const guard = decisionAnswerGuard(client);
		const read = deferred<string[]>();
		guard.begin("local", "dec_1");
		const fetching = client.fetchQuery(viewQuery(guard, bell, () => read.promise));
		await Promise.resolve();
		guard.settle("local", "dec_1", false);
		read.resolve(["dec_1"]);
		await fetching;
		expect(guard.state("local", "dec_1", bell)).toBe("unconfirmed");
	});

	// A refresh cancelled by an unmount or another invalidation may still get
	// its response; React Query discards it, so the view has learned nothing.
	it("keeps a hold when a cancelled refresh completes late and the next refresh fails", async () => {
		const client = new QueryClient();
		const guard = decisionAnswerGuard(client);
		guard.begin("local", "dec_1");
		guard.settle("local", "dec_1", false);
		const late = deferred<string[]>();
		const cancelled = client.fetchQuery(viewQuery(guard, panel, () => late.promise)).catch(() => undefined);
		await Promise.resolve();
		await client.cancelQueries({ queryKey: ["decisions", "test", panel] });
		late.resolve(["dec_1"]);
		await cancelled;
		await client.fetchQuery(viewQuery(guard, panel, () => Promise.reject(new Error("offline")))).catch(() => undefined);
		expect(guard.state("local", "dec_1", panel)).toBe("unconfirmed");

		await client.fetchQuery(viewQuery(guard, panel, async () => ["dec_1"]));
		expect(guard.state("local", "dec_1", panel)).toBeUndefined();
	});

	it("does not treat a cache write as a read of the daemon", async () => {
		const client = new QueryClient();
		const guard = decisionAnswerGuard(client);
		await client.fetchQuery(viewQuery(guard, bell, async () => ["dec_1"]));
		guard.begin("local", "dec_1");
		guard.settle("local", "dec_1", false);
		client.setQueryData(["decisions", "test", bell], ["dec_1", "dec_2"]);
		expect(guard.state("local", "dec_1", bell)).toBe("unconfirmed");
	});
});
