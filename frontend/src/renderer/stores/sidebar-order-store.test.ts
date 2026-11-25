import { beforeEach, expect, it, vi } from "vitest";

beforeEach(() => {
	window.localStorage.clear();
	vi.resetModules();
});

it("restores the saved drag and creation order after a renderer restart", async () => {
	const { useSidebarOrderStore } = await import("./sidebar-order-store");
	useSidebarOrderStore.getState().setProjectOrder(["beta", "alpha"]);
	useSidebarOrderStore.getState().moveProjectToTop("zulu");

	vi.resetModules();
	const restarted = await import("./sidebar-order-store");
	expect(restarted.useSidebarOrderStore.getState().projectOrder).toEqual(["zulu", "beta", "alpha"]);
	restarted.useSidebarOrderStore.getState().reconcileProjectOrder(["alpha", "beta", "older-cloud", "zulu"]);
	expect(restarted.useSidebarOrderStore.getState().projectOrder).toEqual(["zulu", "older-cloud", "beta", "alpha"]);
	expect(restarted.useSidebarOrderStore.getState().pendingCreatedProjectIds).toEqual([]);
});

it("keeps creation priority through partial snapshots until the new project arrives", async () => {
	const { useSidebarOrderStore } = await import("./sidebar-order-store");
	const store = useSidebarOrderStore.getState();
	store.setProjectOrder(["beta", "alpha"]);
	store.moveProjectToTop("zulu");
	store.reconcileProjectOrder(["alpha", "beta", "older-cloud"]);
	expect(useSidebarOrderStore.getState().projectOrder).toEqual(["zulu", "older-cloud", "beta", "alpha"]);
	expect(useSidebarOrderStore.getState().pendingCreatedProjectIds).toEqual(["zulu"]);
	store.reconcileProjectOrder(["alpha", "beta", "older-cloud", "zulu"]);
	expect(useSidebarOrderStore.getState().pendingCreatedProjectIds).toEqual([]);
	store.reconcileProjectOrder(["newer-project", "alpha", "beta", "older-cloud", "zulu"]);
	expect(useSidebarOrderStore.getState().projectOrder).toEqual(["newer-project", "zulu", "older-cloud", "beta", "alpha"]);
});

it("lets a manual reorder replace pending creation priority", async () => {
	const { useSidebarOrderStore } = await import("./sidebar-order-store");
	const store = useSidebarOrderStore.getState();
	store.setProjectOrder(["beta", "alpha"]);
	store.moveProjectToTop("zulu");
	store.setProjectOrder(["beta", "zulu", "alpha"]);
	store.reconcileProjectOrder(["alpha", "beta", "zulu"]);
	expect(useSidebarOrderStore.getState().projectOrder).toEqual(["beta", "zulu", "alpha"]);
	expect(useSidebarOrderStore.getState().pendingCreatedProjectIds).toEqual([]);
	expect(window.localStorage.getItem("ao.sidebar.pending-created-projects")).toBeNull();
});

it("keeps creation order when newer projects appear before older pending projects", async () => {
	const { useSidebarOrderStore } = await import("./sidebar-order-store");
	const store = useSidebarOrderStore.getState();
	store.setProjectOrder(["alpha"]);
	store.moveProjectToTop("first-created");
	store.moveProjectToTop("second-created");
	store.reconcileProjectOrder(["alpha", "second-created"]);
	store.reconcileProjectOrder(["alpha", "second-created", "older-cloud"]);
	expect(useSidebarOrderStore.getState().projectOrder).toEqual(["second-created", "first-created", "older-cloud", "alpha"]);
	store.reconcileProjectOrder(["alpha", "second-created", "first-created", "older-cloud"]);
	expect(useSidebarOrderStore.getState().projectOrder).toEqual(["second-created", "first-created", "older-cloud", "alpha"]);
	expect(useSidebarOrderStore.getState().pendingCreatedProjectIds).toEqual([]);
});

it.each([
	{ saved: "invalid JSON", expected: [] },
	{ saved: '{"unexpected":true}', expected: [] },
	{ saved: '[4,null,"beta","alpha","beta"]', expected: ["beta", "alpha"] },
])("validates the saved project order at startup: $saved", async ({ saved, expected }) => {
	window.localStorage.setItem("ao.sidebar.project-order", saved);
	const { useSidebarOrderStore } = await import("./sidebar-order-store");
	expect(useSidebarOrderStore.getState().projectOrder).toEqual(expected);
});

it("pins projects to the top and restores the pins after a renderer restart", async () => {
	const { useSidebarOrderStore } = await import("./sidebar-order-store");
	const store = useSidebarOrderStore.getState();
	store.setProjectOrder(["alpha", "beta", "gamma", "delta"]);
	store.setProjectPinned("gamma", true);
	store.setProjectPinned("delta", true);
	expect(useSidebarOrderStore.getState().pinnedProjectIds).toEqual(["gamma", "delta"]);
	expect(useSidebarOrderStore.getState().projectOrder).toEqual(["gamma", "delta", "alpha", "beta"]);

	store.setProjectPinned("gamma", false);
	expect(useSidebarOrderStore.getState().pinnedProjectIds).toEqual(["delta"]);
	expect(useSidebarOrderStore.getState().projectOrder).toEqual(["delta", "gamma", "alpha", "beta"]);

	vi.resetModules();
	const restarted = await import("./sidebar-order-store");
	expect(restarted.useSidebarOrderStore.getState().pinnedProjectIds).toEqual(["delta"]);
});

it("hides a project on one machine only and restores the hidden list after a renderer restart", async () => {
	const { HIDDEN_PROJECTS_STORAGE_KEY, hiddenProjectKey, useSidebarOrderStore } = await import("./sidebar-order-store");
	const store = useSidebarOrderStore.getState();
	store.setProjectHidden("skyvern-cloud", "https://m4-mini.example:3001", true);
	store.setProjectHidden("cinder-cloud", undefined, true);
	// Hiding twice does not add a second entry.
	store.setProjectHidden("cinder-cloud", undefined, true);

	const m4 = hiddenProjectKey("skyvern-cloud", "https://m4-mini.example:3001");
	const hidden = useSidebarOrderStore.getState().hiddenProjectKeys;
	expect(hidden).toEqual([m4, "cinder-cloud"]);
	// The same project id here, on the local host, and on another machine stays listed.
	expect(hidden).not.toContain(hiddenProjectKey("skyvern-cloud", "https://m6-mini.example:3001"));
	expect(hidden).not.toContain(hiddenProjectKey("skyvern-cloud"));
	expect(hidden).not.toContain(hiddenProjectKey("skyvern-cloud", "local"));
	expect(hiddenProjectKey("cinder-cloud", "local")).toBe("cinder-cloud");
	// Hiding is a list preference: order and pins are left alone.
	expect(useSidebarOrderStore.getState().projectOrder).toEqual([]);
	expect(useSidebarOrderStore.getState().pinnedProjectIds).toEqual([]);
	expect(JSON.parse(window.localStorage.getItem(HIDDEN_PROJECTS_STORAGE_KEY) ?? "null")).toEqual([m4, "cinder-cloud"]);

	vi.resetModules();
	const restarted = await import("./sidebar-order-store");
	expect(restarted.useSidebarOrderStore.getState().hiddenProjectKeys).toEqual([m4, "cinder-cloud"]);

	restarted.useSidebarOrderStore.getState().setProjectHidden("skyvern-cloud", "https://m4-mini.example:3001", false);
	expect(restarted.useSidebarOrderStore.getState().hiddenProjectKeys).toEqual(["cinder-cloud"]);
	restarted.useSidebarOrderStore.getState().setProjectHidden("cinder-cloud", undefined, false);
	expect(restarted.useSidebarOrderStore.getState().hiddenProjectKeys).toEqual([]);
	expect(window.localStorage.getItem(HIDDEN_PROJECTS_STORAGE_KEY)).toBeNull();
});

it("keeps a pin through hiding, so the project returns to the pinned group", async () => {
	const { useSidebarOrderStore } = await import("./sidebar-order-store");
	const store = useSidebarOrderStore.getState();
	store.setProjectOrder(["alpha", "beta"]);
	store.setProjectPinned("beta", true);
	store.setProjectHidden("beta", undefined, true);
	store.setProjectHidden("beta", undefined, false);
	expect(useSidebarOrderStore.getState().pinnedProjectIds).toEqual(["beta"]);
	expect(useSidebarOrderStore.getState().projectOrder).toEqual(["beta", "alpha"]);
});

it("ignores a malformed saved hidden list", async () => {
	window.localStorage.setItem("ao.sidebar.hidden-projects", '[1,"remote:h:p",null,"remote:h:p"]');
	const { useSidebarOrderStore } = await import("./sidebar-order-store");
	expect(useSidebarOrderStore.getState().hiddenProjectKeys).toEqual(["remote:h:p"]);
});
