import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ShellTerminal } from "../hooks/useShellTerminals";
import { ShellTerminalsView } from "./ShellTerminalsView";

const { shellTerminals, terminalPaneMock } = vi.hoisted(() => ({
	shellTerminals: { value: [] as ShellTerminal[] },
	terminalPaneMock: vi.fn(),
}));

vi.mock("../hooks/useShellTerminals", () => ({
	useCloseShellTerminal: () => ({ mutate: vi.fn() }),
	useRenameShellTerminal: () => ({ mutate: vi.fn() }),
	useConnectedShellTerminals: () => shellTerminals.value,
}));

vi.mock("../lib/shell-context", () => ({
	useShell: () => ({ daemonStatus: { state: "ready" } }),
}));

vi.mock("./TerminalPane", () => ({
	TerminalPane: (props: unknown) => {
		terminalPaneMock(props);
		return <div>terminal body</div>;
	},
}));

beforeEach(() => {
	shellTerminals.value = [];
	terminalPaneMock.mockClear();
});

describe("ShellTerminalsView", () => {
	it("points the empty state at the visible plus tab-strip control", () => {
		render(<ShellTerminalsView />);

		expect(screen.getByText("No terminals open")).toBeInTheDocument();
		expect(screen.getByText(/use the \+ button/i)).toBeInTheDocument();
		expect(screen.queryByText(/terminal button/i)).not.toBeInTheDocument();
	});

	it("shows a standalone terminal from a remote host", async () => {
		shellTerminals.value = [{
			host: "http://192.0.2.10:3011",
			handleId: "remote-shell",
			workingDir: "/repo/remote",
			title: "remote shell",
			createdAt: "2026-08-13T00:00:00Z",
		}];

		render(<ShellTerminalsView />);

		expect(await screen.findByRole("tab", { name: "remote shell" })).toBeInTheDocument();
		expect(terminalPaneMock).toHaveBeenLastCalledWith(
			expect.objectContaining({
				terminalTarget: expect.objectContaining({
					host: "http://192.0.2.10:3011",
					handleId: "remote-shell",
				}),
			}),
		);
	});
});
