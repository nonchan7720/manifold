import { ExtensionClientTransport, TabClientTransport } from "@mcp-b/transports";
import { generateAppSession } from "../shared/appSession";
import { isReconnectBridgeMessage } from "../shared/messages";
import type { ReadyAwareTransport, TransportLike } from "../shared/types";
import { createBridgeSession } from "./bridgeSession";
import { connectWithRetry } from "./connectWithRetry";

// One appSession per content script injection (a page reload gets a new one).
const appSession = generateAppSession();

// bridgeSession re-arms connectWithRetry on a `reconnect-webmcp-bridge`
// message (background/navigationReconnect.ts, popup's Reconnect button).
const session = createBridgeSession({
  connectPageTransport: async () =>
    (await connectWithRetry({
      createTransport: () =>
        new TabClientTransport({
          targetOrigin: location.origin,
        }) as unknown as ReadyAwareTransport,
    })) as unknown as TransportLike,
  createExtensionTransport: () =>
    new ExtensionClientTransport({
      portName: appSession,
    }) as unknown as TransportLike,
  onError: (message, error) => {
    console.error(`[manifold-webmcp] ${message}`, error);
  },
});

void session.attempt();

chrome.runtime.onMessage.addListener((message) => {
  if (!isReconnectBridgeMessage(message)) return undefined;
  void session.attempt();
  return undefined;
});
