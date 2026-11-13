import { sessionUiKey } from "../lib/hosts";
import type { AgentSwitchSummary } from "../types/workspace";

export type AgentSwitch = AgentSwitchSummary;
export const agentSwitchesQueryKey = (sessionId: string, hostId?: string) =>
 ["session-agent-switches", sessionUiKey(sessionId, hostId)] as const;
