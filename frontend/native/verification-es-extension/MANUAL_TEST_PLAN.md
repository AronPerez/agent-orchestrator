# Post-entitlement manual test plan

Do not run this plan until Apple has granted the Endpoint Security entitlement and the activation/packaging work in later slices has been reviewed. Never disable SIP or enable system-extension developer mode.

## Prerequisites

- Use a dedicated test Mac for each supported release from macOS 13 onward, covering Apple silicon and Intel.
- Sign the containing app, extension, and per-run supervisor with the same approved Team ID. Give only the extension `com.apple.developer.endpoint-security.client`; sign the supervisor with the identifier `dev.agent-orchestrator.verification-supervisor`.
- Install and activate through the containing app's reviewed `OSSystemExtensionRequest` flow, then grant Full Disk Access through normal System Settings approval.
- If a daemon or supervisor harness is used, set private `AO_DATA_DIR`, `AO_RUN_FILE`, and `AO_PORT` values. Never use the defaults on a development machine.

## Checks

1. Connect the supervisor with reciprocal code-signing requirements. Confirm an unsigned, differently signed, wrong-Team-ID, or wrong-signing-identifier client receives no protocol messages.
2. Spawn a root suspended and register its `(pid, pidversion)`. After receiving acceptance and the extension source UUID, install the observation receive state and send the versioned `readyForObservations` request. Release the root only after readiness succeeds. Confirm a failure injected between registration and readiness is buffered until readiness, then both delivered without being lost and reported as unsuccessful readiness so the gate stays closed; confirm no observation is delivered before readiness.
3. Exercise direct fork, fork then exec, normal exit, `posix_spawn`, fast double-fork, `setsid`, cleared environment, and closed/redirected descriptors. Confirm every descendant produces contiguous per-run observations and exact identity transitions.
4. Reuse a PID after its earlier process exits. Confirm the changed `pidversion` cannot match or receive a termination intended for the old execution.
5. Terminate the supervisor connection and separately stop/restart the extension during a run. Confirm both runs fail unverified and the restarted source UUID cannot continue the old stream.
6. Use the fake source to inject a skipped, duplicate, reversed, and overflowing source sequence. Confirm one failure observation is emitted and no later root registration or lifecycle observation is accepted.
7. Create event pressure sufficient to produce a real Endpoint Security global-sequence gap if feasible on the dedicated test host. Confirm the same fail-closed result; do not treat inability to induce kernel loss as a pass for this check.
8. Inspect captured XPC payloads and logs. They may contain protocol/source/run IDs, numeric process identities, event kinds, exit status, and failure codes only—never paths, command text, arguments, environment values, project contents, credentials, or email.

Record OS/build versions, architecture, signing Team ID (not certificates or secrets), the ordered observation stream, and the failure evidence for each case. Remove the extension later through the containing app's reviewed deactivation flow.
