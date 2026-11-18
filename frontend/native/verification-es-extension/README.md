# macOS verification Endpoint Security prototype

This development-only target observes `fork`, `exec`, and `exit` notifications. It does not execute commands, inspect arguments or environments, read project files, or authorize/deny system activity.

The wire protocol uses `(pid, pidversion)` for one process execution, a UUID for each extension process, Endpoint Security's global client sequence for source-gap detection, and a separate contiguous sequence for each run's XPC stream. Root registration is two-phase: the extension returns acceptance while buffering observations, then the future supervisor installs its receive state and sends `readyForObservations`. Only that explicit readiness request enables delivery, and the supervisor releases its suspended root only after readiness succeeds with no terminal failure. A source gap, invalid sequence, unsupported ES message version, source failure, or XPC disconnect makes the run unverified.

Incoming XPC connections must be signed by the extension's Team ID with the signing identifier `dev.agent-orchestrator.verification-supervisor`. The future supervisor must reciprocally require the extension bundle identifier, Endpoint Security entitlement, and same Team ID before activating its XPC connection.

The target is off by default. `AO_MACOS_ES_EXTENSION_DEV=1 npm run build:verification-es-extension` compile-checks an unsigned `.systemextension` bundle under `${AO_DATA_DIR:-~/.ao}/build-cache`; it never signs, installs, or activates it. The bundle is not part of Electron packaging. Apple must grant `com.apple.developer.endpoint-security.client` before a signed runtime test can work.

See [MANUAL_TEST_PLAN.md](MANUAL_TEST_PLAN.md) for the post-entitlement test plan.
