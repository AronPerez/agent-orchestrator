import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { useUiStore } from "../stores/ui-store";
import { CreateProjectFlow } from "./CreateProjectFlow";

const { navigateMock } = vi.hoisted(() => ({ navigateMock: vi.fn() }));

vi.mock("@tanstack/react-router", async (importOriginal) => ({
	...(await importOriginal<typeof import("@tanstack/react-router")>()),
	useNavigate: () => navigateMock,
}));

beforeEach(() => {
	vi.restoreAllMocks();
	navigateMock.mockReset();
	vi.spyOn(aoBridge.remotes, "list").mockResolvedValue([{ label: "workbox", url: "http://192.0.2.1:3011" }]);
	vi.spyOn(aoBridge.remotes, "probe").mockResolvedValue("online");
	useUiStore.setState({ remoteHosts: true });
});

function renderFlow(props: Partial<Parameters<typeof CreateProjectFlow>[0]> = {}) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={queryClient}>
			<CreateProjectFlow
				embedded
				mode="choose"
				onCloneProject={vi.fn()}
				onCreateProject={vi.fn()}
				onInitializeProject={vi.fn()}
				{...props}
			/>
		</QueryClientProvider>,
	);
}

async function selectWorkbox() {
	await userEvent.click(await screen.findByRole("button", { name: /^host:/i }));
	await userEvent.click(await screen.findByRole("button", { name: /^workbox/ }));
}

describe("CreateProjectFlow — remote host", () => {
	it("defaults to the local host and keeps the native folder picker", async () => {
		const chooseDirectory = vi.spyOn(aoBridge.app, "chooseDirectory").mockResolvedValue(null);
		renderFlow();
		await userEvent.click(screen.getByRole("button", { name: /open local repository/i }));
		expect(chooseDirectory).toHaveBeenCalled();
	});

	it("replaces the folder picker with an absolute-path field for a remote host", async () => {
		const chooseDirectory = vi.spyOn(aoBridge.app, "chooseDirectory").mockResolvedValue(null);
		renderFlow();
		await selectWorkbox();
		await userEvent.click(screen.getByRole("button", { name: /open local repository/i }));
		expect(screen.getByLabelText(/path on workbox/i)).toBeInTheDocument();
		expect(chooseDirectory).not.toHaveBeenCalled();
	});

	it("registers the project on the remote daemon, not the local one", async () => {
		const request = vi.spyOn(aoBridge.remotes, "request").mockResolvedValue({ status: 201, body: { id: "p1" } });
		const onCreateProject = vi.fn();
		renderFlow({ onCreateProject });
		await selectWorkbox();
		await userEvent.click(screen.getByRole("button", { name: /open local repository/i }));
		await userEvent.type(screen.getByLabelText(/path on workbox/i), "/srv/repo");
		await userEvent.click(screen.getByRole("button", { name: /add project on workbox/i }));

		expect(request).toHaveBeenCalledWith("http://192.0.2.1:3011", {
			method: "POST",
			path: "/api/v1/projects",
			body: { path: "/srv/repo", asWorkspace: false },
		});
		// The local create path must not also fire — that would register the
		// project twice, on the wrong machine.
		expect(onCreateProject).not.toHaveBeenCalled();
	});

	it("carries the workspace choice through to the remote daemon", async () => {
		const request = vi.spyOn(aoBridge.remotes, "request").mockResolvedValue({ status: 201, body: { id: "p1" } });
		renderFlow();
		await selectWorkbox();
		await userEvent.click(screen.getByRole("button", { name: /add a workspace folder/i }));
		await userEvent.type(screen.getByLabelText(/path on workbox/i), "/srv/team");
		await userEvent.click(screen.getByRole("button", { name: /add project on workbox/i }));

		expect(request).toHaveBeenCalledWith("http://192.0.2.1:3011", {
			method: "POST",
			path: "/api/v1/projects",
			body: { path: "/srv/team", asWorkspace: true },
		});
	});

	it("shows the daemon's own rejection rather than guessing locally", async () => {
		vi.spyOn(aoBridge.remotes, "request").mockResolvedValue({
			status: 400,
			body: { error: "path must be absolute on the daemon host" },
		});
		renderFlow();
		await selectWorkbox();
		await userEvent.click(screen.getByRole("button", { name: /open local repository/i }));
		await userEvent.type(screen.getByLabelText(/path on workbox/i), "~/repo");
		await userEvent.click(screen.getByRole("button", { name: /add project on workbox/i }));
		expect(await screen.findByRole("alert")).toHaveTextContent(/must be absolute on the daemon host/i);
	});

	it("browsing fills the remote path field", async () => {
		vi.spyOn(aoBridge.remotes, "request")
			.mockResolvedValueOnce({
				status: 200,
				body: { path: "/srv", parent: "/", entries: [{ name: "repo", path: "/srv/repo", gitRepo: true }] },
			})
			.mockResolvedValueOnce({ status: 200, body: { path: "/srv/repo", parent: "/srv", entries: [] } });
		renderFlow();
		await selectWorkbox();
		await userEvent.click(screen.getByRole("button", { name: /open local repository/i }));
		await userEvent.click(screen.getByRole("button", { name: /browse/i }));
		// Descend into repo, then take the folder the picker is standing in.
		await userEvent.click(await screen.findByRole("button", { name: /^repo/ }));
		await userEvent.click(screen.getByRole("button", { name: /choose this folder/i }));
		expect(screen.getByLabelText(/path on workbox/i)).toHaveValue("/srv/repo");
	});

	it("keeps a typed path working alongside Browse", async () => {
		const request = vi.spyOn(aoBridge.remotes, "request").mockResolvedValue({ status: 201, body: { id: "p1" } });
		renderFlow();
		await selectWorkbox();
		await userEvent.click(screen.getByRole("button", { name: /open local repository/i }));
		await userEvent.type(screen.getByLabelText(/path on workbox/i), "/srv/typed");
		await userEvent.click(screen.getByRole("button", { name: /add project on workbox/i }));
		expect(request).toHaveBeenCalledWith("http://192.0.2.1:3011", {
			method: "POST",
			path: "/api/v1/projects",
			body: { path: "/srv/typed", asWorkspace: false },
		});
	});

	it("skips the local git preflight on the remote path", async () => {
		const scanImportFolder = vi.spyOn(aoBridge.app, "scanImportFolder");
		const checkAncestorRepo = vi.spyOn(aoBridge.app, "checkAncestorRepo");
		vi.spyOn(aoBridge.remotes, "request").mockResolvedValue({ status: 201, body: { id: "p1" } });
		renderFlow();
		await selectWorkbox();
		await userEvent.click(screen.getByRole("button", { name: /add a workspace folder/i }));
		await userEvent.type(screen.getByLabelText(/path on workbox/i), "/srv/team");
		await userEvent.click(screen.getByRole("button", { name: /add project on workbox/i }));
		// Both shell out on this machine and would judge the wrong filesystem.
		expect(scanImportFolder).not.toHaveBeenCalled();
		expect(checkAncestorRepo).not.toHaveBeenCalled();
	});
	// A path the host already knows is not a failure — it is the project the user
	// is looking for. The dead end was "A project at this path is already
	// registered" + "Choose a different folder to try again", with nowhere to go.
	it("recognizes an already-registered path instead of failing the import", async () => {
		vi.spyOn(aoBridge.remotes, "request").mockResolvedValue({
			status: 409,
			body: {
				error: "conflict",
				code: "PATH_ALREADY_REGISTERED",
				message: "A project at this path is already registered",
				details: { existingProjectId: "skyvern-cloud", suggestedProjectId: "skyvern-cloud-2" },
			},
		});
		renderFlow();
		await selectWorkbox();
		await userEvent.click(screen.getByRole("button", { name: /open local repository/i }));
		await userEvent.type(screen.getByLabelText(/path on workbox/i), "/srv/skyvern");
		await userEvent.click(screen.getByRole("button", { name: /add project on workbox/i }));

		expect(await screen.findByText(/already registered on workbox/i)).toBeInTheDocument();
		expect(screen.queryByText(/choose a different folder to try again/i)).not.toBeInTheDocument();
		expect(screen.queryByText(/a project at this path is already registered/i)).not.toBeInTheDocument();
	});

	it("opens the project the remote host already has", async () => {
		vi.spyOn(aoBridge.remotes, "request").mockResolvedValue({
			status: 409,
			body: {
				code: "PATH_ALREADY_REGISTERED",
				message: "A project at this path is already registered",
				details: { existingProjectId: "skyvern-cloud" },
			},
		});
		renderFlow();
		await selectWorkbox();
		await userEvent.click(screen.getByRole("button", { name: /open local repository/i }));
		await userEvent.type(screen.getByLabelText(/path on workbox/i), "/srv/skyvern");
		await userEvent.click(screen.getByRole("button", { name: /add project on workbox/i }));
		await userEvent.click(await screen.findByRole("button", { name: /view project/i }));

		// The host id is the saved host url, so the same project id on another
		// host cannot be the one that opens.
		expect(navigateMock).toHaveBeenCalledWith({
			to: "/host/$hostId/project/$projectId",
			params: { hostId: "http://192.0.2.1:3011", projectId: "skyvern-cloud" },
		});
	});

	// An older daemon answers the same conflict without details. There is no
	// project to point at, so the honest fallback is the daemon's own sentence.
	it("falls back to the daemon message when the conflict names no project", async () => {
		vi.spyOn(aoBridge.remotes, "request").mockResolvedValue({
			status: 409,
			body: { code: "PATH_ALREADY_REGISTERED", message: "A project at this path is already registered" },
		});
		renderFlow();
		await selectWorkbox();
		await userEvent.click(screen.getByRole("button", { name: /open local repository/i }));
		await userEvent.type(screen.getByLabelText(/path on workbox/i), "/srv/skyvern");
		await userEvent.click(screen.getByRole("button", { name: /add project on workbox/i }));

		expect(await screen.findByRole("alert")).toHaveTextContent(/a project at this path is already registered/i);
		expect(screen.queryByRole("button", { name: /view project/i })).not.toBeInTheDocument();
	});
});
