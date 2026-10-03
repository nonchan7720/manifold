[日本語](README_ja.md)

# Postman CLI end-to-end suite

Runs a [Postman](https://learning.postman.com/docs/postman-cli/) collection against a locally built gateway to check tool search (`gateway.toolSearch`), tool filtering (`tools.include` / `overrides`) and tool authorization (OPA) together. CI runs it on every pull request (`.github/workflows/postman.yaml`).

```text
OPA (docker)  <--  manifold gateway  -->  stub Petstore API (Go)
                        ^
                  postman collection run
```

| File | Role |
| ---- | ---- |
| `run.sh` | Builds the gateway and the stub, starts OPA with `examples/opa/policy.rego`, runs the collection, then checks the audit log |
| `config.yaml` | Gateway config: `toolSearch.threshold: 1`, two Petstore servers (`petstore` with 5 filtered/renamed tools, `petstore_small` with 1), `authz` on, `audit` to a file |
| `opa/data.json` | Two groups: `readers` (`get_pet`, `findpetsbystatus`) and `operators` (everything) |
| `stub/main.go` | Serves the petstore fixture spec and a few operations, echoing the `Authorization` header |
| `manifold-tool-search.postman_collection.json` | The requests and assertions |

## Run locally

Needs `go`, `docker`, `curl` and the Postman CLI (no Postman account: a local collection file runs without `postman login`).

```bash
make postman          # or tests/postman/run.sh
```

Logs, the audit file and the JUnit report are written to `tests/postman/tmp/`.

## What the collection checks

- No bearer token → `401`.
- `operators` on `petstore`: `tools/list` is only `tool_search`, whose description lists the five visible tools under their exposed names (`get_pet`, not `getpetbyid`; no `addpet`). `tool_search` works with `regexp`, `bm25` and `fuzzy`, an invalid regexp is a tool error, the hidden `get_pet` is callable and the caller's token reaches the stub, and the original name `getpetbyid` is unknown.
- `readers` on `petstore`: `tool_search`'s description and results only contain the two allowed tools; `deletepet` is denied.
- No identity headers: `tools/list` and `tool_search` are both denied.
- `petstore_small`: one visible tool stays below the threshold, so it is listed and callable as is.
- Audit log: `tool_search` and `get_pet` recorded as `success`, `deletepet` and the identity-less `tool_search` as `denied`.
