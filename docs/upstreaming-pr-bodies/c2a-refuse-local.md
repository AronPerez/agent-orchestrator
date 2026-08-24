## What

`ao doctor`, `preview`, `import`, `start`, `dev import-projects` and the hidden `daemon` command do not fail against `--url`: they succeed, on the wrong machine, and say nothing about it. Each now refuses. `ao hooks` and `ao agent-process supervise` are daemon-local callbacks and now pin to the local daemon.

## Why

Part of the CLI half of the remote-hosts series proposed in #RFC, on top of the `--url` flag. With `--url` these commands succeed on the wrong machine and say nothing about it: `ao doctor --url` reports the laptop's git, tmux and data dir; `ao import --url` opens the laptop's database; `ao start --url` opens the laptop's desktop app. A command that acted on the wrong host cannot be caught after the fact — the output looks exactly like success.

## How

`refuseLocalOnly` produces one message shape: it names the flag that pointed off-box (`--url` **or** `AO_URL`, whichever it was), names the URL it points at, says why the command cannot honour it, and says where to run it instead. It never guesses at a remote equivalent. Exit code 2 — passing `--url` to a command that cannot use it is flag misuse, not a runtime failure.

`ao preview` refuses at `sessionPreviewPath`, the single chokepoint every `preview` subcommand goes through, rather than in five separate `RunE`s. `ao doctor` refuses rather than labelling its report: every check but `daemon` describes this host, and labelling would mean adding a host field to `--json`, changing output scripts parse, to keep a report whose one honest line `ao status --url` already gives.

`ao daemon` is the one asymmetry: it refuses an explicit `--url` but ignores `AO_URL`. It is spawned by the desktop app rather than typed, and refusing on an exported shell variable — the very thing a remote-access guide tells people to set — would turn a working remote setup into a dead desktop app on the operator's own machine.

`ao hooks` and `ao agent-process supervise` make the same split for the same reason, and it matters more there. They report activity for a session that exists on this machine; against a remote daemon that returns `SESSION_NOT_FOUND` and exits 0, so the local activity feed silently goes dead while the agent keeps working. An explicit `--url` is refused; an exported `AO_URL` is ignored and the ignore is appended to `hooks.log`, so it is discoverable without a hook ever being the thing that breaks. The log line goes to `hooks.log` only and not to stderr: an agent's hook runner swallows stderr, and `supervise` shares the user's terminal, where a line on every agent launch would be noise.

Every guard returns `nil` on its first line without a remote target, so with no `--url` and no `AO_URL` each of these commands behaves exactly as before.

## Testing

`cd backend && go build ./... && go vet ./... && go test ./... && go test -race ./internal/cli/`; `gofmt -l` clean. Eight new tests: the message shape and exit code for both flag sources; end-to-end refusals for `doctor`, `preview`, `import` and `start` where the real assertion is an **empty request log**; the same four unchanged with no target; the four daemon-local-callback cases (ignore `AO_URL`, refuse `--url`, stay best-effort under an ignored `AO_URL`, unchanged with no target); and `ao dev import-projects`, which additionally proves the refusal happens *before* any local path resolution by pointing `--from-data-dir` at the target dir and asserting the same-dir error is not what surfaces.

No frontend file and no OpenAPI surface is touched, so the `frontend`, `renderer-smoke` and `api-drift` CI jobs are unaffected.

## Checklist

- [x] Branched from `main`
- [x] One focused change; links the related issue
- [x] Follows AGENTS.md conventions and PR hygiene
- [x] Tests added for user-visible behavior
- [x] Relevant CI checks pass for the area touched
