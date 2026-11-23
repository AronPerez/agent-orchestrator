import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { DecisionDraftScope, useDecisionDraft } from "./DecisionDraftScope";

function Field({ label }: { label: string }) {
	const [text, setText] = useState("");
	useDecisionDraft(text.trim() !== "");
	return <input aria-label={label} value={text} onChange={(event) => setText(event.target.value)} />;
}

describe("DecisionDraftScope", () => {
	it("reports once when the first form gains unsent text and once when the last loses it", async () => {
		const onDraftChange = vi.fn();
		render(<DecisionDraftScope onDraftChange={onDraftChange}><Field label="one" /><Field label="two" /></DecisionDraftScope>);
		expect(onDraftChange).not.toHaveBeenCalled();
		await userEvent.type(screen.getByLabelText("one"), "a");
		await userEvent.type(screen.getByLabelText("two"), "b");
		expect(onDraftChange.mock.calls).toEqual([[true]]);
		await userEvent.clear(screen.getByLabelText("one"));
		expect(onDraftChange.mock.calls).toEqual([[true]]);
		await userEvent.clear(screen.getByLabelText("two"));
		expect(onDraftChange.mock.calls).toEqual([[true], [false]]);
	});

	it("counts a form that goes away as holding nothing", async () => {
		const onDraftChange = vi.fn();
		const view = render(<DecisionDraftScope onDraftChange={onDraftChange}><Field label="one" /></DecisionDraftScope>);
		await userEvent.type(screen.getByLabelText("one"), "a");
		view.rerender(<DecisionDraftScope onDraftChange={onDraftChange}><span /></DecisionDraftScope>);
		expect(onDraftChange.mock.calls).toEqual([[true], [false]]);
	});

	it("does nothing for a form outside any scope", async () => {
		render(<Field label="alone" />);
		await userEvent.type(screen.getByLabelText("alone"), "a");
		expect(screen.getByLabelText("alone")).toHaveValue("a");
	});
});
