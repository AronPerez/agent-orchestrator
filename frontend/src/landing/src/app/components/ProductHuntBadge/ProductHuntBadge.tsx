import type { ReactNode } from "react";

const PRODUCT_HUNT_URL =
	"https://www.producthunt.com/products/agent-orchestrator?launch=agent-orchestrator";

export type ProductHuntIntent = "badge" | "upvote";

const INTENT_LABEL: Record<ProductHuntIntent, string> = {
	badge: "Find Agent Orchestrator on Product Hunt",
	upvote: "Upvote us on Product Hunt",
};

type ProductHuntBadgeProps = {
	/**
	 * The badge visual. Pass Product Hunt's official embed `<img>` here so we do
	 * not hardcode an asset (the embed URL depends on the launch/post id). When
	 * omitted, a plain text label is rendered so the CTA still works.
	 */
	children?: ReactNode;
	className?: string;
	/** Which CTA this instance is; defaults to the plain badge. */
	intent?: ProductHuntIntent;
};

export function ProductHuntBadge({
	children,
	className,
	intent = "badge",
}: ProductHuntBadgeProps) {
	return (
		<a
			href={PRODUCT_HUNT_URL}
			target="_blank"
			rel="noopener noreferrer"
			className={className}
			aria-label="Agent Orchestrator on Product Hunt"
		>
			{children ?? INTENT_LABEL[intent]}
		</a>
	);
}
