import * as SecureStore from "expo-secure-store";
import { Platform } from "react-native";

// Platform-aware secret storage. On native, secrets (the daemon connection
// password, push registration) live in the device keystore (iOS Keychain /
// Android Keystore) via expo-secure-store — which is NOT implemented on web
// (calls like setItemAsync throw "…is not a function").
//
// The web target runs in a browser with no secure keystore, so it falls back to
// localStorage: origin-scoped, and the same durability the non-secret config
// already gets from AsyncStorage on web. This is a deliberate web-only trade-off
// (plaintext-at-rest for a LAN/Tailscale daemon password) — the alternative,
// in-memory-only, would force the user to re-enter the password on every reload.
// Native storage is unchanged and still keystore-backed.
const isWeb = Platform.OS === "web";

export async function secureGetItem(key: string): Promise<string | null> {
	if (isWeb) {
		try {
			return typeof localStorage !== "undefined" ? localStorage.getItem(key) : null;
		} catch {
			return null;
		}
	}
	return SecureStore.getItemAsync(key);
}

export async function secureSetItem(key: string, value: string): Promise<void> {
	if (isWeb) {
		try {
			if (typeof localStorage !== "undefined") localStorage.setItem(key, value);
		} catch {
			// best-effort: a full/blocked localStorage shouldn't crash the connect flow
		}
		return;
	}
	await SecureStore.setItemAsync(key, value);
}

export async function secureDeleteItem(key: string): Promise<void> {
	if (isWeb) {
		try {
			if (typeof localStorage !== "undefined") localStorage.removeItem(key);
		} catch {
			// best-effort
		}
		return;
	}
	await SecureStore.deleteItemAsync(key);
}
