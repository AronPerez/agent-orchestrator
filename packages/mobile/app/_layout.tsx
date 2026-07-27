import { Stack } from "expo-router";
import { StatusBar } from "expo-status-bar";
import { Platform } from "react-native";
import { SafeAreaProvider } from "react-native-safe-area-context";
import { PushManager } from "../lib/PushManager";
import { AppProvider } from "../lib/store";
import { theme } from "../lib/theme";

export default function RootLayout() {
	return (
		<SafeAreaProvider>
			<AppProvider>
				<StatusBar style="light" />
				{/* Push notifications are native-only: expo-notifications' response
				    APIs (getLastNotificationResponseAsync, the response listener) are
				    not implemented on web, so mounting PushManager there throws. The
				    web target has no push, so skip it entirely. */}
				{Platform.OS !== "web" ? <PushManager /> : null}
				<Stack
					screenOptions={{
						headerStyle: { backgroundColor: theme.bgSurface },
						headerTintColor: theme.textPrimary,
						headerTitleStyle: { fontWeight: "700" },
						headerShadowVisible: false,
						contentStyle: { backgroundColor: theme.bgBase },
					}}
				>
					<Stack.Screen name="(tabs)" options={{ headerShown: false }} />
					<Stack.Screen name="session/[id]" options={{ title: "Terminal", headerBackTitle: "Back" }} />
					<Stack.Screen name="spawn" options={{ presentation: "modal", title: "New agent" }} />
					<Stack.Screen name="pair" options={{ presentation: "modal", title: "Scan pairing code" }} />
				</Stack>
			</AppProvider>
		</SafeAreaProvider>
	);
}
