# Manifold WebMCP Bridge (browser extension)

[日本語](README_ja.md) | English

A Chrome MV3 extension that bridges [WebMCP](https://webmachinelearning.github.io/webmcp/) tools
registered by a web page (`document.modelContext`) to a Manifold reverse connection gateway, so a
server-side AI agent can call them through Manifold's `/mcp/{server_name}` endpoint. See
[docs/design/webmcp-reverse-gateway.md](../../docs/design/webmcp-reverse-gateway.md) for the full
protocol design; this extension implements the **pairing + `type: static`** MVP described there.

## How it works

- **background/** (service worker) holds the single WebSocket connection to the edge endpoint
  (e.g. `ws://localhost:8081/edge/ws`), performs first-message auth, sends heartbeat pings, and
  reconnects with backoff. On every `ready` frame it registers a dynamic content script
  (`chrome.scripting.registerContentScripts`) scoped to exactly the origins the server allows.
- **content/** (dynamically registered, not declared in `manifest.json`) connects to the page's
  WebMCP server via [`@mcp-b/transports`](https://github.com/WebMCP-org/npm-packages)'
  `TabClientTransport`, and to the background service worker via `ExtensionClientTransport`. It
  only relays raw MCP JSON-RPC messages between the two — it never parses or acts on them. Because
  a page's WebMCP server can start after the content script does (e.g. behind a lazy-loaded chunk),
  the `TabClientTransport` handshake is retried with backoff (`connectWithRetry.ts`) instead of
  relying on the transport's single check-ready probe.
- **content/nativeAdapter.ts** (dynamically registered in the page's MAIN world) covers pages that
  expose a genuinely native `document.modelContext` with no `@mcp-b/global`-style postMessage
  server behind it — see [Native WebMCP pages](#native-webmcp-pages) below.
- **popup/** lets you set the edge URL, exchange a pairing code for an edge token
  (`POST {edge origin}/edge/pair`), see the connection status and bridged tab origins, and log out
  (discards the stored edge token).

## Build

```bash
pnpm install
pnpm build       # type-checks, then builds dist/
pnpm test        # vitest
pnpm dev         # vite watch build (no auto-reload; reload the unpacked extension manually)
```

## Load the unpacked extension

1. `pnpm build`
2. Open `chrome://extensions`, enable "Developer mode"
3. "Load unpacked" → select `tools/extension/dist`

## Pairing

1. Start Manifold with a `gateway.edge` config (see
   [docs/design/webmcp-reverse-gateway.md](../../docs/design/webmcp-reverse-gateway.md#configuration) —
   for local/single-user use, set `gateway.edge.pairing.type: static`; static is local-only —
   never expose the edge endpoints to a public network).
2. Call the reverse server's `create_pairing_code` tool (e.g. from an MCP client connected through
   Manifold) to get a short-lived code.
3. Open the extension popup, enter the edge WebSocket URL (e.g. `ws://localhost:8081/edge/ws`) and
   the pairing code, and submit. The popup exchanges the code for an edge token and stores it in
   `chrome.storage.local`.
4. Once paired, open a tab on one of the origins the server allows and keep it open — the
   extension bridges it automatically. "Log out" in the popup discards the stored token.

## Native WebMCP pages

Some pages expose Chromium's native producer API — `document.modelContext.getTools()` (per the
[WebMCP spec](https://webmachinelearning.github.io/webmcp/)) plus `executeTool()`, a Chrome-specific
extension not in the spec draft (feature-detect before calling it) — directly, without a
`@mcp-b/global`-style `postMessage` server behind it. `content/nativeAdapter.ts` is
registered in the page's **MAIN world** (`chrome.scripting`'s `world: "MAIN"`, alongside the
isolated-world bridge script, for the same allowed origins) to cover this case:

- It wraps `document.modelContext` with [`@mcp-b/webmcp-ts-sdk`](https://www.npmjs.com/package/@mcp-b/webmcp-ts-sdk)'s
  `BrowserMcpServer` (`{ native: document.modelContext }`), which is the SDK's own supported way to
  mirror an existing native/polyfilled `modelContext` into an MCP server — no hand-rolled
  `getTools`/`executeTool` bridging was needed.
- The wrapped server is exposed over a `TabServerTransport` on the same `mcp-default` channel
  `@mcp-b/global` itself uses, so `content/index.ts`'s `TabClientTransport` reaches it with no
  changes to the isolated-world protocol.
- It no-ops when `document.modelContext` is already a `BrowserMcpServer` instance (detected via the
  SDK's `SERVER_MARKER_PROPERTY` marker), which is the case whenever `@mcp-b/global` is present —
  this avoids a second, competing `TabServerTransport` on the same channel.
- **Limitation**: only the initial tool list and tools *added* afterward are reflected (via the
  native `toolchange` event re-running `syncNativeTools()`). Tools *removed* from
  `document.modelContext` after the adapter's first sync are not removed from the bridged server —
  this is a limitation of `BrowserMcpServer.syncNativeTools()` itself (it only backfills missing
  tools), not something this extension adds on top.

## Known limitations (MVP scope)

- `host_permissions` is `<all_urls>`. Since the set of allowed origins is only known at runtime
  (from the server's `ready` frame), the extension can't declare a narrower static permission set
  up front; scoping this down (e.g. via `chrome.permissions.request` once origins are known) is
  left for a follow-up.
- A tab that was already open before pairing is re-bridged automatically on its next navigation
  (full page load or SPA route change) or via the popup's Reconnect button — no manual reload
  needed.
- `forwardAuth` edge mode is not implemented by this extension (pairing mode only).
- `connectWithRetry` gives up after 30s if a page's WebMCP server (polyfilled or native) never
  becomes ready; the tab is simply not bridged in that case.
- See [Native WebMCP pages](#native-webmcp-pages) above for the native-adapter-specific limitation
  (tool removals not reflected after the initial sync).

## Packages used

- [`@mcp-b/transports`](https://www.npmjs.com/package/@mcp-b/transports) — `TabClientTransport`
  (content script ↔ page), `TabServerTransport` (native adapter ↔ page), and
  `ExtensionClientTransport`/`ExtensionServerTransport` (content script ↔ background). No
  hand-rolled `postMessage` or `chrome.runtime.Port` protocol was needed for any of these
  boundaries.
- [`@mcp-b/webmcp-ts-sdk`](https://www.npmjs.com/package/@mcp-b/webmcp-ts-sdk) — `BrowserMcpServer`,
  used by the native adapter to mirror a native `document.modelContext` into an MCP server without
  hand-rolled tool-list/execute bridging.
- [`vite-plugin-web-extension`](https://www.npmjs.com/package/vite-plugin-web-extension) — MV3
  build (manifest-driven multi-entry bundling).
- [`vitest`](https://vitest.dev/) + `jsdom` — unit tests.

## References

- [WebMCP specification](https://webmachinelearning.github.io/webmcp/) — W3C Web Machine Learning Community Group (Draft Community Group Report)
- [WebMCP | AI on Chrome](https://developer.chrome.com/docs/ai/webmcp) — Chrome's implementation docs (flags, origin trial, examples)
- [Join the WebMCP origin trial](https://developer.chrome.com/blog/ai-webmcp-origin-trial) — Chrome origin trial announcement
