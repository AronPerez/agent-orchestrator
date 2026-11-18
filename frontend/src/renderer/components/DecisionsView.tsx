import { useTranslation } from "react-i18next";
import { ListChecks } from "@/components/app-icons";
import { PageTopbar, PageTopbarTitle } from "./PageTopbar";

export type DecisionsViewProps = {
	/** The selected call from `?call=`. */
	callId?: string;
	/** Search words from `?q=`. */
	query?: string;
};

/** The Decisions page: this computer's ledger as a live swimlane board. */
export function DecisionsView(_props: DecisionsViewProps) {
	const { t } = useTranslation();
	return (
		<div className="flex min-h-0 flex-1 flex-col bg-background">
			<PageTopbar lead={<PageTopbarTitle as="h1" icon={<ListChecks className="size-icon-md" aria-hidden="true" />}>{t("decisions.title")}</PageTopbarTitle>} />
		</div>
	);
}
