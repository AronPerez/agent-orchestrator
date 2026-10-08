import React from "react";
import { useTranslation } from "react-i18next";

type Props = {
	children: React.ReactNode;
};

type BoundaryProps = Props & {
	fallbackBody: string;
	fallbackTitle: string;
};

type State = {
	hasError: boolean;
};

class AppErrorBoundary extends React.Component<BoundaryProps, State> {
	state: State = { hasError: false };

	static getDerivedStateFromError() {
		return { hasError: true };
	}

	render() {
		if (this.state.hasError) {
			return (
				<div className="flex h-screen items-center justify-center bg-background px-6 text-center text-foreground">
					<div>
						<h1 className="text-heading-sm font-semibold">{this.props.fallbackTitle}</h1>
						<p className="mt-2 text-sm text-muted-foreground">{this.props.fallbackBody}</p>
					</div>
				</div>
			);
		}
		return this.props.children;
	}
}

export function AppBoundary({ children }: Props) {
	const { t } = useTranslation();
	return (
		<AppErrorBoundary fallbackBody={t("appError.body")} fallbackTitle={t("appError.title")}>
			{children}
		</AppErrorBoundary>
	);
}
