import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));
vi.mock("../lib/host-clients", () => ({ clientForSessionHost: () => ({ GET: getMock }) }));
vi.mock("./ui/hover-card", () => ({
	HoverCardContent: ({ children, ...props }: React.HTMLAttributes<HTMLDivElement>) => <div {...props}>{children}</div>,
}));

import { DecisionLinkPreviewCard } from "./DecisionLinkPreviewCard";

const call = {
	id: "dec_1",
	link: "ao://decisions/dec_1",
	state: "needs_you",
	stateLabel: "Needs you",
	title: "Which auth scheme for the Nango proxy?",
	context: "Private context that must not appear in a hover card.",
	options: [],
	links: [],
	projectId: "shop",
	projectName: "skyvern-cloud",
	streamSessionId: "epic",
	streamName: "Power BI and Nango",
	hostId: "h_a",
	machineLabel: "mac-mini",
	ownerSessionId: "w-5512",
	ownerSessionName: "worker 5512",
	raisedAt: "2026-11-18T10:00:00Z",
	retired: false,
};

function renderCard(href: string, onOpen?: () => void) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const wrap = (node: ReactNode) => <QueryClientProvider client={client}>{node}</QueryClientProvider>;
	return render(wrap(<DecisionLinkPreviewCard href={href} onOpen={onOpen} />));
}

beforeEach(() => getMock.mockReset());

describe("DecisionLinkPreviewCard", () => {
	it("shows the call's title, state, project, stream and owner from this computer's ledger", async () => {
		getMock.mockResolvedValue({ data: { call, history: [] }, response: { status: 200 } });
		const onOpen = vi.fn();
		renderCard("ao://decisions/dec_1", onOpen);

		expect(await screen.findByText("Which auth scheme for the Nango proxy?")).toBeInTheDocument();
		expect(screen.getByText("Needs you")).toBeInTheDocument();
		expect(screen.getByText("skyvern-cloud · Power BI and Nango")).toBeInTheDocument();
		expect(screen.getByText("mac-mini · worker 5512")).toBeInTheDocument();
		expect(screen.queryByText(/Private context/)).not.toBeInTheDocument();
		expect(getMock).toHaveBeenCalledWith("/api/v1/decisions/{id}", expect.objectContaining({ params: { path: { id: "dec_1" } } }));

		// Like a session preview, the whole card opens on a pointer click; the link is the keyboard path.
		await userEvent.click(screen.getByText("Which auth scheme for the Nango proxy?"));
		expect(onOpen).toHaveBeenCalledOnce();
	});

	it("says a call that is not on this computer is unavailable", async () => {
		getMock.mockResolvedValue({ error: { error: { code: "DECISION_NOT_FOUND", message: "Unknown decision" } }, response: { status: 404 } });
		renderCard("ao://decisions/dec_gone");
		expect(await screen.findByText("This decision is not available on this computer")).toBeInTheDocument();
	});

	it("reports a failed read without guessing", async () => {
		getMock.mockResolvedValue({ error: { error: { code: "DECISION_GET_FAILED", message: "Unable to get decision" } }, response: { status: 500 } });
		renderCard("ao://decisions/dec_1");
		// The detail query retries once before reporting the failure.
		expect(await screen.findByRole("alert", undefined, { timeout: 4_000 })).toHaveTextContent("Could not load this decision");
	});

	it("never fetches for a malformed link", () => {
		renderCard("ao://decisions/a/b");
		expect(screen.getByText("This decision is not available on this computer")).toBeInTheDocument();
		expect(getMock).not.toHaveBeenCalled();
	});
});
