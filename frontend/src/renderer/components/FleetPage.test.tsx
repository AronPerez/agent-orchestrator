import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { FleetPage } from "./FleetPage";

const { fleetProps, navigateMock } = vi.hoisted(() => ({
	fleetProps: { current: undefined as undefined | {
		localWorkspaces: readonly WorkspaceSummary[];
		remoteWorkspaces?: readonly WorkspaceSummary[];
		remoteHosts?: readonly { hostId: string }[];
		onOpen: (session: WorkspaceSession, hostId?: string) => void;
	} },
	navigateMock: vi.fn(),
}));

vi.mock("@tanstack/react-router", async (importOriginal) => ({
	...(await importOriginal<typeof import("@tanstack/react-router")>()),
	useNavigate: () => navigateMock,
}));

const localWorkspaces = [{ id: "local-project", name: "Local", path: "/local", sessions: [] }] as WorkspaceSummary[];
const remoteWorkspaces = [{ id: "remote-project", name: "Remote", path: "/remote", hostId: "build-box", sessions: [] }] as WorkspaceSummary[];

vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ data: localWorkspaces, isError: false }),
}));

vi.mock("../lib/shell-context", () => ({
	useShellMaybe: () => ({
		remoteHosts: [{ hostId: "build-box", label: "Build box", status: "connected", url: "https://build-box.test" }],
		remoteWorkspaces,
	}),
}));

vi.mock("./FleetOverview", () => ({
	FleetOverview: (props: typeof fleetProps.current) => {
		if (!props) return null;
		fleetProps.current = props;
		return <button type="button" onClick={() => props.onOpen({ id: "worker", workspaceId: "remote-project", kind: "worker" } as WorkspaceSession, "build-box")}>Open remote worker</button>;
	},
}));

describe("FleetPage", () => {
	beforeEach(() => {
		fleetProps.current = undefined;
		navigateMock.mockReset();
	});

	it("passes the whole cached fleet to the overview and opens a session on its machine", () => {
		render(<FleetPage />);

		expect(screen.getByText("Fleet")).toBeInTheDocument();
		expect(fleetProps.current?.localWorkspaces).toEqual(localWorkspaces);
		expect(fleetProps.current?.remoteWorkspaces).toEqual(remoteWorkspaces);
		expect(fleetProps.current?.remoteHosts).toEqual([expect.objectContaining({ hostId: "build-box" })]);

		fireEvent.click(screen.getByRole("button", { name: "Open remote worker" }));
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/host/$hostId/project/$projectId/session/$sessionId",
			params: { hostId: "build-box", projectId: "remote-project", sessionId: "worker" },
		});
	});
});
