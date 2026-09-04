import type { ReactNode } from "react";

// The board subhead (mc-board .dashboard-main__subhead): a 21px bold title with
// a muted one-line subtitle, optionally a trailing count.
export function DashboardSubhead({
	title,
	subtitle,
	count,
	actions,
}: {
	title: string;
	subtitle: string;
	count?: number;
	actions?: ReactNode;
}) {
	return (
		// Wraps on narrow viewports so the action cluster drops below the title
		// instead of overflowing; the one-line subtitle is desktop-only (md+).
		<div className="flex flex-wrap items-center gap-x-3 gap-y-2 px-4.5 pt-5.5">
			<div className="flex min-w-0 items-baseline gap-3">
				<h1 className="text-heading font-bold tracking-tight-xl text-foreground">{title}</h1>
				{typeof count === "number" && <span className="font-mono text-control text-passive">{count}</span>}
				<span className="hidden text-md-sm text-passive md:inline">{subtitle}</span>
			</div>
			{actions ? <div className="ml-auto flex shrink-0 items-center gap-2">{actions}</div> : null}
		</div>
	);
}
