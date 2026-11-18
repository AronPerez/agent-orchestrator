import AsyncStorage from "@react-native-async-storage/async-storage";
import {
	limitFleetCollapsePreferences,
	parseFleetCollapsePreferences,
	type FleetCollapsePreferences,
} from "../../product-ui/src/fleet-tree";

export const FLEET_COLLAPSE_STORAGE_KEY = "ao.fleet.branch-state.v1";

export async function readFleetCollapsePreferences(): Promise<Record<string, boolean>> {
	const raw = await AsyncStorage.getItem(FLEET_COLLAPSE_STORAGE_KEY).catch(() => null);
	return parseFleetCollapsePreferences(raw);
}

export async function writeFleetCollapsePreferences(preferences: FleetCollapsePreferences): Promise<void> {
	await AsyncStorage.setItem(
		FLEET_COLLAPSE_STORAGE_KEY,
		JSON.stringify(limitFleetCollapsePreferences(preferences)),
	).catch(() => {});
}
