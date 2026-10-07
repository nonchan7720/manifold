[English](README.md)

# Postman CLI による結合テスト

[Postman CLI](https://learning.postman.com/docs/postman-cli/) のコレクションを、ローカルでビルドしたゲートウェイに対して実行し、ツールの絞り込み（`tools.include` / `overrides`）・ツール認可（OPA）・監査ログをまとめて検証します。CI では PR ごとに実行します（`.github/workflows/postman.yaml`）。

```text
OPA (docker)  <--  manifold gateway  -->  スタブの Petstore API (Go)
                        ^
                  postman collection run
```

| ファイル | 役割 |
| -------- | ---- |
| `run.sh` | ゲートウェイとスタブをビルドし、`examples/opa/policy.rego` で OPA を起動してコレクションを実行し、最後に監査ログを確認する |
| `config.yaml` | ゲートウェイ設定。Petstore を 2 サーバー（`petstore` は絞り込みとリネーム後の 5 ツール、`petstore_small` は 1 ツール）、`authz` 有効、`audit` をファイルへ出力 |
| `opa/data.json` | グループ 2 つ。`readers`（`get_pet`・`findpetsbystatus`）と `operators`（全部） |
| `stub/main.go` | petstore のフィクスチャ spec と数個の operation を提供し、`Authorization` ヘッダーをそのまま返す |
| `manifold.postman_collection.json` | リクエストとアサーション |

## ローカルで実行する

`go`・`docker`・`curl`・Postman CLI が必要です。`go` と Postman CLI は `mise install` で入ります（[`mise.toml`](../../mise.toml) の `postman-cli`）。Postman のアカウントは不要です（ローカルのコレクションファイルは `postman login` なしで実行できます）。

```bash
make postman          # または tests/postman/run.sh
```

ログ・監査ログ・JUnit レポートは `tests/postman/tmp/` に書き出されます。

## コレクションで確認していること

- bearer トークン無し → `401`
- `operators` で `petstore`: `tools/list` は絞り込み後の 5 ツールを公開名で返し（`getpetbyid` ではなく `get_pet`。`addpet` は無い）、`get_pet` を呼ぶと呼び出し元のトークンがスタブまで届き、元の名前 `getpetbyid` は unknown になる
- `readers` で `petstore`: `tools/list` には許可された 2 ツールだけが出る。`deletepet` は拒否される
- 識別ヘッダー無し: `tools/list` は拒否される
- `petstore_small`: 絞り込みで残した 1 ツールだけが一覧に出て、呼び出せる
- 監査ログ: `get_pet` と `getinventory` が `success`、存在しない `getpetbyid` が `error`、`deletepet` が `denied` で記録される
