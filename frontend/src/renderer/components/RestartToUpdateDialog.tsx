import { useTranslation } from "react-i18next";
import {
	cancelUpdateInstall,
	confirmUpdateInstall,
	useUpdateInstallStore,
} from "../hooks/useRequestUpdateInstall";
import { ConfirmDialog } from "./ConfirmDialog";

export function RestartToUpdateDialog() {
	const { t } = useTranslation();
	const { phase, promptOpen, risk, failed, returnFocusTo } = useUpdateInstallStore();
	return (
		<ConfirmDialog
			open={promptOpen}
			title={t("update.restart.title")}
			description={
				<div className="space-y-2">
					{risk.count > 0 && <p className="font-medium text-foreground">{t("update.restart.sessionsTitle", { count: risk.count })}</p>}
					<p>{t(risk.unknown ? "update.restart.unknownWorkers" : "update.restart.sessionsBody")}</p>
				</div>
			}
			confirmLabel={t("update.restart.confirm")}
			busy={phase === "checking" || phase === "installing"}
			error={failed ? t("update.restart.prepareFailed") : null}
			onCloseAutoFocus={(event) => {
				if (returnFocusTo?.isConnected) {
					event.preventDefault();
					returnFocusTo.focus();
				}
			}}
			onConfirm={confirmUpdateInstall}
			onOpenChange={(open) => { if (!open) cancelUpdateInstall(); }}
		/>
	);
}
