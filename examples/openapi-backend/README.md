# OpenAPI backend example — Swagger Petstore

Turns the public [Swagger Petstore](https://petstore3.swagger.io/) OpenAPI 3.x spec into MCP tools. No credentials required — this is the fastest way to see Manifold in action.

## Run

```bash
cd examples/openapi-backend
export ENCRYPT_KEY=$(openssl rand -base64 32)
manifold gateway
```

Manifold fetches the OpenAPI spec lazily on the first request and generates one MCP tool per operation (`getPetById`, `findPetsByStatus`, ...).

## Try it

List registered servers:

```bash
curl http://localhost:9999/mcp/list
```

Connect from Claude Code:

```bash
claude mcp add --transport http petstore http://localhost:9999/mcp/petstore
```

Then ask something like *"Find available pets in the petstore"*.

## Adapting to your own API

Replace `spec` and `baseURL` with your own API. If it needs a static API key, add `authValue`:

```yaml
mcpServers:
  my-api:
    description: My internal API
    spec: https://api.example.com/openapi.json
    baseURL: https://api.example.com
    authValue:
      header: Authorization
      prefix: "Bearer "
      value: ${MY_API_TOKEN}
```
