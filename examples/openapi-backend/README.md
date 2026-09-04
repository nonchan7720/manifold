# OpenAPI backend example — Swagger Petstore

Turns the public [Swagger Petstore](https://petstore3.swagger.io/) OpenAPI 3.x spec into MCP tools. No credentials required — this is the fastest way to see Manifold in action.

## Run

```bash
cd examples/openapi-backend
# Generate once and reuse — see ../README.md
export ENCRYPT_KEY=${ENCRYPT_KEY:-$(openssl rand -base64 32)}
manifold gateway
```

Manifold fetches the OpenAPI spec at startup (`Init`) and generates one MCP tool per operation; a fetch or parse failure fails startup. Tool names are the lowercased `operationId` (`getpetbyid`, `findpetsbystatus`, ...).

## See which tools will be generated

Before starting the gateway, `manifold openapi tools` prints the same catalog Manifold would register — no network access beyond fetching the spec, and no gateway process:

```bash
manifold openapi tools -c config
```

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

## Start from a generated file

Instead of fetching the spec at every startup, generate a tools file once and have Manifold start from it:

```yaml
mcpServers:
  petstore:
    description: Swagger Petstore sample API
    spec: https://petstore3.swagger.io/api/v3/openapi.json
    baseURL: https://petstore3.swagger.io/api/v3
    tools:
      file: ./generated/petstore.yaml
```

```bash
manifold openapi generate -c config
manifold gateway -c config
```

The gateway now starts from `./generated/petstore.yaml` with no network access to `spec`. See [Configuration reference](../../README.md#mcpserversnametools) for details, including what happens when the file goes stale.

Check that the committed file still matches the upstream spec — e.g. in CI — without writing anything:

```bash
manifold openapi generate -c config --check
```

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
