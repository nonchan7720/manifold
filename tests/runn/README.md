[日本語](README_ja.md)

# runn end-to-end suite

Runs [runn](https://github.com/k1LoW/runn) runbooks against a locally built gateway to check tool filtering (`tools.include` / `overrides`), tool authorization (OPA), the Streamable HTTP protocol edge cases, the catalog endpoint and the audit log together. CI runs it on every pull request (`.github/workflows/runn.yaml`).

```text
OPA (docker)  <--  manifold gateway  -->  stub Petstore API (Go)
                        ^
                     runn run
```

| File | Role |
| ---- | ---- |
| `run.sh` | Builds the gateway and the stub, starts OPA with `examples/opa/policy.rego`, runs the runbooks, then checks the audit log |
| `config.yaml` | Gateway config: two Petstore servers (`petstore` with 5 filtered/renamed tools, `petstore_small` with 1), `authz` on, `audit` to a file, `maxRequestBodySize: 4096` |
| `opa/data.json` | Two groups: `readers` (`get_pet`, `findpetsbystatus`, `petstore_small/*`) and `operators` (everything, plus `catalog`) |
| `stub/main.go` | Serves the petstore fixture spec and a few operations, echoing the `Authorization` header |
| `runbooks/*.yml` | The runbooks, run in file name order (see below) |

## Run locally

Needs `go`, `docker`, `curl` and [runn](https://github.com/k1LoW/runn). `mise install` provides `go` and runn (`runn` in [`mise.toml`](../../mise.toml)). Without docker, point `OPA_BIN` at an [`opa`](https://www.openpolicyagent.org/docs/#running-opa) binary and it is started directly.

```bash
make runn             # or tests/runn/run.sh
OPA_BIN=/path/to/opa tests/runn/run.sh   # without docker
```

The runbooks read the gateway URL from `BASE_URL` (default `http://127.0.0.1:19999`), so they can also be run by hand against a gateway that is already up: `cd tests/runn && BASE_URL=... runn run --verbose --debug-on-failure 'runbooks/*.yml'`. `runn run --run 30_authz` runs one runbook.

Logs, the audit file and the runn output (`runn.log`) are written to `tests/runn/tmp/`. A failed step prints its request and response (`--debug-on-failure`) and a tree of the assertion with the value of each sub-expression.

## Writing runbooks

Streamable HTTP answers `tools/list` and `tools/call` with `text/event-stream`, so each test decodes the JSON-RPC message from the `data:` line of `current.res.rawBody` with expr (`let rpc = fromJSON(...)`). The common request headers are YAML anchors in `vars` (`<<: [*mcp, *operators]`). The decoding is repeated in each test (rather than bound once) so that every test is self-contained and its failure tree shows the decoded message.

## What the suite checks

| Runbook | Checks |
| ------- | ------ |
| `00_health.yml` | `/healthz` and `/readyz` are `200` without credentials |
| `10_auth.yml` | `initialize` works (with or without identity headers); no token, `Basic`, lowercase `bearer`, empty or empty-valued `Authorization` → `401` with a `WWW-Authenticate: Bearer resource_metadata=...` challenge; authentication runs before the body is parsed |
| `20_tool_filter.yml` | `operators` on `petstore`: `tools/list` returns the five included tools under their exposed names (`get_pet`, not `getpetbyid`; no `addpet`) with the original description; `get_pet` is callable and the caller's token reaches the stub; `getpetbyid`, `addpet`, unknown and empty tool names are `unknown tool`; `find*` also exposes `findpetsbytags`. `petstore_small`: only `getinventory`, callable; includes are independent per server |
| `30_authz.yml` | `readers`: `tools/list` has only `findpetsbystatus` and `get_pet`; `get_pet` works; `findpetsbystatus` forwards its query; `deletepet`, the included-but-unlisted `findpetsbytags`, the renamed-away `getpetbyid` and unknown tools are all `tool not allowed by policy`; `petstore_small/*` works. `operators` can call `deletepet`. Identity: missing, half-present, empty or separator-only `x-user-id` / `x-user-groups` are denied; unknown groups get an empty `tools/list` and denied calls; group names are case-sensitive; several groups are unioned (blanks and empty elements ignored); header names are case-insensitive |
| `35_bypass.yml` | `x-authz-bypass: true` skips authorization (even against a restrictive group); `TRUE`, `1` do not; `false` keeps the policy |
| `40_protocol_edge.yml` | Unknown server (`400`), extra path segment (`404`); malformed JSON, empty body, missing / JSON-only / SSE-only `Accept` (`400`), wrong `Content-Type` (`415`), `GET` / `DELETE` (`405`), unsupported `MCP-Protocol-Version` (`400`); notification (`202`), `ping`, unknown method, missing `params`, string ids; batches (rejected on current protocols, authorized per message on the legacy one); `tools/call` with a missing / wrongly typed / unknown argument; the `maxRequestBodySize` limit (`413`, checked before authentication) |
| `50_catalog.yml` | `GET /mcp/list` is open and sorted by name; `?tools=true` needs the `catalog` permission (or the bypass header): `403 {"error":"forbidden"}` for no identity, `readers`, unknown groups and half identities; `operators` get the tools after include and rename |
| `run.sh` | Audit log: `get_pet`, `getinventory` and `deletepet` (as `operators`) recorded as `success`, the unknown `getpetbyid` as `error` and `deletepet` (as `readers`) as `denied` |

Some of these pin down behaviour that is the go-sdk's rather than the gateway's (the `400` for an unknown method or an unknown server name, the `Accept` check). They are there to notice when an SDK upgrade changes them. A backend `4xx` (for instance the stub's `400` for a non-numeric pet id) comes back as a normal tool result carrying the response text, not as `isError: true`; the `argument_*` steps record that as it is today.
