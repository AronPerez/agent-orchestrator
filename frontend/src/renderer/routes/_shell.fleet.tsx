import { createFileRoute } from "@tanstack/react-router";
import { FleetPage } from "../components/FleetPage";

export const Route = createFileRoute("/_shell/fleet")({ component: FleetPage });
