import type { TransportLike } from "../shared/types";
import { createPageBridge } from "./pageBridge";

export interface BridgeSessionDeps {
  /** Runs connectWithRetry's finite handshake window and resolves once the page's WebMCP server answers, or rejects once it gives up. */
  connectPageTransport: () => Promise<TransportLike>;
  createExtensionTransport: () => TransportLike;
  onError?: (message: string, error: unknown) => void;
}

export interface BridgeSession {
  /** No-ops if already bridged or an attempt is already in flight. */
  attempt: () => Promise<void>;
  isBridged: () => boolean;
}

export function createBridgeSession(deps: BridgeSessionDeps): BridgeSession {
  let bridged = false;
  let inFlight: Promise<void> | undefined;

  async function run(): Promise<void> {
    try {
      const pageTransport = await deps.connectPageTransport();
      const bridge = createPageBridge({
        pageTransport,
        extensionTransport: deps.createExtensionTransport(),
      });
      await bridge.start();
      bridged = true;
    } catch (error) {
      deps.onError?.("failed to bridge WebMCP tools to the extension", error);
    }
  }

  return {
    attempt() {
      if (bridged) return Promise.resolve();
      if (!inFlight) {
        inFlight = run().finally(() => {
          inFlight = undefined;
        });
      }
      return inFlight;
    },
    isBridged: () => bridged,
  };
}
