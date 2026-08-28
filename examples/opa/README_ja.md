[English](README.md)

# ツール認可の例 — OPA サイドカー

[`openapi-backend`](../openapi-backend/) の Petstore サンプルの手前に [OPA](https://www.openpolicyagent.org/) サイドカーを追加し、`petstore` の `tools/call` / `tools/list` を呼び出し元のグループ単位で認可する。設定の全リファレンスはルート README の [「Tool authorization (OPA sidecar)」](../../README.md#tool-authorization-opa-sidecar) を参照。

## 構成

- `policy.rego` — Manifold が問い合わせる `allow`（単一ツール）、`allowed_tools`（一括）、`allow_catalog`（`GET /mcp/list?tools=true`）のルール
- `data.json` — 4 つのサンプルグループ。それぞれ [ULID 風の不透明な](https://github.com/ulid/spec) グループ ID を `<server>/<tool>` の glob パターン一覧と `catalog` フラグの一方または両方に対応付ける（`catalog` は `tools` とは独立しており、どちらか一方だけ・両方・どちらも無し、いずれの組み合わせも取れる）
- `compose.yaml` — これらのファイルを `-b` バンドルディレクトリとして読み込んで OPA を起動する
- `config.yaml` — `openapi-backend` の `petstore` サーバーに `authz.enabled: true` を追加したもの

| グループ ID | 許可される操作 |
| ----------- | -------------- |
| `01J8X9QZ3KZFN8P8V6H2R5T4WC` | 読み取り専用: `getpetbyid`, `findpetsbystatus`, `getinventory` |
| `01J8X9R14V0S9WQKX9DAT2F7NB` | `petstore` の全ツール（`petstore/*`）— ツール一覧の閲覧権限は無し |
| `01J8X9RM8D3V1CQ0K7P5N2T9YH` | 任意サーバーの `getpetbyid`（`*/getpetbyid`）— サーバー横断パターンの例 |
| `01J8X9T2E7B4M6XR0N8Q3V5KDA` | ツールは一切実行できないが、絞り込みのないツール一覧を読める（`catalog: true`）— ポリシー作成者向けに、実行権限を持たせずにツール一覧だけ見せたい場合の例 |

## 実行

```bash
cd examples/opa
docker compose up -d          # OPA を :8181 で起動

# 一度生成して使い回す — ../README.md を参照
export ENCRYPT_KEY=${ENCRYPT_KEY:-$(openssl rand -base64 32)}
mkdir -p tmp
manifold gateway
```

## 試してみる

`x-user-id` / `x-user-groups` は、前段のプロキシが呼び出し元を認証した後に注入するヘッダーの代わり（ルート README の前提条件を参照 — Manifold はこれをそのまま信頼する）。

`/mcp/{name}` は `Authorization: Bearer <token>` ヘッダーも必須。Manifold のパススルー JWT ミドルウェアは値が存在することだけを確認し、検証せずに上流の API へ転送する。Petstore バックエンドは認証不要なので、ここでは任意の値でよい。

読み取り専用グループが許可されたツールを呼ぶ — 成功:

```bash
curl -s http://localhost:9999/mcp/petstore \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Authorization: Bearer dummy-token' \
  -H 'x-user-id: user-001' \
  -H 'x-user-groups: 01J8X9QZ3KZFN8P8V6H2R5T4WC' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"getpetbyid","arguments":{"petId":1}}}'
```

同じグループが許可されていないツールを呼ぶ — JSON-RPC エラーで拒否:

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

読み取り専用グループの `tools/list` は許可された 3 ツールのみを返す。`x-user-groups` を管理者グループの `01J8X9R14V0S9WQKX9DAT2F7NB` に変えて実行すると、`petstore` の全ツールが返る:

```bash
curl -s http://localhost:9999/mcp/petstore \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Authorization: Bearer dummy-token' \
  -H 'x-user-id: user-001' \
  -H 'x-user-groups: 01J8X9QZ3KZFN8P8V6H2R5T4WC' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/list"}'
```

ポリシーを書くには存在する全ての `<server>/<tool>` の組を把握する必要がある — `GET /mcp/list?tools=true` は絞り込みのないその一覧を返す。`allow` / `allowed_tools` ではなく `allow_catalog` ルールで判定する。これはツールの実行権限とは別の許可であり、下の例ではツールを一切実行できないグループが一覧を読める一方、`petstore` の全ツールを実行できるグループ（`01J8X9R14V0S9WQKX9DAT2F7NB`）は一覧の取得を拒否される:

```bash
curl -s 'http://localhost:9999/mcp/list?tools=true' \
  -H 'x-user-id: user-001' \
  -H 'x-user-groups: 01J8X9T2E7B4M6XR0N8Q3V5KDA'

curl -s -o /dev/null -w '%{http_code}\n' 'http://localhost:9999/mcp/list?tools=true' \
  -H 'x-user-id: user-001' \
  -H 'x-user-groups: 01J8X9R14V0S9WQKX9DAT2F7NB'
# 403
```

`x-user-id` / `x-user-groups` を付けない場合、または `docker compose stop opa` で OPA を止めた場合、どちらもすべての呼び出しを拒否する（fail-closed）。前者では Manifold は OPA に問い合わせすらしない:

```bash
curl -s http://localhost:9999/mcp/petstore \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Authorization: Bearer dummy-token' \
  -d '{"jsonrpc":"2.0","id":4,"method":"tools/list"}'
# {"jsonrpc":"2.0","id":4,"error":{"code":-32603,"message":"tool not allowed by policy"}}
```

`x-authz-bypass: true` を付けるとそのリクエスト 1 件だけ authz を完全に無効化できます（ルート README の [「テナントごとに認可を無効化する」](../../README_ja.md#テナントごとに認可を無効化する) を参照）。読み取り専用グループでも、ポリシーで許可していない `deletepet` を呼び出せるようになります:

```bash
curl -s http://localhost:9999/mcp/petstore \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'Authorization: Bearer dummy-token' \
  -H 'x-user-id: user-001' \
  -H 'x-user-groups: 01J8X9QZ3KZFN8P8V6H2R5T4WC' \
  -H 'x-authz-bypass: true' \
  -d '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"deletepet","arguments":{"petId":1}}}'
```

## 自分のポリシーに置き換える

`policy.rego` / `data.json` を自分のものに置き換える。ルール名 `allow` / `allowed_tools` / `allow_catalog` はそのまま使うか、`authz.decisionPath` で別名を指す。本番ではローカルファイルのマウントではなく、`data.json` / `policy.rego` を OPA の [bundle](https://www.openpolicyagent.org/docs/management-bundles) として HTTP で配布し、すべての `allow` / `allowed_tools` / `allow_catalog` 問い合わせを追跡できるよう OPA の [decision log](https://www.openpolicyagent.org/docs/management-decision-logs) を有効にすることを推奨する。

実サーバーから bundle を配信するようにすると、取得に失敗しても強制は止まらない — OPA は最後に activate した bundle で判定を継続する。すべての判定が拒否になるのは、起動後に一度も bundle を activate できていない場合（`data` が空のまま）だけである。これは Health API の `bundles=true` チェックで検知できる（ルート README の「運用上の推奨事項」参照）。
