import { RECONNECT_BRIDGE_MESSAGE } from "../shared/messages";

export interface WebNavigationDetails {
  tabId: number;
  url: string;
  frameId: number;
}

export interface WebNavigationEvent {
  addListener: (callback: (details: WebNavigationDetails) => void) => void;
}

export interface WebNavigationApi {
  onCompleted: WebNavigationEvent;
  onHistoryStateUpdated: WebNavigationEvent;
  onReferenceFragmentUpdated: WebNavigationEvent;
}

export interface TabMessagingApi {
  sendMessage: (tabId: number, message: unknown) => Promise<unknown>;
}

export interface NavigationReconnectDeps {
  webNavigation: WebNavigationApi;
  tabs: TabMessagingApi;
  /** Origins the edge server currently allows (from the last `ready` frame). */
  getAllowedOrigins: () => string[];
  /** True when the tab already has a live bridge (an appSessionRegistry entry) — skip it. */
  isTabBridged: (tabId: number) => boolean;
}

function originOf(url: string): string | undefined {
  try {
    return new URL(url).origin;
  } catch {
    return undefined;
  }
}

/** Re-arms connectWithRetry on navigations (full or SPA route changes) within an allowed origin, skipping already-bridged tabs. */
export function wireNavigationReconnect(deps: NavigationReconnectDeps): void {
  function handleNavigation(details: WebNavigationDetails): void {
    if (details.frameId !== 0) return; // top-level frame only
    const origin = originOf(details.url);
    if (!origin || !deps.getAllowedOrigins().includes(origin)) return;
    if (deps.isTabBridged(details.tabId)) return;

    // No content script listening yet is harmless — ignore.
    void deps.tabs.sendMessage(details.tabId, RECONNECT_BRIDGE_MESSAGE).catch(() => undefined);
  }

  deps.webNavigation.onCompleted.addListener(handleNavigation);
  deps.webNavigation.onHistoryStateUpdated.addListener(handleNavigation);
  deps.webNavigation.onReferenceFragmentUpdated.addListener(handleNavigation);
}
