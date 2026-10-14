import { useTranslation } from "react-i18next";
import { X } from "lucide-react";
import { Button } from "./ui/button";
import { Input } from "./ui/input";

export type BoardFindBarProps = {
	matches: number;
	onClose: () => void;
	onQueryChange: (query: string) => void;
	query: string;
	total: number;
};

/**
 * Floating filter strip over the board content. Presentational only — the board
 * owns the query, the shortcut, and the filtering.
 */
export function BoardFindBar({ matches, onClose, onQueryChange, query, total }: BoardFindBarProps) {
	const { t } = useTranslation();
	return (
		<div
			className="absolute right-3 top-3 z-20 flex items-center gap-2 rounded-lg border border-border-strong bg-popover py-2 pl-3 pr-2 text-popover-foreground shadow-lg"
			data-testid="board-find-bar"
			role="search"
		>
			<Input
				aria-label={t("board.find.placeholder")}
				autoFocus
				className="h-control-md w-56"
				onChange={(event) => onQueryChange(event.target.value)}
				placeholder={t("board.find.placeholder")}
				value={query}
			/>
			<span className="shrink-0 font-mono text-xs tabular-nums text-muted-foreground">
				{t("board.find.count", { matches, total })}
			</span>
			<Button aria-label={t("board.find.close")} onClick={onClose} size="icon-sm" type="button" variant="ghost">
				<X aria-hidden="true" className="size-icon-md" />
			</Button>
		</div>
	);
}
