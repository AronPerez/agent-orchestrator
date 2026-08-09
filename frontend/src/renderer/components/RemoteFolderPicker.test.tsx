import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { RemoteFolderPicker } from "./RemoteFolderPicker";

const listing = (path: string, parent: string, entries: Array<{ name: string; path: string; gitRepo: boolean }>) => ({
	status: 200,
	body: { path, parent, entries },
});

beforeEach(() => {
	vi.restoreAllMocks();
});

function renderPicker(props: Partial<Parameters<typeof RemoteFolderPicker>[0]> = {}) {
	render(
		<RemoteFolderPicker
			hostUrl="http://192.0.2.1:3011"
			hostLabel="workbox"
			open
			onOpenChange={vi.fn()}
			onSelect={vi.fn()}
			{...props}
		/>,
	);
}

describe("RemoteFolderPicker", () => {
	it("opens at the host's home and marks git repos", async () => {
		vi.spyOn(aoBridge.remotes, "request").mockResolvedValue(
			listing("/home/dev", "/home", [
				{ name: "repo", path: "/home/dev/repo", gitRepo: true },
				{ name: "notes", path: "/home/dev/notes", gitRepo: false },
			]),
		);
		renderPicker();

		expect(await screen.findByRole("button", { name: /^repo/ })).toBeInTheDocument();
		expect(aoBridge.remotes.request).toHaveBeenCalledWith("http://192.0.2.1:3011", {
			method: "GET",
			path: "/api/v1/fs/dirs",
		});
		expect(screen.getByRole("button", { name: /^repo/ })).toHaveTextContent(/git/i);
		expect(screen.getByRole("button", { name: /^notes/ })).not.toHaveTextContent(/git/i);
	});

	it("descends into a directory and can go up", async () => {
		const request = vi
			.spyOn(aoBridge.remotes, "request")
			.mockResolvedValueOnce(listing("/home/dev", "/home", [{ name: "src", path: "/home/dev/src", gitRepo: false }]))
			.mockResolvedValueOnce(
				listing("/home/dev/src", "/home/dev", [{ name: "app", path: "/home/dev/src/app", gitRepo: true }]),
			)
			.mockResolvedValueOnce(listing("/home/dev", "/home", [{ name: "src", path: "/home/dev/src", gitRepo: false }]));

		renderPicker();
		await userEvent.click(await screen.findByRole("button", { name: /^src/ }));

		expect(await screen.findByRole("button", { name: /^app/ })).toBeInTheDocument();
		expect(request).toHaveBeenLastCalledWith("http://192.0.2.1:3011", {
			method: "GET",
			path: `/api/v1/fs/dirs?path=${encodeURIComponent("/home/dev/src")}`,
		});

		await userEvent.click(screen.getByRole("button", { name: /up/i }));
		expect(await screen.findByRole("button", { name: /^src/ })).toBeInTheDocument();
		expect(request).toHaveBeenLastCalledWith("http://192.0.2.1:3011", {
			method: "GET",
			path: `/api/v1/fs/dirs?path=${encodeURIComponent("/home/dev")}`,
		});
	});

	it("selects the current directory", async () => {
		vi.spyOn(aoBridge.remotes, "request").mockResolvedValue(listing("/home/dev/repo", "/home/dev", []));
		const onSelect = vi.fn();
		renderPicker({ onSelect });

		await userEvent.click(await screen.findByRole("button", { name: /choose this folder/i }));
		expect(onSelect).toHaveBeenCalledWith("/home/dev/repo");
	});

	it("renders the daemon's error text when browsing fails", async () => {
		// The locked envelope is flat {code, error, message} (schema.ts APIError),
		// not a nested {error:{message}}.
		vi.spyOn(aoBridge.remotes, "request").mockResolvedValue({
			status: 403,
			body: {
				code: "FS_FORBIDDEN",
				error: "the daemon may not read that directory",
				message: "the daemon may not read that directory",
			},
		});
		renderPicker();

		expect(await screen.findByRole("alert")).toHaveTextContent(/may not read/i);
	});
});
