# Mobile Session and PR Visibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep the terminal focused on session open and render every PR counted by the `/prs` page.

**Architecture:** Preview discovery remains a read-only availability update in the session screen. PR grouping uses PR state—not the owning session's lifecycle—to choose an active or passive visible section.

**Tech Stack:** Expo Router, React Native Web, TypeScript, Node built-in assertions.

## Global Constraints

- Do not add dependencies or change the daemon API.
- Stage only the named mobile files and documentation; keep unrelated dirty files unstaged.
- Keep browser opening user-initiated through the existing globe button.

---

### Task 1: Restore session and PR visibility

**Files:**

- Modify: `packages/mobile/app/session/[id].tsx:267-421`
- Modify: `packages/mobile/app/(tabs)/prs.tsx:10,634-664`
- Create: `packages/mobile/scripts/session-preview.test.js`
- Create: `packages/mobile/scripts/pr-visibility.test.js`

**Interfaces:**

- Consumes: `getPreview(cfg, id)` returning `{ entry, url } | null`.
- Produces: preview availability state without changing `browserOpen`; `groupPRs()` assigns every open, merged, or closed `DashboardPR` to a visible section.

- [x] **Step 1: Write failing regression checks**

```js
assert.doesNotMatch(previewPoll, /setBrowserOpen\(true\)/);
assert.doesNotMatch(grouping, /isTerminalStatus\(item\.session\.status\)/);
assert.match(grouping, /if \(pr\.state === "closed"\) return "dead"/);
```

- [x] **Step 2: Run checks to verify failure**

Run: `node scripts/session-preview.test.js && node scripts/pr-visibility.test.js`

Expected: the preview check passes; the PR visibility check fails because terminal session state currently controls grouping and closed PRs return `null`.

- [x] **Step 3: Implement the minimal grouping change**

```ts
function classifyPR(pr: DashboardPR): Exclude<SectionId, "dead"> | "dead" {
	if (pr.state === "merged") return "merged";
	if (pr.state === "closed") return "dead";
	if (pr.ciStatus === "failing" || pr.reviewDecision === "changes_requested") return "needs";
	if (pr.reviewDecision === "approved" && pr.ciStatus === "passing" && pr.mergeability?.mergeable) return "ready";
	return "review";
}
```

Remove the terminal-session early return from `groupPRs()`. Keep the existing `Dead sessions` passive section as the closed-PR bucket.

- [x] **Step 4: Verify checks and types**

Run: `node scripts/session-preview.test.js && node scripts/pr-visibility.test.js && npm run typecheck`

Expected: both regression checks print `ok` and TypeScript exits 0.

- [x] **Step 5: Commit the focused implementation**

```bash
git add 'packages/mobile/app/session/[id].tsx' 'packages/mobile/app/(tabs)/prs.tsx' packages/mobile/scripts/session-preview.test.js packages/mobile/scripts/pr-visibility.test.js docs/superpowers/specs/2026-07-10-mobile-session-pr-visibility-design.md docs/superpowers/plans/2026-07-10-mobile-session-pr-visibility.md
git commit -m "fix(mobile): keep sessions and prs visible"
```
