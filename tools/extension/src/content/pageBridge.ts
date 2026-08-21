import type { TransportLike } from "../shared/types";

export interface PageBridgeDeps {
  /**
   * Client side of a TabClientTransport connected to the page's WebMCP bridge.
   * Must already be started (see connectWithRetry.ts) — the bridge only wires
   * message relaying and close propagation for it.
   */
  pageTransport: TransportLike;
  /** Client side of an ExtensionClientTransport connected to the background service worker. */
  extensionTransport: TransportLike;
}

export interface PageBridge {
  start: () => Promise<void>;
  close: () => Promise<void>;
}

/**
 * Relays raw MCP JSON-RPC messages between the page's WebMCP server and the
 * background service worker. Neither side is parsed or acted on here — the
 * mcp frame payload is forwarded untouched, per
 * docs/design/webmcp-reverse-gateway.ja.md ("フレーム定義").
 */
export function createPageBridge(deps: PageBridgeDeps): PageBridge {
  const { pageTransport, extensionTransport } = deps;

  pageTransport.onmessage = (message) => {
    void extensionTransport.send(message);
  };
  extensionTransport.onmessage = (message) => {
    void pageTransport.send(message);
  };
  // If the background connection is lost, the page-side transport is also
  // torn down so a fresh content script injection (new appSession) can retry.
  extensionTransport.onclose = () => {
    void pageTransport.close();
  };

  return {
    async start() {
      await extensionTransport.start();
    },
    async close() {
      await Promise.all([pageTransport.close(), extensionTransport.close()]);
    },
  };
}
