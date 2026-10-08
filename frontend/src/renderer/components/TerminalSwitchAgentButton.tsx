import { useQueryClient } from "@tanstack/react-query";
import { Check, ChevronRight, LoaderCircle, Repeat2, TriangleAlert } from "lucide-react";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import {
	clearSwitchAgentState,
	createSwitchAgentIdempotencyKey,
	useSwitchAgent,
} from "../hooks/useSwitchAgent";
import type { AgentSwitchPresentation } from "../lib/agent-switch-presentation";
import { cn } from "../lib/utils";
import { sessionIsActive, type AgentSwitchSummary, type WorkspaceSession } from "../types/workspace";
import { AgentAvatar } from "./AgentAvatar";
import { canSwitchAgentHarness, SWITCH_AGENT_OPTIONS, SwitchAgentDialog } from "./SwitchAgentDialog";
import { TopbarButton } from "./TopbarButton";
import {
	DropdownMenuItem,
	DropdownMenuSub,
	DropdownMenuSubContent,
	DropdownMenuSubTrigger,
} from "./ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

type TerminalSwitchAgentButtonProps = {
	agentSwitch?: AgentSwitchSummary;
	container?: HTMLElement | null;
	disabled?: boolean;
	onOpenChange: ((open: boolean) => void) | undefined;
	open: boolean;
	presentation?: AgentSwitchPresentation;
	session: WorkspaceSession;
	switchError: string | null;
	variant?: "icon" | "menu-item";
};

export function canSwitchAgent(session: WorkspaceSession, presentation?: AgentSwitchPresentation): boolean {
	const controlPresentation = presentation?.outcome === "success" ? undefined : presentation;
	return (
		session.kind === "worker" &&
		!session.isTerminated &&
		canSwitchAgentHarness(session.provider) &&
		(Boolean(controlPresentation) || sessionIsActive(session))
	);
}

export function TerminalSwitchAgentButton({
	agentSwitch,
	container,
	disabled,
	onOpenChange,
	open,
	presentation,
	session,
	switchError,
	variant = "icon",
}: TerminalSwitchAgentButtonProps) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const switchAgent = useSwitchAgent();
	const controlPresentation = presentation?.outcome === "success" ? undefined : presentation;
	const switching = controlPresentation?.outcome === "in_progress";
	const warning = controlPresentation?.outcome === "failure" || controlPresentation?.outcome === "recovery";
	const blocksNewSwitch = switching || disabled || switchAgent.isPending;

	useEffect(() => {
		if (switchError) onOpenChange?.(true);
	}, [onOpenChange, switchError]);

	if (!canSwitchAgent(session, controlPresentation)) {
		return null;
	}

	const label = controlPresentation
		? t(controlPresentation.compactLabelKey, controlPresentation.values)
		: t("switchAgent.action");
	const handleOpenChange = (nextOpen: boolean) => {
		onOpenChange?.(nextOpen);
		if (!nextOpen && switchError) {
			clearSwitchAgentState(queryClient, session);
		}
	};

	const icon = warning ? (
		<TriangleAlert aria-hidden="true" className="size-icon-sm" />
	) : switching ? (
		<LoaderCircle aria-hidden="true" className="agent-switch-toolbar-spinner size-icon-sm animate-spin" />
	) : (
		<Repeat2 aria-hidden="true" className="size-4 stroke-[1.8]" />
	);

	return (
		<>
			{variant === "menu-item" && !controlPresentation ? (
				<DropdownMenuSub>
					<DropdownMenuSubTrigger disabled={blocksNewSwitch}>
						{icon}
						{label}
						<ChevronRight aria-hidden="true" className="ml-auto !size-icon-sm" />
					</DropdownMenuSubTrigger>
					<DropdownMenuSubContent>
						{SWITCH_AGENT_OPTIONS.map((option) => {
							const current = option.value === session.provider;
							return (
								<DropdownMenuItem
									key={option.value}
									disabled={current || blocksNewSwitch}
									onSelect={() =>
										switchAgent.mutate({
											session,
											targetHarness: option.value,
											model: "",
											idempotencyKey: createSwitchAgentIdempotencyKey(),
										})
									}
								>
									<AgentAvatar className="size-icon-base" decorative provider={option.value} />
									<span className="flex-1">{option.label}</span>
									{current ? (
										<>
											<Check aria-hidden="true" className="!size-icon-sm" />
											{t("switchAgent.current")}
										</>
									) : null}
								</DropdownMenuItem>
							);
						})}
					</DropdownMenuSubContent>
				</DropdownMenuSub>
			) : variant === "menu-item" ? (
				<DropdownMenuItem
					className={cn(warning && "text-warning focus:text-warning [&_svg]:text-warning")}
					disabled={blocksNewSwitch}
					onSelect={() => handleOpenChange(true)}
				>
					{icon}
					{label}
				</DropdownMenuItem>
			) : (
				<Tooltip>
					<TooltipTrigger asChild>
						<TopbarButton
							aria-busy={switching && controlPresentation?.animate ? true : undefined}
							aria-label={label}
							className={cn(
								warning && "text-warning hover:bg-warning/10 hover:text-warning",
							)}
							disabled={blocksNewSwitch}
							onClick={() => {
								if (!open) handleOpenChange(true);
							}}
							onPointerDown={(event) => {
								if (!open) return;
								event.preventDefault();
								event.stopPropagation();
							}}
							type="button"
							variant="icon"
						>
							{icon}
						</TopbarButton>
					</TooltipTrigger>
					<TooltipContent>{label}</TooltipContent>
				</Tooltip>
			)}
			{open && container && variant !== "menu-item" ? (
				<SwitchAgentDialog
					agentSwitch={agentSwitch}
					container={container}
					onOpenChange={handleOpenChange}
					open
					session={session}
				/>
			) : null}
		</>
	);
}
