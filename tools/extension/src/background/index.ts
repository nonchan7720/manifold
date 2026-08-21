import { ExtensionServerTransport } from "@mcp-b/transports";
import type { TransportLike } from "../shared/types";
import { createBackgroundApp } from "./app";
import type { WebSocketLike } from "./edgeSocket";

const app = createBackgroundApp({
  runtime: chrome.runtime,
  scripting: chrome.scripting,
  storageArea: chrome.storage.local,
  storageOnChanged: chrome.storage.onChanged,
  // See edgeSocket.ts: native WebSocket already satisfies WebSocketLike; the
  // cast only bridges the onmessage/onerror payload typing (see content/index.ts).
  connectSocket: (url) => new WebSocket(url) as unknown as WebSocketLike,
  wrapPort: (port) => new ExtensionServerTransport(port) as unknown as TransportLike,
});

app.start().catch((error: unknown) => {
  console.error("[manifold-webmcp] failed to start the edge connection", error);
});
