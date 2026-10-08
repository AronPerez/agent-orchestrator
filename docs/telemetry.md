# Privacy and retained networking

This fork removes product analytics and remote crash reporting from the daemon,
CLI, Electron main process, renderer, mobile app, and marketing site. There is
no telemetry consent switch to enable them. Remote-reporting clients,
identifiers, endpoints, environment configuration, and build-time keys are
removed rather than disabled by an opt-out setting.

## What remains local

- Desktop console and daemon logs, local SQLite operational events, usage/cost
  displays, and debugging/performance diagnostics stay on the machine.
- `AO_TELEMETRY_EVENTS=on` retains its existing name only for opt-in local SQLite
  diagnostic events; it defaults to off and has no remote exporter.
- Existing database migrations and local diagnostic records are retained. This
  change does not delete users' existing state or previously uploaded data.
- Historical design documents may describe upstream reporting that this fork no
  longer implements; they are not instructions to enable it.

## Network features retained

Removing analytics does not make the application offline. Provider integrations,
Cloud sessions, explicitly configured remote hosts, downloads, package installs,
and update checks still need their functional network requests.

The desktop updater checks release feeds and downloads updates. Its underlying
`electron-updater` dependency sends an `x-user-staging-id` installation UUID for
staged rollout selection; the application's direct GitHub checks send an app
version in `User-Agent`. These are retained as part of the existing update
mechanism, not treated as evidence of an identifier-free or offline application.
Servers also receive ordinary connection metadata such as source IP addresses.
Mobile store/update mechanisms are likewise retained.

Update-feed routing is unchanged: release builds default to
`Untrivial-ai/agent-orchestrator` unless `AO_RELEASE_REPO` is set. A fork build
using that upstream feed can later install upstream behavior, including reporting
that this change removes; choose the fork release feed when distributing it.

The optional CI pod runner uses the Daytona SDK, whose transitive OpenTelemetry
packages remain installed. AO does not initialize that exporter or configure a
vendor destination. The SDK supports tracing when an operator explicitly enables
it through its own configuration or environment; this integration is retained
rather than removing the functional pod runner.

Hosted Cloud deployment scripts retain AWS CloudWatch logs, metrics, alarms,
and dashboards (and optional SNS alerts) as operator-managed infrastructure
observability. Those deployment logs leave the server for AWS; they are not
local-only diagnostics and were not removed as product analytics.

Repository scripts hard-disable Next.js and Expo CLI telemetry using the tools'
official environment settings. Calling those tools directly, outside these
scripts, bypasses that configuration.

Third-party tools launched or installed by AO have their own privacy behavior.
Removing AO's reporting does not change those tools or package managers.
