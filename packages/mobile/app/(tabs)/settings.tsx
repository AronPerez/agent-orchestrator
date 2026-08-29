import { Feather } from "@expo/vector-icons";
import * as Application from "expo-application";
import Constants from "expo-constants";
import { useFocusEffect, useRouter } from "expo-router";
import * as Updates from "expo-updates";
import { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Alert, Platform, Pressable, ScrollView, StyleSheet, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { ApiError, pingServer } from "../../lib/api";
import { bugReportBody, formatVersionLine, type BuildInfo } from "../../lib/appInfo";
import {
	DEFAULT_CONFIG,
	isConfigured,
	listServers,
	loadConfig,
	removeServer,
	switchServer,
	type ServerConfig,
	type ServerEntry,
} from "../../lib/config";
import { classifyConnectionFailure, describeConnectionFailure } from "../../lib/connectionError";
import { forgetServer } from "../../lib/disconnect";
import type { Theme } from "../../lib/theme";
import { haptics } from "../../lib/haptics";
import { connectSheetRoute, projectSheetRoute } from "../../lib/sheetResult";
import { preferenceLabel } from "../../lib/themePreference";
import { getPushStatus, openNotificationSettings, registerForPush, unregisterFromPush } from "../../lib/push";
import { describePushToggle, describeRegisterFailure, type PushStatus } from "../../lib/pushStatus";
import { openGitHub } from "../../lib/openGitHub";
import { useApp } from "../../lib/store";
import { checkAndDownload, describeUpdateRow, type UpdateOutcome } from "../../lib/updates";
import { useTabScrollToTop } from "../../lib/useTabScrollToTop";
import { Dot, ScreenHeader, SettingsGroup, SettingsRow, SettingsToggle } from "../../lib/ui";
import { useTheme, useThemedStyles, useThemeState } from "../../lib/ThemeProvider";

const ISSUES_URL = "https://github.com/AgentWrapper/agent-orchestrator/issues/new";

export default function SettingsScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();
	const router = useRouter();
	const { reloadConfig, projects, connection, activeProjectId, setActiveProject } = useApp();
	const scrollRef = useTabScrollToTop<ScrollView>();

	const [cfg, setCfg] = useState<ServerConfig>(DEFAULT_CONFIG);
	const [servers, setServers] = useState<ServerEntry[]>([]);
	const [activeId, setActiveId] = useState<string | null>(null);
	const [loaded, setLoaded] = useState(false);
	const { preference } = useThemeState();

	const refresh = useCallback(async () => {
		const [c, list] = await Promise.all([loadConfig(), listServers()]);
		setCfg(c);
		setServers(list.servers);
		setActiveId(list.activeId);
		setLoaded(true);
	}, []);

	// Reload the saved config every time the screen regains focus — not just on
	// mount — so returning from the pairing flow (which writes host/port to
	// storage then navigates back here) repaints the rows with the new values.
	useFocusEffect(
		useCallback(() => {
			void refresh();
		}, [refresh]),
	);

	// Switching nodes swaps the whole world behind the app: the store restarts its
	// poll off the new config, and push re-registers itself (registerForPush
	// unregisters the daemon it was last pointed at). Project ids are per-node, so
	// the saved filter has to go back to "all" or the board filters on an id the
	// new node has never heard of.
	const applyActiveChange = useCallback(async () => {
		setActiveProject("all");
		await reloadConfig();
		await refresh();
	}, [reloadConfig, refresh, setActiveProject]);

	if (!loaded) {
		return (
			<View style={styles.center}>
				<ActivityIndicator color={t.blue} />
			</View>
		);
	}

	const paired = isConfigured(cfg);
	const activeProject = projects.find((p) => p.id === activeProjectId);

	return (
		<View style={styles.screen}>
			<View style={{ height: insets.top }} />
			<ScreenHeader title="Settings" status={connection} />
			<ScrollView
				ref={scrollRef}
				contentContainerStyle={{ padding: 16, paddingBottom: 120 }}
				keyboardShouldPersistTaps="handled"
			>
				<ConnectionSection cfg={cfg} paired={paired} connection={connection} />

				<ServersSection
					servers={servers}
					activeId={activeId}
					onSwitch={async (id) => {
						await switchServer(id);
						await applyActiveChange();
					}}
					onRemove={async (entry) => {
						// Removing the node we're on is a disconnect: forgetServer is the
						// path that also unregisters this device from that daemon's push,
						// and clearConfig promotes whatever node is left.
						if (entry.id === activeId) {
							await forgetServer();
							await applyActiveChange();
							return;
						}
						await removeServer(entry.id);
						await refresh();
					}}
					onAdd={() => router.push(connectSheetRoute(() => void applyActiveChange()))}
				/>

				<SettingsGroup title="Projects" footer="Scopes the Agents and PRs tabs.">
					<SettingsRow
						icon="folder"
						label="Active project"
						value={activeProject?.name ?? "All projects"}
						onPress={() =>
							router.push(
								projectSheetRoute({
									selected: activeProjectId,
									onSelect: (id) => {
										// Picking a project scopes the board and takes you there —
										// the choice and its effect land in one step.
										setActiveProject(id);
										router.navigate("/");
									},
								}),
							)
						}
					/>
				</SettingsGroup>

				<SettingsGroup title="Appearance">
					<SettingsRow
						icon="moon"
						label="Theme"
						value={preferenceLabel(preference)}
						onPress={() => router.push("/sheets/theme")}
					/>
				</SettingsGroup>

				<NotificationsSection />

				<AboutSection
					onForget={async () => {
						await forgetServer();
						await applyActiveChange();
						// Forgetting the active node promotes the next one, so only an app
						// left with no nodes at all belongs back in onboarding.
						if (!isConfigured(await loadConfig())) router.replace("/onboarding");
					}}
				/>
			</ScrollView>
		</View>
	);
}

// The node switcher. One `ao` runs per machine, so a user with ten of them needs
// to say which one the app is looking at — and nothing else in the app has to
// know: switching rewrites the active config and the store repolls off it.
//
// Settings-only by decision: a header switcher would put a destructive-feeling
// context swap one stray tap from the board.
function ServersSection({
	servers,
	activeId,
	onSwitch,
	onRemove,
	onAdd,
}: {
	servers: ServerEntry[];
	activeId: string | null;
	onSwitch: (id: string) => Promise<void>;
	onRemove: (entry: ServerEntry) => Promise<void>;
	onAdd: () => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [busy, setBusy] = useState<string | null>(null);

	// Nothing saved yet means an unpaired app, where the Connection row above is
	// already the way in — a second empty list would just be noise.
	if (servers.length === 0) return null;

	function confirmRemove(entry: ServerEntry) {
		const active = entry.id === activeId;
		Alert.alert(
			`Remove ${entry.label}?`,
			active
				? "This device will stop receiving notifications from it, and its saved address and password will be removed."
				: "Its saved address and password will be removed from this device.",
			[
				{ text: "Cancel", style: "cancel" },
				{
					text: "Remove",
					style: "destructive",
					onPress: async () => {
						setBusy(entry.id);
						try {
							await onRemove(entry);
						} finally {
							setBusy(null);
						}
					},
				},
			],
		);
	}

	return (
		<SettingsGroup title="Servers" footer="Each node runs its own ao. Tap one to switch the app to it.">
			{servers.map((s) => (
				<SettingsRow
					key={s.id}
					icon="server"
					label={`${s.label} · ${s.host}:${s.httpPort}`}
					loading={busy === s.id}
					disabled={!!busy}
					onPress={async () => {
						if (s.id === activeId) return;
						setBusy(s.id);
						try {
							await onSwitch(s.id);
						} finally {
							setBusy(null);
						}
					}}
					right={
						busy === s.id ? (
							<ActivityIndicator size="small" color={t.textTertiary} />
						) : (
							<View style={styles.serverActions}>
								{s.id === activeId ? <Feather name="check" size={17} color={t.blue} /> : null}
								<Pressable
									hitSlop={10}
									accessibilityRole="button"
									accessibilityLabel={`Remove ${s.label}`}
									onPress={() => confirmRemove(s)}
									style={({ pressed }) => pressed && { opacity: 0.6 }}
								>
									<Feather name="trash-2" size={16} color={t.red} />
								</Pressable>
							</View>
						)
					}
				/>
			))}
			<SettingsRow icon="plus" label="Add server" disabled={!!busy} onPress={onAdd} />
		</SettingsGroup>
	);
}

// Connection is one row, not a form. `/pair` already owns the whole flow —
// camera scan, permission fallbacks, and the "Enter details manually" sheet that
// opens prefilled from the saved config — so editing a connection and creating
// one go through the same door instead of two divergent forms.
function ConnectionSection({
	cfg,
	paired,
	connection,
}: {
	cfg: ServerConfig;
	paired: boolean;
	connection: string;
}) {
	const t = useTheme();
	const router = useRouter();
	const [testing, setTesting] = useState(false);
	const [result, setResult] = useState<{ ok: boolean; msg: string } | null>(null);

	// Drop a stale failure once the background poller reports a live connection,
	// so the row doesn't keep showing a scary error while the app is connected.
	useEffect(() => {
		if (connection === "open") setResult((r) => (r && !r.ok ? null : r));
	}, [connection]);

	const dotColor =
		connection === "open" ? t.green : connection === "connecting" ? t.amber : t.textFaint;

	async function test() {
		setTesting(true);
		setResult(null);
		try {
			const count = await pingServer(cfg);
			haptics.success();
			setResult({ ok: true, msg: `Connected — ${count} session${count === 1 ? "" : "s"}` });
		} catch (e) {
			haptics.error();
			const status = e instanceof ApiError ? e.status : undefined;
			const { title } = describeConnectionFailure(classifyConnectionFailure(status), {
				host: cfg.host,
				port: cfg.httpPort,
				platform: Platform.OS,
			});
			setResult({ ok: false, msg: title });
		} finally {
			setTesting(false);
		}
	}

	return (
		<SettingsGroup
			title="Connection"
			footer="Your PC's Tailscale name / 100.x address, or its LAN IP on the same Wi-Fi."
		>
			<SettingsRow
				icon="link"
				label="Connect AO"
				value={paired ? `${cfg.host}:${cfg.httpPort}` : "Not connected"}
				leading={paired ? <Dot color={dotColor} size={7} breathing={connection === "connecting"} /> : undefined}
				onPress={() => router.navigate("/pair")}
			/>
			<SettingsRow
				icon="activity"
				label="Test connection"
				value={result?.msg}
				valueColor={result ? (result.ok ? t.green : t.red) : undefined}
				loading={testing}
				disabled={!paired}
				onPress={test}
			/>
		</SettingsGroup>
	);
}

// Push collapsed to a single switch. The old card offered up to three different
// buttons (Enable / Register / Open settings) for what a user thinks of as one
// setting; `describePushToggle` folds those states into one control plus a
// footer that explains where it currently stands.
function NotificationsSection() {
	const router = useRouter();
	const { config, connection } = useApp();
	const [status, setStatus] = useState<PushStatus | null>(null);
	const [busy, setBusy] = useState(false);

	const refresh = useCallback(() => {
		getPushStatus()
			.then(setStatus)
			.catch(() => {});
	}, []);

	// Reload on focus and whenever the connection flips — registration happens
	// automatically on a successful connect, so the state can change without any
	// action on this screen.
	useFocusEffect(useCallback(() => refresh(), [refresh]));
	useEffect(() => refresh(), [connection, refresh]);

	const toggle = describePushToggle(status, config);

	async function onToggle(next: boolean) {
		// A permanent denial can only be undone in system settings; the OS will
		// not let the app prompt again, so say so rather than failing silently.
		if (toggle.blocked) {
			Alert.alert(
				"Notifications are blocked",
				"Allow notifications for AO in your system settings, then come back.",
				[{ text: "Not now", style: "cancel" }, { text: "Open settings", onPress: openNotificationSettings }],
			);
			return;
		}
		setBusy(true);
		try {
			if (!next) {
				await unregisterFromPush();
				haptics.tap();
			} else if (config) {
				// A deliberate tap is the right moment to spend the one-shot OS prompt.
				const result = await registerForPush(config, { ask: true });
				if (result.ok) {
					haptics.success();
				} else {
					haptics.error();
					const { title, message } = describeRegisterFailure(result.reason, Platform.OS, result.status);
					Alert.alert(title, message);
				}
			}
		} finally {
			setBusy(false);
			refresh();
		}
	}

	return (
		<SettingsGroup title="Notifications" footer={toggle.footer}>
			<SettingsToggle
				icon="bell"
				label="Agent notifications"
				value={toggle.value}
				disabled={toggle.disabled}
				busy={busy}
				onValueChange={onToggle}
			/>
			<SettingsRow icon="clock" label="History" onPress={() => router.navigate("/notifications")} />
		</SettingsGroup>
	);
}

function AboutSection({ onForget }: { onForget: () => Promise<void> }) {
	const [forgetting, setForgetting] = useState(false);

	// From the binary: with EAS-managed build numbers app.json has no buildNumber.
	const build: BuildInfo = {
		version: Application.nativeApplicationVersion ?? Constants.expoConfig?.version,
		build: Application.nativeBuildVersion,
		updateId: Updates.updateId,
		channel: Updates.channel,
		runtimeVersion: Updates.runtimeVersion,
		embedded: Updates.isEnabled ? Updates.isEmbeddedLaunch : undefined,
	};

	// Routed through openGitHub for consistency, though this one always lands in
	// the browser: the GitHub app has no deep link that accepts a prefilled issue
	// body, and the attached diagnostics are the point of this button.
	function report() {
		const body = encodeURIComponent(bugReportBody(build, Platform.OS, Platform.Version));
		void openGitHub(`${ISSUES_URL}?body=${body}`);
	}

	function confirmForget() {
		Alert.alert(
			"Disconnect & forget server?",
			"This device will stop receiving notifications and the saved address and password will be removed.",
			[
				{ text: "Cancel", style: "cancel" },
				{
					text: "Disconnect",
					style: "destructive",
					onPress: async () => {
						setForgetting(true);
						try {
							await onForget();
						} finally {
							setForgetting(false);
						}
					},
				},
			],
		);
	}

	return (
		<SettingsGroup title="About">
			<SettingsRow icon="info" label="Version" value={formatVersionLine(build)} />
			<UpdateRow />
			<SettingsRow icon="mail" label="Report a problem" onPress={report} />
			<SettingsRow
				icon="power"
				label="Disconnect & forget server"
				destructive
				loading={forgetting}
				onPress={confirmForget}
			/>
		</SettingsGroup>
	);
}

// Check on demand, or restart into a downloaded update.
function UpdateRow() {
	const t = useTheme();
	const { isUpdatePending, isChecking, isDownloading } = Updates.useUpdates();
	const [manual, setManual] = useState<UpdateOutcome | null>(null);
	const [busy, setBusy] = useState(false);

	const row = describeUpdateRow({
		enabled: Updates.isEnabled,
		pending: isUpdatePending,
		phase: isDownloading ? "downloading" : isChecking || busy ? "checking" : "idle",
		lastManual: manual,
	});

	async function onPress() {
		if (row.action === "restart") {
			try {
				await Updates.reloadAsync();
			} catch (e) {
				console.warn("[updates] reload failed", e);
			}
			return;
		}
		setBusy(true);
		setManual(null);
		try {
			const outcome = await checkAndDownload(Updates);
			setManual(outcome);
			if (outcome.kind === "error") haptics.error();
			else haptics.success();
		} finally {
			setBusy(false);
		}
	}

	return (
		<SettingsRow
			icon="download-cloud"
			label="App updates"
			value={row.value}
			valueColor={row.tone === "good" ? t.green : row.tone === "bad" ? t.red : undefined}
			loading={row.busy}
			onPress={row.action ? onPress : undefined}
		/>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: t.bgBase },
	serverActions: { flexDirection: "row", alignItems: "center", gap: 14 },
});
