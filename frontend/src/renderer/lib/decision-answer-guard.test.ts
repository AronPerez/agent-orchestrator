import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { decisionAnswerGuard, guardedDecisionRead } from "./decision-answer-guard";

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

	// A live refresh can read the call while the answer is still being sent;
	// it finishing after the answer fails proves nothing about the answer.
	it("keeps a lost answer held when a read that was already in flight finishes after the failure", async () => {
		const guard = decisionAnswerGuard(new QueryClient());
		let finish!: (calls: string[]) => void;
		guard.begin("local", "dec_1");
		const read = guardedDecisionRead(guard, bell, () => new Promise<string[]>((resolve) => { finish = resolve; }));
		guard.settle("local", "dec_1", false);
		finish(["dec_1"]);
		await expect(read).resolves.toEqual(["dec_1"]);
		expect(guard.state("local", "dec_1", bell)).toBe("unconfirmed");

		await guardedDecisionRead(guard, bell, async () => []);
		expect(guard.state("local", "dec_1", bell)).toBeUndefined();
	});

	it("advances nothing when a read fails", async () => {
		const guard = decisionAnswerGuard(new QueryClient());
		guard.begin("local", "dec_1");
		guard.settle("local", "dec_1", false);
		await expect(guardedDecisionRead(guard, panel, () => Promise.reject(new Error("offline")))).rejects.toThrow("offline");
		expect(guard.state("local", "dec_1", panel)).toBe("unconfirmed");
	});
});
