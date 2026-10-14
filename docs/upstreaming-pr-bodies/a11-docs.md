## Ticket

No upstream issue yet. Design note: [remote hosts RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md).

## Problem

The remote-hosts feature has no user-facing documentation and no written trust boundary. Someone turning the flag on has to infer from the UI what a saved host is, what credential it uses, where that credential lives, and what the plaintext-HTTP LAN listener does and does not protect. Undocumented security properties get assumed rather than checked.

## Solution

Setup documentation for connecting a remote host, a written statement of the trust boundary (what the connection password authorises, why the loopback proxy exists, what stays on the machine), and an ADR recording the design decision. Documentation only — no product code, so nothing here changes behaviour with the flag on or off.

## How Has This Been Tested?

Documentation only: five files, no code. `git diff --stat` against the current `main` (`c9a0adb2`) shows changes confined to `docs/`, `AGENTS.md` and three landing-site `.mdx` pages; no test, no build target and no CI job covers behaviour here. Prose was proofread against the shipped behaviour of the branches it describes rather than against the plan documents.

## Artifacts (if appropriate):

No renderable surface: documentation and one `AGENTS.md` paragraph. The landing pages render through the existing docs pipeline unchanged.

## Implementation notes

This PR describes behaviour that only exists once the rest of the series has merged, so it is deliberately last in the order even though it imports nothing and could technically be reviewed at any point.

Content: how to save a host and connect to it; what the connection password authorises and what it does not; why the desktop app proxies remote traffic through a loopback listener rather than letting the renderer authenticate directly; and the ADR that records the decision so the next person does not re-litigate it from scratch.
