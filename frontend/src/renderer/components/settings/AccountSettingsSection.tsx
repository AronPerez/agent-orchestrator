import { User } from "@/components/app-icons";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { paidPlanBadge, useCloudBilling } from "../../hooks/useCloudBilling";
import { useCloudLocalAuth } from "../../hooks/useCloudLocalAuth";
import { useCloudSession, type CloudAccount } from "../../lib/cloud-session";
import { cn } from "../../lib/utils";
import { useLocalSignInDialogStore } from "../../stores/local-signin-dialog-store";
import { Button } from "../ui/button";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

/**
 * Settings › General › Account: the Orchestrator account used by remote
 * hosts, the mobile app, sharing and Cloud. It is the one place to sign in or
 * out outside the flows that ask for it when needed (Share, a Cloud project,
 * first-run onboarding). Hidden in builds without sign-in, and while the
 * session is still loading so the wrong state never flashes.
 */
export function AccountSettingsSection() {
	const { t } = useTranslation();
	const { configured, status } = useCloudSession();
	if (!configured || status === "loading") return null;

	return (
		<SettingsSection title={t("settings.account.title")} sectionId="account" grouped>
			{status === "authenticated" ? <SignedInRow /> : <SignedOutRow />}
		</SettingsSection>
	);
}

function SignedOutRow() {
	const { t } = useTranslation();
	const { signIn } = useCloudSession();
	// Dev + loopback control plane: the local email/password dialog instead of
	// WorkOS. Sign in waits for that answer, so an early click never opens the
	// wrong flow (the answer arrives over IPC after this page mounts).
	const { available: localAuthAvailable, checked } = useCloudLocalAuth();
	const openLocalSignIn = useLocalSignInDialogStore((s) => s.openDialog);

	return (
		<SettingsRow label={t("settings.account.cloud")} description={t("settings.account.signedOutHint")}>
			<Button
				disabled={!checked}
				onClick={() => (localAuthAvailable ? openLocalSignIn() : signIn())}
				size="sm"
				type="button"
				variant="secondary"
			>
				{t("settings.account.signIn")}
			</Button>
		</SettingsRow>
	);
}

function SignedInRow() {
	const { t } = useTranslation();
	const { session, signOut } = useCloudSession();
	// The plan badge is self-serve billing only; a contract org has no plan to show.
	const billing = useCloudBilling();
	const plan = billing.offering.selfServeBilling ? paidPlanBadge(billing.data) : null;
	const user = session?.user;
	const name = accountName(user);
	const email = user?.email;

	return (
		<SettingsRow
			leading={<AccountAvatar user={user} />}
			label={
				<span className="flex min-w-0 items-center gap-1.5">
					<span className="min-w-0 truncate">{name}</span>
					{plan ? (
						<span className="shrink-0 rounded-full bg-[var(--color-status-working)] px-1.5 text-[length:var(--font-size-xs)] font-medium leading-4 text-white">
							{plan}
						</span>
					) : null}
				</span>
			}
			description={email && email !== name ? <span className="break-all">{email}</span> : undefined}
		>
			<Button onClick={() => void signOut()} size="sm" type="button" variant="ghost">
				{t("settings.account.signOut")}
			</Button>
		</SettingsRow>
	);
}

type AccountUser = CloudAccount["user"] | undefined;

const FALLBACK_NAME = "Orchestrator Cloud";

/** The signed-in person's name, falling back to their email. */
function accountName(user: AccountUser): string {
	const display = user?.displayName?.trim();
	if (display && display !== user?.email) return display;
	return user?.email ?? FALLBACK_NAME;
}

/** The profile picture from the sign-in provider, or the person's initials. */
function AccountAvatar({ user }: { user: AccountUser }) {
	const [broken, setBroken] = useState(false);
	if (user?.avatarUrl && !broken) {
		return (
			<img
				src={user.avatarUrl}
				alt=""
				aria-hidden="true"
				referrerPolicy="no-referrer"
				className="size-7 shrink-0 rounded-full object-cover"
				onError={() => setBroken(true)}
			/>
		);
	}
	const source = accountName(user);
	const initials = source.includes("@")
		? source.charAt(0).toUpperCase()
		: source.split(/\s+/).filter(Boolean).slice(0, 2).map((part) => part.charAt(0).toUpperCase()).join("");
	return (
		<span
			aria-hidden="true"
			className={cn(
				"grid size-7 shrink-0 place-items-center rounded-full bg-[var(--color-bg-tertiary)] text-[length:var(--font-size-xs)] font-medium leading-none text-settings-label",
				!initials && "[&_svg]:size-icon-md",
			)}
		>
			{initials || <User />}
		</span>
	);
}
