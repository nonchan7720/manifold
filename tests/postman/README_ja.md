[English](README.md)

# Postman CLI による結合テスト

[Postman CLI](https://learning.postman.com/docs/postman-cli/) のコレクションを、ローカルでビルドしたゲートウェイに対して実行し、ツール検索（`gateway.toolSearch`）・ツールの絞り込み（`tools.include` / `overrides`）・ツール認可（OPA）をまとめて検証します。CI では PR ごとに実行します（`.github/workflows/postman.yaml`）。

```text
OPA (docker)  <--  manifold gateway  -->  スタブの Petstore API (Go)
                        ^
                  postman collection run
```

| ファイル | 役割 |
| -------- | ---- |
| `run.sh` | ゲートウェイとスタブをビルドし、`examples/opa/policy.rego` で OPA を起動してコレクションを実行し、最後に監査ログを確認する |
| `config.yaml` | ゲートウェイ設定。`toolSearch.threshold: 1`、Petstore を 2 サーバー（`petstore` は絞り込みとリネーム後の 5 ツール、`petstore_small` は 1 ツール）、`authz` 有効、`audit` をファイルへ出力 |
| `opa/data.json` | グループ 2 つ。`readers`（`get_pet`・`findpetsbystatus`）と `operators`（全部） |
| `stub/main.go` | petstore のフィクスチャ spec と数個の operation を提供し、`Authorization` ヘッダーをそのまま返す |
| `manifold-tool-search.postman_collection.json` | リクエストとアサーション |

## ローカルで実行する

`go`・`docker`・`curl`・Postman CLI が必要です。Postman のアカウントは不要です（ローカルのコレクションファイルは `postman login` なしで実行できます）。

```bash
make postman          # または tests/postman/run.sh
```

ログ・監査ログ・JUnit レポートは `tests/postman/tmp/` に書き出されます。

## コレクションで確認していること

- bearer トークン無し → `401`
- `operators` で `petstore`: `tools/list` は `tool_search` 1 件だけになり、説明文には見える 5 ツールが公開名で載る（`getpetbyid` ではなく `get_pet`。`addpet` は無い）。`tool_search` は `regexp`・`bm25`・`fuzzy` で動き、不正な正規表現は tool error になり、隠れた `get_pet` は直接呼べて呼び出し元のトークンがスタブまで届き、元の名前 `getpetbyid` は unknown になる
- `readers` で `petstore`: `tool_search` の説明文と結果には許可された 2 ツールだけが出る。`deletepet` は拒否される
- 識別ヘッダー無し: `tools/list` も `tool_search` も拒否される
- `petstore_small`: 見えるツールが 1 つなので閾値を超えず、そのまま一覧に出て呼べる
- 監査ログ: `tool_search` と `get_pet` が `success`、`deletepet` と識別ヘッダー無しの `tool_search` が `denied` で記録される
