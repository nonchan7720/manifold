import type { TransportLike } from "../shared/types";

/**
 * Structural subset of Chromium's producer-facing `document.modelContext`
 * (getTools/executeTool, @see https://webmachinelearning.github.io/webmcp/)
 * that this bridge needs to back-fill and re-sync tools.
 */
export interface NativeModelContextLike {
  getTools: () => Promise<unknown[]>;
  executeTool: (tool: unknown, inputArgsJson: string) => Promise<string | null>;
  addEventListener: (type: "toolchange", listener: () => void) => void;
}

/**
 * Structural subset of @mcp-b/webmcp-ts-sdk's BrowserMcpServer that this
 * bridge needs: back-filling tools already registered on `native` and
 * speaking MCP over a transport.
 */
export interface BrowserServerLike {
  syncNativeTools: () => number;
  connect: (transport: TransportLike) => Promise<void>;
}

export interface NativeModelContextBridgeDeps {
  /** document.modelContext (or navigator.modelContext) as seen in the page's main world. */
  native: unknown;
  /** True when `native` is already a BrowserMcpServer instance (e.g. @mcp-b/global installed it). */
  isAlreadyBridged: (native: unknown) => boolean;
  createServer: (native: NativeModelContextLike) => BrowserServerLike;
  createTransport: () => TransportLike;
}

export interface NativeModelContextBridge {
  start: () => Promise<void>;
}

/**
 * Wraps a genuinely native `document.modelContext` (Chromium's producer API,
 * not a postMessage server) with @mcp-b/webmcp-ts-sdk's BrowserMcpServer so it
 * becomes reachable over the same TabServerTransport/TabClientTransport
 * channel the polyfilled (@mcp-b/global) path already uses. Skips pages where
 * `isAlreadyBridged` reports @mcp-b/global (or another BrowserMcpServer) is
 * already installed, to avoid a second, competing TabServerTransport on the
 * same channel.
 *
 * Limitation: tools removed from `native` after the initial sync are not
 * removed from the bridged server — BrowserMcpServer.syncNativeTools() only
 * backfills additions (see @mcp-b/webmcp-ts-sdk's backfillTools skipping
 * already-registered names). A future release upstream may add removal
 * tracking; until then, only additions are reflected on 'toolchange'.
 */
export function createNativeModelContextBridge(deps: NativeModelContextBridgeDeps): NativeModelContextBridge {
  return {
    async start() {
      const { native } = deps;
      if (!native || deps.isAlreadyBridged(native)) return;

      const candidate = native as Partial<NativeModelContextLike>;
      if (typeof candidate.getTools !== "function" || typeof candidate.executeTool !== "function") return;
      const nativeApi = candidate as NativeModelContextLike;

      const server = deps.createServer(nativeApi);
      server.syncNativeTools();
      nativeApi.addEventListener("toolchange", () => {
        server.syncNativeTools();
      });

      await server.connect(deps.createTransport());
    },
  };
}
