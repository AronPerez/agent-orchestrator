import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AddRemoteHostDialog } from "./AddRemoteHostDialog";

const { addMock } = vi.hoisted(() => ({ addMock: vi.fn() }));

// The bridge's `remotes` surface lands with the IPC task; mock the module
// rather than spying on a stub that does not exist yet.
vi.mock("../lib/bridge", () => ({
	aoBridge: { remotes: { add: addMock } },
}));

beforeEach(() => {
	addMock.mockReset();
	addMock.mockResolvedValue("online");
});

async function fillAndSubmit() {
	await userEvent.type(screen.getByLabelText(/name/i), "workbox");
	await userEvent.type(screen.getByLabelText(/address/i), "http://192.0.2.1:3011");
	await userEvent.type(screen.getByLabelText(/password/i), "pw");
	await userEvent.click(screen.getByRole("button", { name: /connect/i }));
}

describe("AddRemoteHostDialog", () => {
	it("saves and reports the new host when it answers", async () => {
		const onAdded = vi.fn();
		render(<AddRemoteHostDialog open onOpenChange={vi.fn()} onAdded={onAdded} />);
		await fillAndSubmit();
		expect(addMock).toHaveBeenCalledWith({ label: "workbox", url: "http://192.0.2.1:3011", password: "pw" });
		expect(onAdded).toHaveBeenCalledWith("http://192.0.2.1:3011");
	});

	it("distinguishes a wrong password from an unreachable host", async () => {
		addMock.mockResolvedValue("unauthorized");
		const onAdded = vi.fn();
		render(<AddRemoteHostDialog open onOpenChange={vi.fn()} onAdded={onAdded} />);
		await fillAndSubmit();
		expect(await screen.findByRole("alert")).toHaveTextContent(/password/i);
		expect(onAdded).not.toHaveBeenCalled();
	});

	it("says the host is unreachable when it does not answer", async () => {
		addMock.mockResolvedValue("offline");
		render(<AddRemoteHostDialog open onOpenChange={vi.fn()} onAdded={vi.fn()} />);
		await fillAndSubmit();
		expect(await screen.findByRole("alert")).toHaveTextContent(/could not reach/i);
	});

	it("rejects a url carrying an embedded credential, as the CLI does", async () => {
		render(<AddRemoteHostDialog open onOpenChange={vi.fn()} onAdded={vi.fn()} />);
		await userEvent.type(screen.getByLabelText(/name/i), "bad");
		await userEvent.type(screen.getByLabelText(/address/i), "http://user:pw@192.0.2.1:3011");
		await userEvent.type(screen.getByLabelText(/password/i), "pw");
		await userEvent.click(screen.getByRole("button", { name: /connect/i }));
		expect(await screen.findByRole("alert")).toHaveTextContent(/must not carry a username or password/i);
		expect(addMock).not.toHaveBeenCalled();
	});
});
