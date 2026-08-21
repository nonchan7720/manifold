import { ExtensionClientTransport, TabClientTransport } from "@mcp-b/transports";
import type { TransportLike } from "../shared/types";
import { createPageBridge } from "./pageBridge";

// One appSession per content script injection: a page reload re-runs this
// module and gets a new UUID, matching "appSession はタブ接続1世代ごとの UUID"
// in docs/design/webmcp-reverse-gateway.ja.md.
const appSession = crypto.randomUUID();

// @mcp-b/transports types onmessage/send around JSONRPCMessage; this bridge
// intentionally treats the payload as opaque (see pageBridge.ts), so the
// wider TransportLike shape is asserted at this boundary.
const bridge = createPageBridge({
  pageTransport: new TabClientTransport({
    targetOrigin: location.origin,
  }) as unknown as TransportLike,
  extensionTransport: new ExtensionClientTransport({
    portName: appSession,
  }) as unknown as TransportLike,
});

bridge.start().catch((error: unknown) => {
  console.error("[manifold-webmcp] failed to bridge WebMCP tools to the extension", error);
});
