import { EDGE_TOKEN_KEY, EDGE_URL_KEY, getEdgeSettings } from "../shared/storage";
import type { TransportLike } from "../shared/types";
import { createAppSessionRegistry } from "./appSessionRegistry";
import type { ScriptingApi } from "./contentScriptSync";
import { syncBridgeContentScript } from "./contentScriptSync";
import type { EdgeConnectionStatus, WebSocketLike } from "./edgeSocket";
import { createEdgeConnection } from "./edgeSocket";

export const GET_STATUS_MESSAGE = { type: "get-status" } as const;

export interface EdgeStatusSnapshot {
  status: EdgeConnectionStatus;
  /** Origins the edge server currently allows (from the last `ready` frame). */
  allowedOrigins: string[];
  /** Origins with a tab actively bridged right now. */
  connectedOrigins: string[];
}

export interface RuntimeApi {
  onConnect: {
    addListener: (callback: (port: chrome.runtime.Port) => void) => void;
  };
  onMessage: {
    addListener: (
      callback: (
        message: unknown,
        sender: chrome.runtime.MessageSender,
        sendResponse: (response: EdgeStatusSnapshot) => void,
      ) => boolean | undefined,
    ) => void;
  };
}

export interface StorageOnChangedApi {
  addListener: (
    callback: (changes: Record<string, chrome.storage.StorageChange>, areaName: string) => void,
  ) => void;
}

export interface BackgroundAppDeps {
  runtime: RuntimeApi;
  scripting: ScriptingApi;
  storageArea: chrome.storage.StorageArea;
  /** Fires when chrome.storage changes, e.g. the popup saving an edge token after pairing. */
  storageOnChanged: StorageOnChangedApi;
  connectSocket: (url: string) => WebSocketLike;
  /** Wraps an incoming port as a JSON-RPC Transport (real: ExtensionServerTransport). */
  wrapPort: (port: chrome.runtime.Port) => TransportLike;
}

export interface BackgroundApp {
  start: () => Promise<void>;
  getStatus: () => EdgeStatusSnapshot;
}

function isGetStatusMessage(message: unknown): boolean {
  return typeof message === "object" && message !== null && (message as { type?: unknown }).type === "get-status";
}

/**
 * Wires the edge connection to incoming tab ports. Each port name is the
 * appSession the content script generated for its current page load; the
 * port's sender gives the origin and tabId Chrome already verified, so
 * neither is trusted from message content, per
 * docs/design/webmcp-reverse-gateway.ja.md ("拡張と identity の紐づけ").
 */
export function createBackgroundApp(deps: BackgroundAppDeps): BackgroundApp {
  const registry = createAppSessionRegistry();
  let status: EdgeConnectionStatus = "closed";
  let allowedOrigins: string[] = [];

  function getStatus(): EdgeStatusSnapshot {
    return {
      status,
      allowedOrigins,
      connectedOrigins: [...new Set(registry.list().map((entry) => entry.origin))],
    };
  }

  const connection = createEdgeConnection({
    connectSocket: deps.connectSocket,
    registry,
    getCredentials: async () => {
      const settings = await getEdgeSettings(deps.storageArea);
      if (!settings.edgeUrl || !settings.edgeToken) return undefined;
      return { edgeUrl: settings.edgeUrl, edgeToken: settings.edgeToken };
    },
    onStatusChange: (nextStatus) => {
      status = nextStatus;
    },
    onReady: (origins) => {
      allowedOrigins = origins;
      void syncBridgeContentScript(origins, deps.scripting);
    },
  });

  // The popup saves edgeUrl/edgeToken to storage after a successful pairing,
  // but that happens in a separate execution context (the popup page), so
  // this service worker's already-run connection.start() never sees the new
  // credentials on its own. Retry once storage gains them, but only from the
  // "closed" state (no credentials, or a fatal auth failure) — otherwise a
  // connect already in flight would end up with two sockets.
  deps.storageOnChanged.addListener((changes, areaName) => {
    if (areaName !== "local") return;
    if (!(EDGE_URL_KEY in changes) && !(EDGE_TOKEN_KEY in changes)) return;
    if (status !== "closed") return;
    void connection.start();
  });

  deps.runtime.onMessage.addListener((message, _sender, sendResponse) => {
    if (!isGetStatusMessage(message)) return undefined;
    sendResponse(getStatus());
    return undefined;
  });

  deps.runtime.onConnect.addListener((port) => {
    const origin = port.sender?.origin;
    const tabId = port.sender?.tab?.id;
    const appSession = port.name;
    if (!origin || tabId === undefined || !appSession) {
      port.disconnect();
      return;
    }

    const transport = deps.wrapPort(port);
    void transport.start().then(() => {
      registry.register({
        origin,
        appSession,
        tabId,
        send: (payload) => {
          void transport.send(payload);
        },
      });
      transport.onmessage = (message) => connection.sendMcpFrame(origin, appSession, message);
      transport.onclose = () => {
        registry.unregister(appSession);
        connection.sendAppDown(origin, appSession);
      };
      connection.sendAppUp(origin, appSession);
    });
  });

  return {
    start: () => connection.start(),
    getStatus,
  };
}
