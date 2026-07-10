# Mobile session and PR visibility

## Scope

- Keep the terminal visible when preview discovery finds an `index.html`; the user opens the preview with the globe button.
- Render every counted PR by its PR state, regardless of whether its owning session has terminated: open PRs remain in active sections, merged PRs appear under Merged, and closed PRs appear in the existing passive bucket.

## Implementation

- Remove preview polling's automatic browser-open transition while retaining availability polling.
- Make PR grouping classify the PR before considering no-longer-active states, so a terminal session cannot hide an open or merged PR.
- Add focused dependency-free regression checks and run the mobile typecheck.

## Delivery

Commit only the session screen, PR screen, focused checks, and this spec on `fix/mobile-session-pr-visibility`; leave existing unrelated renderer, lockfile, and credential-file changes unstaged. Push the branch and open a draft PR against `main`.
