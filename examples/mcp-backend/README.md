# MCP backend example

Proxies external MCP servers through Manifold, giving all of them a single HTTP entry point (plus Manifold's OAuth, telemetry, and resource-link features).

The config demonstrates both transports:

- **stdio** — Manifold spawns the [`@modelcontextprotocol/server-everything`](https://github.com/modelcontextprotocol/servers) reference server as a child process (requires Node.js / `npx`)
- **http** — commented out; proxies a remote Streamable HTTP MCP server such as Notion

## Run

```bash
cd examples/mcp-backend
export ENCRYPT_KEY=$(openssl rand -base64 32)
manifold gateway
```

The stdio process is started lazily on the first request to `/mcp/everything`.

## Try it

```bash
curl http://localhost:9999/mcp/list
```

Connect from Claude Code:

```bash
claude mcp add --transport http everything http://localhost:9999/mcp/everything
```

## Why put a gateway in front of MCP servers?

- One endpoint (and one auth flow) for many backends
- stdio-only servers become network-accessible via Streamable HTTP
- Centralized OpenTelemetry traces/metrics/logs across all backends
