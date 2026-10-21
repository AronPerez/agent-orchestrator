import type { Ref } from "react";
import { useTranslation } from "react-i18next";
import { X } from "lucide-react";
import { Button } from "./ui/button";
import { Input } from "./ui/input";

type BoardFindBarProps = {
	inputRef: Ref<HTMLInputElement>;
	matches: number;
	onClose: () => void;
	onQueryChange: (query: string) => void;
	query: string;
	total: number;
};

export function BoardFindBar({ inputRef, matches, onClose, onQueryChange, query, total }: BoardFindBarProps) {
	const { t } = useTranslation();
	return (
		<div
			aria-label={t("board.find.placeholder")}
			className="absolute right-3 top-3 z-20 flex max-w-[calc(100%-1.5rem)] items-center gap-2 rounded-md border border-border bg-popover p-1.5 text-popover-foreground shadow-sm"
			data-testid="board-find-bar"
			role="search"
		>
			<Input
				aria-label={t("board.find.placeholder")}
				autoFocus
				className="h-control-md w-56 flex-1 focus-visible:border-ring"
				onChange={(event) => onQueryChange(event.target.value)}
				placeholder={t("board.find.placeholder")}
				ref={inputRef}
				value={query}
			/>
			<span aria-live="polite" className="shrink-0 text-xs tabular-nums text-muted-foreground">
				{t("board.find.count", { matches, total })}
			</span>
			<Button aria-label={t("board.find.close")} className="focus-visible:bg-muted" onClick={onClose} size="icon-sm" type="button" variant="ghost">
				<X aria-hidden="true" className="size-icon-md" />
			</Button>
		</div>
	);
}
