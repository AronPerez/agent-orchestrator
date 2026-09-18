import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ImageDiffView } from "./ImageDiffView";

vi.mock("../lib/api-client", () => ({
	getApiBaseUrl: () => "http://127.0.0.1:3001",
	hasTrustedApiBaseUrl: () => true,
}));

describe("ImageDiffView", () => {
	it("clears a side's load failure once the image version changes", async () => {
		const { rerender } = render(
			<ImageDiffView path="docs/logo.png" session={{ host: "local", id: "sess-1" }} split status="modified" version={1} />,
		);

		fireEvent.error(await screen.findByAltText("Before version of docs/logo.png"));
		expect(await screen.findByText("Image preview could not be loaded.")).toBeInTheDocument();

		// Re-saving the file bumps the detail load timestamp, which is what makes
		// the blob URL change. The pane has to retry that new URL instead of
		// staying stuck on the previous failure.
		rerender(<ImageDiffView path="docs/logo.png" session={{ host: "local", id: "sess-1" }} split status="modified" version={2} />);

		const before = await screen.findByAltText("Before version of docs/logo.png");
		expect(before).toHaveAttribute("src", expect.stringContaining("side=before&v=2"));
		expect(screen.queryByText("Image preview could not be loaded.")).not.toBeInTheDocument();
	});

	it("does not read a disconnected remote image from the local daemon", () => {
		render(
			<ImageDiffView
				path="docs/logo.png"
				session={{ host: "http://192.0.2.1:3011", id: "sess-remote" }}
				split
				status="modified"
				version={1}
			/>,
		);

		expect(screen.queryByRole("img")).not.toBeInTheDocument();
		expect(screen.getAllByText("This remote host is disconnected. Reconnect to view its current content.")).toHaveLength(2);
	});
});
