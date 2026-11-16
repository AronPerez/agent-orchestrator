import { act, fireEvent, render as rtlRender, screen } from "@testing-library/react";
import type { ReactElement } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { TitlebarNav } from "./TitlebarNav";
import { TooltipProvider } from "./ui/tooltip";

function render(ui: ReactElement) {
	return rtlRender(<TooltipProvider>{ui}</TooltipProvider>);
}

const { history, router, canGoBack } = vi.hoisted(() => {
	const history = {
		back: vi.fn(),
		forward: vi.fn(),
		location: { state: { __TSR_index: 0 } },
		subscribe: vi.fn((_listener: (event: {
			location: { state: { __TSR_index: number } };
			action: { type: string };
		}) => void) => () => undefined),
	};
	return { history, router: { history }, canGoBack: vi.fn(() => false) };
});

vi.mock("@tanstack/react-router", () => ({
	useCanGoBack: canGoBack,
	useRouter: () => router,
}));

vi.mock("../lib/platform", () => ({
	isLinuxPlatform: () => false,
	isMacPlatform: () => true,
}));

describe("TitlebarNav", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		history.location.state.__TSR_index = 0;
		canGoBack.mockReturnValue(false);
		useUiStore.setState({ isSidebarOpen: true });
	});

	it.each([
		{ open: true, fullscreen: false },
		{ open: false, fullscreen: false },
		{ open: true, fullscreen: true },
		{ open: false, fullscreen: true },
	])("keeps both arrows visible with sidebar open=$open, fullscreen=$fullscreen", ({ open, fullscreen }) => {
		useUiStore.setState({ isSidebarOpen: open });
		const { container } = render(<TitlebarNav isFullScreen={fullscreen} />);
		const nav = container.querySelector('[data-slot="titlebar-nav"]');
		expect(nav).toBeVisible();
		expect(nav).toHaveClass("top-0", fullscreen ? "left-titlebar-cluster-left-fullscreen" : "left-titlebar-cluster-left");
		expect(nav).toHaveClass(fullscreen ? "h-toolbar" : "h-traffic-light-clearance");
		expect(nav?.querySelector('[data-slot="titlebar-brand"], [data-sidebar-brand], img')).toBeNull();
		for (const name of ["Go back", "Go forward"]) {
			const button = screen.getByRole("button", { name });
			expect(button).toBeVisible();
			expect(button).toBeDisabled();
			expect(button).toHaveAttribute("aria-disabled", "true");
			expect(button.closest('[inert], [aria-hidden="true"]')).toBeNull();
			fireEvent.click(button);
		}
		expect(history.back).not.toHaveBeenCalled();
		expect(history.forward).not.toHaveBeenCalled();
	});

	it("keeps the same history controls mounted while toggling the sidebar", () => {
		render(<TitlebarNav />);
		const back = screen.getByRole("button", { name: "Go back" });
		const forward = screen.getByRole("button", { name: "Go forward" });
		fireEvent.click(screen.getByRole("button", { name: "Collapse sidebar" }));
		expect(useUiStore.getState().isSidebarOpen).toBe(false);
		expect(screen.getByRole("button", { name: "Go back" })).toBe(back);
		expect(screen.getByRole("button", { name: "Go forward" })).toBe(forward);
		fireEvent.click(screen.getByRole("button", { name: "Expand sidebar" }));
		expect(useUiStore.getState().isSidebarOpen).toBe(true);
		expect(back).toBeVisible();
		expect(forward).toBeVisible();
	});

	it.each([false, true])("navigates available history with sidebar open=%s", (open) => {
		useUiStore.setState({ isSidebarOpen: open });
		canGoBack.mockReturnValue(true);
		render(<TitlebarNav />);
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
	});

	it("disables forward at the history tip and after a push discards forward entries", () => {
		render(<TitlebarNav />);
		const forward = screen.getByRole("button", { name: "Go forward" });
		const listener = history.subscribe.mock.calls.at(-1)![0];
		const navigate = (index: number, type: string) => act(() => {
			history.location.state.__TSR_index = index;
			listener({ location: history.location, action: { type } });
		});
		navigate(2, "PUSH");
		expect(forward).toBeDisabled();
		navigate(0, "BACK");
		expect(forward).toBeEnabled();
		navigate(2, "FORWARD");
		expect(forward).toBeDisabled();
		navigate(0, "BACK");
		expect(forward).toBeEnabled();
		navigate(1, "PUSH");
		expect(forward).toBeDisabled();
	});

	it("keeps the settings back action available without router history", () => {
		const onBack = vi.fn();
		render(<TitlebarNav onBack={onBack} />);
		const back = screen.getByRole("button", { name: "Go back" });
		expect(back).toBeEnabled();
		fireEvent.click(back);
		expect(onBack).toHaveBeenCalledOnce();
		expect(history.back).not.toHaveBeenCalled();
	});
});
