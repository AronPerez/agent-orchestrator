import { MoreVertical } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { TopbarButton } from "./TopbarButton";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";

export function SessionActionsMenu({
	items,
	inlineStatus,
}: {
	items: ReactNode[];
	inlineStatus?: ReactNode;
}) {
	const { t } = useTranslation();
	const menuItems = items.filter(Boolean);

	return (
		<div className="inline-flex shrink-0 items-center gap-1">
			{inlineStatus}
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<TopbarButton
						aria-label={t("session.actionsMenu")}
						className="size-7 !bg-transparent text-muted-foreground hover:!bg-transparent active:!bg-transparent focus:!bg-transparent data-[state=open]:!bg-transparent hover:text-foreground"
						data-session-actions-trigger
						title={t("session.actionsMenu")}
						type="button"
						variant="icon"
					>
						<MoreVertical aria-hidden="true" className="size-icon-md" />
					</TopbarButton>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end" className="min-w-44">
					{menuItems.length > 0 ? (
						menuItems
					) : (
						<DropdownMenuItem disabled>{t("session.noActions")}</DropdownMenuItem>
					)}
				</DropdownMenuContent>
			</DropdownMenu>
		</div>
	);
}
