# Manifold Examples

Ready-to-run configuration examples for Manifold. Each directory contains a `config.yaml` and a README with step-by-step instructions.

| Example | What it shows |
| ------- | ------------- |
| [`openapi-backend/`](openapi-backend/) | Convert a public OpenAPI spec (Swagger Petstore) into MCP tools — the fastest way to try Manifold |
| [`mcp-backend/`](mcp-backend/) | Proxy an external MCP server (stdio and HTTP transports) |
| [`oauth2-backend/`](oauth2-backend/) | Expose an OAuth-protected REST API (Google Calendar) as MCP tools |

## Prerequisites

- A Manifold binary ([Releases](https://github.com/nonchan7720/manifold/releases)), Docker image (`ghcr.io/nonchan7720/manifold:latest`), or a local checkout (`go run main.go gateway`)
- An encryption key:

```bash
export ENCRYPT_KEY=$(openssl rand -base64 32)
```

All examples use SQLite for storage, so no Redis is required.

## Running an example

```bash
cd examples/openapi-backend
export ENCRYPT_KEY=$(openssl rand -base64 32)
manifold gateway   # reads ./config.yaml
```

## Connecting a client

Manifold speaks Streamable HTTP at `/mcp/{server_name}`. For example, with [Claude Code](https://claude.com/claude-code):

```bash
claude mcp add --transport http petstore http://localhost:9999/mcp/petstore
```

Or list the registered servers:

```bash
curl http://localhost:9999/mcp/list
```
