import AsyncStorage from "@react-native-async-storage/async-storage";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
	FLEET_COLLAPSE_STORAGE_KEY,
	readFleetCollapsePreferences,
	writeFleetCollapsePreferences,
} from "./fleet-collapse-store";

const storage = vi.hoisted(() => ({
	getItem: vi.fn(async () => null as string | null),
	setItem: vi.fn(async () => {}),
}));

vi.mock("@react-native-async-storage/async-storage", () => ({ default: storage }));

describe("fleet collapse storage", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		storage.getItem.mockResolvedValue(null);
	});

	it("restores qualified boolean preferences and ignores invalid values", async () => {
		vi.mocked(AsyncStorage.getItem).mockResolvedValueOnce(JSON.stringify({ local: true, remote: false, bad: "yes" }));
		await expect(readFleetCollapsePreferences()).resolves.toEqual({ local: true, remote: false });
		expect(AsyncStorage.getItem).toHaveBeenCalledWith(FLEET_COLLAPSE_STORAGE_KEY);
	});

	it("writes the complete preference record under AO mobile state", async () => {
		await writeFleetCollapsePreferences({ '["project","mac","ao"]': false });
		expect(AsyncStorage.setItem).toHaveBeenCalledWith(
			FLEET_COLLAPSE_STORAGE_KEY,
			JSON.stringify({ '["project","mac","ao"]': false }),
		);
	});

	it("bounds oversized preference records when writing", async () => {
		await writeFleetCollapsePreferences(Object.fromEntries(
			Array.from({ length: 2_001 }, (_, index) => [String(index), true]),
		));
		const saved = JSON.parse(vi.mocked(AsyncStorage.setItem).mock.calls.at(-1)![1]);
		expect(Object.keys(saved)).toHaveLength(2_000);
		expect(saved["0"]).toBeUndefined();
		expect(saved["2000"]).toBe(true);
	});
});
