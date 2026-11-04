import { TabServerTransport } from "@mcp-b/transports";
import { BrowserMcpServer, isBrowserMcpServer } from "@mcp-b/webmcp-ts-sdk";
import type { TransportLike } from "../shared/types";
import { createNativeModelContextBridge } from "./nativeModelContextBridge";
import type { BrowserServerLike, NativeModelContextLike } from "./nativeModelContextBridge";

type NativeOption = NonNullable<ConstructorParameters<typeof BrowserMcpServer>[1]>["native"];

// Runs in the page's MAIN world (see background/contentScriptSync.ts), so it
// can read the real document.modelContext instead of the isolated-world copy
// content/index.ts sees. Uses the same "mcp-default" TabServerTransport
// channel as @mcp-b/global, so content/index.ts's TabClientTransport reaches
// it without any protocol changes on the isolated-world side.
const bridge = createNativeModelContextBridge({
  native: document.modelContext,
  // SDK v6 はマーカーのプロパティ名を export しないので、判定関数を使う。
  isAlreadyBridged: (native) => isBrowserMcpServer(native),
  createServer: (native: NativeModelContextLike) =>
    new BrowserMcpServer(
      { name: "manifold-native-webmcp-adapter", version: "0.1.0" },
      { native: native as unknown as NativeOption },
    ) as unknown as BrowserServerLike,
  createTransport: () =>
    new TabServerTransport({ allowedOrigins: [location.origin] }) as unknown as TransportLike,
});

bridge.start().catch((error: unknown) => {
  console.error("[manifold-webmcp] failed to bridge the native document.modelContext", error);
});
