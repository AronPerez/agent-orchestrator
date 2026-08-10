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

async function fillAndSubmit(address = "http://192.0.2.1:3011") {
	await userEvent.type(screen.getByLabelText(/name/i), "workbox");
	// userEvent reads "[" and "{" as key descriptors; doubling them types the
	// literal character, which is what a bracketed IPv6 address needs.
	await userEvent.type(screen.getByLabelText(/address/i), address.replace(/[[{]/g, "$&$&"));
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

	// A bare "host:port" is what people type. It used to reach fetch() unparsed,
	// throw, and come back as "could not reach that host" — sending someone to
	// debug a network when they had only omitted a scheme.
	describe("address normalization", () => {
		const saved = ["schemeless host and port", "192.168.1.250:3011", "http://192.168.1.250:3011"] as const;
		const cases: Array<readonly [string, string, string]> = [
			saved,
			["schemeless host", "workbox", "http://workbox"],
			["bracketed IPv6 and port", "[fe80::1]:3011", "http://[fe80::1]:3011"],
			["an already-schemed url, left alone", "http://192.0.2.1:3011", "http://192.0.2.1:3011"],
			["https, left alone", "https://box.example:3011", "https://box.example:3011"],
			["a trailing slash", "http://192.0.2.1:3011/", "http://192.0.2.1:3011"],
		];

		for (const [name, typed, expected] of cases) {
			it(`saves ${name} as ${expected}`, async () => {
				const onAdded = vi.fn();
				render(<AddRemoteHostDialog open onOpenChange={vi.fn()} onAdded={onAdded} />);
				await fillAndSubmit(typed);
				expect(addMock).toHaveBeenCalledWith({ label: "workbox", url: expected, password: "pw" });
				expect(onAdded).toHaveBeenCalledWith(expected);
			});
		}

		it("shows the address it will actually save before connecting", async () => {
			render(<AddRemoteHostDialog open onOpenChange={vi.fn()} onAdded={vi.fn()} />);
			await userEvent.type(screen.getByLabelText(/address/i), "192.168.1.250:3011");
			expect(screen.getByText(/http:\/\/192\.168\.1\.250:3011/)).toBeInTheDocument();
		});

		it("stays quiet when the typed address is already the saved one", async () => {
			render(<AddRemoteHostDialog open onOpenChange={vi.fn()} onAdded={vi.fn()} />);
			await userEvent.type(screen.getByLabelText(/address/i), "http://192.0.2.1:3011");
			expect(screen.queryByText(/will connect to/i)).not.toBeInTheDocument();
		});
	});

	// The whole point of the split: a typo and a silent host must not share a
	// sentence, because they send the user to different places.
	it("blames the address, not the network, when the address cannot name a host", async () => {
		render(<AddRemoteHostDialog open onOpenChange={vi.fn()} onAdded={vi.fn()} />);
		await fillAndSubmit("not a url");

		const alert = await screen.findByRole("alert");
		expect(alert).toHaveTextContent(/not a valid address/i);
		expect(alert).not.toHaveTextContent(/could not reach/i);
		// Never probed: there was nothing to probe.
		expect(addMock).not.toHaveBeenCalled();
	});

	it("rejects a scheme that is not http or https", async () => {
		render(<AddRemoteHostDialog open onOpenChange={vi.fn()} onAdded={vi.fn()} />);
		await fillAndSubmit("ftp://192.0.2.1");
		expect(await screen.findByRole("alert")).toHaveTextContent(/not a valid address/i);
		expect(addMock).not.toHaveBeenCalled();
	});

	it("announces the probe instead of only grinding to a disabled button", async () => {
		let release: (health: string) => void = () => {};
		addMock.mockReturnValue(
			new Promise<string>((resolve) => {
				release = resolve;
			}),
		);
		render(<AddRemoteHostDialog open onOpenChange={vi.fn()} onAdded={vi.fn()} />);
		await fillAndSubmit();

		// Conveyed as text in a live region, not by a disabled button alone.
		expect(await screen.findByRole("status")).toHaveTextContent(/connecting/i);
		expect(screen.getByRole("button", { name: /connect/i })).toHaveAttribute("aria-busy", "true");

		release("online");
	});

	it("drops a stale error as soon as the input that caused it changes", async () => {
		addMock.mockResolvedValue("unauthorized");
		render(<AddRemoteHostDialog open onOpenChange={vi.fn()} onAdded={vi.fn()} />);
		await fillAndSubmit();
		expect(await screen.findByRole("alert")).toHaveTextContent(/password/i);

		await userEvent.type(screen.getByLabelText(/password/i), "2");
		// A rejection left standing over a corrected password reads as a second one.
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
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
