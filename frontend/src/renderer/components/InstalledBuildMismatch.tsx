import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { useUiStore } from "../stores/ui-store";
import { RestartToUpdateDialog } from "./RestartToUpdateDialog";
import { Button } from "./ui/button";
import { TooltipProvider } from "./ui/tooltip";

/** The normal shell must never mount against another build's main process. */
export function InstalledBuildMismatch() {
	const { t } = useTranslation();
	const open = useUiStore((state) => state.openUpdateInstallPrompt);
	useEffect(() => { open(); }, [open]);
	return <TooltipProvider>
		<main className="flex h-screen flex-col items-center justify-center gap-4 bg-background p-6 text-foreground">
			<p>{t("update.restart.retry")}</p>
			<Button onClick={open}>{t("update.restart.options")}</Button>
		</main>
		<RestartToUpdateDialog />
	</TooltipProvider>;
}
