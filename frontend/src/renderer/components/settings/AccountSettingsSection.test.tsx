import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AccountSettingsSection } from "./AccountSettingsSection";

const mocks = vi.hoisted(() => ({
	session: {
		configured: true,
		status: "unauthenticated" as "authenticated" | "loading" | "unauthenticated",
		session: null as null | { user: { email: string; displayName: string; avatarUrl?: string } },
		signIn: vi.fn(),
		signOut: vi.fn().mockResolvedValue(undefined),
	},
	localAuthAvailable: false,
	openDialog: vi.fn(),
	billing: { offering: { selfServeBilling: false }, data: undefined as unknown },
}));

vi.mock("../../lib/cloud-session", () => ({ useCloudSession: () => mocks.session }));
vi.mock("../../hooks/useCloudLocalAuth", () => ({ useCloudLocalAuth: () => ({ available: mocks.localAuthAvailable }) }));
vi.mock("../../stores/local-signin-dialog-store", () => ({
	useLocalSignInDialogStore: (select: (state: { openDialog: () => void }) => unknown) => select({ openDialog: mocks.openDialog }),
}));
vi.mock("../../hooks/useCloudBilling", () => ({
	useCloudBilling: () => mocks.billing,
	paidPlanBadge: (summary: { plan?: string } | undefined) => (summary?.plan ? "Pro" : null),
}));

beforeEach(() => {
	mocks.session.configured = true;
	mocks.session.status = "unauthenticated";
	mocks.session.session = null;
	mocks.session.signIn.mockReset();
	mocks.session.signOut.mockReset().mockResolvedValue(undefined);
	mocks.localAuthAvailable = false;
	mocks.openDialog.mockReset();
	mocks.billing = { offering: { selfServeBilling: false }, data: undefined };
});

describe("AccountSettingsSection", () => {
	it("offers sign-in while signed out", async () => {
		render(<AccountSettingsSection />);

		expect(screen.getByRole("heading", { name: "Account" })).toBeInTheDocument();
		expect(screen.getByText("Orchestrator Cloud")).toBeInTheDocument();
		expect(screen.getByText("Sign in to use the mobile app, remote machines and cloud sandboxes.")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
		expect(mocks.session.signIn).toHaveBeenCalledOnce();
		expect(mocks.openDialog).not.toHaveBeenCalled();
	});

	it("opens the local sign-in dialog against a local control plane", async () => {
		mocks.localAuthAvailable = true;
		render(<AccountSettingsSection />);

		await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
		expect(mocks.openDialog).toHaveBeenCalledOnce();
		expect(mocks.session.signIn).not.toHaveBeenCalled();
	});

	it("shows the signed-in account with its plan and signs out", async () => {
		mocks.session.status = "authenticated";
		mocks.session.session = { user: { email: "ada@example.com", displayName: "Ada Park" } };
		mocks.billing = { offering: { selfServeBilling: true }, data: { plan: "pro" } };
		render(<AccountSettingsSection />);

		expect(screen.getByText("Ada Park")).toBeInTheDocument();
		expect(screen.getByText("ada@example.com")).toBeInTheDocument();
		expect(screen.getByText("Pro")).toBeInTheDocument();
		expect(screen.getByText("AP")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Sign in" })).toBeNull();
		await userEvent.click(screen.getByRole("button", { name: "Sign out" }));
		expect(mocks.session.signOut).toHaveBeenCalledOnce();
	});

	it("names a signed-in account without a display name by its email once", () => {
		mocks.session.status = "authenticated";
		mocks.session.session = { user: { email: "ada@example.com", displayName: "" } };
		render(<AccountSettingsSection />);

		expect(screen.getAllByText("ada@example.com")).toHaveLength(1);
		expect(screen.getByText("A")).toBeInTheDocument();
	});

	it("renders nothing while the session loads or without sign-in", () => {
		mocks.session.status = "loading";
		const { container, rerender } = render(<AccountSettingsSection />);
		expect(container).toBeEmptyDOMElement();

		mocks.session.status = "unauthenticated";
		mocks.session.configured = false;
		rerender(<AccountSettingsSection />);
		expect(container).toBeEmptyDOMElement();
	});
});
