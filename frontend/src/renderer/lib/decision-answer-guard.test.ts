import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { decisionAnswerGuard, guardedDecisionRead } from "./decision-answer-guard";

describe("decision answer guard", () => {
	it("is shared by everything using one query client and separate across clients", () => {
		const client = new QueryClient();
		expect(decisionAnswerGuard(client)).toBe(decisionAnswerGuard(client));
		expect(decisionAnswerGuard(new QueryClient())).not.toBe(decisionAnswerGuard(client));
	});

	it("holds a call while its answer is in flight and frees it when the daemon confirms", () => {
		const guard = decisionAnswerGuard(new QueryClient());
		guard.begin("local", "dec_1");
		expect(guard.state("local", "dec_1")).toBe("sending");
		// A read cannot settle an answer that has not settled.
		guard.readSucceeded("local", guard.readStarted());
		expect(guard.state("local", "dec_1")).toBe("sending");
		guard.settle("local", "dec_1", true);
		expect(guard.state("local", "dec_1")).toBeUndefined();
	});

	it("keeps a lost answer held through reads that started before it failed", () => {
		const guard = decisionAnswerGuard(new QueryClient());
		guard.begin("local", "dec_1");
		const inFlight = guard.readStarted(); // a live refresh already under way
		guard.settle("local", "dec_1", false);
		expect(guard.state("local", "dec_1")).toBe("unconfirmed");
		guard.readSucceeded("local", inFlight);
		expect(guard.state("local", "dec_1")).toBe("unconfirmed");

		guard.readSucceeded("local", guard.readStarted());
		expect(guard.state("local", "dec_1")).toBeUndefined();
	});

	it("frees only the host, or the one call, a later read actually covered", () => {
		const guard = decisionAnswerGuard(new QueryClient());
		for (const [host, id] of [["local", "dec_1"], ["local", "dec_2"], ["remote-a", "dec_1"]] as const) {
			guard.begin(host, id);
			guard.settle(host, id, false);
		}
		guard.readSucceeded("local", guard.readStarted(), "dec_2");
		expect(guard.state("local", "dec_1")).toBe("unconfirmed");
		expect(guard.state("local", "dec_2")).toBeUndefined();
		guard.readSucceeded("local", guard.readStarted());
		expect(guard.state("local", "dec_1")).toBeUndefined();
		expect(guard.state("remote-a", "dec_1")).toBe("unconfirmed");
	});

	it("tells subscribers when a call is held or freed", () => {
		const guard = decisionAnswerGuard(new QueryClient());
		let changes = 0;
		const unsubscribe = guard.subscribe(() => { changes += 1; });
		guard.begin("local", "dec_1");
		guard.settle("local", "dec_1", false);
		guard.readSucceeded("local", guard.readStarted());
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
		const read = guardedDecisionRead(guard, "local", undefined, () => new Promise<string[]>((resolve) => { finish = resolve; }));
		guard.settle("local", "dec_1", false);
		finish(["dec_1"]);
		await expect(read).resolves.toEqual(["dec_1"]);
		expect(guard.state("local", "dec_1")).toBe("unconfirmed");

		await guardedDecisionRead(guard, "local", undefined, async () => []);
		expect(guard.state("local", "dec_1")).toBeUndefined();
	});

	it("frees nothing when a read fails", async () => {
		const guard = decisionAnswerGuard(new QueryClient());
		guard.begin("local", "dec_1");
		guard.settle("local", "dec_1", false);
		await expect(guardedDecisionRead(guard, "local", "dec_1", () => Promise.reject(new Error("offline")))).rejects.toThrow("offline");
		expect(guard.state("local", "dec_1")).toBe("unconfirmed");
	});
});
