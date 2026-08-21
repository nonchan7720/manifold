# WebMCP demo page

[日本語](README_ja.md) | English

A minimal web page that registers a few [WebMCP](https://webmachinelearning.github.io/webmcp/)
tools on `document.modelContext`, for exercising the
[Manifold WebMCP browser extension](../../tools/extension/) end-to-end without needing a real
production WebMCP-enabled site.

Uses [`@mcp-b/global`](https://www.npmjs.com/package/@mcp-b/global) to polyfill
`document.modelContext` in browsers that don't implement the WebMCP spec natively yet.

## Tools registered

| Tool | What it does |
| --- | --- |
| `echo` | Returns the given `message` back as text |
| `get_current_time` | Returns the current time as an ISO-8601 string |
| `increment_counter` / `decrement_counter` | Adjusts an on-page counter (optional `by`, default 1) and returns the new value |

## Run

```bash
pnpm install
pnpm dev     # http://localhost:5173
```

Keep the tab open, pair the [browser extension](../../tools/extension/) with Manifold, and open a
reverse `mcpServer` configured with `origin: http://localhost:5173` to call these tools from an
agent through `/mcp/{server_name}`.
