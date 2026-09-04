import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type {
  BrowserAgentActivityState,
  BrowserDevToolsPlacement,
  BrowserDevToolsState,
  BrowserNavState,
  BrowserRect,
  BrowserTabState,
  BrowserTabsState,
} from "../../main/browser-view-host";
import type {
  BrowserAnnotationCancelPayload,
  BrowserAnnotationSubmitPayload,
} from "../../shared/browser-annotations";
import { OPEN_BROWSER_OVERLAY_SELECTOR } from "../lib/dom-selectors";
import { refKey, type Ref } from "../lib/hosts";
import { MAX_BROWSER_TABS } from "../lib/browser-tab-order";

export type { BrowserNavState };

export type ClosedBrowserTab = {
  id: string;
  title: string;
  url: string;
  favicon?: string;
};

const MAX_CLOSED_TABS = 5;

// Mirrors the main process's isBlankBrowserEntry (browser-view-host.ts):
// a freshly-opened tab reports its URL as the literal string "about:blank"
// once its initial load settles, not an empty string — a plain truthiness
// check on `url` treats that as "real" content worth remembering.
function isBlankTabUrl(url: string): boolean {
  return !url || url === "about:blank";
}

type UseBrowserViewOptions = {
  session: Ref;
  /**
   * Persistent-profile opt-in, resolved by the caller from project config
   * (see usePersistentBrowserProfile). Omitting it entirely keeps the default:
   * a throwaway memory-only profile, decided immediately.
   *
   * `pending` exists because a WebContentsView's partition is fixed at
   * construction and cannot be changed afterwards. Creating the view before the
   * project has answered would lock the session onto the wrong profile for its
   * whole life, so the view waits instead.
   */
  persistentProfile?: { pending: true } | { pending: false; key: string };
  active: boolean;
  poppedOut: boolean;
  /**
   * When true, the view is cleared and the daemon-driven preview is suppressed.
   * Use when the session is terminated: the old preview content should not
   * remain visible even if the DB still carries a preview_url.
   */
  terminated?: boolean;
  /**
   * Preview target driven by the daemon (via `ao preview`, streamed over CDC).
   * When set, the view navigates here automatically; an empty value clears it.
   */
  previewUrl?: string;
  /**
   * Monotonic counter the daemon bumps on every `ao preview` call, even when
   * previewUrl is unchanged. The view re-navigates whenever it advances, so a
   * repeated `ao preview <same-url>` still refreshes (and CDC replays of an
   * unrelated session update, which leave it unchanged, are ignored).
   */
  previewRevision?: number;
};

export type BrowserViewModel = {
  viewId: string;
  navState: BrowserNavState;
  slotRef: (node: HTMLDivElement | null) => void;
  navigate: (url: string) => Promise<void>;
  goBack: () => Promise<void>;
  goForward: () => Promise<void>;
  reload: () => Promise<void>;
  stop: () => Promise<void>;
  tabs: BrowserTabState[];
  activeTabId: string;
  tabNotice: string;
  selectTab: (tabId: string) => Promise<void>;
  closeTab: (tabId: string) => Promise<void>;
  openTab: (url?: string) => Promise<void>;
  reorderTabs: (orderedIds: string[]) => void;
  closedTabs: ClosedBrowserTab[];
	reopenClosedTab: (tabId?: string) => Promise<void>;
  devtoolsState: BrowserDevToolsState;
  openDevTools: () => Promise<void>;
  closeDevTools: () => Promise<void>;
  setDevToolsPlacement: (placement: BrowserDevToolsPlacement) => Promise<void>;
  agentBrowserActive: boolean;
  agentBrowserActivity: BrowserAgentActivityState | null;
  destroy: () => void;
  annotationMode: boolean;
  setAnnotationMode: (enabled: boolean) => Promise<void>;
  /**
   * "native" in Electron (a window-level WebContentsView paints into the slot);
   * "web" in a plain browser, where there is no WebContentsView so the panel
   * renders an <iframe> at `iframeSrc` (remounted when `iframeKey` changes).
   */
  mode: "native" | "web";
  iframeSrc: string;
  iframeKey: number;
};

const EMPTY_NAV_STATE: BrowserNavState = {
  viewId: "",
  url: "",
  title: "",
  canGoBack: false,
  canGoForward: false,
  isLoading: false,
};

const EMPTY_TABS_STATE: BrowserTabsState = {
  viewId: "",
  activeTabId: "",
  tabs: [],
};

const EMPTY_DEVTOOLS_STATE: BrowserDevToolsState = {
  viewId: "",
  open: false,
  activeTabId: "",
  placement: "undocked",
};

type PreviewTrigger = { revision: number | null; target: string };

// The native view survives React session switches, so remember which preview
// trigger was already consumed for each session. This prevents switching back
// from reasserting previewUrl over a URL the user manually navigated to.
const consumedPreviewTriggers = new Map<string, PreviewTrigger>();

export function resetConsumedPreviewTriggersForTest(): void {
  consumedPreviewTriggers.clear();
}

// Recently Closed has no main-process backing (unlike live tabs, which
// survive a session switch because they're kept alive in the main process
// and simply re-fetched) — it's built up purely from this hook's own
// bookkeeping. Without this, switching sessions and back lost the list for
// good, even though nothing about it actually changed.
const closedTabsBySession = new Map<string, ClosedBrowserTab[]>();

export function resetClosedTabsForTest(): void {
  closedTabsBySession.clear();
}

const HIDDEN_RECT: BrowserRect = { x: 0, y: 0, width: 0, height: 0 };

// ResizeHandle.tsx sits at the inspector panel's left edge with a
// `--size-resize-handle-offset` (6px) negative inset, so only its right half
// (0 to 6px, inside the panel) survives the panel's `overflow-hidden` — the
// left half is clipped away. That surviving 6px is inside `[data-panel]`, the
// same territory the browser view fills, so without this reserve the native
// view covers the handle at rest and a drag can never start once a page is
// loaded (only continuing an already-started drag is handled elsewhere, via
// the `is-resizing-x` watcher below). Keep in sync with tokens.css.
const RESIZE_HANDLE_RESERVE_PX = 6;

// The native WebContentsView is a window-level overlay, so DOM `overflow:
// hidden` never clips it — it paints wherever the slot's bounding box lands.
// During inspector open/close the column slides on a transform (the slot box
// moves without a ResizeObserver size change), so also clip to `.session-split`.
function visibleSlotRect(node: HTMLElement): BrowserRect {
  const rect = node.getBoundingClientRect();
  let { left, top, right, bottom } = rect;
  const panel = node.closest<HTMLElement>("[data-panel]");
  if (panel) {
    const bounds = panel.getBoundingClientRect();
    left = Math.max(left, bounds.left + RESIZE_HANDLE_RESERVE_PX);
    top = Math.max(top, bounds.top);
    right = Math.min(right, bounds.right);
    bottom = Math.min(bottom, bounds.bottom);
  }
  const split = node.closest<HTMLElement>(".session-split");
  if (split) {
    const bounds = split.getBoundingClientRect();
    left = Math.max(left, bounds.left);
    top = Math.max(top, bounds.top);
    right = Math.min(right, bounds.right);
    bottom = Math.min(bottom, bounds.bottom);
  }
  return {
    x: left,
    y: top,
    width: Math.max(0, right - left),
    height: Math.max(0, bottom - top),
  };
}

// A plain browser has no window.ao bridge, so the native WebContentsView never
// exists there. Detected once per call; `window.ao` is present for the whole
// lifetime of an Electron renderer and absent for the whole lifetime of the web
// app, so the branch below is stable across renders (rules-of-hooks safe).
function hasNativeBrowser(): boolean {
  return typeof window !== "undefined" && !!window.ao?.browser;
}

export function useBrowserView(
  options: UseBrowserViewOptions,
): BrowserViewModel {
  const native = useNativeBrowserView(options);
  const web = useWebBrowserView(options, !hasNativeBrowser());
  return hasNativeBrowser() ? native : web;
}

// `requestFullscreen` (the terminal pane's fullscreen button) promotes an element
// into the DOM top layer, which covers every other DOM node — but not the native
// view, which Chromium composites above the page regardless. The transition also
// leaves the slot's own box untouched, since the top layer does not reflow
// normal-flow siblings, so neither the ResizeObserver nor `resize` fires and the
// view would keep painting at its pre-fullscreen bounds, over the fullscreen
// element and without its own (now hidden) toolbar. Nothing outside the
// fullscreen subtree is visible, so hide the view unless the slot is inside it.
function hiddenByFullscreen(node: HTMLElement): boolean {
  // Truthy, not `!== null`: the spec says null, but jsdom (and older engines)
  // leave `fullscreenElement` undefined when nothing is fullscreen.
  const fullscreen = document.fullscreenElement;
  return Boolean(fullscreen) && !fullscreen!.contains(node);
}

function useNativeBrowserView({
  session,
  active,
  poppedOut,
  terminated,
  previewUrl,
  previewRevision,
  persistentProfile,
}: UseBrowserViewOptions): BrowserViewModel {
  const sessionId = refKey(session);
  // Which browser profile this session gets is a PROJECT decision. The daemon
  // stamps the same key onto every agent command, so an agent-first session is
  // already correct without the renderer; this covers the other order — a human
  // opening the panel before any agent command lands. Both sides read the same
  // project config, so they cannot disagree about the partition.
  const profileKey = persistentProfile?.pending
    ? ""
    : (persistentProfile?.key ?? "");
  const profileKeyResolved = persistentProfile?.pending !== true;
  const [viewId, setViewId] = useState("");
  const [navState, setNavState] = useState<BrowserNavState>(EMPTY_NAV_STATE);
  const [annotationMode, setAnnotationModeState] = useState(false);
  const [tabsState, setTabsState] =
    useState<BrowserTabsState>(EMPTY_TABS_STATE);
  // Display-only tab order (drag-to-reorder). Re-projected onto every incoming
  // tabsState push below, since the main process's own tab order is not
  // authoritative and browser:tabsState pushes on every nav/title event.
  const [tabOrder, setTabOrder] = useState<string[]>([]);
  const [devtoolsState, setDevtoolsState] =
    useState<BrowserDevToolsState>(EMPTY_DEVTOOLS_STATE);
  const [tabNotice, setTabNotice] = useState("");
  const [closedTabs, setClosedTabs] = useState<ClosedBrowserTab[]>([]);
  const [agentBrowserActive, setAgentBrowserActive] = useState(false);
  const [agentBrowserActivity, setAgentBrowserActivity] =
    useState<BrowserAgentActivityState | null>(null);
  const [stateSessionId, setStateSessionId] = useState(sessionId);
  const slotNodeRef = useRef<HTMLDivElement | null>(null);
  const viewIdRef = useRef("");
  const annotationModeRef = useRef(false);
  const activeRef = useRef(active);
  const poppedOutRef = useRef(poppedOut);
  const frameRef = useRef<number | null>(null);
  const settleTimerRef = useRef<number | null>(null);
  const observerRef = useRef<ResizeObserver | null>(null);
  const previewTriggerRef = useRef<{
    revision: number | null;
    target: string;
  } | null>(null);
  const overlayOpenRef = useRef(false);
  const tabNoticeTimerRef = useRef<number | null>(null);
  const tabsStateRef = useRef(tabsState);
  const hasNativeBrowser = Boolean(window.ao?.browser);

  useEffect(() => {
    activeRef.current = active;
  }, [active]);

  useEffect(() => {
    tabsStateRef.current = tabsState;
  }, [tabsState]);

  useEffect(() => {
    annotationModeRef.current = annotationMode;
  }, [annotationMode]);

  const showTabNotice = useCallback((message: string) => {
    setTabNotice(message);
    if (tabNoticeTimerRef.current !== null)
      window.clearTimeout(tabNoticeTimerRef.current);
    tabNoticeTimerRef.current = window.setTimeout(() => {
      tabNoticeTimerRef.current = null;
      setTabNotice("");
    }, 3_000);
  }, []);

  // Single choke point for every closedTabs mutation, so the module-level
  // per-session cache (closedTabsBySession) can never drift from what's
  // actually shown.
  const updateClosedTabs = useCallback(
    (updater: (current: ClosedBrowserTab[]) => ClosedBrowserTab[]) => {
			const current = closedTabsBySession.get(sessionId) ?? [];
        const next = updater(current);
        closedTabsBySession.set(sessionId, next);
			setClosedTabs(next);
    },
    [sessionId],
  );

  const sendHiddenBounds = useCallback((id = viewIdRef.current) => {
    if (!id) return;
    window.ao?.browser.setBounds({
      viewId: id,
      rect: HIDDEN_RECT,
      visible: false,
    });
  }, []);

  const measureAndSend = useCallback(() => {
    // measureAndSend runs both from the scheduleMeasure() rAF callback and as a
    // direct synchronous call (parking on overlay open, the settle timer). A
    // direct call may land while a scheduled frame is still queued, so cancel
    // that live handle rather than blindly nulling it — otherwise the
    // scheduleMeasure() dedupe guard and cancelScheduledMeasure() cleanup would
    // both trust a frameRef that no longer reflects the pending frame.
    if (frameRef.current !== null) {
      if (window.cancelAnimationFrame)
        window.cancelAnimationFrame(frameRef.current);
      window.clearTimeout(frameRef.current);
    }
    frameRef.current = null;
    const id = viewIdRef.current;
    const node = slotNodeRef.current;
    if (!id) return;
    if (
      !activeRef.current ||
      !node ||
      !node.isConnected ||
      hiddenByFullscreen(node)
    ) {
      sendHiddenBounds(id);
      return;
    }
    const rect = visibleSlotRect(node);
    const payload = {
      viewId: id,
      rect,
      visible: rect.width > 0 && rect.height > 0,
    };
    window.ao?.browser.setBounds(payload);
  }, [sendHiddenBounds]);

  const cancelScheduledMeasure = useCallback(() => {
    if (frameRef.current === null) return;
    if (window.cancelAnimationFrame) {
      window.cancelAnimationFrame(frameRef.current);
    }
    window.clearTimeout(frameRef.current);
    frameRef.current = null;
  }, []);

  const scheduleMeasure = useCallback(() => {
    if (frameRef.current !== null) return;
    frameRef.current = window.requestAnimationFrame
      ? window.requestAnimationFrame(() => measureAndSend())
      : window.setTimeout(() => measureAndSend(), 16);
  }, [measureAndSend]);

  // A ResizeObserver only fires on size changes, so a position-only layout shift
  // leaves the native overlay at stale bounds: entering/leaving pop-out moves the
  // slot into a different panel, and opening the inspector (what `ao preview`
  // does) reflows the slot's x without changing the observed node's box size.
  // Neither fires the observer, so the view visibly spills over the sidebar/
  // terminal until an unrelated window resize re-measures it. Re-measure now and
  // again once the panel transition has settled (~240ms) so the final geometry
  // always wins.
  const scheduleSettleMeasure = useCallback(() => {
    scheduleMeasure();
    if (settleTimerRef.current !== null)
      window.clearTimeout(settleTimerRef.current);
    settleTimerRef.current = window.setTimeout(() => {
      settleTimerRef.current = null;
      measureAndSend();
    }, 280);
  }, [measureAndSend, scheduleMeasure]);

  const slotRef = useCallback(
    (node: HTMLDivElement | null) => {
      observerRef.current?.disconnect();
      slotNodeRef.current = node;
      if (!node) {
        sendHiddenBounds();
        return;
      }
      const observer = new ResizeObserver(scheduleMeasure);
      observer.observe(node);
      // The inspector column keeps a stable width and slides on `x`; the
      // layout gap's width is what actually changes every spring frame.
      // Observing it re-measures through the whole animation so the native
      // view tracks the sliding rail instead of lagging at the last size.
      const column = node.closest("[data-panel]");
      if (column) observer.observe(column);
      const gap = node
        .closest(".session-split")
        ?.querySelector("[data-slot='inspector-gap']");
      if (gap) observer.observe(gap);
      observerRef.current = observer;
      scheduleMeasure();
    },
    [scheduleMeasure, sendHiddenBounds],
  );

  useEffect(() => {
    let disposed = false;
    // Preview revisions are scoped to a session. A native view survives session
    // switches, so seed from the per-session consumed trigger to avoid
    // reasserting previewUrl over manual navigation on switch-back.
    previewTriggerRef.current = hasNativeBrowser
      ? (consumedPreviewTriggers.get(sessionId) ?? null)
      : null;
    setStateSessionId(sessionId);
    setViewId("");
    setNavState(EMPTY_NAV_STATE);
    setTabsState(EMPTY_TABS_STATE);
    // Tab ids (`t1`, `t2`, ...) restart per session, so a stale order from the
    // previous session could otherwise silently reapply to the new one.
    setTabOrder([]);
    setDevtoolsState(EMPTY_DEVTOOLS_STATE);
    setTabNotice("");
    // Restore this session's own Recently Closed list rather than wiping it —
    // switching away and back should find it exactly as it was, same as the
    // live tabs the native view already keeps.
    setClosedTabs(closedTabsBySession.get(sessionId) ?? []);
    setAgentBrowserActive(false);
    setAgentBrowserActivity(null);
    if (tabNoticeTimerRef.current !== null) {
      window.clearTimeout(tabNoticeTimerRef.current);
      tabNoticeTimerRef.current = null;
    }
    if (!hasNativeBrowser) {
      const state = {
        ...EMPTY_NAV_STATE,
        viewId: `preview-${sessionId}`,
        url: "",
        title: "",
      };
      viewIdRef.current = state.viewId;
      setViewId(state.viewId);
      setNavState(state);
      setDevtoolsState((current) => ({
        ...current,
        viewId: state.viewId,
        activeTabId: "",
      }));
      return () => {
        disposed = true;
        viewIdRef.current = "";
      };
    }
    // Deferred, not skipped: creating the view now and learning the project's
    // answer afterwards would fix the partition to the wrong one for the life of
    // the session, and a WebContentsView's partition cannot be changed later.
    //
    // The key is passed only when there IS one, so the default path's IPC call
    // stays exactly the single-argument call it has always been.
    // The third argument is the session's host, so a remote project's key lands
    // on a host-scoped partition ("local" is a no-op in main). It rides along
    // with the key because it only ever modifies one.
    const ensured = !profileKeyResolved
      ? undefined
      : profileKey
        ? window.ao?.browser.ensure(sessionId, profileKey, session.host)
        : window.ao?.browser.ensure(sessionId);
    ensured?.then((state) => {
      if (disposed) return;
      viewIdRef.current = state.viewId;
      setViewId(state.viewId);
      setNavState(state);
      void window.ao?.browser
        .getTabs(state.viewId)
        .then((tabs) => {
          if (!disposed && viewIdRef.current === tabs.viewId)
            setTabsState(tabs);
        })
        .catch(() => undefined);
      scheduleSettleMeasure();
    });
    return () => {
      disposed = true;
      const id = viewIdRef.current;
      if (id) {
        if (annotationModeRef.current) {
          void window.ao?.browser.setAnnotationMode({
            viewId: id,
            enabled: false,
          });
          setAnnotationModeState(false);
        }
        sendHiddenBounds(id);
      }
      viewIdRef.current = "";
    };
  }, [
    hasNativeBrowser,
    profileKey,
    profileKeyResolved,
    scheduleSettleMeasure,
    sendHiddenBounds,
    sessionId,
  ]);

  useEffect(() => {
    return window.ao?.browser.onNavState((state) => {
      if (state.viewId !== viewIdRef.current) return;
      setNavState(state);
    });
  }, []);

  useEffect(() => {
    return window.ao?.browser.onTabsState((state) => {
      if (state.viewId !== viewIdRef.current) return;
      setTabsState(state);
			const change = state.change;
			if (change?.kind === "popup") {
      showTabNotice("Opened new tab");
				return;
			}
			if (change?.kind !== "closed" || !change.tab || isBlankTabUrl(change.tab.url)) return;
			const { id, title, url, favicon } = change.tab;
			updateClosedTabs((current) => [
				{ id, title, url, favicon },
				...current.filter((tab) => tab.id !== id),
			].slice(0, MAX_CLOSED_TABS));
    });
	}, [showTabNotice, updateClosedTabs]);

  // Re-project the persisted display order onto every incoming tabsState push:
  // browser:tabsState fires on every nav/title-update/loading-state change for
  // any tab, so a one-shot local reorder would otherwise be clobbered by the
  // very next push. New tabs (via "+", popups, agent tab-new) append at the end.
  useEffect(() => {
    const incomingIds = tabsState.tabs.map((tab) => tab.id);
    setTabOrder((prev) => {
      const kept = prev.filter((id) => incomingIds.includes(id));
      const added = incomingIds.filter((id) => !kept.includes(id));
      return kept.length === prev.length && added.length === 0
        ? prev
        : [...kept, ...added];
    });
  }, [tabsState.tabs]);

  const tabs = useMemo(() => {
    const byId = new Map(tabsState.tabs.map((tab) => [tab.id, tab]));
    return tabOrder
      .map((id) => byId.get(id))
      .filter((tab): tab is BrowserTabState => Boolean(tab));
  }, [tabOrder, tabsState.tabs]);

  const reorderTabs = useCallback(
    (orderedIds: string[]) => setTabOrder(orderedIds),
    [],
  );

  useEffect(() => {
    return window.ao?.browser.onDevToolsState((state) => {
      if (state.viewId !== viewIdRef.current) return;
      setDevtoolsState(state);
    });
  }, []);

  useEffect(() => {
    return window.ao?.browser.onAgentActivity((state) => {
      if (state.viewId !== viewIdRef.current) return;
      setAgentBrowserActive(state.active);
      setAgentBrowserActivity(state);
    });
  }, []);

  useEffect(
    () => () => {
      if (tabNoticeTimerRef.current !== null)
        window.clearTimeout(tabNoticeTimerRef.current);
    },
    [],
  );

  useLayoutEffect(() => {
    if (active) {
      scheduleSettleMeasure();
    } else {
      sendHiddenBounds();
    }
  }, [
    active,
    navState.url,
    poppedOut,
    scheduleSettleMeasure,
    sendHiddenBounds,
  ]);

  useEffect(() => {
    if (poppedOutRef.current === poppedOut) return;
    poppedOutRef.current = poppedOut;
    if (!hasNativeBrowser || !activeRef.current) {
      scheduleSettleMeasure();
      return;
    }
    measureAndSend();
    window.setTimeout(() => {
      measureAndSend();
    }, 0);
    scheduleSettleMeasure();
  }, [hasNativeBrowser, measureAndSend, poppedOut, scheduleSettleMeasure]);

  useEffect(() => {
    if (!hasNativeBrowser) return;
    const update = () => {
      const open =
        document.body.classList.contains("is-resizing-x") ||
        document.querySelector(OPEN_BROWSER_OVERLAY_SELECTOR) !== null;
      if (open === overlayOpenRef.current) return;
      overlayOpenRef.current = open;
      // The live page never moves or becomes a bitmap. Reordering the explicit
      // transparent shell is the complete overlay handoff.
      window.ao?.browser.setOverlayOpen(open);
      if (!open) scheduleSettleMeasure();
    };
    update();
    const observer = new MutationObserver(update);
    // Radix reuses its portal node and flips `data-state` in place rather than
    // adding/removing a body child, so a `childList`-only observer misses the
    // open/close transition under rapid toggling and the overlay state desyncs.
    // Watch subtree attribute flips on `data-state` too so the transition is
    // always observed. This widens the firing rate a lot — `data-state` is used
    // across Radix (tooltips, accordions, selects, switches, …), so `update()`
    // now runs a document-wide querySelector on activity anywhere in the app
    // before it can bail. Cheap enough in practice, but not free.
    observer.observe(document.body, {
      childList: true,
      subtree: true,
      attributes: true,
      attributeFilter: ["data-state"],
    });
    // useResizable.ts toggles `is-resizing-x` on <body> (outside React) while the
    // inspector's own drag handle is held — that handle sits right at the edge of
    // the browser panel, and the WebContentsView swallows every pointer event the
    // instant the cursor crosses into it, killing the drag dead mid-resize. Raise
    // the shell for the same duration so the drag keeps receiving events there too.
    // A dedicated, non-subtree observer keeps this cheap: unlike `data-state` above,
    // `class` churns on nearly every render throughout the app, so watching it
    // subtree-wide would run `update()` far more often than the dialog/menu case.
    const resizeObserver = new MutationObserver(update);
    resizeObserver.observe(document.body, {
      attributes: true,
      attributeFilter: ["class"],
    });
    return () => {
      observer.disconnect();
      resizeObserver.disconnect();
      window.ao?.browser.setOverlayOpen(false);
      overlayOpenRef.current = false;
    };
  }, [hasNativeBrowser, scheduleSettleMeasure]);

  useEffect(() => {
    const handle = () => scheduleMeasure();
    // Fullscreen animates on macOS, so settle-measure: hiding lands on the
    // leading edge, and the restore on exit waits for the final geometry.
    const handleFullscreenChange = () => scheduleSettleMeasure();
    window.addEventListener("resize", handle);
    window.addEventListener("scroll", handle, true);
    document.addEventListener("fullscreenchange", handleFullscreenChange);
    return () => {
      window.removeEventListener("resize", handle);
      window.removeEventListener("scroll", handle, true);
      document.removeEventListener("fullscreenchange", handleFullscreenChange);
      observerRef.current?.disconnect();
      cancelScheduledMeasure();
      if (settleTimerRef.current !== null)
        window.clearTimeout(settleTimerRef.current);
    };
  }, [cancelScheduledMeasure, scheduleMeasure, scheduleSettleMeasure]);

  const withView = useCallback(
    async (fn: (id: string) => Promise<BrowserNavState | void>) => {
      const id = viewIdRef.current;
      if (!id) return;
      try {
        const next = await fn(id);
        if (next) setNavState(next);
      } catch {
        // navigation errors are handled by the did-fail-load event channel
      }
    },
    [],
  );

  const setAnnotationMode = useCallback(
    async (enabled: boolean) => {
      const id = viewIdRef.current;
      if (!id || !hasNativeBrowser) {
        setAnnotationModeState(false);
        return;
      }
      await window.ao!.browser.setAnnotationMode({ viewId: id, enabled });
      setAnnotationModeState(enabled);
    },
    [hasNativeBrowser],
  );

  const selectTab = useCallback(
    async (tabId: string) => {
      const viewId = viewIdRef.current;
      if (!viewId || !hasNativeBrowser) return;
      try {
        const state = await window.ao!.browser.selectTab({ viewId, tabId });
        if (viewIdRef.current === state.viewId) setTabsState(state);
      } catch {
        // Fire-and-forget from the rail (`void onSelectTab(...)`) — without
        // this the click just silently does nothing, with no way to tell a
        // slow response from a dead button.
        showTabNotice("Couldn't switch to that tab");
      }
    },
    [hasNativeBrowser, showTabNotice],
  );

  const closeTab = useCallback(
    async (tabId: string) => {
      const viewId = viewIdRef.current;
      if (!viewId || !hasNativeBrowser) return;
      // Read from the ref, not the tabsState closure, so this callback's
      // identity stays stable across tab updates instead of churning on
      // every nav/title-update/loading-state push (it cascades into
      // handleCloseTab in BrowserTabsRail.tsx otherwise).
      const closing = tabsStateRef.current.tabs.find((tab) => tab.id === tabId);
      try {
        const state = await window.ao!.browser.closeTab({ viewId, tabId });
        if (viewIdRef.current !== state.viewId) return;
        setTabsState(state);
        // Only remember it once the main process confirms it's actually gone —
        // closeTab can silently no-op (the tab stays in state.tabs), and
        // recording it as "closed" anyway would show it in Recently Closed
        // while it's still sitting right there in the live tab list. Only real,
        // distinguishable tabs are worth keeping — a blank tab has nothing to
        // reopen.
        const stillOpen = state.tabs.some((tab) => tab.id === tabId);
        if (closing && !stillOpen && !isBlankTabUrl(closing.url)) {
          const { id, title, url, favicon } = closing;
          updateClosedTabs((current) =>
            [
              { id, title, url, favicon },
              ...current.filter((t) => t.id !== id),
            ].slice(0, MAX_CLOSED_TABS),
          );
        }
      } catch {
        showTabNotice("Couldn't close that tab");
        // The main process can mutate its own tab state before reporting a
        // close as failed (e.g. the automation runtime's own closeTarget
        // callback already removed the tab, then the overall command still
        // reports failure) — resync instead of leaving this tab's row
        // showing in the rail after it's genuinely gone, which just
        // re-fails identically on every retry.
        window.ao?.browser
          .getTabs(viewId)
          .then((state) => {
            if (viewIdRef.current === state.viewId) setTabsState(state);
          })
          .catch(() => undefined);
      }
    },
    [hasNativeBrowser, showTabNotice, updateClosedTabs],
  );

  const openTab = useCallback(
    async (url?: string) => {
      const viewId = viewIdRef.current;
      if (!viewId || !hasNativeBrowser) return;
      const state = await window.ao!.browser.openTab({ viewId, url });
      if (viewIdRef.current === state.viewId) setTabsState(state);
    },
    [hasNativeBrowser],
  );

  const reopenClosedTab = useCallback(
		async (tabId?: string) => {
			const current = closedTabsBySession.get(sessionId) ?? [];
			const entry = tabId ? current.find((tab) => tab.id === tabId) : current[0];
      if (!entry) return;
      // Gate on the same cap the "+" button already respects, instead of
      // discovering BROWSER_TAB_LIMIT only after the entry is gone.
      if (tabsStateRef.current.tabs.length >= MAX_BROWSER_TABS) {
        showTabNotice("Reached the tab limit");
        return;
      }
      updateClosedTabs((current) =>
        current.filter((tab) => tab.id !== entry.id),
      );
      try {
        await openTab(entry.url);
      } catch {
        // Still possible to race the cap (e.g. the agent opens a tab between
        // this row rendering and the click) — restore instead of losing the
        // entry silently.
        updateClosedTabs((current) =>
          [entry, ...current.filter((tab) => tab.id !== entry.id)].slice(
            0,
            MAX_CLOSED_TABS,
          ),
        );
        showTabNotice("Couldn't reopen that tab");
      }
    },
		[openTab, sessionId, showTabNotice, updateClosedTabs],
  );

  const runDevtools = useCallback(
    async (
      operation: "open" | "close" | "setPlacement",
      placement?: BrowserDevToolsPlacement,
    ) => {
      const id = viewIdRef.current;
      if (!id || !hasNativeBrowser) return;
      try {
        const next = await window.ao!.browser.devtools({
          viewId: id,
          operation,
          placement,
        });
        if (viewIdRef.current === next.viewId) setDevtoolsState(next);
      } catch {
        // The main process reports the unavailable state through the normal
        // browser lifecycle; a failed optional DevTools action should not
        // become an unhandled renderer rejection.
      }
    },
    [hasNativeBrowser],
  );

  useEffect(() => {
    const handleDone = (
      payload: BrowserAnnotationSubmitPayload | BrowserAnnotationCancelPayload,
    ) => {
      if (payload.viewId !== viewIdRef.current) return;
      setAnnotationModeState(false);
    };
    const offSubmit = window.ao?.browser.onAnnotationSubmit(handleDone);
    const offCancel = window.ao?.browser.onAnnotationCancel(handleDone);
    return () => {
      offSubmit?.();
      offCancel?.();
    };
  }, []);

  useEffect(() => {
    if (navState.url || !annotationModeRef.current) return;
    void setAnnotationMode(false);
  }, [navState.url, setAnnotationMode]);

  const navigate = useCallback(
    (url: string) => {
      if (!hasNativeBrowser) {
        const normalized = url.trim();
        setNavState((current) => ({
          ...current,
          url: normalized,
          title: normalized ? "AO preview" : "",
          isLoading: false,
        }));
        return Promise.resolve();
      }
      return withView((id) => window.ao!.browser.navigate({ viewId: id, url }));
    },
    [hasNativeBrowser, withView],
  );

  const clear = useCallback(() => {
    if (!hasNativeBrowser) {
      setNavState((current) => ({
        ...current,
        url: "",
        title: "",
        isLoading: false,
      }));
      return Promise.resolve();
    }
    return withView((id) => window.ao!.browser.clear(id));
  }, [hasNativeBrowser, withView]);

  // Drive the view from the daemon-set preview target. Current daemons key
  // this on previewRevision (bumped on every `ao preview` call); older daemons
  // did not send it, so fall back to URL changes for compatibility.
  useEffect(() => {
    // During a session switch React still renders once with the prior
    // viewId state, while the cleanup has already cleared viewIdRef. Do not
    // consume the new session's preview revision against that stale view.
    if (!viewId || viewIdRef.current !== viewId || terminated) return;
    const target = previewUrl?.trim() ?? "";
    const revision =
      typeof previewRevision === "number" ? previewRevision : null;
    const previous = previewTriggerRef.current;
    if (previous?.revision === revision && previous.target === target) return;
    if (revision !== null && previous?.revision === revision) return;
    const consumed: PreviewTrigger = { revision, target };
    previewTriggerRef.current = consumed;
    if (hasNativeBrowser) consumedPreviewTriggers.set(sessionId, consumed);
    if (target) {
      void navigate(target);
    } else if ((revision !== null && revision > 0) || previous?.target) {
      void clear();
    }
  }, [
    clear,
    hasNativeBrowser,
    navigate,
    previewRevision,
    previewUrl,
    sessionId,
    terminated,
    viewId,
  ]);

  const destroy = useCallback(() => {
    const id = viewIdRef.current;
    if (!id) return;
    if (annotationModeRef.current) {
      void window.ao?.browser.setAnnotationMode({ viewId: id, enabled: false });
      setAnnotationModeState(false);
    }
    overlayOpenRef.current = false;
    sendHiddenBounds(id);
    window.ao?.browser.destroy(id);
    viewIdRef.current = "";
    setViewId("");
    setNavState(EMPTY_NAV_STATE);
    setTabsState(EMPTY_TABS_STATE);
    setClosedTabs([]);
  }, [sendHiddenBounds]);

  // Termination invalidates the complete session-owned browser, including all
  // tabs, captures, profile state, and target mappings. `clear` remains the
  // explicit preview-reset operation.
  useEffect(() => {
    if (!terminated || !viewId) return;
    consumedPreviewTriggers.delete(sessionId);
    closedTabsBySession.delete(sessionId);
    destroy();
  }, [destroy, sessionId, terminated, viewId]);

  // Hook state survives a `sessionId` prop change until the reset effect above
  // commits. Keep navigation, tab, and activity state hidden during that
  // intervening render so consumers can never interpret the departed session's
  // state as belonging to the destination session.
  const stateBelongsToSession = stateSessionId === sessionId;

  return {
    viewId: stateBelongsToSession ? viewId : "",
    navState: stateBelongsToSession ? navState : EMPTY_NAV_STATE,
    slotRef,
    navigate,
    goBack: () =>
      hasNativeBrowser
        ? withView((id) => window.ao!.browser.goBack(id))
        : Promise.resolve(),
    goForward: () =>
      hasNativeBrowser
        ? withView((id) => window.ao!.browser.goForward(id))
        : Promise.resolve(),
    reload: () =>
      hasNativeBrowser
        ? withView((id) => window.ao!.browser.reload(id))
        : Promise.resolve(),
    stop: () =>
      hasNativeBrowser
        ? withView((id) => window.ao!.browser.stop(id))
        : Promise.resolve(),
    tabs: stateBelongsToSession ? tabs : [],
    activeTabId: stateBelongsToSession ? tabsState.activeTabId : "",
    tabNotice: stateBelongsToSession ? tabNotice : "",
    selectTab,
    closeTab,
    openTab,
    reorderTabs,
    closedTabs: stateBelongsToSession ? closedTabs : [],
    reopenClosedTab,
    devtoolsState: stateBelongsToSession ? devtoolsState : EMPTY_DEVTOOLS_STATE,
    openDevTools: () => runDevtools("open"),
    closeDevTools: () => runDevtools("close"),
    setDevToolsPlacement: (placement) => runDevtools("setPlacement", placement),
    agentBrowserActive: stateBelongsToSession && agentBrowserActive,
    agentBrowserActivity: stateBelongsToSession ? agentBrowserActivity : null,
    destroy,
    annotationMode,
    setAnnotationMode,
    mode: "native",
    iframeSrc: "",
    iframeKey: 0,
  };
}

// Add a scheme to a user- or preview-supplied URL so it is loadable in an
// <iframe>. Only http/https can be framed from the web app's http(s) origin
// (file:// is cross-origin-blocked), so anything else resolves to "" and the
// panel keeps its empty state. Bare hosts default to http for localhost-like
// targets (the `ao preview` dev-server case) and https otherwise, mirroring the
// native host's withDefaultScheme.
export function normalizeWebPreviewURL(raw: string): string {
  const trimmed = raw.trim();
  if (trimmed === "") return "";
  const hasScheme = /^[a-zA-Z][a-zA-Z\d+.-]*:\/\//.test(trimmed);
  const isLocal =
    /^(localhost|127(?:\.\d{1,3}){3}|0\.0\.0\.0|\[::1\])(?::\d+)?(?:[/?#]|$)/i.test(
      trimmed,
    );
  const candidate = hasScheme
    ? trimmed
    : `${isLocal ? "http" : "https"}://${trimmed}`;
  try {
    const url = new URL(candidate);
    return url.protocol === "http:" || url.protocol === "https:"
      ? url.href
      : "";
  } catch {
    return "";
  }
}

// Web-app fallback: no WebContentsView, so preview lives in an <iframe>. Tracks
// the navigated URL and a reload nonce; the panel renders the iframe. Cross-
// origin framing hides history/title, so back/forward/stop are inert and the
// URL bar reflects only what we navigated to. `enabled` is false in Electron so
// this hook stays inert while the native one drives the real view.
function useWebBrowserView(
  { session, previewUrl, previewRevision }: UseBrowserViewOptions,
  enabled: boolean,
): BrowserViewModel {
  const sessionId = refKey(session);
  const [url, setUrl] = useState("");
  const [iframeKey, setIframeKey] = useState(0);
  const previewTriggerRef = useRef<{
    revision: number | null;
    target: string;
  } | null>(null);
  const slotRef = useCallback(() => {}, []);

  const navigate = useCallback(async (next: string) => {
    const normalized = normalizeWebPreviewURL(next);
    if (!normalized) return;
    setUrl(normalized);
    setIframeKey((key) => key + 1);
  }, []);

  const clear = useCallback(async () => {
    setUrl("");
  }, []);

  const reload = useCallback(async () => {
    setIframeKey((key) => key + 1);
  }, []);

  // Reset when the session changes so one worker's preview never leaks into the
  // next (mirrors the native ensure()-per-session lifecycle).
  useEffect(() => {
    if (!enabled) return;
    setUrl("");
    previewTriggerRef.current = null;
  }, [enabled, sessionId]);

  // Drive the iframe from `ao preview` exactly like the native path.
  useEffect(() => {
    if (!enabled) return;
    const target = previewUrl?.trim() ?? "";
    const revision =
      typeof previewRevision === "number" ? previewRevision : null;
    const previous = previewTriggerRef.current;
    if (previous?.revision === revision && previous.target === target) return;
    if (revision !== null && previous?.revision === revision) return;
    previewTriggerRef.current = { revision, target };
    if (target) {
      void navigate(target);
    } else if ((revision !== null && revision > 0) || previous?.target) {
      void clear();
    }
  }, [clear, enabled, navigate, previewRevision, previewUrl]);

  const navState: BrowserNavState = {
    viewId: url ? "web" : "",
    url,
    title: url,
    canGoBack: false,
    canGoForward: false,
    isLoading: false,
  };

  return {
    viewId: url ? "web" : "",
    navState,
    devtoolsState: EMPTY_DEVTOOLS_STATE,
    openDevTools: async () => {},
    closeDevTools: async () => {},
    setDevToolsPlacement: async () => {},
    slotRef,
    navigate,
    goBack: async () => {},
    goForward: async () => {},
    reload,
    stop: async () => {},
    // Tabs and the agent browser are driven by the native WebContentsView
    // broker, which the web app has no equivalent for: an empty tab list
    // disables the tab controls rather than showing ones that cannot work.
    tabs: [],
    activeTabId: "",
    tabNotice: "",
    selectTab: async () => {},
    closeTab: async () => {},
    openTab: async () => {},
    reorderTabs: () => {},
    closedTabs: [],
    reopenClosedTab: async () => {},
    agentBrowserActive: false,
    agentBrowserActivity: null,
    destroy: () => setUrl(""),
    annotationMode: false,
    setAnnotationMode: async () => {},
    mode: "web",
    iframeSrc: url,
    iframeKey,
  };
}
