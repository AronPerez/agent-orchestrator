import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { TerminalTabFrame } from "./TerminalTabFrame";

describe("TerminalTabFrame selection", () => {
	it.each(["leading", "trailing"] as const)("selects from empty %s action chrome without selecting from controls", (actionPosition) => {
		const onSelect = vi.fn();
		const onAction = vi.fn();
		render(
			<TerminalTabFrame
				active={false}
				action={<button onClick={onAction}>Action</button>}
				actionPosition={actionPosition}
				buttonProps={{ role: "tab", onClick: onSelect }}
				trailingAction={<button onClick={onAction}>Trailing action</button>}
			>
				<span>Agent</span>
			</TerminalTabFrame>,
		);

		// Full-height action wrappers cover blank tab space above/below each control.
		for (const name of ["Action", "Trailing action"]) {
			const action = screen.getByRole("button", { name });
			fireEvent.click(action.parentElement!);
			expect(onSelect).toHaveBeenCalledOnce();
			expect(onAction).not.toHaveBeenCalled();
			onSelect.mockClear();
			fireEvent.click(action);
			expect(onAction).toHaveBeenCalledOnce();
			expect(onSelect).not.toHaveBeenCalled();
			onAction.mockClear();
		}

		fireEvent.click(screen.getByText("Agent"));
		expect(onSelect).toHaveBeenCalledOnce();
		onSelect.mockClear();
		fireEvent.click(screen.getByRole("tab").closest("[data-terminal-tab-frame]")!);
		expect(onSelect).toHaveBeenCalledOnce();
	});

	it.each(["disabled", "editing"])("does not select from chrome while %s", (state) => {
		const onSelect = vi.fn();
		const { container } = render(
			<TerminalTabFrame
				active={false}
				action={<button>Action</button>}
				buttonProps={{ disabled: state === "disabled", onClick: onSelect }}
				editingContent={state === "editing" ? <input aria-label="Rename" /> : undefined}
			>
				Agent
			</TerminalTabFrame>,
		);
		fireEvent.click(screen.getByRole("button", { name: "Action" }).parentElement!);
		fireEvent.click(container.querySelector("[data-terminal-tab-frame]")!);
		expect(onSelect).not.toHaveBeenCalled();
	});
});
