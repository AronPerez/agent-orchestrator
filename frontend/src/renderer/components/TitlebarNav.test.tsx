import { act, fireEvent, render as rtlRender, screen } from "@testing-library/react";
import type { ReactElement } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { TitlebarNav } from "./TitlebarNav";
import { TooltipProvider } from "./ui/tooltip";

function render(ui: ReactElement) {
	return rtlRender(<TooltipProvider>{ui}</TooltipProvider>);
}

const { history, canGoBack } = vi.hoisted(() => ({
	canGoBack: vi.fn(() => false),
	history: {
		back: vi.fn(),
		forward: vi.fn(),
		location: { state: { __TSR_index: 0 } },
		subscribe: vi.fn((_listener: (event: {
			location: { state: { __TSR_index: number } };
			action: { type: string };
		}) => void) => () => undefined),
	},
}));

vi.mock("@tanstack/react-router", () => ({
	useCanGoBack: canGoBack,
	useRouter: () => ({ history }),
}));

vi.mock("../lib/platform", () => ({
	isLinuxPlatform: () => false,
	isMacPlatform: () => true,
}));

describe("TitlebarNav", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		canGoBack.mockReturnValue(false);
		useUiStore.setState({ isSidebarOpen: true });
	});

	it.each([false, true])("keeps the same header row with sidebar open=%s", (open) => {
		useUiStore.setState({ isSidebarOpen: open });
		const { container, rerender } = render(<TitlebarNav />);
		const nav = container.querySelector('[data-slot="titlebar-nav"]');
		expect(nav).toHaveClass("top-px", "h-traffic-light-clearance", "left-titlebar-cluster-left");
		rerender(<TooltipProvider><TitlebarNav isFullScreen /></TooltipProvider>);
		expect(nav).toHaveClass("top-px", "h-traffic-light-clearance", "left-titlebar-cluster-left-fullscreen");
		expect(screen.getByRole("button", { name: "Go back" })).toBeDisabled();
	});

	it("keeps history arrows visible after the sidebar settles open", async () => {
		render(<><div data-slot="sidebar-container" /><TitlebarNav /></>);
		await act(() => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));

		expect(screen.queryByText("Orchestrator.inc")).not.toBeInTheDocument();
		for (const name of ["Go back", "Go forward"]) {
			const button = screen.getByRole("button", { name });
			expect(button).toBeVisible();
			expect(button).toBeDisabled();
			expect(button.style).toHaveProperty("WebkitAppRegion", "no-drag");
		}
	});

	it("navigates history with the sidebar open and respects the history lock", () => {
		canGoBack.mockReturnValue(true);
		const { rerender } = render(<TitlebarNav />);
		const listener = history.subscribe.mock.calls.at(-1)![0];
		act(() => {
			listener({ location: { state: { __TSR_index: 2 } }, action: { type: "PUSH" } });
			listener({ location: { state: { __TSR_index: 1 } }, action: { type: "BACK" } });
		});
		const back = screen.getByRole("button", { name: "Go back" });
		const forward = screen.getByRole("button", { name: "Go forward" });
		expect(back).toBeEnabled();
		expect(forward).toBeEnabled();
		fireEvent.click(back);
		fireEvent.click(forward);
		expect(history.back).toHaveBeenCalledOnce();
		expect(history.forward).toHaveBeenCalledOnce();

		rerender(<TooltipProvider><TitlebarNav historyLocked /></TooltipProvider>);
		expect(back).toBeDisabled();
		expect(forward).toBeDisabled();
	});
});
