[日本語](README_ja.md)

# Manifold Examples

Ready-to-run configuration examples for Manifold. Each directory contains a `config.yaml` and a README with step-by-step instructions.

| Example | What it shows |
| ------- | ------------- |
| [`openapi-backend/`](openapi-backend/) | Convert a public OpenAPI spec (Swagger Petstore) into MCP tools — the fastest way to try Manifold |
| [`mcp-backend/`](mcp-backend/) | Proxy an external MCP server (stdio and HTTP transports) |
| [`oauth2-backend/`](oauth2-backend/) | Expose an OAuth-protected REST API (Google Calendar) as MCP tools |
| [`opa/`](opa/) | Authorize `tools/call` / `tools/list` per caller group with an OPA sidecar |

## Prerequisites

- A Manifold binary ([Releases](https://github.com/nonchan7720/manifold/releases)), Docker image (`ghcr.io/nonchan7720/manifold:latest`), or a local checkout (`go run main.go gateway`)
- An encryption key. Generate it **once** and store it securely (e.g. in a `.env` file) — stored sessions and tokens are encrypted with this key, so generating a new key invalidates everything previously stored:

```bash
export ENCRYPT_KEY=$(openssl rand -base64 32)
echo "ENCRYPT_KEY=$ENCRYPT_KEY" >> .env   # keep it for later runs
```

All examples use SQLite for storage, so no Redis is required.

## Running an example

```bash
cd examples/openapi-backend
manifold gateway   # reads ./config.yaml; requires ENCRYPT_KEY to be set
```

Or with Docker (the working directory inside the container is `/home/nonroot`):

```bash
cd examples/openapi-backend
mkdir -p tmp
docker run -p 9999:9999 \
  -e ENCRYPT_KEY \
  -v $(pwd)/config.yaml:/home/nonroot/config.yaml \
  -v $(pwd)/tmp:/home/nonroot/tmp \
  ghcr.io/nonchan7720/manifold:latest
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
