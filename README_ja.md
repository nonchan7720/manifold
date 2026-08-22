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
- **MCP バックエンド統合**: 外部 MCP サーバーへの透過的なリバースプロキシ
- **OAuth 2.1 サーバー**: PKCE (S256) 対応の認証サーバーを内蔵
- **バックエンド認証方式の選択**: 静的ヘッダー（`authValue`）/ OAuth 2.0（`oauth2`）/ API キーの Token Exchange（`tokenExchange`）から 1 つを選択
- **リソースリンク対応**: ツールのレスポンスに含まれるバイナリ等を S3 へ保存し、ダウンロード URL（リソースリンク）として返却
- **遅延接続**: バックエンドへの接続を初回リクエスト時に確立（ゲートウェイ起動時のバックエンド依存性を排除）
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

`authValue` / `oauth2` / `tokenExchange` は排他で、同時に設定できるのは 1 つだけです。

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

`http` / `grpc` エクスポーターには `addr`（ホスト:ポート）または `url` を指定します。`grpc` は `insecure` も指定できます。`metrics.exporterType: pull` の場合は OTLP push の代わりに `/metrics` エンドポイントで Prometheus 形式のメトリクスを公開します。

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

## HTTP エンドポイント

Manifold が公開する HTTP エンドポイントの一覧です。

### MCP

| メソッド | パス                 | 説明                                     |
| -------- | -------------------- | ---------------------------------------- |
| `POST`   | `/mcp/{server_name}` | MCP リクエスト（Streamable HTTP）        |
| `GET`    | `/mcp/list`          | 登録済みサーバーの一覧（名前と説明）取得 |

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
