import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { navigateMock } = vi.hoisted(() => ({ navigateMock: vi.fn() }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigateMock }));

import { useDecisionLinkNavigation } from "./use-decision-link-navigation";

beforeEach(() => navigateMock.mockReset());

describe("useDecisionLinkNavigation", () => {
	it("opens a decision link as the selected call on the Decisions page, clearing any search", () => {
		const { result } = renderHook(() => useDecisionLinkNavigation());
		expect(result.current("ao://decisions/dec_1")).toBe(true);
		expect(navigateMock).toHaveBeenCalledWith({ to: "/decisions", search: { call: "dec_1" } });
	});

	it("leaves every other link to the next opener", () => {
		const { result } = renderHook(() => useDecisionLinkNavigation());
		for (const url of ["ao://sessions/p/s", "ao://decisions/a/b", "https://example.com"]) {
			expect(result.current(url), url).toBe(false);
		}
		expect(navigateMock).not.toHaveBeenCalled();
	});
});
