import { describe, expect, it, vi } from "vitest";
import { RECONNECT_BRIDGE_MESSAGE } from "../shared/messages";
import { wireNavigationReconnect } from "./navigationReconnect";
import type { WebNavigationApi, WebNavigationDetails } from "./navigationReconnect";

type NavigationEventName = "onCompleted" | "onHistoryStateUpdated" | "onReferenceFragmentUpdated";

function createFakeWebNavigation() {
  const listeners: Record<NavigationEventName, Array<(details: WebNavigationDetails) => void>> = {
    onCompleted: [],
    onHistoryStateUpdated: [],
    onReferenceFragmentUpdated: [],
  };
  const api: WebNavigationApi = {
    onCompleted: { addListener: (cb) => listeners.onCompleted.push(cb) },
    onHistoryStateUpdated: { addListener: (cb) => listeners.onHistoryStateUpdated.push(cb) },
    onReferenceFragmentUpdated: { addListener: (cb) => listeners.onReferenceFragmentUpdated.push(cb) },
  };
  return {
    api,
    fire: (event: NavigationEventName, details: WebNavigationDetails) => {
      for (const listener of listeners[event]) listener(details);
    },
  };
}

function setup(options: { allowedOrigins?: string[]; bridgedTabIds?: number[] } = {}) {
  const webNavigation = createFakeWebNavigation();
  const sendMessage = vi.fn(async () => undefined);
  const bridgedTabIds = new Set(options.bridgedTabIds ?? []);

  wireNavigationReconnect({
    webNavigation: webNavigation.api,
    tabs: { sendMessage },
    getAllowedOrigins: () => options.allowedOrigins ?? ["http://payable.lvh.me:4456"],
    isTabBridged: (tabId) => bridgedTabIds.has(tabId),
  });

  return { fire: webNavigation.fire, sendMessage };
}

describe("wireNavigationReconnect", () => {
  it("asks an unbridged tab's content script to reconnect on a full navigation completing", () => {
    const { fire, sendMessage } = setup();

    fire("onCompleted", { tabId: 7, url: "http://payable.lvh.me:4456/invoices", frameId: 0 });

    expect(sendMessage).toHaveBeenCalledWith(7, RECONNECT_BRIDGE_MESSAGE);
  });

  it("also reconnects on an SPA history-state route change", () => {
    const { fire, sendMessage } = setup();

    fire("onHistoryStateUpdated", { tabId: 7, url: "http://payable.lvh.me:4456/reports", frameId: 0 });

    expect(sendMessage).toHaveBeenCalledWith(7, RECONNECT_BRIDGE_MESSAGE);
  });

  it("also reconnects on a reference-fragment (hash route) update", () => {
    const { fire, sendMessage } = setup();

    fire("onReferenceFragmentUpdated", { tabId: 7, url: "http://payable.lvh.me:4456/#/reports", frameId: 0 });

    expect(sendMessage).toHaveBeenCalledWith(7, RECONNECT_BRIDGE_MESSAGE);
  });

  it("does nothing for a tab that is already bridged", () => {
    const { fire, sendMessage } = setup({ bridgedTabIds: [7] });

    fire("onCompleted", { tabId: 7, url: "http://payable.lvh.me:4456/invoices", frameId: 0 });

    expect(sendMessage).not.toHaveBeenCalled();
  });

  it("does nothing for an origin the edge server does not currently allow", () => {
    const { fire, sendMessage } = setup({ allowedOrigins: ["http://localhost:5173"] });

    fire("onCompleted", { tabId: 7, url: "http://payable.lvh.me:4456/invoices", frameId: 0 });

    expect(sendMessage).not.toHaveBeenCalled();
  });

  it("ignores subframe navigations", () => {
    const { fire, sendMessage } = setup();

    fire("onCompleted", { tabId: 7, url: "http://payable.lvh.me:4456/iframe-content", frameId: 3 });

    expect(sendMessage).not.toHaveBeenCalled();
  });

  it("does not throw when the content script isn't listening yet", async () => {
    const webNavigation = createFakeWebNavigation();
    const sendMessage = vi.fn(async () => {
      throw new Error("Could not establish connection. Receiving end does not exist.");
    });

    wireNavigationReconnect({
      webNavigation: webNavigation.api,
      tabs: { sendMessage },
      getAllowedOrigins: () => ["http://payable.lvh.me:4456"],
      isTabBridged: () => false,
    });

    expect(() =>
      webNavigation.fire("onCompleted", { tabId: 7, url: "http://payable.lvh.me:4456/invoices", frameId: 0 }),
    ).not.toThrow();
  });
});
