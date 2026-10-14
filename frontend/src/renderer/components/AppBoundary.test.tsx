import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AppBoundary } from "./AppBoundary";

describe("AppBoundary", () => {
	afterEach(() => vi.restoreAllMocks());

	it("renders healthy children", () => {
		render(<AppBoundary><p>Workspace</p></AppBoundary>);
		expect(screen.getByText("Workspace")).toBeInTheDocument();
	});

	it("keeps the existing fallback when a child fails without making a network request", () => {
		vi.spyOn(console, "error").mockImplementation(() => undefined);
		const fetch = vi.spyOn(globalThis, "fetch");
		function Broken(): never {
			throw new Error("Render failed");
		}
		render(<AppBoundary><Broken /></AppBoundary>);
		expect(screen.getByRole("heading")).toHaveTextContent("The app hit an unexpected error.");
		expect(screen.getByText("Restart the app or check the daemon logs if this keeps happening.")).toBeInTheDocument();
		expect(fetch).not.toHaveBeenCalled();
	});
});
