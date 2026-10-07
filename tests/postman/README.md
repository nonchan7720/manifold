[日本語](README_ja.md)

# Postman CLI end-to-end suite

Runs a [Postman](https://learning.postman.com/docs/postman-cli/) collection against a locally built gateway to check tool filtering (`tools.include` / `overrides`), tool authorization (OPA) and the audit log together. CI runs it on every pull request (`.github/workflows/postman.yaml`).

```text
OPA (docker)  <--  manifold gateway  -->  stub Petstore API (Go)
                        ^
                  postman collection run
```

| File | Role |
| ---- | ---- |
| `run.sh` | Builds the gateway and the stub, starts OPA with `examples/opa/policy.rego`, runs the collection, then checks the audit log |
| `config.yaml` | Gateway config: two Petstore servers (`petstore` with 5 filtered/renamed tools, `petstore_small` with 1), `authz` on, `audit` to a file |
| `opa/data.json` | Two groups: `readers` (`get_pet`, `findpetsbystatus`) and `operators` (everything) |
| `stub/main.go` | Serves the petstore fixture spec and a few operations, echoing the `Authorization` header |
| `manifold.postman_collection.json` | The requests and assertions |

## Run locally

Needs `go`, `docker`, `curl` and the Postman CLI. `mise install` provides `go` and the Postman CLI (`postman-cli` in [`mise.toml`](../../mise.toml)); no Postman account is needed, since a local collection file runs without `postman login`.

```bash
make postman          # or tests/postman/run.sh
```

Logs, the audit file and the JUnit report are written to `tests/postman/tmp/`.

## What the collection checks

- No bearer token → `401`.
- `operators` on `petstore`: `tools/list` returns the five included tools under their exposed names (`get_pet`, not `getpetbyid`; no `addpet`), `get_pet` is callable and the caller's token reaches the stub, and the original name `getpetbyid` is unknown.
- `readers` on `petstore`: `tools/list` only contains the two allowed tools; `deletepet` is denied.
- No identity headers: `tools/list` is denied.
- `petstore_small`: only the single included tool is listed, and it is callable.
- Audit log: `get_pet` and `getinventory` recorded as `success`, the unknown `getpetbyid` as `error` and `deletepet` as `denied`.
