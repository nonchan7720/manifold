[日本語](README_ja.md)

# Tool authorization example — OPA sidecar

Adds an [OPA](https://www.openpolicyagent.org/) sidecar in front of the [`openapi-backend`](../openapi-backend/) Petstore example so that `tools/call` and `tools/list` on `petstore` are authorized per caller group. See the [root README's "Tool authorization (OPA sidecar)"](../../README.md#tool-authorization-opa-sidecar) section for the full configuration reference.

## What's here

- `policy.rego` — the `allow` (single tool), `allowed_tools` (batch), and `allow_catalog` (`GET /mcp/list?tools=true`) rules Manifold queries
- `data.json` — four example groups, each a group ID mapped to a list of `<server>/<tool>` glob patterns and/or a `catalog` flag. `catalog` is independent of `tools`: a group can hold either, both, or neither
- `compose.yaml` — starts OPA with these files loaded as a `-b` bundle directory
- `config.yaml` — the `petstore` server from `openapi-backend`, plus `authz.enabled: true`

| Group ID | Grants |
| -------- | ------ |
| `petstore-readers` | Read-only: `getpetbyid`, `findpetsbystatus`, `getinventory` |
| `petstore-operators` | All `petstore` tools (`petstore/*`) — no catalog access |
| `pet-lookup` | `getpetbyid` on any server (`*/getpetbyid`) — shows a cross-server pattern |
| `policy-authors` | No tools at all — can't call or list anything, but can read the unfiltered tool catalog (`catalog: true`); for policy authors who need to see every `<server>/<tool>` pair without any execution rights |

These group IDs are readable names for this example only; the root README recommends [immutable opaque IDs](../../README.md#tool-authorization-opa-sidecar) (e.g. ULIDs) rather than display names in production, since display names can change.

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
  -H 'x-user-groups: petstore-readers' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"getpetbyid","arguments":{"petId":1}}}'
```

Same group, calling a tool it was not granted — denied with a JSON-RPC error:

```bash
curl -s http://localhost:9999/mcp/petstore \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Authorization: Bearer dummy-token' \
  -H 'x-user-id: user-001' \
  -H 'x-user-groups: petstore-readers' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deletepet","arguments":{"petId":1}}}'
# {"jsonrpc":"2.0","id":2,"error":{"code":-32603,"message":"tool not allowed by policy"}}
```

`tools/list` for the read-only group only lists the three tools its patterns match; run it again with the operator group's `x-user-groups: petstore-operators` to see every `petstore` tool instead:

```bash
curl -s http://localhost:9999/mcp/petstore \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Authorization: Bearer dummy-token' \
  -H 'x-user-id: user-001' \
  -H 'x-user-groups: petstore-readers' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/list"}'
```

Writing a policy requires knowing every `<server>/<tool>` pair that exists — `GET /mcp/list?tools=true` returns that unfiltered catalog, gated by the `allow_catalog` rule instead of `allow` / `allowed_tools`. This is a separate grant from tool execution: the catalog-only group below can't call or list a single `petstore` tool but can read the catalog, while the group that can call every `petstore` tool (`petstore-operators`) is denied the catalog:

```bash
curl -s 'http://localhost:9999/mcp/list?tools=true' \
  -H 'x-user-id: user-001' \
  -H 'x-user-groups: policy-authors'

curl -s -o /dev/null -w '%{http_code}\n' 'http://localhost:9999/mcp/list?tools=true' \
  -H 'x-user-id: user-001' \
  -H 'x-user-groups: petstore-operators'
# 403
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

`x-authz-bypass: true` disables authz entirely for a single request (see the root README's ["Disabling authorization per tenant"](../../README.md#disabling-authorization-per-tenant)). The read-only group can now call `deletepet`, which its policy does not grant:

```bash
curl -s http://localhost:9999/mcp/petstore \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Authorization: Bearer dummy-token' \
  -H 'x-user-id: user-001' \
  -H 'x-user-groups: petstore-readers' \
  -H 'x-authz-bypass: true' \
  -d '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"deletepet","arguments":{"petId":1}}}'
```

## Adapting to your own policy

Replace `policy.rego` / `data.json` with your own, keeping the `allow`, `allowed_tools`, and `allow_catalog` rule names (or point `authz.decisionPath` at different ones). For production, prefer distributing `data.json` and `policy.rego` as an OPA [bundle](https://www.openpolicyagent.org/docs/management-bundles) served over HTTP rather than mounting local files, and enable OPA's [decision log](https://www.openpolicyagent.org/docs/management-decision-logs) for an audit trail of every `allow` / `allowed_tools` / `allow_catalog` query.

Once bundles come from a real server, a fetch failure doesn't stop enforcement — OPA keeps deciding with the last bundle it activated. Only the case where OPA has never activated a bundle since startup denies everything (`data` stays empty). Watch for that with the Health API's `bundles=true` check (see the root README's "Operating recommendations").
