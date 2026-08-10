import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { HostSelect } from "./HostSelect";
import { LOCAL_HOST_ID, type Host } from "../hooks/useRemoteHosts";

const hosts: Host[] = [
	{ id: LOCAL_HOST_ID, label: "This Mac", url: null, status: "local" },
	{ id: "http://192.0.2.1:3011", label: "workbox", url: "http://192.0.2.1:3011", status: "online" },
	{ id: "http://192.0.2.9:3011", label: "mini", url: "http://192.0.2.9:3011", status: "offline" },
];

describe("HostSelect", () => {
	it("shows the selected host's label on the trigger", () => {
		render(<HostSelect hosts={hosts} value={LOCAL_HOST_ID} onChange={vi.fn()} onAddHost={vi.fn()} />);
		expect(screen.getByRole("combobox")).toHaveTextContent("This Mac");
	});

	it("reports the chosen host id", async () => {
		const onChange = vi.fn();
		render(<HostSelect hosts={hosts} value={LOCAL_HOST_ID} onChange={onChange} onAddHost={vi.fn()} />);
		await userEvent.click(screen.getByRole("combobox"));
		await userEvent.click(screen.getByRole("option", { name: /workbox/ }));
		expect(onChange).toHaveBeenCalledWith("http://192.0.2.1:3011");
	});

	it("offers adding a host", async () => {
		const onAddHost = vi.fn();
		render(<HostSelect hosts={hosts} value={LOCAL_HOST_ID} onChange={vi.fn()} onAddHost={onAddHost} />);
		await userEvent.click(screen.getByRole("combobox"));
		await userEvent.click(screen.getByRole("option", { name: /add remote host/i }));
		expect(onAddHost).toHaveBeenCalled();
	});

	it("does not let an offline host be selected", async () => {
		const onChange = vi.fn();
		render(<HostSelect hosts={hosts} value={LOCAL_HOST_ID} onChange={onChange} onAddHost={vi.fn()} />);
		await userEvent.click(screen.getByRole("combobox"));
		expect(screen.getByRole("option", { name: /mini/ })).toHaveAttribute("aria-disabled", "true");
	});

	it("states each remote's status as text, not colour alone", async () => {
		render(<HostSelect hosts={hosts} value={LOCAL_HOST_ID} onChange={vi.fn()} onAddHost={vi.fn()} />);
		await userEvent.click(screen.getByRole("combobox"));
		expect(screen.getByRole("option", { name: /mini/ })).toHaveTextContent(/disconnected/i);
	});

	it("offers Edit and Remove on each saved host, naming which one", async () => {
		const onEditHost = vi.fn();
		const onRemoveHost = vi.fn();
		const onChange = vi.fn();
		render(
			<HostSelect
				hosts={hosts}
				value={LOCAL_HOST_ID}
				onChange={onChange}
				onAddHost={vi.fn()}
				onEditHost={onEditHost}
				onRemoveHost={onRemoveHost}
			/>,
		);
		await userEvent.click(screen.getByRole("combobox"));

		// "Edit" alone would be three identical buttons to a screen reader.
		await userEvent.click(screen.getByRole("button", { name: /edit workbox/i }));
		expect(onEditHost).toHaveBeenCalledWith({ label: "workbox", url: "http://192.0.2.1:3011" });

		// Both actions close the list, because both open a dialog on top of it.
		expect(screen.queryByRole("button", { name: /remove mini/i })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("combobox"));
		await userEvent.click(screen.getByRole("button", { name: /remove mini/i }));
		expect(onRemoveHost).toHaveBeenCalledWith({ label: "mini", url: "http://192.0.2.9:3011" });
		// Neither action may select the row it sits on.
		expect(onChange).not.toHaveBeenCalled();
	});

	// An unreachable host is exactly the one that needs editing, so its actions
	// must survive the row being disabled.
	it("keeps Edit and Remove usable on a host that cannot be reached", async () => {
		const onEditHost = vi.fn();
		render(
			<HostSelect
				hosts={hosts}
				value={LOCAL_HOST_ID}
				onChange={vi.fn()}
				onAddHost={vi.fn()}
				onEditHost={onEditHost}
				onRemoveHost={vi.fn()}
			/>,
		);
		await userEvent.click(screen.getByRole("combobox"));
		await userEvent.click(screen.getByRole("button", { name: /edit mini/i }));
		expect(onEditHost).toHaveBeenCalledWith({ label: "mini", url: "http://192.0.2.9:3011" });
	});

	it("offers neither on This Mac, which is not a saved host", async () => {
		render(
			<HostSelect
				hosts={hosts}
				value={LOCAL_HOST_ID}
				onChange={vi.fn()}
				onAddHost={vi.fn()}
				onEditHost={vi.fn()}
				onRemoveHost={vi.fn()}
			/>,
		);
		await userEvent.click(screen.getByRole("combobox"));
		expect(screen.queryByRole("button", { name: /this mac/i })).not.toBeInTheDocument();
	});

	it("offers a Connect action on an unreachable host and re-probes it", async () => {
		const onReconnect = vi.fn();
		const onChange = vi.fn();
		render(
			<HostSelect
				hosts={hosts}
				value={LOCAL_HOST_ID}
				onChange={onChange}
				onAddHost={vi.fn()}
				onReconnect={onReconnect}
			/>,
		);
		await userEvent.click(screen.getByRole("combobox"));
		await userEvent.click(screen.getByRole("button", { name: /connect/i }));
		expect(onReconnect).toHaveBeenCalledWith("http://192.0.2.9:3011");
		// The row itself must not get selected by clicking its inline action. The
		// list stays open so the re-probe is visible in place, and Radix aria-hides
		// the trigger while it is — hence `hidden: true` rather than a plain query.
		expect(onChange).not.toHaveBeenCalled();
		expect(screen.getByRole("combobox", { hidden: true })).toHaveTextContent("This Mac");
	});
});
