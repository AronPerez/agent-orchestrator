import { describe, expect, it } from "vitest";
import { citesDecision, decisionMaterial, decisionTextParts, type DecisionTextContext, type DecisionTextPart } from "./decision-references";

const PR = "https://github.com/acme/app/pull/4120";
const context: DecisionTextContext = {
	projectId: "app",
	ownerSessionId: "app-224",
	pullRequestUrls: [PR],
	workspaces: [
		{
			id: "app",
			sessions: [
				{ id: "app-224", artifactFiles: [{ path: "structure.md" }, { path: "option-a.html" }] },
				{ id: "app-226", artifactFiles: [{ path: "review.md" }, { path: "notes.md" }] },
				{ id: "app-230", artifactFiles: [{ path: "notes.md" }] },
			],
		},
		{ id: "other", sessions: [{ id: "other-9", artifactFiles: [{ path: "review.md" }] }] },
	],
};

const linked = (parts: DecisionTextPart[]) => parts.filter((part) => part.kind !== "text");
const joined = (parts: DecisionTextPart[]) => parts.map((part) => part.text).join("");

describe("decisionTextParts", () => {
	it("keeps the writer's text intact around what it names", () => {
		const text = "You asked (your answer on ao://decisions/dec_0b7c2e1a-5d34-4f8e-9a61-2c3d4e5f6a7b). The trees are in /Users/me/.ao/data/artifacts/app-224/structure.md (Artifacts tab of session app-224). Nothing outside draft PR #4120 is built.";
		const parts = decisionTextParts(text, context);
		expect(joined(parts)).toBe(text);
		expect(linked(parts)).toEqual([
			{ kind: "decision", text: "ao://decisions/dec_0b7c2e1a-5d34-4f8e-9a61-2c3d4e5f6a7b", callId: "dec_0b7c2e1a-5d34-4f8e-9a61-2c3d4e5f6a7b", href: "ao://decisions/dec_0b7c2e1a-5d34-4f8e-9a61-2c3d4e5f6a7b" },
			{ kind: "artifact", text: "/Users/me/.ao/data/artifacts/app-224/structure.md", href: "ao://sessions/app/app-224/artifacts/structure.md", name: "structure.md", sessionId: "app-224" },
			{ kind: "session", text: "app-224", href: "ao://sessions/app/app-224", sessionId: "app-224" },
			{ kind: "pr", text: "PR #4120", url: PR, number: 4120 },
		]);
	});

	it("names a bare file only when exactly one listed artifact has that name, the raising session's first", () => {
		const parts = linked(decisionTextParts("See option-a.html, review.md, notes.md and missing.md.", context));
		expect(parts.map((part) => part.kind === "artifact" ? part.href : part.text)).toEqual([
			"ao://sessions/app/app-224/artifacts/option-a.html",
			// Listed in this project and another: the project's own copy.
			"ao://sessions/app/app-226/artifacts/review.md",
		]);
	});

	it("leaves ids, numbers and pull request references that name nothing as text", () => {
		const text = "Conflicts with 10 PRs (scan of 11-24), app-999, #44 and agent-7.";
		expect(linked(decisionTextParts(text, context))).toEqual([]);
	});

	it("draws a pull request URL as a pull request and any other URL as a web link, without trailing punctuation", () => {
		const parts = linked(decisionTextParts(`Outcome: ${PR}. Thread (https://example.com/t?x=1).`, context));
		expect(parts).toEqual([
			{ kind: "pr", text: PR, url: PR, number: 4120 },
			{ kind: "web", text: "https://example.com/t?x=1", url: "https://example.com/t?x=1" },
		]);
	});

	it("does not read a session id inside an artifact path or a URL", () => {
		const parts = linked(decisionTextParts("All under /Users/me/.ao/data/artifacts/app-224/ and https://x.dev/app-224.", context));
		expect(parts).toEqual([{ kind: "web", text: "https://x.dev/app-224", url: "https://x.dev/app-224" }]);
	});

	it("leaves #12 as text when two linked repositories share that number", () => {
		const twice = { ...context, pullRequestUrls: ["https://github.com/a/b/pull/12", "https://github.com/c/d/pull/12"] };
		expect(linked(decisionTextParts("See #12.", twice))).toEqual([]);
	});
});

describe("decisionMaterial", () => {
	it("gathers earlier calls (not the call itself), files, pull requests and other links once each", () => {
		const call = {
			id: "dec_self",
			links: [{ label: "PR #4120", url: PR }, { label: "Thread", url: "https://slack.com/x" }],
			outcomeLink: "https://github.com/acme/app/pull/4121",
		};
		const parts = decisionTextParts("ao://decisions/dec_a and ao://decisions/dec_a, ao://decisions/dec_self, structure.md, structure.md, #4120", { ...context });
		expect(decisionMaterial(call, parts)).toEqual({
			decisions: ["dec_a"],
			artifacts: [{ href: "ao://sessions/app/app-224/artifacts/structure.md", name: "structure.md", sessionId: "app-224" }],
			pullRequests: [PR, "https://github.com/acme/app/pull/4121"],
			links: [{ label: "Thread", url: "https://slack.com/x" }],
		});
	});

	it("lists an earlier call named by its link", () => {
		const id = "dec_0b7c2e1a-5d34-4f8e-9a61-2c3d4e5f6a7b";
		const parts = decisionTextParts(`Follow-up to ao://decisions/${id}, where you asked.`, context);
		expect(decisionMaterial({ id: "dec_self", links: [] }, parts).decisions).toEqual([id]);
	});
});

describe("citesDecision", () => {
	it("matches a call's whole id, bare or in its link, and not a longer id that starts with it", () => {
		expect(citesDecision("Follow-up to ao://decisions/dec_1.", "dec_1")).toBe(true);
		expect(citesDecision("See dec_1 first", "dec_1")).toBe(true);
		expect(citesDecision("About dec_10 and dec_1-b.", "dec_1")).toBe(false);
	});
});
