import { describe, expect, it, vi } from "vitest";
import { RECONNECT_BRIDGE_MESSAGE } from "../shared/messages";
import type { TransportLike } from "../shared/types";
import { createBackgroundApp } from "./app";
import type { TabsApi } from "./app";
import type { WebSocketLike } from "./edgeSocket";
import type { ScriptingApi } from "./contentScriptSync";
import type { WebNavigationApi, WebNavigationDetails } from "./navigationReconnect";

class FakeWebSocket implements WebSocketLike {
  static instances: FakeWebSocket[] = [];
  readyState = 1;
  sent: string[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onclose: ((event: { code: number }) => void) | null = null;
  onerror: ((event: unknown) => void) | null = null;

  constructor() {
    FakeWebSocket.instances.push(this);
  }

  send(data: string) {
    this.sent.push(data);
  }

  close() {
    this.readyState = 3;
  }

  receive(frame: unknown) {
    this.onmessage?.({ data: JSON.stringify(frame) });
  }
}

function createFakeTransport(): TransportLike {
  return {
    start: vi.fn(async () => undefined),
    send: vi.fn(async () => undefined),
    close: vi.fn(async () => undefined),
    onmessage: null,
    onclose: null,
    onerror: null,
  };
}

function createFakePort(overrides: Partial<chrome.runtime.Port> = {}): chrome.runtime.Port {
  return {
    name: "session-1",
    sender: { origin: "https://app1.example.com", tab: { id: 7 } } as chrome.runtime.MessageSender,
    disconnect: vi.fn(),
    onMessage: { addListener: vi.fn(), removeListener: vi.fn() },
    onDisconnect: { addListener: vi.fn(), removeListener: vi.fn() },
    postMessage: vi.fn(),
    ...overrides,
  } as unknown as chrome.runtime.Port;
}

function createFakeScripting(): ScriptingApi {
  return {
    getRegisteredContentScripts: vi.fn(async () => []),
    registerContentScripts: vi.fn(async () => undefined),
    updateContentScripts: vi.fn(async () => undefined),
    unregisterContentScripts: vi.fn(async () => undefined),
  };
}

function createFakeTabs(tabsById: Record<number, { id: number; url: string }> = {}): TabsApi {
  return {
    query: vi.fn(async () => Object.values(tabsById)),
    sendMessage: vi.fn(async () => undefined),
  };
}

function createFakeWebNavigation() {
  const listeners: Array<(details: WebNavigationDetails) => void> = [];
  const api: WebNavigationApi = {
    onCompleted: { addListener: (cb) => listeners.push(cb) },
    onHistoryStateUpdated: { addListener: (cb) => listeners.push(cb) },
    onReferenceFragmentUpdated: { addListener: (cb) => listeners.push(cb) },
  };
  return { api, fire: (details: WebNavigationDetails) => listeners.forEach((cb) => cb(details)) };
}

function setup(
  options: {
    storedSettings?: { edgeUrl?: string; edgeToken?: string };
    tabsById?: Record<number, { id: number; url: string }>;
  } = {},
) {
  let onConnectListener: ((port: chrome.runtime.Port) => void) | undefined;
  let onMessageListener:
    | ((message: unknown, sender: chrome.runtime.MessageSender, sendResponse: (response: unknown) => void) => void)
    | undefined;
  let onStorageChangedListener:
    | ((changes: Record<string, chrome.storage.StorageChange>, areaName: string) => void)
    | undefined;
  const scripting = createFakeScripting();
  const tabs = createFakeTabs(options.tabsById);
  const webNavigation = createFakeWebNavigation();
  const storedSettings = options.storedSettings ?? {
    edgeUrl: "ws://localhost:8081/edge/ws",
    edgeToken: "edge-token",
  };
  const storageArea = {
    get: vi.fn(async () => storedSettings),
    set: vi.fn(async () => undefined),
    remove: vi.fn(async () => undefined),
  } as unknown as chrome.storage.StorageArea;
  const transport = createFakeTransport();

  const app = createBackgroundApp({
    runtime: {
      onConnect: {
        addListener: (cb) => {
          onConnectListener = cb;
        },
      },
      onMessage: {
        addListener: (cb) => {
          onMessageListener = cb;
        },
      },
    },
    storageOnChanged: {
      addListener: (cb) => {
        onStorageChangedListener = cb;
      },
    },
    scripting,
    storageArea,
    connectSocket: () => new FakeWebSocket(),
    wrapPort: () => transport,
    tabs,
    webNavigation: webNavigation.api,
  });

  return {
    app,
    scripting,
    tabs,
    fireNavigation: webNavigation.fire,
    transport,
    storedSettings,
    connect: () => onConnectListener,
    changeStorage: (changes: Record<string, chrome.storage.StorageChange>, areaName = "local") =>
      onStorageChangedListener?.(changes, areaName),
    sendMessage: (message: unknown) =>
      new Promise((resolve) => onMessageListener?.(message, {} as chrome.runtime.MessageSender, resolve)),
  };
}

describe("createBackgroundApp", () => {
  it("connects to the edge server on start", async () => {
    const { app } = setup();
    await app.start();
    expect(FakeWebSocket.instances.length).toBeGreaterThan(0);
  });

  it("registers the bridge content script for the origins in the ready frame", async () => {
    const { app, scripting } = setup();
    await app.start();
    const socket = FakeWebSocket.instances.at(-1);
    socket?.receive({ type: "ready", heartbeatSec: 20, origins: ["https://app1.example.com"] });

    await vi.waitUntil(() => (scripting.registerContentScripts as ReturnType<typeof vi.fn>).mock.calls.length > 0);
  });

  it("wraps an incoming port, registers the tab, and relays its messages as mcp frames", async () => {
    const { app, connect, transport } = setup();
    await app.start();
    const socket = FakeWebSocket.instances.at(-1);
    socket?.receive({ type: "ready", heartbeatSec: 20, origins: ["https://app1.example.com"] });

    const port = createFakePort();
    connect()?.(port);
    await vi.waitUntil(() => (transport.start as ReturnType<typeof vi.fn>).mock.calls.length > 0);

    const message = { jsonrpc: "2.0", id: 1, method: "tools/list" };
    transport.onmessage?.(message);

    expect(socket?.sent).toContain(
      JSON.stringify({ type: "mcp", origin: "https://app1.example.com", appSession: "session-1", payload: message }),
    );
  });

  it("rejects a port with no sender origin", async () => {
    const { app, connect } = setup();
    await app.start();

    const port = createFakePort({ sender: {} as chrome.runtime.MessageSender });
    connect()?.(port);

    expect(port.disconnect).toHaveBeenCalled();
  });

  it("sends app.down and unregisters the tab when the port's transport closes", async () => {
    const { app, connect, transport } = setup();
    await app.start();
    const socket = FakeWebSocket.instances.at(-1);
    socket?.receive({ type: "ready", heartbeatSec: 20, origins: ["https://app1.example.com"] });

    const port = createFakePort();
    connect()?.(port);
    await vi.waitUntil(() => (transport.start as ReturnType<typeof vi.fn>).mock.calls.length > 0);

    transport.onclose?.();

    expect(socket?.sent).toContain(
      JSON.stringify({ type: "app.down", origin: "https://app1.example.com", appSession: "session-1" }),
    );
  });

  it("reports status, allowed origins, and connected origins via getStatus", async () => {
    const { app, connect } = setup();
    await app.start();
    const socket = FakeWebSocket.instances.at(-1);
    socket?.receive({ type: "ready", heartbeatSec: 20, origins: ["https://app1.example.com"] });

    expect(app.getStatus()).toEqual({
      status: "ready",
      allowedOrigins: ["https://app1.example.com"],
      connectedOrigins: [],
    });

    connect()?.(createFakePort());
    await vi.waitUntil(() => app.getStatus().connectedOrigins.length > 0);

    expect(app.getStatus().connectedOrigins).toEqual(["https://app1.example.com"]);
  });

  it("responds to a get-status runtime message with the current snapshot", async () => {
    const { app, sendMessage } = setup();
    await app.start();

    const response = await sendMessage({ type: "get-status" });

    expect(response).toEqual(app.getStatus());
  });

  it("ignores runtime messages that are not a get-status request", async () => {
    const { sendMessage } = setup();

    const response = await Promise.race([
      sendMessage({ type: "something-else" }),
      new Promise((resolve) => setTimeout(() => resolve("no-response"), 10)),
    ]);

    expect(response).toBe("no-response");
  });

  it("reconnects once the popup pairs and stores an edge token, with no credentials at start", async () => {
    const { app, changeStorage, storedSettings } = setup({ storedSettings: {} });
    const before = FakeWebSocket.instances.length;
    await app.start();
    expect(app.getStatus().status).toBe("closed");
    expect(FakeWebSocket.instances.length).toBe(before);

    storedSettings.edgeUrl = "ws://localhost:8081/edge/ws";
    storedSettings.edgeToken = "edge-token";
    changeStorage({ edgeToken: { newValue: "edge-token" } });

    await vi.waitUntil(() => FakeWebSocket.instances.length > before);
  });

  it("does not open a second connection when storage changes while already connecting", async () => {
    const { app, changeStorage } = setup();
    const before = FakeWebSocket.instances.length;
    await app.start();
    expect(FakeWebSocket.instances.length).toBe(before + 1);

    changeStorage({ edgeToken: { newValue: "edge-token" } });

    expect(FakeWebSocket.instances.length).toBe(before + 1);
  });

  it("ignores storage changes in areas other than local", async () => {
    const { app, changeStorage } = setup({ storedSettings: {} });
    const before = FakeWebSocket.instances.length;
    await app.start();
    expect(FakeWebSocket.instances.length).toBe(before);

    changeStorage({ edgeToken: { newValue: "edge-token" } }, "sync");

    expect(FakeWebSocket.instances.length).toBe(before);
  });

  it("asks an unbridged tab's content script to reconnect on a navigation within an allowed origin", async () => {
    const { app, fireNavigation, tabs } = setup();
    await app.start();
    const socket = FakeWebSocket.instances.at(-1);
    socket?.receive({ type: "ready", heartbeatSec: 20, origins: ["https://app1.example.com"] });

    fireNavigation({ tabId: 42, url: "https://app1.example.com/reports", frameId: 0 });

    expect(tabs.sendMessage).toHaveBeenCalledWith(42, RECONNECT_BRIDGE_MESSAGE);
  });

  it("does not ask an already-bridged tab to reconnect on navigation", async () => {
    const { app, connect, fireNavigation, tabs } = setup();
    await app.start();
    const socket = FakeWebSocket.instances.at(-1);
    socket?.receive({ type: "ready", heartbeatSec: 20, origins: ["https://app1.example.com"] });
    connect()?.(createFakePort({ sender: { origin: "https://app1.example.com", tab: { id: 42 } } as chrome.runtime.MessageSender }));
    await vi.waitUntil(() => app.getStatus().connectedOrigins.length > 0);

    fireNavigation({ tabId: 42, url: "https://app1.example.com/reports", frameId: 0 });

    expect(tabs.sendMessage).not.toHaveBeenCalled();
  });

  it("on a reconnect-request message, reconnects unbridged allowed tabs and reports the resulting status", async () => {
    const { app, sendMessage, tabs } = setup({
      tabsById: { 42: { id: 42, url: "https://app1.example.com/reports" }, 43: { id: 43, url: "https://other.example.com/" } },
    });
    await app.start();
    const socket = FakeWebSocket.instances.at(-1);
    socket?.receive({ type: "ready", heartbeatSec: 20, origins: ["https://app1.example.com"] });

    const response = await sendMessage({ type: "reconnect-request" });

    expect(tabs.query).toHaveBeenCalledWith({ url: ["https://app1.example.com/*"] });
    expect(tabs.sendMessage).toHaveBeenCalledWith(42, RECONNECT_BRIDGE_MESSAGE);
    expect(response).toEqual(app.getStatus());
  });

  it("skips an already-bridged tab when handling a reconnect-request", async () => {
    const { app, connect, sendMessage, tabs } = setup({
      tabsById: { 42: { id: 42, url: "https://app1.example.com/reports" } },
    });
    await app.start();
    const socket = FakeWebSocket.instances.at(-1);
    socket?.receive({ type: "ready", heartbeatSec: 20, origins: ["https://app1.example.com"] });
    connect()?.(createFakePort({ sender: { origin: "https://app1.example.com", tab: { id: 42 } } as chrome.runtime.MessageSender }));
    await vi.waitUntil(() => app.getStatus().connectedOrigins.length > 0);

    await sendMessage({ type: "reconnect-request" });

    expect(tabs.sendMessage).not.toHaveBeenCalled();
  });

  it("reconnects the edge connection on a reconnect-request when it was closed", async () => {
    const { app, sendMessage, storedSettings } = setup({ storedSettings: {} });
    const before = FakeWebSocket.instances.length;
    await app.start();
    expect(app.getStatus().status).toBe("closed");
    storedSettings.edgeUrl = "ws://localhost:8081/edge/ws";
    storedSettings.edgeToken = "edge-token";

    await sendMessage({ type: "reconnect-request" });

    expect(FakeWebSocket.instances.length).toBeGreaterThan(before);
  });

  it("still reports status on a reconnect-request when reconnecting unbridged tabs fails", async () => {
    const { app, sendMessage, tabs } = setup({
      tabsById: { 42: { id: 42, url: "https://app1.example.com/reports" } },
    });
    await app.start();
    const socket = FakeWebSocket.instances.at(-1);
    socket?.receive({ type: "ready", heartbeatSec: 20, origins: ["https://app1.example.com"] });
    vi.mocked(tabs.query).mockRejectedValueOnce(new Error("boom"));

    const response = await sendMessage({ type: "reconnect-request" });

    expect(response).toEqual(app.getStatus());
  });
});
