import { createFileRoute } from "@tanstack/react-router";
import { DecisionsView } from "../components/DecisionsView";
import { validateDecisionsSearch } from "../lib/decisions-route";

export const Route = createFileRoute("/_shell/decisions")({
	validateSearch: validateDecisionsSearch,
	component: DecisionsRoute,
});

function DecisionsRoute() {
	const search = Route.useSearch();
	return <DecisionsView callId={search.call} query={search.q} />;
}
