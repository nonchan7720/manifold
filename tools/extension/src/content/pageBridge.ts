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
  /** Fires once when either side closes on its own (not via an explicit close()), so the caller can re-bridge. */
  onClose?: () => void;
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
  const { pageTransport, extensionTransport, onClose } = deps;
  let closed = false;

  pageTransport.onmessage = (message) => {
    void extensionTransport.send(message);
  };
  extensionTransport.onmessage = (message) => {
    void pageTransport.send(message);
  };
  // If either transport is lost, tear down the other side too and notify the
  // caller so a fresh content script injection or bridge attempt can retry.
  // Guarded by `closed` because closing one side may in turn fire the other
  // side's onclose.
  extensionTransport.onclose = () => {
    if (closed) return;
    closed = true;
    void pageTransport.close();
    onClose?.();
  };
  pageTransport.onclose = () => {
    if (closed) return;
    closed = true;
    void extensionTransport.close();
    onClose?.();
  };

  return {
    async start() {
      await extensionTransport.start();
    },
    async close() {
      closed = true;
      await Promise.all([pageTransport.close(), extensionTransport.close()]);
    },
  };
}
