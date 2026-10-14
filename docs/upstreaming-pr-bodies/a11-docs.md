## What

The documentation for remote hosts: a setup page with the trust boundary
it assumes, an ADR recording the design, and one `AGENTS.md` rule.

## Why

Part of the remote-hosts series proposed in #RFC. It imports nothing, so it can be reviewed at any point — but it *describes* behaviour that only exists once the one-tree PR merges, so it is the last one to open.

The setup page exists because the honest version of "connect a second machine" needs the boundary stated plainly. The connection password gates every data route on the LAN listener; it does **not** encrypt anything, does not authenticate the remote machine to you, and does not limit what a holder can do. That is why the assumed boundary is a trusted network, and why both encrypted alternatives are written out end to end rather than mentioned.

## How

`configuration/remote-sessions.mdx` covers connecting a host, the trust boundary, binding the listener to Tailscale, tunnelling over SSH, and the macOS Local Network privacy failure.

Two things in it are worth the space. The SSH section calls out the local-port trap: on macOS, `ssh -L 3011:…` binds `127.0.0.1:3011` *alongside* a daemon already listening on every interface, so the forward succeeds, `ExitOnForwardFailure` never fires, and this machine's own connections quietly go to the **remote** daemon. And the macOS section gives a conclusive way to tell Local Network privacy apart from a real network problem — `curl` keeps working against the same address, which is exactly what makes it confusing.

`docs/adr/0003-remote-hosts-renderer-fanout.md` records the design and, more usefully, the alternatives and why each was rejected: hub federation in the daemon (moves peer credentials behind a socket every local process can reach), one-active-host-at-a-time (shipped as an interim state and rejected on use), global id namespacing (breaks parity with each daemon's own CLI and URLs). It carries the security review's four fixed findings and its accepted risks, the decision to keep saved hosts in a `0600` file rather than an OS keychain, and the SSH spike's conclusion that a tunnel needs no proxy change and therefore ships as a recipe first.

`0001` and `0002` are taken and another proposal in flight also claims `0002`, so this claims `0003` and says so — renumber on merge if that one lands first.

The `AGENTS.md` addition is one bullet, and it is the invariant a future change could quietly break: the remote-host proxy binds `127.0.0.1` only, requires a per-activation token carried in the URL path, strips that token before forwarding, never logs the request path, and never hands a connection password to the renderer.

## Testing

Documentation only — no code, no suites, no CI surface. The landing page uses only MDX components already used elsewhere in `content/docs`.
