[English](README.md)

# runn による結合テスト

[runn](https://github.com/k1LoW/runn) のランブックを、ローカルでビルドしたゲートウェイに対して実行し、ツールの絞り込み（`tools.include` / `overrides`）・ツール認可（OPA）・Streamable HTTP プロトコルのエッジケース・カタログ API・監査ログをまとめて検証します。CI では PR ごとに実行します（`.github/workflows/runn.yaml`）。

```text
OPA (docker)  <--  manifold gateway  -->  スタブの Petstore API (Go)
                        ^
                     runn run
```

| ファイル | 役割 |
| -------- | ---- |
| `run.sh` | ゲートウェイとスタブをビルドし、`examples/opa/policy.rego` で OPA を起動してランブックを実行し、最後に監査ログを確認する |
| `config.yaml` | ゲートウェイ設定。Petstore を 2 サーバー（`petstore` は絞り込みとリネーム後の 5 ツール、`petstore_small` は 1 ツール）、`authz` 有効、`audit` をファイルへ出力、`maxRequestBodySize: 4096` |
| `opa/data.json` | グループ 2 つ。`readers`（`get_pet`・`findpetsbystatus`・`petstore_small/*`）と `operators`（全部。`catalog` 付き） |
| `stub/main.go` | petstore のフィクスチャ spec と数個の operation を提供し、`Authorization` ヘッダーをそのまま返す |
| `runbooks/*.yml` | ランブック。ファイル名順に実行する（下記） |

## ローカルで実行する

`go`・`docker`・`curl`・[runn](https://github.com/k1LoW/runn) が必要です。`go` と runn は `mise install` で入ります（[`mise.toml`](../../mise.toml) の `runn`）。docker が無い場合は `OPA_BIN` に [`opa`](https://www.openpolicyagent.org/docs/#running-opa) バイナリのパスを指定すると、直接起動します。

```bash
make runn             # または tests/runn/run.sh
OPA_BIN=/path/to/opa tests/runn/run.sh   # docker なしの場合
```

ランブックはゲートウェイの URL を `BASE_URL`（既定は `http://127.0.0.1:19999`）から読むので、すでに起動しているゲートウェイに対して手で実行することもできます: `cd tests/runn && BASE_URL=... runn run --verbose --debug-on-failure 'runbooks/*.yml'`。`runn run runbooks/30_authz.yml` で 1 つのランブックだけ実行できます。

ログ・監査ログ・runn の出力（`runn.log`）は `tests/runn/tmp/` に書き出されます。失敗したステップはリクエストとレスポンス（`--debug-on-failure`）と、アサーションを部分式ごとの値つきで展開したツリーを出力します。

## ランブックの書き方

Streamable HTTP は `tools/list` や `tools/call` に `text/event-stream` で応答するため、各 test で `current.res.rawBody` の `data:` 行から JSON-RPC メッセージを expr で取り出します（`let rpc = fromJSON(...)`）。共通のリクエストヘッダーは `vars` の YAML アンカーです（`<<: [*mcp, *operators]`）。デコードは bind せず test ごとに繰り返しています。各 test が自己完結し、失敗時のツリーにデコード後のメッセージが出るためです。

## 確認していること

| ランブック | 確認内容 |
| ---------- | -------- |
| `00_health.yml` | `/healthz` と `/readyz` が認証なしで `200` |
| `10_auth.yml` | `initialize` が通る（識別ヘッダーの有無によらず）。トークン無し・`Basic`・小文字の `bearer`・空の `Authorization`・値が空の `Authorization` は `401` で、`WWW-Authenticate: Bearer resource_metadata=...` が付く。認証は本文のパースより先に行われる |
| `20_tool_filter.yml` | `operators` で `petstore`: `tools/list` は絞り込み後の 5 ツールを公開名で返し（`getpetbyid` ではなく `get_pet`。`addpet` は無い）元の description を保つ。`get_pet` を呼ぶと呼び出し元のトークンがスタブまで届く。`getpetbyid`・`addpet`・存在しない名前・空の名前は `unknown tool`。`find*` は `findpetsbytags` も公開する。`petstore_small`: `getinventory` だけが出て呼び出せる。include はサーバーごとに独立 |
| `30_authz.yml` | `readers`: `tools/list` は `findpetsbystatus` と `get_pet` だけ。`get_pet` は呼べ、`findpetsbystatus` はクエリが転送される。`deletepet`・include 済みだがポリシー外の `findpetsbytags`・リネーム前の `getpetbyid`・存在しないツールはすべて `tool not allowed by policy`。`petstore_small/*` は呼べる。`operators` は `deletepet` を呼べる。識別: `x-user-id` / `x-user-groups` が無い・片方だけ・空・区切り文字だけなら拒否。未知のグループは `tools/list` が空で呼び出しは拒否。グループ名は大文字小文字を区別。複数グループは和集合（空白と空要素は無視）。ヘッダー名は大文字小文字を区別しない |
| `35_bypass.yml` | `x-authz-bypass: true` で認可をスキップ（制限的なグループがあっても）。`TRUE`・`1` はスキップしない。`false` ならポリシーが効く |
| `40_protocol_edge.yml` | 未知のサーバー（`400`）、余分なパス（`404`）。不正な JSON・空の本文・`Accept` 無し / JSON のみ / SSE のみ（`400`）、`Content-Type` 違い（`415`）、`GET` / `DELETE`（`405`）、未対応の `MCP-Protocol-Version`（`400`）。notification（`202`）・`ping`・未知のメソッド・`params` 欠落・文字列の id。バッチ（現行プロトコルでは拒否、旧プロトコルではメッセージごとに認可）。`tools/call` の引数の欠落・型違い・未知のキー。`maxRequestBodySize` の上限（`413`。認証より先に判定） |
| `50_catalog.yml` | `GET /mcp/list` は認証不要で名前順。`?tools=true` は `catalog` 権限（または bypass ヘッダー）が必要で、識別ヘッダー無し・`readers`・未知のグループ・片方だけの識別は `403 {"error":"forbidden"}`。`operators` は include / リネーム後のツールを取得できる |
| `run.sh` | 監査ログ: `get_pet`・`getinventory`・`deletepet`（`operators`）が `success`、存在しない `getpetbyid` が `error`、`deletepet`（`readers`）が `denied` で記録される |

go-sdk の挙動をそのまま固定しているものもあります（未知のメソッドや未知のサーバー名の `400`、`Accept` の検査）。SDK の更新で挙動が変わったときに気付くためです。バックエンドの `4xx`（スタブの数値でない pet id への `400` など）は `isError: true` ではなく、応答本文を載せた通常のツール結果として返ります。`argument_*` のステップは現状をそのまま記録しています。
