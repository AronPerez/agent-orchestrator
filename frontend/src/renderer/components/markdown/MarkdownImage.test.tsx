import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { MarkdownImage } from "./MarkdownImage";
import { MarkdownFileContext } from "./markdown-file-context";

vi.mock("../../lib/api-client", () => ({ getApiBaseUrl: () => "http://127.0.0.1:3001" }));

const context = { sessionId: "s1", filePath: "docs/guide.md", version: 1 };

describe("MarkdownImage", () => {
	it("uses CORS for workspace images and retains the error fallback", () => {
		render(<MarkdownFileContext.Provider value={context}><MarkdownImage src="./logo.png" alt="Logo" /></MarkdownFileContext.Provider>);
		const image = screen.getByRole("img");
		expect(image).toHaveAttribute("src", expect.stringContaining("/api/v1/sessions/s1/workspace/file/blob?path=docs%2Flogo.png"));
		expect(image).toHaveAttribute("crossorigin", "anonymous");
		fireEvent.error(image);
		expect(screen.queryByRole("img")).not.toBeInTheDocument();
		expect(screen.getByText("Logo")).toBeInTheDocument();
	});

	it.each(["https://example.com/logo.png", "/logo.png", "data:image/png;base64,AA=="])("keeps external/absolute images in no-cors mode: %s", (src) => {
		render(<MarkdownFileContext.Provider value={context}><MarkdownImage src={src} alt="Logo" /></MarkdownFileContext.Provider>);
		expect(screen.getByRole("img")).toHaveAttribute("src", src);
		expect(screen.getByRole("img")).not.toHaveAttribute("crossorigin");
	});

	it("keeps artifact images on their existing no-cors path", () => {
		render(<MarkdownFileContext.Provider value={{ ...context, artifactOrigin: "http://ao-preview-artifact.s1.localhost:3001" }}><MarkdownImage src="./logo.png" alt="Logo" /></MarkdownFileContext.Provider>);
		expect(screen.getByRole("img")).toHaveAttribute("src", "http://ao-preview-artifact.s1.localhost:3001/docs/logo.png");
		expect(screen.getByRole("img")).not.toHaveAttribute("crossorigin");
	});
});
