import { useTranslation } from "react-i18next";
import { useLocalHostId } from "../hooks/useLocalHostId";
import { useOpenFleetSession } from "../hooks/useOpenFleetSession";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { useShellMaybe } from "../lib/shell-context";
import { FleetOverview } from "./FleetOverview";
import { PageTopbar, PageTopbarTitle } from "./PageTopbar";

/** One read-only hierarchy across every local and cached remote project. */
export function FleetPage() {
	const { t } = useTranslation();
	const localWorkspaces = useWorkspaceQuery();
	const shell = useShellMaybe();
	const localHostId = useLocalHostId();
	const openSession = useOpenFleetSession();

	return (
		<div className="relative flex h-full min-h-0 flex-col bg-background text-foreground" data-testid="fleet-page">
			<PageTopbar lead={<PageTopbarTitle>{t("fleet.tab")}</PageTopbarTitle>} />
			{localWorkspaces.isError ? <p className="px-4 py-2 text-xs text-destructive" role="alert">{t("fleet.stale")}</p> : null}
			<FleetOverview
				localHostId={localHostId}
				localWorkspaces={localWorkspaces.data ?? []}
				onOpen={openSession}
				remoteHosts={shell?.remoteHosts}
				remoteWorkspaces={shell?.remoteWorkspaces}
				remoteFailedHostIds={shell?.remoteFailedHostIds}
				remoteLoadedProjectHostIds={shell?.remoteLoadedProjectHostIds}
				remoteLoadedSessionHostIds={shell?.remoteLoadedSessionHostIds}
			/>
		</div>
	);
}
