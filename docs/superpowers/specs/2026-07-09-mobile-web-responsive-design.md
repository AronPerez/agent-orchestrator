# Mobile web build — first-class responsive desktop layout

**Date:** 2026-07-09
**Surface:** `packages/mobile` (Expo Router / React Native), the **react-native-web build** viewed in a desktop browser.
**Pipeline:** produced via the `design-ui` pipeline (frame → inspiration → concept → build), adapted to this repo (DS = this app's own `theme.ts`/`ui.tsx`, which already mirror `DESIGN.md`; spec lives in-repo, not `cloud_docs/`).

## Frame

- **What it is.** `packages/mobile` is a phone app first; its web build renders the same phone screens via react-native-web. Screens are `View(flex:1) → ScreenHeader → ScrollView/SectionList`, cards at `marginHorizontal: 12`, **no `max-width` anywhere**. On a ~1900px browser the single phone column stretches edge-to-edge: cards span the viewport, buttons span the viewport, oceans of dead space below. That is the whole problem.
- **Job to be done.** The web build must be a **first-class web surface** (user decision, 2026-07-09): genuinely use the horizontal space with a real desktop layout — not merely a centered phone column.
- **Deciding constraint.** This is React-Native-first. Responsive behaviour must come from `useWindowDimensions()`-driven branches inside RN screens, not a rewrite. Keep the phone layout intact for narrow widths; add a `wide` layout for `md+`.
- **Platform:** web (desktop browser) is the target being improved; iOS/Android phone layouts must not regress.

## Design-system inventory (what we reuse)

The app already has a DS whose header comment says it "mirrors AO's DESIGN.md exactly so the phone app reads as the same product." We build entirely from it:

- **Tokens** — `lib/theme.ts`: surfaces (`bgBase`/`bgColumn`/`bgElevated`), text ramp, hairline borders, rationed semantics (blue/orange/amber/red/green) + tints, `attentionMeta` (zone → label/color/order), `statusVisual`, `ciVisual`.
- **Components** — `lib/ui.tsx`: `Card`, `Button`, `Pill`, `Chip`, `Dot`, `StatusBadge`, `SectionHeader`, `ScreenHeader`, `ConnectionPill`, `EmptyState`.
- **Cards** — `lib/SessionCard.tsx` (Kanban), `PRCard`/`OrchestratorCard` (screen-local).

## Inspiration (provenance)

The north star `DESIGN.md` names is the **agent-orchestrator web app**, and a working implementation of its desktop board lives **in this repo**: `frontend/src/renderer/components/SessionsBoard.tsx`. This is higher-signal than any external reference — the point of this app's DS is that it "reads as the same product." Patterns stolen from it:

- **Responsive board strategy** — `flex flex-col gap-3  md:grid md:h-full md:grid-cols-4 md:gap-2`: phone = columns stacked, page scrolls; **md+ = four-column grid filling the height, each column scrolls internally.**
- **Four columns, left→right by flow** — **Working · Needs you · In review · Ready to merge**. "Done/Terminated" is a **collapsed bottom bar**, not a column.
- **Column chrome** — rounded column, dot + uppercase tracked label + mono count. (The renderer adds a top gradient glow; see Gaps.)
- **Card** — status badge (dot + label), right-aligned agent label, `line-clamp-2` title, branch mono, PR footer. Our `SessionCard` already matches this shape.

## Chosen direction — "Mirror the board" (decision 2026-07-09)

Keep the four bottom tabs on every viewport (one nav model). On `wide` (`md+`) each screen becomes a real desktop layout that fills the width; on `phone` the existing layout is untouched.

### Breakpoint

- `WIDE_MIN = 768` (matches the renderer's Tailwind `md`).
- New hook `useBreakpoint(): "phone" | "wide"` off `useWindowDimensions()`.
- Outer board/grid containers get a generous **`maxWidth: 1600` centered** (`alignSelf:'center', width:'100%'`) so ultrawide monitors don't stretch to absurdity while still "using the width".

### Per-screen behaviour

**Kanban (`app/(tabs)/index.tsx`)** — the marquee screen.
- `phone`: unchanged (`SectionList` grouped by attention, stat tiles, ProjectSwitcher, FAB).
- `wide`: replace the list with a **four-column board**. Screen is `flex:1`; a fixed-height board row (`flex:1`) holds four columns (`flex:1` each) with their **own vertical `ScrollView`** (columns scroll internally). Stat tiles + ProjectSwitcher stay above the board.
  - Column → attention-key mapping (fold the app's finer zones into the renderer's four):
    - **Working** ← `working`
    - **Needs you** ← `action`, `respond`, `review`
    - **In review** ← `pending`
    - **Ready to merge** ← `merge`
  - `done` → a **collapsed "Done / Terminated" bottom bar** (chevron + label + count; expands to wrapped chips), ported from the renderer's done-bar.
  - Column header: `Dot(color)` + uppercase label + mono count. Cards: existing `SessionCard`.
  - FAB stays on both breakpoints (ponytail; a header "New task" button is a possible later nicety, not in scope).

**PRs (`app/(tabs)/prs.tsx`)**
- `phone`: unchanged (vertical list of `PRCard`).
- `wide`: render the filtered cards in a **wrapping card grid** — container `flexDirection:'row', flexWrap:'wrap', gap:12`, each `PRCard` sized to a **~400px basis** (`flexBasis: 400, flexGrow: 1, maxWidth: 520`). Inside a ~400px card the existing `flex:1` `Session`/`Open` button pair reads correctly instead of spanning the viewport.

**Orchestrator (`app/(tabs)/orchestrator.tsx`)**
- `phone`: unchanged.
- `wide`: same **wrapping card grid**, each `OrchestratorCard` at a **~440px basis** (`flexBasis: 440, flexGrow: 1, maxWidth: 560`).

**Settings (`app/(tabs)/settings.tsx`)** — a form (`TextInput`s, `Switch`, project list).
- `wide`: constrain the form/content to a **centered ~560px column** so inputs aren't 1900px wide. No structural change otherwise.

**Session (`app/session/[id].tsx`)** — the terminal.
- No change; a terminal correctly fills the width. Verify it does not regress.

**Bottom tab bar (`app/(tabs)/_layout.tsx`)** — kept on all viewports (chosen direction). Out of scope: centering the tab items on ultrawide (minor; revisit only if it looks off).

### Shared layout primitive

One new file `lib/responsive.tsx` exporting pure-layout helpers (no new visual primitives):
- `useBreakpoint()` — the hook above.
- `CardGrid({ minWidth, maxWidth, children })` — phone = column; wide = row/wrap grid with the given card basis. Used by PRs + Orchestrator.
- `BoardColumn({ color, label, count, children })` — one board lane (header + internally-scrolling body). Used by Kanban's wide path.
- `WideContainer({ maxWidth = 1600, children })` — centered max-width wrapper.

## Gap ledger

- **Column top-glow gradient** — the renderer uses a CSS `linear-gradient`. RN has no gradient without `expo-linear-gradient`, which is **not** a dependency. **Decision: omit the glow** (flat `theme.bgColumn` column background + colored dot/label). No new dependency; ponytail-clean. Revisit only if the columns read as flat/lifeless in the visual pass.
- No other gaps — every visual is expressible with existing tokens/components. **No `DESIGN.md` change and no design-system ticket falls out.**

## States

- **Empty** — reuse `EmptyState`; on wide it stays centered in the content area (board/grid renders nothing, empty state fills). Kanban keeps its error/"no active agents"/"connect" states; PRs/Orchestrator keep theirs.
- **Loading** — Kanban's centered `ActivityIndicator` path unchanged.
- **Not configured** — unchanged (`EmptyState` "Connect to AO").

## Verification plan (design-ui rungs)

1. **Mechanical** — `npx tsc --noEmit` (via nvm node); repo lint if present. Confirm no card/label clip when a container's width changes.
2. **Accessibility** — every card/button reachable by Tab with a visible focus ring; the Done-bar toggle and column presence don't trap focus; icon-only controls keep labels. Static checks in code, tab-order confirmed in the browser at rung 3.
3. **Visual (automated)** — boot the Expo web dev server (`npm run web`), drive it with Claude-in-Chrome: open each tab at a **wide** width (~1440px) and a **phone** width (~420px); screenshot. Confirm: 4-column board fills width with internal column scroll; PRs/Orchestrator cards wrap into a grid with content-sized buttons; Settings form is a centered column; phone layout is unchanged; tabs stay at the bottom.
4. **Human (last resort)** — only if no browser-driving capability is reachable.

## Out of scope

- Sidebar/desktop-shell nav (rejected concept "Desktop shell").
- Centered-workspace max-width framing (rejected concept "Centered workspace").
- Column glow gradient / new dependencies.
- Header "New task" button, tab-bar centering — possible later niceties, not this change.
- Any change to the phone (narrow) layout beyond what falls out of shared refactors.
