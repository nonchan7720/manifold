[日本語](README_ja.md)

# Tool authorization example — OPA sidecar

Adds an [OPA](https://www.openpolicyagent.org/) sidecar in front of the [`openapi-backend`](../openapi-backend/) Petstore example so that `tools/call` and `tools/list` on `petstore` are authorized per caller group. See the [root README's "Tool authorization (OPA sidecar)"](../../README.md#tool-authorization-opa-sidecar) section for the full configuration reference.

## What's here

- `policy.rego` — the `allow` (single tool) and `allowed_tools` (batch) rules Manifold queries
- `data.json` — three example groups, each an [opaque, ULID-like](https://github.com/ulid/spec) group ID mapped to a list of `<server>/<tool>` glob patterns
- `compose.yaml` — starts OPA with these files loaded as a `-b` bundle directory
- `config.yaml` — the `petstore` server from `openapi-backend`, plus `authz.enabled: true`

| Group ID | Grants |
| -------- | ------ |
| `01J8X9QZ3KZFN8P8V6H2R5T4WC` | Read-only: `getpetbyid`, `findpetsbystatus`, `getinventory` |
| `01J8X9R14V0S9WQKX9DAT2F7NB` | All `petstore` tools (`petstore/*`) |
| `01J8X9RM8D3V1CQ0K7P5N2T9YH` | `getpetbyid` on any server (`*/getpetbyid`) — shows a cross-server pattern |

## Run

```bash
cd examples/opa
docker compose up -d          # starts OPA on :8181

# Generate once and reuse — see ../README.md
export ENCRYPT_KEY=${ENCRYPT_KEY:-$(openssl rand -base64 32)}
mkdir -p tmp
manifold gateway
```

## Try it

`x-user-id` / `x-user-groups` stand in for the headers a fronting proxy would inject after authenticating the caller (see the root README's prerequisites — Manifold trusts them as-is).

`/mcp/{name}` also requires an `Authorization: Bearer <token>` header; Manifold's pass-through JWT middleware only checks that it is present and forwards it to the upstream API without verifying it. The Petstore backend doesn't require authentication, so any value works here.

Read-only group, calling an allowed tool — succeeds:

```bash
curl -s http://localhost:9999/mcp/petstore \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Authorization: Bearer dummy-token' \
  -H 'x-user-id: user-001' \
  -H 'x-user-groups: 01J8X9QZ3KZFN8P8V6H2R5T4WC' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"getpetbyid","arguments":{"petId":1}}}'
```

Same group, calling a tool it was not granted — denied with a JSON-RPC error:

```bash
curl -s http://localhost:9999/mcp/petstore \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Authorization: Bearer dummy-token' \
  -H 'x-user-id: user-001' \
  -H 'x-user-groups: 01J8X9QZ3KZFN8P8V6H2R5T4WC' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deletepet","arguments":{"petId":1}}}'
# {"jsonrpc":"2.0","id":2,"error":{"code":-32603,"message":"tool not allowed by policy"}}
```

`tools/list` for the read-only group only lists the three tools its patterns match; run it again with the admin group's `x-user-groups: 01J8X9R14V0S9WQKX9DAT2F7NB` to see every `petstore` tool instead:

```bash
curl -s http://localhost:9999/mcp/petstore \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Authorization: Bearer dummy-token' \
  -H 'x-user-id: user-001' \
  -H 'x-user-groups: 01J8X9QZ3KZFN8P8V6H2R5T4WC' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/list"}'
```

Missing `x-user-id` / `x-user-groups`, or `docker compose stop opa` — both deny every call (fail-closed) without Manifold ever reaching OPA in the first case:

```bash
curl -s http://localhost:9999/mcp/petstore \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Authorization: Bearer dummy-token' \
  -d '{"jsonrpc":"2.0","id":4,"method":"tools/list"}'
# {"jsonrpc":"2.0","id":4,"error":{"code":-32603,"message":"tool not allowed by policy"}}
```

## Adapting to your own policy

Replace `policy.rego` / `data.json` with your own, keeping the `allow` and `allowed_tools` rule names (or point `authz.decisionPath` at different ones). For production, prefer distributing `data.json` and `policy.rego` as an OPA [bundle](https://www.openpolicyagent.org/docs/management-bundles) served over HTTP rather than mounting local files, and enable OPA's [decision log](https://www.openpolicyagent.org/docs/management-decision-logs) for an audit trail of every `allow` / `allowed_tools` query.
