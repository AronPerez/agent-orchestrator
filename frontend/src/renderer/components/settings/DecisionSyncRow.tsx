import { RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { aoBridge } from "../../lib/bridge";
import { clientForSessionHost } from "../../lib/host-clients";
import { LOCAL_HOST } from "../../lib/hosts";
import { Switch } from "../ui/switch";
import { SettingsRow } from "./SettingsRow";

type DecisionPeer = components["schemas"]["DecisionPeer"];

/** This computer's decision sync links, read from its own daemon. Holds no tokens. */
export async function fetchDecisionPeers(): Promise<DecisionPeer[]> {
	const { data, error } = await clientForSessionHost(LOCAL_HOST).GET("/api/v1/decision-peers");
	if (error || !data) throw new Error("Could not load decision sync links");
	return data.peers;
}

/**
 * The "Sync decisions" switch for one saved host. Turning it on links the host
 * with the credential already saved for it; the renderer never sees that
 * credential or the sync token.
 */
export function DecisionSyncRow({ host, peers, onChanged }: {
	host: { hostId: string; label: string; url: string };
	peers: DecisionPeer[] | undefined;
	onChanged: () => void;
}) {
	const { t } = useTranslation();
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState("");
	const [warnings, setWarnings] = useState<string[]>([]);
	const peer = peers?.find((item) => item.hostId === host.hostId);
	const linked = peer?.direction === "outbound";
	const toggle = async (next: boolean) => {
		setBusy(true);
		setError("");
		setWarnings([]);
		try {
			const result = await aoBridge.remotes.setDecisionSync(host.url, next);
			if (!result.ok) throw new Error(result.code);
			setWarnings(result.warnings.map((warning) => `ao://decisions/${warning.callId}`));
			onChanged();
		} catch {
			setError(t("settings.remoteHosts.syncFailed"));
		} finally {
			setBusy(false);
		}
	};
	const description = !host.hostId
		? t("settings.remoteHosts.syncNeedsHostId")
		: peer ? t(`decisions.peerState.${peer.state}`) : undefined;
	return (
		<>
			<SettingsRow icon={RefreshCw} label={t("settings.remoteHosts.syncDecisions")} description={description}>
				<Switch
					aria-label={`${t("settings.remoteHosts.syncDecisions")} · ${host.label}`}
					checked={linked}
					disabled={!host.hostId || busy || peers === undefined}
					onCheckedChange={(next) => void toggle(next)}
				/>
			</SettingsRow>
			{error ? <p className="px-3 py-1 text-xs text-error" role="alert">{error}</p> : null}
			{warnings.length > 0 ? (
				<div className="px-3 py-1 text-xs text-status-in-review" role="status">
					<p>{t("settings.remoteHosts.syncWarnings")}</p>
					<ul className="mt-1 flex flex-col gap-0.5 font-mono">{warnings.map((link) => <li key={link}>{link}</li>)}</ul>
				</div>
			) : null}
		</>
	);
}

/** Loads the peer list once per Settings view and on demand. */
export function useDecisionPeers() {
	const [peers, setPeers] = useState<DecisionPeer[] | undefined>(undefined);
	const reload = useCallback(() => {
		fetchDecisionPeers().then(setPeers, () => setPeers([]));
	}, []);
	useEffect(() => { reload(); }, [reload]);
	return { peers, reload };
}
