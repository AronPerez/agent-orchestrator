import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { DecisionInboxEntry } from "../lib/decision-inbox";
import { DecisionInbox } from "./DecisionInbox";

const entry: DecisionInboxEntry = {
	sourceHostId: "remote-a",
	sourceHostLabel: "Studio laptop",
	call: {
		id: "dec_1",
		link: "ao://decisions/dec_1",
		state: "needs_you",
		stateLabel: "Needs you",
		title: "Which release path should checkout use?",
		context: "The fast path reaches customers today but skips the extended soak.",
		options: [
			{ label: "Ship today", consequence: "Release after the focused checks.", recommended: true },
			{ label: "Soak overnight", consequence: "Delay release by one day for the full suite." },
		],
		links: [],
		projectId: "checkout",
		projectName: "Checkout",
		hostId: "h_origin",
		machineLabel: "Mac mini",
		ownerSessionId: "worker-1",
		ownerSessionName: "Release worker",
		raisedAt: "2026-11-18T10:00:00Z",
		retired: false,
	},
};

const planEntry: DecisionInboxEntry = {
	...entry,
	call: {
		...entry.call,
		title: "Approve this plan?",
		reference: "plan-first",
		options: [
			{ label: "Approve", consequence: "Start implementation exactly as planned.", recommended: true },
			{ label: "Approve with changes", consequence: "Apply the approval note, then start implementation." },
			{ label: "Do not proceed", consequence: "Stop without implementation changes." },
		],
	},
};

describe("DecisionInbox", () => {
	it("shows the exact ask, identity, consequences, and recommendation", () => {
		render(<DecisionInbox entries={[entry]} isLoading={false} loadFailed={false} onAnswer={vi.fn()} onRetry={vi.fn()} />);

		expect(screen.getByText("Decision needed · Checkout")).toBeInTheDocument();
		expect(screen.getByText("Which release path should checkout use?")).toBeInTheDocument();
		expect(screen.getByText(entry.call.context)).toBeInTheDocument();
		expect(screen.getByText("Release worker · Mac mini")).toBeInTheDocument();
		expect(screen.getByText("Release after the focused checks.")).toBeInTheDocument();
		expect(screen.getByText("Delay release by one day for the full suite.")).toBeInTheDocument();
		expect(screen.getByText("Recommended")).toBeInTheDocument();
	});

	it("sends the one-based offered option inline", async () => {
		const onAnswer = vi.fn();
		render(<DecisionInbox entries={[entry]} isLoading={false} loadFailed={false} onAnswer={onAnswer} onRetry={vi.fn()} />);

		await userEvent.click(screen.getByRole("button", { name: "Soak overnight" }));
		expect(onAnswer).toHaveBeenCalledWith(entry, 2);
	});

	it("requires and sends the note for a plan approval with changes", async () => {
		const onAnswer = vi.fn();
		render(<DecisionInbox entries={[planEntry]} isLoading={false} loadFailed={false} onAnswer={onAnswer} onRetry={vi.fn()} />);

		const submit = screen.getByRole("button", { name: "Approve with changes" });
		expect(submit).toBeDisabled();

		await userEvent.type(screen.getByLabelText("Changes"), "  Add a rollback step.  ");
		expect(submit).toBeEnabled();
		await userEvent.click(submit);

		expect(onAnswer).toHaveBeenCalledWith(planEntry, 2, "Add a rollback step.");
	});

	it("disables every choice for the call while its answer is being sent", () => {
		render(
			<DecisionInbox
				entries={[entry]}
				isLoading={false}
				loadFailed={false}
				onAnswer={vi.fn()}
				onRetry={vi.fn()}
				pending={{ sourceHostId: "remote-a", callId: "dec_1", option: 1 }}
			/>,
		);

		expect(screen.getByRole("button", { name: /Ship today/ })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Soak overnight" })).toBeDisabled();
	});

	it("serializes answers while one call is being sent", () => {
		const other = {
			...entry,
			call: { ...entry.call, id: "dec_2", title: "Which rollout region should go first?" },
		};
		render(
			<DecisionInbox
				entries={[entry, other]}
				isLoading={false}
				loadFailed={false}
				onAnswer={vi.fn()}
				onRetry={vi.fn()}
				pending={{ sourceHostId: "remote-a", callId: "dec_1", option: 1 }}
			/>,
		);

		expect(screen.getAllByRole("button", { name: /Ship today/ })).toHaveLength(2);
		for (const button of screen.getAllByRole("button")) expect(button).toBeDisabled();
	});

	it("keeps the ask actionable after an answer fails", () => {
		render(
			<DecisionInbox
				entries={[entry]}
				isLoading={false}
				loadFailed={false}
				onAnswer={vi.fn()}
				onRetry={vi.fn()}
				answerError={{ sourceHostId: "remote-a", callId: "dec_1", message: "The call changed." }}
			/>,
		);

		expect(screen.getByRole("alert")).toHaveTextContent("The call changed.");
		expect(screen.getByRole("button", { name: /Ship today/ })).toBeEnabled();
	});

	it("offers a retry when decision calls could not load", async () => {
		const onRetry = vi.fn();
		render(<DecisionInbox entries={[]} isLoading={false} loadFailed onAnswer={vi.fn()} onRetry={onRetry} />);

		expect(screen.getByText("Could not load decisions.")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(onRetry).toHaveBeenCalledTimes(1);
	});
});
