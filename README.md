# Manifold

**One interface. Many connections. Manifold.**

[![CI](https://github.com/nonchan7720/manifold/actions/workflows/ci.yaml/badge.svg)](https://github.com/nonchan7720/manifold/actions/workflows/ci.yaml)
[![Release](https://img.shields.io/github/v/release/nonchan7720/manifold)](https://github.com/nonchan7720/manifold/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

English | [日本語](README_ja.md)

Manifold is a gateway that acts as an MCP server while connecting to multiple external MCP servers and OpenAPI / Swagger-compliant REST APIs on the backend.

## Why "Manifold"?

The name **Manifold** comes from an engine's **intake manifold**.

An intake manifold is the component that distributes air and fuel evenly and efficiently from a single inlet to multiple cylinders. We named this project **Manifold** because its structure is similar.

| Engine manifold        | This project                         |
| ---------------------- | ------------------------------------ |
| Single inlet           | Requests from MCP clients            |
| Distribution / routing | Protocol conversion / routing        |
| To multiple cylinders  | To multiple external MCP / REST APIs |

## Architecture

```text
MCP Client
    │
    ▼
┌─────────────┐
│   Manifold  │   ← this server
└─────────────┘
    │       │
    ▼       ▼
External  OpenAPI / Swagger
MCP       REST API Server
Server
```

## Features

- **OpenAPI / Swagger → MCP conversion**: Automatically generates MCP tools from OpenAPI 3.x / Swagger 2.x specifications
- **MCP backend aggregation**: Transparent reverse proxy to external MCP servers
- **Built-in OAuth 2.1 server**: Authorization server with PKCE (S256) support
- **Pluggable backend authentication**: Choose one of static header (`authValue`) / OAuth 2.0 (`oauth2`) / API key Token Exchange (`tokenExchange`)
- **Resource links**: Stores binary content from tool responses in S3 and returns download URLs (resource links)
- **Lazy connection**: Connects to backends on first request (no backend dependency at gateway startup)
- **Selectable storage**: Session / token management backed by Redis or SQLite
- **OpenTelemetry support**: OTLP export of traces, metrics, and logs (metrics also support Prometheus-style pull)

## Requirements

- Go 1.26+
- Redis or SQLite (for session management)

## Installation

### Download binary

Download the latest binary from [Releases](https://github.com/nonchan7720/manifold/releases).

### Build from source

```bash
git clone https://github.com/nonchan7720/manifold.git
cd manifold
go build -o manifold .
```

### Docker

```bash
docker pull ghcr.io/nonchan7720/manifold:latest
```

## Usage

### Start the gateway

```bash
# Run the binary
manifold gateway

# Specify a config file explicitly (-c / --config, config name without extension)
manifold gateway -c config

# Run from source
go run main.go gateway

# Docker (working directory is /home/nonroot)
docker run -p 9999:9999 \
  -v $(pwd)/config.yaml:/home/nonroot/config.yaml \
  ghcr.io/nonchan7720/manifold:latest
```

### Docker Compose (development)

Starts a development environment including Redis.

```bash
docker compose up -d
```

Ready-to-run configuration examples are available in the [`examples/`](examples/) directory.

## Configuration

Place a configuration file (`config.yaml`) in the current directory or in a `config/` subdirectory.
Configuration values support environment variable expansion in the form `${VAR}` or `${VAR:-default}`.

### Connecting to an MCP backend

Expose an external MCP server through Manifold.

```yaml
gateway:
  port: 9999
  # openssl rand -base64 32
  encryptKey: ${ENCRYPT_KEY}

mcpServers:
  my-mcp-server:
    description: External MCP server
    transport: http
    url: http://localhost:8080/mcp

sqlite:
  path: ./tmp/manifold.db
```

### Connecting to an OpenAPI / Swagger backend

Automatically generate MCP tools from an OpenAPI specification.

```yaml
gateway:
  port: 9999
  encryptKey: ${ENCRYPT_KEY}

mcpServers:
  my-api:
    description: Sample REST API
    spec: https://example.com/api/openapi.json
    baseURL: https://example.com
```

### OpenAPI backend with OAuth 2.0 authentication

```yaml
gateway:
  port: 9999
  encryptKey: ${ENCRYPT_KEY}

mcpServers:
  my-api:
    description: OAuth-protected API
    spec: https://example.com/api/openapi.json
    baseURL: https://example.com
    oauth2:
      clientID: YOUR_CLIENT_ID
      clientSecret: YOUR_CLIENT_SECRET
      authURL: https://example.com/oauth/authorize
      tokenURL: https://example.com/oauth/token
      scopes:
        - read
        - write

redis:
  addrs:
    - "${REDIS_ADDRS:-localhost:6379}"
  db: ${REDIS_DB:-0}
```

### Configuration reference

#### `gateway`

| Field        | Type   | Description                                                                                                      |
| ------------ | ------ | ---------------------------------------------------------------------------------------------------------------- |
| `port`       | int    | Listening port (default: 8081)                                                                                   |
| `key`        | string | TLS private key file path (optional)                                                                             |
| `cert`       | string | TLS certificate file path (optional)                                                                             |
| `encryptKey` | string | Token encryption key (**required**). Base64-encoded 32-byte AES-256 key. Generate with `openssl rand -base64 32` |
| `specRefresh.interval` | duration | Interval for re-fetching OpenAPI mode specs (e.g. `5m`). Unset or `0` disables refreshing |

#### `gateway.specRefresh`

Periodically re-fetches the specs of OpenAPI mode servers (`mcpServers.<name>.spec`) and updates the MCP tool definitions without restarting Manifold. Added tools are registered, removed tools are unregistered, and connected clients are notified via `notifications/tools/list_changed`.

```yaml
gateway:
  specRefresh:
    interval: 5m
```

Changes are detected by hashing the fetched spec document, so a change made only in an externally `$ref`-ed document leaves the hash unchanged and is not picked up. When a fetch or parse fails, the existing tool definitions are kept and the next interval retries.

#### `mcpServers.<name>`

Server names (`<name>`) are used in URL paths, so only alphanumerics, `_`, and `-` are allowed.

| Field           | Type              | Description                                                          |
| --------------- | ----------------- | -------------------------------------------------------------------- |
| `description`   | string            | Server description (**required**; included in `/mcp/list` responses) |
| `transport`     | string            | Transport for MCP backends (`http` or `stdio`)                       |
| `url`           | string            | Endpoint for the HTTP transport                                      |
| `command`       | string            | Command for the stdio transport                                      |
| `args`          | []string          | Arguments for the stdio command                                      |
| `env`           | map[string]string | Environment variables for the stdio process                          |
| `spec`          | string            | Path or URL of an OpenAPI/Swagger specification                      |
| `baseURL`       | string            | API base URL in OpenAPI mode (required when `spec` is set)           |
| `headers`       | map[string]string | Extra headers added to API requests                                  |
| `authValue`     | object            | Static authentication settings (`header`, `prefix`, `value`)         |
| `oauth2`        | object            | OAuth 2.0 settings (see below)                                       |
| `tokenExchange` | object            | Token Exchange settings (see below)                                  |
| `specRefreshInterval` | duration    | Per-server override of `gateway.specRefresh.interval`. `0` disables refreshing for this server |

`authValue` / `oauth2` / `tokenExchange` are mutually exclusive; only one may be configured at a time.

#### `mcpServers.<name>.oauth2`

| Field          | Type     | Description                                         |
| -------------- | -------- | --------------------------------------------------- |
| `clientID`     | string   | Client ID (**required**)                            |
| `clientSecret` | string   | Client secret (**required**)                        |
| `authURL`      | string   | Authorization endpoint (**required**; absolute URL) |
| `tokenURL`     | string   | Token endpoint (**required**; absolute URL)         |
| `scopes`       | []string | Scopes to request                                   |

#### `mcpServers.<name>.tokenExchange`

Exchanges the API key received from the client for an OAuth token at the specified token exchange endpoint, and uses it for backend requests. Exchange results are cached, and rate limits (429) are respected.

| Field | Type   | Description                                                |
| ----- | ------ | ---------------------------------------------------------- |
| `url` | string | Absolute URL of the token exchange endpoint (**required**) |

#### `redis`

| Field          | Type     | Description                                           |
| -------------- | -------- | ----------------------------------------------------- |
| `url`          | string   | Redis URL (e.g. `redis://user:pass@localhost:6379/0`) |
| `addrs`        | []string | List of host:port pairs (for Cluster/Sentinel)        |
| `user`         | string   | Username                                              |
| `password`     | string   | Password                                              |
| `db`           | int      | Database number                                       |
| `master_name`  | string   | Sentinel master name                                  |
| `tls`          | bool     | Enable TLS                                            |
| `cluster_mode` | bool     | Enable Cluster mode                                   |

#### `sqlite`

| Field  | Type   | Description                                   |
| ------ | ------ | --------------------------------------------- |
| `path` | string | Database file path (`:memory:` for in-memory) |

Either `redis` or `sqlite` must be configured.

#### `storage`

Stores content included in OpenAPI/Swagger tool responses (images, binaries, etc.) in external storage and returns resource links (download URLs). When unset, no storage is used.

| Field          | Type   | Description                                                                                |
| -------------- | ------ | ------------------------------------------------------------------------------------------ |
| `type`         | string | Storage type. Currently only `s3` is supported                                             |
| `hostURL`      | string | Host for download URLs (when set, content is served via Manifold's `/media/download/{id}`) |
| `s3.bucket`    | string | S3 bucket name (required when `type: s3`)                                                  |
| `s3.keyPrefix` | string | S3 object key prefix (required when `type: s3`)                                            |

```yaml
storage:
  type: s3
  hostURL: https://manifold.example.com
  s3:
    bucket: my-bucket
    keyPrefix: manifold/media
```

#### `fileFetch`

When a URL is passed to a file input field of an OpenAPI/Swagger tool, Manifold downloads the file from that URL. As an SSRF countermeasure, connections to private/loopback/link-local IPs and the `http://` scheme are rejected by default.

| Field          | Type     | Description                                                                                               |
| -------------- | -------- | --------------------------------------------------------------------------------------------------------- |
| `allowLocal`   | bool     | Allow connections to private/loopback IPs and `http://` (for testing with local stacks; default: `false`) |
| `allowedHosts` | []string | Allowlist of hosts (hostname, or `host:port`). Empty allows all hosts (private IP blocking still applies) |
| `maxSize`      | int64    | Maximum bytes for downloaded/base64/text content. 0 or unset defaults to 524288000 (500 MiB)              |

Each field can also be overridden via environment variables (`FILEFETCH_MAXSIZE`, `FILEFETCH_ALLOWLOCAL`, `FILEFETCH_ALLOWEDHOSTS`).

```yaml
fileFetch:
  allowLocal: false
  maxSize: 524288000 # 500MiB
  # allowedHosts:
  #   - example.com
  #   - files.example.com:8443
```

#### `telemetry`

Output settings for traces, metrics, and logs via OpenTelemetry.

| Field             | Type   | Description                                                                   |
| ----------------- | ------ | ----------------------------------------------------------------------------- |
| `serviceName`     | string | Service name                                                                  |
| `environment`     | string | Environment name (`deployment.environment` attribute)                         |
| `gzipCompression` | bool   | Gzip compression for OTLP export                                              |
| `trace`           | object | Trace settings (`enabled`, `http`, `grpc`)                                    |
| `metrics`         | object | Metrics settings (`enabled`, `exporterType`: `push` / `pull`, `http`, `grpc`) |
| `logs`            | object | Log settings (`enabled`, `http`, `grpc`)                                      |

For the `http` / `grpc` exporters, specify `addr` (host:port) or `url`. `grpc` also accepts `insecure`. With `metrics.exporterType: pull`, Prometheus-format metrics are exposed at the `/metrics` endpoint instead of OTLP push.

```yaml
telemetry:
  serviceName: manifold
  trace:
    enabled: true
    grpc:
      addr: localhost:4317
      insecure: true
  metrics:
    enabled: true
    exporterType: push
    grpc:
      addr: localhost:4317
      insecure: true
  logs:
    enabled: true
    grpc:
      addr: localhost:4317
      insecure: true
```

## Tool authorization (OPA sidecar)

Manifold can enforce which `server/tool` pairs a caller may use on `tools/call` and `tools/list`, delegating each decision to an external [OPA](https://www.openpolicyagent.org/) sidecar. Disabled by default (`authz.enabled: false`, preserving prior behavior); authentication, group resolution, and policy storage stay out of Manifold's scope — it trusts identity headers injected by an upstream layer and queries OPA for the decision.

```yaml
authz:
  enabled: true
  opaURL: http://localhost:8181
  timeout: 3s
  decisionPath:
    list: /v1/data/mcp/authz/allowed_tools
    call: /v1/data/mcp/authz/allow
  headers:
    userID: x-user-id
    userGroups: x-user-groups
  adminGroups:
    - team-platform
```

| Field | Type | Default | Description |
| ----- | ---- | ------- | ----------- |
| `enabled` | bool | `false` | Enables the authz middleware. Every other field below is only read when `true` |
| `opaURL` | string | `http://localhost:8181` | Base URL of the OPA sidecar (`http` or `https`) |
| `timeout` | duration | `3s` | Per-decision HTTP timeout |
| `decisionPath.list` | string | `/v1/data/mcp/authz/allowed_tools` | OPA data path queried once per `tools/list` |
| `decisionPath.call` | string | `/v1/data/mcp/authz/allow` | OPA data path queried once per `tools/call` |
| `headers.userID` | string | `x-user-id` | Inbound header carrying the caller's user ID |
| `headers.userGroups` | string | `x-user-groups` | Inbound header carrying the caller's groups, comma-separated |
| `headers.bypass` | string | `x-authz-bypass` | Inbound header that, set to the exact string `true`, disables authz enforcement for that one request (see "Disabling authorization per tenant" below) |
| `adminGroups` | []string | `[]` | Groups allowed to call `GET /mcp/list?tools=true` (see "Tool catalog for policy authoring" below). Empty denies every caller |

Manifold treats the `headers.userID` value as an opaque string: it doesn't interpret it, just passes it through to OPA's `input.user` as-is. In a multi-tenant deployment, use a format that includes the tenant (e.g. `{tenant}:{user}`) so policies can tell tenants apart. `headers.userGroups` values should likewise be immutable opaque IDs (e.g. [ULIDs](https://github.com/ulid/spec)) rather than display names, since display names can change.

### Prerequisites

Manifold trusts `headers.userID` / `headers.userGroups` — and, if configured, `headers.bypass` — on every request without verifying them itself, the same caveat as the WebMCP reverse gateway's `forwardAuth` mode (see its Trust boundary section in `docs/design/webmcp-reverse-gateway.md`). Before enabling `authz.enabled`:

- The fronting proxy must strip or overwrite any client-supplied headers of the same names, so a caller cannot forge its own identity
- Direct access to Manifold bypassing that proxy must be blocked at the network layer (e.g. a Kubernetes `NetworkPolicy`)
- **`headers.bypass` is more sensitive than the identity headers**: a caller that can set it to `true` disables authorization entirely for its own requests, regardless of identity or group membership. The fronting proxy must strip or overwrite it with the same rigor, and every network path that can reach Manifold without going through that proxy must be closed at the network layer — not merely authenticated separately

### Decision contract

Manifold POSTs `{"input": ...}` to `opaURL + decisionPath.call` for every `tools/call`, and to `opaURL + decisionPath.list` once per `tools/list` (batched across every tool, not queried per tool):

```jsonc
// tools/call
{"input": {"user": "user-042", "groups": ["team-finance"], "server": "billing-svc", "tool": "create_invoice"}}
// → {"result": true}

// tools/list
{"input": {"user": "user-042", "groups": ["team-finance"], "tools": [{"server": "billing-svc", "name": "create_invoice"}, ...]}}
// → {"result": [{"server": "billing-svc", "name": "create_invoice"}, ...]}
```

Manifold does not prescribe a shape for OPA's `data` document; policies are free to structure it however they like — see [`examples/opa/`](examples/opa/) for a working `policy.rego` and `data.json` (`data.policies[<group id>].tools` as a list of `<server>/<tool>` glob patterns).

### Tool catalog for policy authoring

Writing a policy requires knowing every `<server>/<tool>` pair that exists, but `tools/list` only ever shows what the caller is already allowed to see. `GET /mcp/list?tools=true` returns the unfiltered catalog instead: when `authz.enabled` is `false` it's open to anyone, and when `true` it requires the caller to be in one of `authz.adminGroups` (identified the same way as `tools/call` — `headers.userID` / `headers.userGroups`), responding `403 {"error": "forbidden"}` otherwise.

```jsonc
{
  "mcp": [
    {
      "name": "petstore",
      "description": "Swagger Petstore sample API",
      "tools": [
        {"name": "getpetbyid", "description": "Find pet by ID."}
      ]
    },
    // A WebMCP reverse server's tools only exist per-browser-connection, so
    // it reports "dynamic" instead of a tool list.
    {"name": "billing-svc", "description": "browser app", "dynamic": true},
    // A backend that failed to connect still lists (with "error" instead of
    // "tools") rather than dropping out of the response.
    {"name": "crm", "description": "CRM MCP backend", "error": "connect: dial tcp: connection refused"}
  ]
}
```

### Disabling authorization per tenant

A fronting proxy that multiplexes several tenants behind one Manifold deployment can disable authz for a single request without flipping `authz.enabled` globally: set `headers.bypass` (default `x-authz-bypass`) to the exact string `true`. Any other value — `True`, `1`, empty, or the header missing — goes through the normal authz checks (fail-closed).

When bypassed, for that request:

- `tools/call` skips OPA and reaches the tool directly
- `tools/list` returns the backend's full tool list, unfiltered
- `GET /mcp/list?tools=true` returns `200` with the full catalog regardless of `adminGroups` membership

This is equivalent to `authz.enabled: false` for that one request. Manifold logs `decision: bypass` (with `server` / `method`, no identity — none was resolved) so bypassed requests are distinguishable from `allow` / `deny` in an audit trail.

### Fail-closed behavior

Every ambiguous or failing case denies the request rather than allowing it:

- A missing or empty `headers.userID` / `headers.userGroups` denies without querying OPA
- A non-200 response, a response missing the expected `result` field, a timeout, or a connection failure to OPA all deny
- `tools/list` filtering is a convenience — it hides tools the caller cannot use so they don't clutter a client's tool picker — but it is not the enforcement point. Enforcement happens on `tools/call`; a client that already knows a tool's name (e.g. from a stale list) is still denied there
- A reverse (WebMCP) `mcpServers` entry always registers a `create_pairing_code` tool (see `docs/design/webmcp-reverse-gateway.md`), and `authz.enabled` covers it like any other tool. A group that should be able to pair with such a server needs `<server>/create_pairing_code` in its policy, or pairing itself is denied
- This also holds one level down, inside OPA itself: if a bundle fetch fails, OPA keeps enforcing with the last bundle it activated — a bundle server outage stops policy updates, not decisions. But if OPA has never activated a bundle since startup (the bundle server was unreachable at boot, for example), `data` stays empty and every decision comes back `false` / `[]`, which fail-closes the same way. Bundle fetch failures are still worth alerting on — see "Operating recommendations" below

### Operating recommendations

- Enable OPA's [decision log](https://www.openpolicyagent.org/docs/management-decision-logs) for an audit trail of every `allow` / `allowed_tools` query. Each event should carry `user` / `groups` / `server` / `tool` / the decision, and the revision of the policy data that produced it — without a data revision there's no way to tell which policy version a given decision was made under
- Distribute policy and data as an OPA [bundle](https://www.openpolicyagent.org/docs/management-bundles) served over HTTP rather than mounting local files, so policy updates don't require restarting the sidecar. Bundle mode also stamps every decision log event with `bundles.<name>.revision`, which is where that revision comes from
- Monitor OPA's bundle fetch status (see "Fail-closed behavior" above for what a failure does to enforcement): OPA's Health API (`GET /health?bundles=true`) reports unhealthy until every configured bundle has been activated at least once, so it doubles as a readiness probe. The status API and decision log also surface fetch failures

See [`examples/opa/`](examples/opa/) for a runnable OPA sidecar with sample policy and data.

## HTTP endpoints

The HTTP endpoints exposed by Manifold.

### MCP

| Method | Path                 | Description                                      |
| ------ | -------------------- | ------------------------------------------------ |
| `POST` | `/mcp/{server_name}` | MCP requests (Streamable HTTP)                   |
| `GET`  | `/mcp/list`          | List registered servers (names and descriptions). Add `?tools=true` for the tool catalog (see "Tool catalog for policy authoring" above) |

### OAuth 2.1

| Method | Path                                                        | Description                            |
| ------ | ----------------------------------------------------------- | -------------------------------------- |
| `GET`  | `/.well-known/oauth-authorization-server/mcp/{server_name}` | Authorization Server metadata          |
| `GET`  | `/.well-known/oauth-protected-resource/mcp/{server_name}`   | Protected Resource metadata            |
| `GET`  | `/{server_name}/auth/login`                                 | Redirect to the login page             |
| `GET`  | `/{server_name}/auth/callback`                              | OAuth callback                         |
| `POST` | `/{server_name}/auth/token`                                 | Token issuance                         |
| `POST` | `/{server_name}/auth/clients`                               | Dynamic client registration (RFC 7591) |
| `GET`  | `/authorize`, `/callback`                                   | Aliases without a server name          |
| `POST` | `/token`, `/register`                                       | Aliases without a server name          |

### Other

| Method | Path                   | Description                                                           |
| ------ | ---------------------- | --------------------------------------------------------------------- |
| `GET`  | `/media/download/{id}` | Download stored content (only when `storage.hostURL` is set)          |
| `GET`  | `/metrics`             | Prometheus metrics (only when `telemetry.metrics.exporterType: pull`) |

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for how to set up a development environment and submit changes.

### Test

```bash
make test
```

### Lint

```bash
make lint
```

## Inspiration

This project is inspired by the **Agent / MCP Gateway** of [LiteLLM](https://github.com/BerriAI/litellm).

Just as LiteLLM's MCP Gateway provides a unified access point to multiple MCP servers, Manifold aims to be a gateway that connects a single MCP interface to many MCP servers / REST APIs.

## License

MIT License
