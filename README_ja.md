# Manifold

**One interface. Many connections. Manifold.**

[![CI](https://github.com/nonchan7720/manifold/actions/workflows/ci.yaml/badge.svg)](https://github.com/nonchan7720/manifold/actions/workflows/ci.yaml)
[![Release](https://img.shields.io/github/v/release/nonchan7720/manifold)](https://github.com/nonchan7720/manifold/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

[English](README.md) | 日本語

Manifold は MCP サーバーとして振る舞いながら、バックエンドで複数の外部 MCP サーバーや OpenAPI / Swagger 準拠の REST API へ接続するゲートウェイです。

## Why "Manifold"?

**Manifold**（マニフォールド）はエンジンの**吸気マニフォールド**から来ています。

吸気マニフォールドは、エンジンの単一の入口から複数のシリンダーへ、均等かつ効率的に空気と燃料を分配する部品です。このプロジェクトの構造と似ていることから **Manifold** と名付けました。

| エンジンのマニフォールド | このプロジェクト                 |
| ------------------------ | -------------------------------- |
| 単一の入口               | MCP クライアントからのリクエスト |
| 分配・整流               | プロトコル変換・ルーティング     |
| 複数のシリンダーへ       | 複数の外部 MCP / REST API へ     |

## アーキテクチャ

```text
MCP Client
    │
    ▼
┌─────────────┐
│   Manifold  │   ← このサーバー
└─────────────┘
    │       │
    ▼       ▼
External  OpenAPI / Swagger
MCP       REST API Server
Server
```

## 主な機能

- **OpenAPI / Swagger → MCP 自動変換**: OpenAPI 3.x / Swagger 2.x 仕様から MCP ツールを自動生成
- **静的ツールカタログ**: ゲートウェイを起動する前に OpenAPI 仕様から生成される MCP ツールを確認でき（`manifold openapi tools`）、起動時に spec を取得する代わりに、コミットして diff できる生成物ファイルから起動できる（`manifold openapi generate`、`mcpServers.<name>.tools.file`）
- **MCP バックエンド統合**: 外部 MCP サーバーへの透過的なリバースプロキシ
- **OAuth 2.1 サーバー**: PKCE (S256) 対応の認証サーバーを内蔵
- **バックエンド認証方式の選択**: 静的ヘッダー（`authValue`）/ OAuth 2.0（`oauth2`）/ API キーの Token Exchange（`tokenExchange`）から 1 つを選択
- **リソースリンク対応**: ツールのレスポンスに含まれるバイナリ等を S3 へ保存し、ダウンロード URL（リソースリンク）として返却
- **遅延接続（stdio）/ ステートレス接続（http）**: stdio バックエンドは初回リクエスト時に接続を確立（ゲートウェイ起動時のバックエンド依存性を排除）。http バックエンドはリクエストごとに接続を確立し、呼び出し元をまたいでセッションを共有しない
- **ストレージ選択可能**: Redis または SQLite によるセッション・トークン管理
- **OpenTelemetry 対応**: トレース・メトリクス・ログの OTLP エクスポート（メトリクスは Prometheus 形式の pull にも対応）

## 必要要件

- Go 1.26+
- Redis または SQLite（セッション管理用）

## インストール

### バイナリダウンロード

[Releases](https://github.com/nonchan7720/manifold/releases) から最新バイナリをダウンロードしてください。

### ソースからビルド

```bash
git clone https://github.com/nonchan7720/manifold.git
cd manifold
go build -o manifold .
```

### Docker

```bash
docker pull ghcr.io/nonchan7720/manifold:latest
```

## 使い方

### 起動

```bash
# バイナリ実行
manifold gateway

# 設定ファイルを明示指定（-c / --config、拡張子なしの設定名）
manifold gateway -c config

# ソースから実行
go run main.go gateway

# Docker（作業ディレクトリは /home/nonroot）
docker run -p 9999:9999 \
  -v $(pwd)/config.yaml:/home/nonroot/config.yaml \
  ghcr.io/nonchan7720/manifold:latest
```

### Docker Compose（開発環境）

Redis を含む開発環境を一括起動します。

```bash
docker compose up -d
```

すぐに動かせる設定例は [`examples/`](examples/) ディレクトリにあります。

### 生成される MCP ツールの確認と生成

OpenAPI モードのサーバー（`spec` を設定したサーバー）について、`manifold openapi` サブコマンドでゲートウェイが登録するツールを確認でき、起動時に spec を取得しないファイルへ書き出すこともできます。

```bash
# 全 OpenAPI モードサーバーで登録されるツールを表示する（ゲートウェイは起動しない）
manifold openapi tools -c config

# 1 サーバーだけ、inputSchema まで含めて表示する
manifold openapi tools -c config --server petstore --json

# tools.file が設定されている全サーバーの生成物ファイルを書き出す
manifold openapi generate -c config
```

`openapi tools` の出力例:

```text
SERVER    TOOL          OPERATION          DESCRIPTION
petstore  addpet        POST /pet          Add a new pet to the store.
petstore  getpetbyid    GET /pet/{petId}   Find pet by ID.
```

生成物ファイル（`tools.file`）は YAML で、diff しやすい `tools` セクションのあとに解決済みの spec が続きます。

```yaml
version: 1
generatedBy: manifold 1.12.0
source:
  spec: https://petstore3.swagger.io/api/v3/openapi.json
  sha256: "..."
  fetchedAt: "2026-09-04T00:00:00Z"
format: openapi3
tools:
  - name: getpetbyid
    operation: GET /pet/{petId}
    description: Find pet by ID.
    binaryResponse: false
    inputSchema: { ... }
spec: { ... }   # openapi3 ドキュメント（外部 $ref を内部化済み）
```

#### バイナリのフィールドとレスポンス

`multipart/form-data` または `application/x-www-form-urlencoded` のプロパティで `format: binary` のものは、単なる文字列としては公開されない。文字列（base64 の内容、またはファイルを取得する URL）か、入力元を明示するオブジェクト（`url` / `base64` / `text` / `content` のいずれか 1 つと、任意の `filename` / `contentType`）を受け付ける `oneOf` になり、クライアントがファイル入力と判別できるよう `_meta.manifold.file: true` が付く。成功レスポンスがバイナリ（`image/png` や `application/octet-stream` など）の operation は `binaryResponse: true` になり、実行時のレスポンスはバイナリとして扱われ、`storage` を設定していれば resource link として返される（[`storage`](#storage) 参照）。アップロード 1 つとダウンロード 1 つを持つ spec から生成した例:

```yaml
tools:
  - name: uploadfile
    operation: POST /files
    description: Upload a file
    binaryResponse: false
    inputSchema:
      properties:
        file:
          _meta:
            manifold:
              file: true
              fileInputHint: 'Provide the file content as a base64-encoded string, or as a URL (e.g. a presigned URL) to download the file from. For explicit control, an object may be passed instead with one of these keys: {url:"..."} ...'
          description: File to upload
          oneOf:
            - description: Base64-encoded file content, or a URL (e.g. a presigned URL) to download the file from.
              type: string
            - description: Explicit file source; provide exactly one of url/base64/text/content.
              properties:
                base64: { type: string, description: Base64-encoded file content. }
                url: { type: string, description: URL to download the file content from. }
                text: { type: string, description: Raw (non-base64-encoded) text file content. }
                content: { type: string, description: Legacy auto-detected base64 or URL content. }
                filename: { type: string, description: Filename to use for the upload. }
                contentType: { type: string, description: MIME content type to use for the upload. }
              type: object
        label:
          _meta: {}
          description: ""
          type: string
      required:
        - file
      type: object
  - name: downloadfile
    operation: GET /files/{fileId}/content
    description: Download a file
    binaryResponse: true
    inputSchema:
      properties:
        fileId:
          description: ""
          type: string
      required:
        - fileId
      type: object
```

推奨するワークフロー:

1. サーバーの設定に `tools.file` を追加し（[`mcpServers.<name>.tools`](#mcpserversnametools) 参照）、`manifold openapi generate -c config` を実行する
2. 生成物ファイルをコミットする。`tools` セクションにより、上流 spec の変更が通常の PR diff としてレビューできる
3. ゲートウェイを起動する（`manifold gateway -c config`）。起動時にファイルからツールを読み込み、`spec` へのネットワークアクセスは発生しない
4. 上流の spec が変わったら `manifold openapi generate -c config` を再実行してコミットし直す。spec は変わったのにファイルを再生成し忘れると、ゲートウェイの起動が「再生成してください」というエラーで失敗する

## 設定

設定ファイル（`config.yaml`）をカレントディレクトリまたは `config/` サブディレクトリに配置します。
設定値には `${VAR}` または `${VAR:-default}` 形式の環境変数展開が使えます。

### MCP バックエンドへの接続

外部 MCP サーバーを Manifold 経由で公開します。

```yaml
gateway:
  port: 9999
  # openssl rand -base64 32
  encryptKey: ${ENCRYPT_KEY}

mcpServers:
  my-mcp-server:
    description: 外部 MCP サーバー
    transport: http
    url: http://localhost:8080/mcp

sqlite:
  path: ./tmp/manifold.db
```

### OpenAPI / Swagger バックエンドへの接続

OpenAPI 仕様から MCP ツールを自動生成します。

```yaml
gateway:
  port: 9999
  encryptKey: ${ENCRYPT_KEY}

mcpServers:
  my-api:
    description: サンプル REST API
    spec: https://example.com/api/openapi.json
    baseURL: https://example.com
```

### OAuth 2.0 認証付きの OpenAPI バックエンド

```yaml
gateway:
  port: 9999
  encryptKey: ${ENCRYPT_KEY}

mcpServers:
  my-api:
    description: OAuth 認証付き API
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

### 設定リファレンス

#### `gateway`

| フィールド   | 型     | 説明                                                                              |
| ------------ | ------ | --------------------------------------------------------------------------------- |
| `port`       | int    | リスニングポート（デフォルト: 8081）                                              |
| `key`        | string | TLS 秘密鍵ファイルパス（オプション）                                              |
| `cert`       | string | TLS 証明書ファイルパス（オプション）                                              |
| `encryptKey` | string | トークン暗号化キー（**必須**）。base64 エンコードした 32 バイトの AES-256 キー。`openssl rand -base64 32` で生成 |
| `specRefresh.interval` | duration | OpenAPI モードの spec を再取得する間隔（例: `5m`）。未設定または `0` でリフレッシュ無効 |

#### `gateway.specRefresh`

OpenAPI モードのサーバー（`mcpServers.<name>.spec`）の spec を定期的に取り直し、Manifold を再起動せずに MCP ツール定義を更新します。増えたツールは登録され、消えたツールは登録解除され、接続中のクライアントには `notifications/tools/list_changed` が通知されます。

```yaml
gateway:
  specRefresh:
    interval: 5m
```

変更検知は取得した spec 本体のハッシュで行うため、外部 `$ref` 先だけが更新された場合はハッシュが変わらず検知できません。取得やパースに失敗した場合は既存のツール定義を維持し、次の間隔で再試行します。

#### `mcpServers.<name>`

サーバー名（`<name>`）は URL パスに使われるため、英数字・`_`・`-` のみ使用できます。

| フィールド      | 型                | 説明                                                       |
| --------------- | ----------------- | ---------------------------------------------------------- |
| `description`   | string            | サーバーの説明（**必須**。`/mcp/list` のレスポンスに含まれる） |
| `transport`     | string            | MCP バックエンド用トランスポート（`http` または `stdio`）  |
| `url`           | string            | HTTP トランスポートのエンドポイント                        |
| `command`       | string            | stdio トランスポートのコマンド                             |
| `args`          | []string          | stdio コマンドの引数                                       |
| `env`           | map[string]string | stdio プロセスの環境変数                                   |
| `spec`          | string            | OpenAPI/Swagger 仕様ファイルのパスまたは URL               |
| `baseURL`       | string            | OpenAPI モードでの API ベース URL（`spec` 指定時は必須）   |
| `headers`       | map[string]string | API リクエストに追加するヘッダー                           |
| `authValue`     | object            | 静的認証設定（`header`, `prefix`, `value`）                |
| `oauth2`        | object            | OAuth 2.0 設定（下記参照）                                 |
| `tokenExchange` | object            | Token Exchange 設定（下記参照）                            |
| `specRefreshInterval` | duration    | `gateway.specRefresh.interval` のサーバー単位の上書き。`0` でこのサーバーのみリフレッシュ無効 |
| `tools.file`    | string            | 生成物ファイルのパス（[`mcpServers.<name>.tools`](#mcpserversnametools) 参照）。設定すると、ゲートウェイは `spec` を取得せずこのファイルから起動する |

`authValue` / `oauth2` / `tokenExchange` は排他で、同時に設定できるのは 1 つだけです。

#### `mcpServers.<name>.tools`

`tools.file` は `manifold openapi generate` が書き出す生成物ファイルを指す（[生成される MCP ツールの確認と生成](#生成される-mcp-ツールの確認と生成) 参照）。設定すると、ゲートウェイは起動時にも `specRefresh` でも `spec` を取得しない。ツールと（すでに解決済みの）spec をファイルから直接読み込み、ネットワークアクセスは発生しない。

```yaml
mcpServers:
  petstore:
    description: Swagger Petstore
    spec: https://petstore3.swagger.io/api/v3/openapi.json   # tools.file 使用時も必須。生成物に source.spec として記録される
    baseURL: https://petstore3.swagger.io/api/v3
    tools:
      file: ./generated/petstore.yaml
```

- `tools.file` を設定していても `spec` と `baseURL` は引き続き必須。`spec` は生成物に生成元として記録され、`baseURL` は生成物内の spec から導出されない
- 起動時、Manifold はファイルに埋め込まれた spec からツールカタログを再構築し、ファイルの `tools` セクションと突き合わせる。一致しない場合（ファイルが埋め込まれた spec に対して古い、あるいは手で編集された場合）、起動は失敗する。例: `server "petstore": generated tools are stale: tool "addpet" description differs (run "manifold openapi generate")`
- `tools.file` と正の値の `specRefreshInterval` は排他。`tools.file` を持つサーバーは `gateway.specRefresh` の対象からも除外される — 取り直す元の spec が無いため
- `tools.file` はローカルパスのみ指定可能。URL は拒否される
- Phase 1 では OpenAPI 3.x の spec のみ対応。`spec` が Swagger 2.x の場合、`tools.file` は使用できない
- 生成物ファイルには解決済みの spec がそのまま埋め込まれるため、内部ホスト名や spec 中の例示値も含まれる。公開リポジトリへコミットする前に内容を確認すること

#### `mcpServers.<name>.oauth2`

| フィールド     | 型       | 説明                                          |
| -------------- | -------- | --------------------------------------------- |
| `clientID`     | string   | クライアント ID（**必須**）                   |
| `clientSecret` | string   | クライアントシークレット（**必須**）          |
| `authURL`      | string   | Authorization Endpoint（**必須**。絶対 URL）  |
| `tokenURL`     | string   | Token Endpoint（**必須**。絶対 URL）          |
| `scopes`       | []string | リクエストするスコープ                        |

#### `mcpServers.<name>.tokenExchange`

クライアントから受け取った API キーを、指定 URL のトークン交換エンドポイントで OAuth トークンに交換してバックエンドへのリクエストに使用します。交換結果はキャッシュされ、レートリミット（429）にも追従します。

| フィールド | 型     | 説明                                             |
| ---------- | ------ | ------------------------------------------------ |
| `url`      | string | トークン交換エンドポイントの絶対 URL（**必須**） |

#### `redis`

| フィールド     | 型       | 説明                                                  |
| -------------- | -------- | ----------------------------------------------------- |
| `url`          | string   | Redis URL（例: `redis://user:pass@localhost:6379/0`） |
| `addrs`        | []string | ホスト:ポートのリスト（Cluster/Sentinel 用）          |
| `user`         | string   | ユーザー名                                            |
| `password`     | string   | パスワード                                            |
| `db`           | int      | データベース番号                                      |
| `master_name`  | string   | Sentinel マスター名                                   |
| `tls`          | bool     | TLS 有効化                                            |
| `cluster_mode` | bool     | Cluster モード有効化                                  |

#### `sqlite`

| フィールド | 型     | 説明                                                |
| ---------- | ------ | --------------------------------------------------- |
| `path`     | string | データベースファイルパス（`:memory:` でインメモリ） |

`redis` と `sqlite` はどちらか一方の設定が必須です。

#### `storage`

OpenAPI/Swagger ツールのレスポンスに含まれるコンテンツ（画像・バイナリ等）を外部ストレージへ保存し、リソースリンク（ダウンロード URL）として返します。未設定の場合はストレージ保存を行いません。

| フィールド     | 型     | 説明                                                                         |
| -------------- | ------ | ---------------------------------------------------------------------------- |
| `type`         | string | ストレージ種別。現在は `s3` のみ対応                                          |
| `hostURL`      | string | ダウンロード URL のホスト（設定時は Manifold の `/media/download/{id}` で配信） |
| `s3.bucket`    | string | S3 バケット名（`type: s3` の場合必須）                                        |
| `s3.keyPrefix` | string | S3 オブジェクトキーのプレフィックス（`type: s3` の場合必須）                  |

```yaml
storage:
  type: s3
  hostURL: https://manifold.example.com
  s3:
    bucket: my-bucket
    keyPrefix: manifold/media
```

#### `fileFetch`

OpenAPI/Swagger ツールのファイル入力フィールドに URL が渡された場合、Manifold がその URL からファイルをダウンロードします。SSRF 対策として、既定ではプライベート/ループバック/リンクローカル IP への接続と `http://` スキームを拒否します。

| フィールド     | 型       | 説明                                                                                                              |
| -------------- | -------- | ------------------------------------------------------------------------------------------------------------------ |
| `allowLocal`   | bool     | プライベート/ループバック IP への接続と `http://` を許可（ローカルスタック等でのテスト用。デフォルト: `false`）    |
| `allowedHosts` | []string | 許可するホストの許可リスト（ホスト名、または `host:port`）。空なら全ホスト許可（プライベート IP 遮断は別途有効）   |
| `maxSize`      | int64    | ダウンロード/base64/text コンテンツの最大バイト数。0 または未指定でデフォルト 524288000（500MiB）                  |

各フィールドは環境変数でも上書きできます（`FILEFETCH_MAXSIZE`, `FILEFETCH_ALLOWLOCAL`, `FILEFETCH_ALLOWEDHOSTS`）。

```yaml
fileFetch:
  allowLocal: false
  maxSize: 524288000 # 500MiB
  # allowedHosts:
  #   - example.com
  #   - files.example.com:8443
```

#### `telemetry`

OpenTelemetry によるトレース・メトリクス・ログの出力設定です。

| フィールド        | 型     | 説明                                                               |
| ----------------- | ------ | ------------------------------------------------------------------ |
| `serviceName`     | string | サービス名                                                         |
| `environment`     | string | 環境名（`deployment.environment` 属性）                            |
| `gzipCompression` | bool   | OTLP エクスポート時の gzip 圧縮                                    |
| `trace`           | object | トレース設定（`enabled`, `http`, `grpc`）                          |
| `metrics`         | object | メトリクス設定（`enabled`, `exporterType`: `push` / `pull`, `http`, `grpc`） |
| `logs`            | object | ログ設定（`enabled`, `http`, `grpc`）                              |

`http` / `grpc` エクスポーターには `addr`（ホスト:ポート）または `url`、加えて任意の `headers`（追加の HTTP ヘッダーの map）を指定できます。`Authorization` ヘッダーを要求する SaaS の OTLP エンドポイントなどで使います。`grpc` は `insecure` も指定できます。`metrics.exporterType: pull` の場合は OTLP push の代わりに `/metrics` エンドポイントで Prometheus 形式のメトリクスを公開します。

`headers` は入れ子の YAML map の代わりに、JSON オブジェクトを 1 つの環境変数に入れて丸ごと渡すこともできます。トークンなどの値を `config.yaml` に書かずデプロイ時に注入したい場合に使います。

```yaml
telemetry:
  trace:
    http:
      url: ${OTEL_EXPORTER_OTLP_TRACES_ENDPOINT}
      headers: ${OTEL_EXPORTER_OTLP_HEADERS_JSON}
```

```sh
export OTEL_EXPORTER_OTLP_HEADERS_JSON='{"Authorization":"Basic xxxxx"}'
```

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

## ツール認可（OPA サイドカー）

Manifold は `tools/call` / `tools/list` に対して、呼び出し元がどの `server/tool` を使えるかを外部の [OPA](https://www.openpolicyagent.org/) サイドカーへの問い合わせで判定・強制できます。既定は無効（`authz.enabled: false`、既存動作を維持）です。認証・グループ解決・ポリシー保存は Manifold の責務外で、前段が注入するアイデンティティヘッダーをそのまま信頼し、判定だけを OPA に問い合わせます。

```yaml
authz:
  enabled: true
  opaURL: http://localhost:8181
  timeout: 3s
  decisionPath:
    list: /v1/data/mcp/authz/allowed_tools
    call: /v1/data/mcp/authz/allow
    catalog: /v1/data/mcp/authz/allow_catalog
  headers:
    userID: x-user-id
    userGroups: x-user-groups
  input:
    user: user
    groups: groups
    server: server
    tool: tool
    tools: tools
    toolName: name
    fromHeaders:
      tenant:
        header: x-tenant-id
        required: true
```

| フィールド | 型 | 既定値 | 説明 |
| ---------- | -- | ------ | ---- |
| `enabled` | bool | `false` | authz ミドルウェアを有効化します。以下のフィールドは `true` のときのみ参照されます |
| `opaURL` | string | `http://localhost:8181` | OPA サイドカーのベース URL（`http` または `https`） |
| `timeout` | duration | `3s` | 判定 1 回あたりの HTTP タイムアウト |
| `decisionPath.list` | string | `/v1/data/mcp/authz/allowed_tools` | `tools/list` ごとに 1 回問い合わせる OPA のデータパス |
| `decisionPath.call` | string | `/v1/data/mcp/authz/allow` | `tools/call` ごとに問い合わせる OPA のデータパス |
| `decisionPath.catalog` | string | `/v1/data/mcp/authz/allow_catalog` | `GET /mcp/list?tools=true` ごとに問い合わせる OPA のデータパス（後述の「ポリシー作成用のツール一覧」参照） |
| `headers.userID` | string | `x-user-id` | 呼び出し元のユーザー ID を運ぶ受信ヘッダー名 |
| `headers.userGroups` | string | `x-user-groups` | 呼び出し元のグループをカンマ区切りで運ぶ受信ヘッダー名 |
| `headers.bypass` | string | `x-authz-bypass` | 完全一致で文字列 `true` を設定すると、そのリクエスト 1 件の authz 強制を無効化する受信ヘッダー名（後述の「テナントごとに認可を無効化する」参照） |
| `input.user` | string | `user` | 全ての判定 input で呼び出し元のユーザー ID に使う JSON キー |
| `input.groups` | string | `groups` | 全ての判定 input で呼び出し元のグループに使う JSON キー |
| `input.server` | string | `server` | `tools/call` の input、および `tools/list` の各配列要素でサーバー名に使う JSON キー |
| `input.tool` | string | `tool` | `tools/call` の input でツール名に使う JSON キー |
| `input.tools` | string | `tools` | `tools/list` の input でツール配列に使う JSON キー |
| `input.toolName` | string | `name` | `tools/list` の各配列要素でツール名に使う JSON キー |
| `input.fromHeaders` | map[string]object | `{}` | 判定 input のフィールド名 → その値を読み取る受信ヘッダーの定義のマップ。既定は空で、何も追加しない。後述の「マルチテナントのポリシーデータ」参照 |
| `input.fromHeaders.<field>.header` | string | — | そのフィールドの値を運ぶ受信ヘッダー名。必須で、有効な HTTP ヘッダー名であること |
| `input.fromHeaders.<field>.required` | bool | `true` | `true`（既定。キーを省略した場合も同じ）ならヘッダーの欠落・空で拒否する。`false` なら拒否せず、そのフィールドを判定 input に入れない |
| `input.fromHeaders.<field>.type` | string | `string` | ヘッダーの生値を JSON のどの型にするか: `string` / `list` / `number`。空文字は `string` と同義で、それ以外の値は起動時に拒否される |

Manifold は `headers.userID` の値を不透明な文字列として扱います。中身を解釈せず、そのまま判定 input のうち `authz.input.user` が指すキー（既定 `user`）に渡すだけです。マルチテナント環境ではテナントを含む形式（例: `{tenant}:{user}`）にして、ポリシー側がテナントを区別できるようにすることを推奨します。または `input.fromHeaders` を使う方法もあります（後述の「マルチテナントのポリシーデータ」参照）。この場合 `headers.userID` にテナントを埋め込む必要はありません。`headers.userGroups` の値も同様に、表示名ではなく不変の不透明 ID（[ULID](https://github.com/ulid/spec) など）を推奨します。表示名は変わりうるためです。

`input` を使うと、ポリシー側の既存の input 契約に Manifold を合わせられる（ポリシー側を Manifold の既定名に書き換える必要がない）。同じ input オブジェクトに同居するキーは互いに異なる値でなければならない: `user` / `groups` / `server` / `tool`（`tools/call` の input）、`user` / `groups` / `tools`（`tools/list` の input）、`server` / `toolName`（`tools/list` の各配列要素）。いずれかの組で衝突すると起動時のバリデーションで拒否される。各キーは空文字も不可。`input.fromHeaders` のフィールド名も同様に空文字不可で、上記のトップレベルのキー（改名されていれば改名後の `user` / `groups` / `server` / `tool` / `tools`）と衝突してはならない。OPA に渡る JSON のキーは case-sensitive なので比較も case-sensitive で、既定のままなら `User` という名前のフィールドは `input.user` とは別キーなので通る。`toolName` は予約されない: `tools` 配列の要素内のキーであってトップレベルには出ないため。同じヘッダーを複数のフィールドに割り当てることは可能。

### 前提条件

Manifold は `headers.userID` / `headers.userGroups`（設定していれば `headers.bypass` および `input.fromHeaders` に列挙した全ヘッダーも含む）を検証せずそのまま信頼します — WebMCP reverse gateway の `forwardAuth` モードと同じ注意点です（`docs/design/webmcp-reverse-gateway.md` の Trust boundary 節を参照）。`authz.enabled` を有効にする前に以下を満たしてください。

- 前段のプロキシが、クライアント由来の同名ヘッダーを必ず strip または上書きし、呼び出し元が自分のアイデンティティを偽装できないようにする
- そのプロキシを経由しない Manifold への直接アクセスを、ネットワーク層（Kubernetes の `NetworkPolicy` 等）で遮断する
- **`headers.bypass` は識別用ヘッダーよりもさらに機微です**: 呼び出し元がこれを `true` に設定できると、アイデンティティやグループ所属に関係なく自分のリクエストの認可を完全に無効化できます。前段のプロキシは同じ厳格さで strip または上書きし、そのプロキシを経由せず Manifold に到達しうるネットワーク経路はすべて（別レイヤーでの認証で済ませるのではなく）ネットワーク層で完全に遮断してください

### input / data の契約

Manifold は `tools/call` ごとに `opaURL + decisionPath.call` へ、`tools/list` ごとに `opaURL + decisionPath.list` へ（ツールごとではなく一括で）、`GET /mcp/list?tools=true` ごとに `opaURL + decisionPath.catalog` へ `{"input": ...}` を POST します。以下の例は既定の `authz.input` キー名を使っている。各キーは変更可能（前述の `input` の表を参照）。

```jsonc
// tools/call
{"input": {"user": "user-042", "groups": ["team-finance"], "server": "billing-svc", "tool": "create_invoice"}}
// → {"result": true}

// tools/list
{"input": {"user": "user-042", "groups": ["team-finance"], "tools": [{"server": "billing-svc", "name": "create_invoice"}, ...]}}
// → {"result": [{"server": "billing-svc", "name": "create_invoice"}, ...]}

// GET /mcp/list?tools=true
{"input": {"user": "user-042", "groups": ["team-finance"]}}
// → {"result": true}
```

OPA 側の `data` の形は Manifold が規定しません。ポリシー側で自由に構成できます。[`examples/opa/`](examples/opa/) に動作する `policy.rego` と `data.json`（`data.policies[<group id>].tools` を `<server>/<tool>` の glob パターン一覧、`data.policies[<group id>].catalog` を真偽値とする例）があります。

### マルチテナントのポリシーデータ

`input.fromHeaders` は判定 input のフィールド名を受信ヘッダーにマッピングする。前段のアイデンティティ層がすでに知っている値（テナント ID・リージョンなど）を、`headers.userID` にエンコードすることなくポリシーへ渡せる。設定した各フィールドは全ての判定種別（`tools/call`、`tools/list`、`GET /mcp/list?tools=true`）で解決され、`user` / `groups` などと並ぶトップレベルのフィールドとして追加される。

```yaml
authz:
  input:
    fromHeaders:
      tenant:
        header: x-tenant-id
        required: true
      roles:
        header: x-roles
        required: false
        type: list
      seat_count:
        header: x-seat-count
        type: number
```

```jsonc
// tools/call
{"input": {"user": "user-042", "groups": ["team-finance"], "server": "billing-svc", "tool": "create_invoice", "tenant": "acme", "roles": ["admin", "auditor"], "seat_count": 42}}
```

`type` はヘッダーの生値を JSON のどの型にするかを決める。

| `type` | 判定 input の値 | 備考 |
| ------ | --------------- | ---- |
| `string`（既定） | ヘッダーの生値そのまま | |
| `list` | 文字列の配列 | `,` で分割し各要素を trim、空要素は除去する（`headers.userGroups` と同じ規則） |
| `number` | JSON の数値 | 生値の桁をそのまま送り、丸めない。数値として解釈できない値は `required` の真偽にかかわらず拒否する |

`required` の既定は `true`。キーを省略すればアイデンティティヘッダーと同じ fail-closed のままになる。`required: false` を指定した場合、ヘッダーの欠落・空（`list` で非空要素が 1 つも無い場合を含む）では空の値を送るのではなく、そのフィールドを**判定 input から丸ごと省く**。ポリシー側では存在チェックを入れること。

```rego
# x-roles ヘッダーが無かったリクエストでは input.roles 自体が存在しないため、
# 直接参照せず既定値付きで読む。
roles := object.get(input, "roles", [])
```

この `tenant` フィールドがあれば、`data` をフラットではなくテナント単位で構成できる。`user` に規約を埋め込まなくても 1 つの bundle で全テナントに対応できる。

```rego
package mcp.authz

default allow := false

allow if {
	tenant_policies := data.tenants[input.tenant].policies
	some group in input.groups
	some pattern in tenant_policies[group].tools
	glob.match(pattern, ["/"], sprintf("%s/%s", [input.server, input.tool]))
}
```

これは前述の `headers.userID` に対する `{tenant}:{user}` 形式の規約の代替になる。`input.fromHeaders` がテナントを明示的に解決するため、`headers.userID` はそのテナント内でユーザーを識別できれば十分になる。

#### テナントごとのデータ配布

Manifold が知っているのは `opaURL` と `decisionPath.*` だけで、policy と `data` をサイドカーへ届ける経路は OPA 側の責務になる（bundle を HTTP で配る方法は後述の「運用上の推奨事項」を参照）。`data` をテナントで引く構成にしたら、どこまで細かく分割するかを選べる。

```mermaid
flowchart LR
    M[Manifold] -->|"POST /v1/data/mcp/authz/allow<br/>input.tenant = acme"| O[OPA sidecar]
    O -.->|poll| B[(bundle サービス)]
    B -.->|"mcp-authz/policy.tar.gz<br/>roots: mcp/authz"| O
    B -.->|"tenants/acme/bundle.tar.gz<br/>roots: tenants/acme"| O
    B -.->|"tenants/globex/bundle.tar.gz<br/>roots: tenants/globex"| O
```

1 つの OPA に複数の bundle を読み込ませ、それぞれが `data` の重ならない部分木を所有する形にできる。あるテナントのポリシーデータを、他のテナントと独立に配布・ロールバックできるようになる。OPA 側の設定は次のようになる。

```yaml
services:
  bundles:
    url: https://bundles.example.com
bundles:
  policy:
    service: bundles
    resource: mcp-authz/policy.tar.gz
  tenant-acme:
    service: bundles
    resource: tenants/acme/bundle.tar.gz
  tenant-globex:
    service: bundles
    resource: tenants/globex/bundle.tar.gz
```

各 bundle の `.manifest` で所有する部分木を宣言する。前述の Rego は `data.tenants[input.tenant]` のまま変更不要。

```jsonc
// mcp-authz/policy.tar.gz
{"revision": "2026-08-29-01", "roots": ["mcp/authz"]}
// tenants/acme/bundle.tar.gz
{"revision": "2026-08-29-01", "roots": ["tenants/acme"]}
```

OPA が bundle をマージする仕組み上、制約が 3 つある。

- **roots は重複できない。** 別の bundle の root と衝突する bundle（`["tenants"]` と `["tenants/acme"]` の同居など）は OPA が activate を拒否する。分割するなら全テナントを分割する必要があり、共通のデータをテナント固有データと同じ部分木に置くこともできない
- **分割は分離ではない。** どの bundle も最終的には 1 つの OPA プロセスの 1 つの `data` ツリーに載るため、`data.tenants.globex` を読むポリシーは読める。テナント境界を担保するのは `input.tenant` で引くポリシーの書き方であり、bundle の境界は更新の粒度と障害の影響範囲を区切るだけ
- **テナント追加は OPA の設定変更になる。** `bundles:` は静的な設定なので、テナントが増えるたびにサイドカーの再設定が要る。OPA の [discovery](https://www.openpolicyagent.org/docs/management-discovery) で bundle 一覧そのものを配ることもできるが、可動部品が 1 つ増える。また bundle ごとに独立して poll するため、テナント数が非常に多い場合はこの方法では素直にスケールしない

もう 1 つの選択肢は、サイドカーを共有しないこと。テナントごとに Manifold + OPA のペアを立てれば、サイドカー自体がテナントを表すので `data` にテナント階層は不要になり、`input.fromHeaders` で解決すべきものも無くなる。

| 構成 | `input.fromHeaders` での `tenant` |
| ---- | --------------------------------- |
| 1 つの Manifold + OPA が複数テナントを捌く | 必要 — テナントを区別できるのは判定 input だけ |
| テナントごとに Manifold + OPA のペアを立てる | 不要 — サイドカーが暗黙にテナントを識別する |

### ポリシー作成用のツール一覧

ポリシーを書くには存在する全ての `<server>/<tool>` の組を把握する必要がありますが、`tools/list` は呼び出し元がすでに使える範囲しか返しません。`GET /mcp/list?tools=true` は絞り込みのない一覧を返します。`authz.enabled` が `false` なら誰でも取得できる。`true` なら `tools/call` が `decisionPath.call` に問い合わせるのと同様に `decisionPath.catalog` に問い合わせる — `headers.userID` / `headers.userGroups` で識別し、アイデンティティの欠落・ポリシーでの拒否・Decider のエラーのいずれでも（静的な許可リストにフォールバックすることなく）`403 {"error": "forbidden"}` を返す。

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
    // WebMCP reverse サーバーのツールはブラウザ接続後にしか決まらないため、
    // ツール一覧の代わりに "dynamic" を返す
    {"name": "billing-svc", "description": "browser app", "dynamic": true},
    // 接続に失敗したバックエンドも "tools" の代わりに "error" を付けて一覧には残る
    {"name": "crm", "description": "CRM MCP backend", "error": "connect: dial tcp: connection refused"}
  ]
}
```

### テナントごとに認可を無効化する

1 台の Manifold の手前で複数テナントを振り分ける前段プロキシは、`authz.enabled` をグローバルに切り替えなくても、`headers.bypass`（既定 `x-authz-bypass`）を完全一致で文字列 `true` に設定することで、リクエスト 1 件単位で authz を無効化できます。それ以外の値（`True`、`1`、空、ヘッダー自体が無い場合を含む）は通常の authz 判定（fail-closed）に従います。

無効化された場合、そのリクエストでは:

- `tools/call` は OPA に問い合わせず、ツールに直接到達する
- `tools/list` は絞り込みなしでバックエンドの全ツール一覧を返す
- `GET /mcp/list?tools=true` は `decisionPath.catalog` に問い合わせず `200` で全カタログを返す

これはそのリクエスト 1 件に限り `authz.enabled: false` と同じ挙動になります。Manifold は `decision: bypass` をログに出します（`server` / `method` は出ますが、アイデンティティは解決していないため出ません）。これにより監査ログ上で `allow` / `deny` と区別できます。

### fail-closed の挙動

判定が曖昧・失敗するケースはすべて許可ではなく拒否になります。

- `headers.userID` / `headers.userGroups` が欠落・空の場合、OPA に問い合わせず拒否
- `input.fromHeaders` に設定した**必須**フィールドのヘッダーが欠落・空の場合も同様に、OPA に問い合わせず拒否。`required` の既定は `true` で、`required: false` のフィールドは拒否ではなく input からの省略になる
- `input.fromHeaders` の値が設定した `type` として解釈できない場合（`type: number` に数値でないヘッダーが来た等）も、`required` の真偽にかかわらず OPA に問い合わせず拒否
- OPA からの非 200 応答、期待する `result` フィールドが無い応答、タイムアウト、接続失敗はすべて拒否
- `tools/list` での絞り込みは補助的なもの — 呼び出し元が使えないツールをクライアントのツール一覧から隠すためのものであり、強制の本体ではありません。強制は `tools/call` で行われるため、（古い一覧などから）ツール名を知っているクライアントでもそこで拒否されます
- reverse（WebMCP）の `mcpServers` エントリは常に `create_pairing_code` というツールを登録します（`docs/design/webmcp-reverse-gateway.md` を参照）。`authz.enabled` は他のツールと同様にこれも対象にするため、そのサーバーとペアリングできるべきグループには `<server>/create_pairing_code` をポリシーに含める必要があります。含めなければペアリング自体が拒否されます
- これは OPA 自身の内部でも一段成り立ちます: bundle の取得に失敗しても、OPA は最後に activate 済みの bundle で判定を継続します — bundle サーバーの障害が止めるのはポリシーの更新であって、判定そのものではありません。ただし起動後に一度も bundle を activate できていない場合（起動時に bundle サーバーへ到達できなかった等）は `data` が空のままなので、すべての判定が `false` / `[]` になり、同じく fail-closed になります。bundle の取得失敗はそれでも監視・アラートの対象にする価値があります — 後述の「運用上の推奨事項」参照

### 運用上の推奨事項

- すべての `allow` / `allowed_tools` / `allow_catalog` 問い合わせを追跡できるよう、OPA の [decision log](https://www.openpolicyagent.org/docs/management-decision-logs) を有効にする。各イベントには判定結果と、その判定の input と同じフィールド、判定に使ったポリシーデータのリビジョンを含めること — リビジョンが無いと、ある判定がどのポリシー版で下されたのか追跡できない。input のフィールドは判定種別ごとに異なる（前述の「input / data の契約」参照）。以下は `authz.input` の既定名で、それぞれ変更可能:

  | 判定 | 問い合わせ | input のフィールド |
  | ---- | ---------- | ------------------- |
  | `allow` | `tools/call` | `user`, `groups`, `server`, `tool` |
  | `allowed_tools` | `tools/list` | `user`, `groups`、および `{server, name}` の配列 `tools` |
  | `allow_catalog` | `GET /mcp/list?tools=true` | `user`, `groups` |

  解決できた `input.fromHeaders` のフィールドは、上記 3 種すべてにトップレベルで入る。`required: false` のフィールドはヘッダーが無かったリクエストの input には現れないので、decision log に出ていないのは想定どおりであってフィールドの取りこぼしではない。

- ローカルファイルのマウントではなく、ポリシーとデータを OPA の [bundle](https://www.openpolicyagent.org/docs/management-bundles) として HTTP 配布し、ポリシー更新のたびにサイドカーを再起動しなくて済むようにする。bundle 運用にすると decision log の各イベントに `bundles.<name>.revision` が入るようになり、そのリビジョンはここから得られる
- OPA の bundle 取得状況を監視する（取得失敗時の判定への影響は前述の「fail-closed の挙動」参照）: OPA の Health API（`GET /health?bundles=true`）は、設定した全 bundle が少なくとも一度 activate されるまで unhealthy を返すため、readiness probe としても使える。status API や decision log からも取得失敗を検知できる

動作する OPA サイドカーとサンプルのポリシー・データは [`examples/opa/`](examples/opa/) を参照してください。

## HTTP エンドポイント

Manifold が公開する HTTP エンドポイントの一覧です。

### MCP

| メソッド | パス                 | 説明                                     |
| -------- | -------------------- | ---------------------------------------- |
| `POST`   | `/mcp/{server_name}` | MCP リクエスト（Streamable HTTP）        |
| `GET`    | `/mcp/list`          | 登録済みサーバーの一覧（名前と説明）取得。`?tools=true` でツール一覧も取得（前述の「ポリシー作成用のツール一覧」参照） |

### OAuth 2.1

| メソッド | パス                                                        | 説明                            |
| -------- | ----------------------------------------------------------- | ------------------------------- |
| `GET`    | `/.well-known/oauth-authorization-server/mcp/{server_name}` | Authorization Server メタデータ |
| `GET`    | `/.well-known/oauth-protected-resource/mcp/{server_name}`   | Protected Resource メタデータ   |
| `GET`    | `/{server_name}/auth/login`                                 | ログインページへリダイレクト    |
| `GET`    | `/{server_name}/auth/callback`                              | OAuth コールバック              |
| `POST`   | `/{server_name}/auth/token`                                 | トークン発行                    |
| `POST`   | `/{server_name}/auth/clients`                               | クライアント動的登録 (RFC 7591) |
| `GET`    | `/authorize`, `/callback`                                   | サーバー名なしのエイリアス      |
| `POST`   | `/token`, `/register`                                       | サーバー名なしのエイリアス      |

### その他

| メソッド | パス                   | 説明                                                          |
| -------- | ---------------------- | ------------------------------------------------------------- |
| `GET`    | `/media/download/{id}` | ストレージ保存コンテンツのダウンロード（`storage.hostURL` 設定時のみ） |
| `GET`    | `/metrics`             | Prometheus メトリクス（`telemetry.metrics.exporterType: pull` 時のみ） |

## 開発

開発環境のセットアップと変更の提出方法は [CONTRIBUTING.md](CONTRIBUTING.md) を参照してください。

### テスト

```bash
make test
```

### Lint

```bash
make lint
```

## インスピレーション

このプロジェクトは [LiteLLM](https://github.com/BerriAI/litellm) の **Agent / MCP Gateway** からインスピレーションを受けています。

LiteLLM の MCP Gateway が複数の MCP サーバーへの統一アクセスポイントを提供するように、Manifold も単一の MCP インターフェースから多数の MCP サーバー / REST API へ接続できるゲートウェイを目指しています。

## ライセンス

MIT License
