import type { components } from "../../api/schema";

export function codexAccountsFixture(): components["schemas"]["CodexAccountsResponse"] {
	return {
		accountRevision: 1,
		activeAccountId: "personal",
		accounts: [{
			id: "personal",
			label: "Personal",
			accountEmail: "engineer@example.test",
			active: true,
			authMethod: "chatgpt",
			status: "valid",
			createdAt: "2026-10-08T12:00:00Z",
			reason: "sensitive diagnostic /private/credentials token=do-not-display",
			reasonCode: "account_valid",
			authentication: {
				state: "authorized", freshness: "fresh", reason: "private auth detail",
				reasonCode: "", checkedAt: "2026-10-08T12:00:00Z", attemptedAt: null,
			},
			capacity: {
				state: "available", freshness: "fresh", reason: "private capacity detail",
				reasonCode: "", remainingPercent: 72, plan: "Plus", additionalBuckets: [],
			},
		}],
		capabilities: {
			globalSwitch: { state: "supported", reason: "", reasonCode: "" },
			nativeLogin: { state: "supported", reason: "", reasonCode: "" },
			resetCreditConsume: { state: "unknown", reason: "", reasonCode: "" },
		},
		deviceReconciliation: { status: "verified", activeAccountVerified: true, retryable: false, reasonCode: "" },
	};
}
