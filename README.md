# Manifold

**One interface. Many connections. Manifold.**

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

```
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
- **遅延接続**: バックエンドへの接続を初回リクエスト時に確立（ゲートウェイ起動時のバックエンド依存性を排除）
- **ストレージ選択可能**: Redis または SQLite によるセッション・トークン管理

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

# ソースから実行
go run main.go gateway

# Docker
docker run -p 9999:9999 \
  -v $(pwd)/config.yaml:/etc/manifold/config.yaml \
  ghcr.io/nonchan7720/manifold:latest
```

### Docker Compose（開発環境）

Redis を含む開発環境を一括起動します。

```bash
docker compose up -d
```

## 設定

設定ファイル（`config.yaml`）をカレントディレクトリまたは `config/` サブディレクトリに配置します。
設定値には `${VAR}` または `${VAR:-default}` 形式の環境変数展開が使えます。

### MCP バックエンドへの接続

外部 MCP サーバーを Manifold 経由で公開します。

```yaml
gateway:
  port: 9999

mcpServers:
  my-mcp-server:
    transport: http
    url: http://localhost:8080/mcp
```

### OpenAPI / Swagger バックエンドへの接続

OpenAPI 仕様から MCP ツールを自動生成します。

```yaml
gateway:
  port: 9999

mcpServers:
  my-api:
    spec: https://example.com/api/openapi.json
    baseURL: https://example.com
```

### OAuth 2.1 認証付きの OpenAPI バックエンド

```yaml
gateway:
  port: 9999

mcpServers:
  my-api:
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

| フィールド | 型     | 説明                                 |
| ---------- | ------ | ------------------------------------ |
| `port`     | int    | リスニングポート（デフォルト: 8081） |
| `key`      | string | TLS 秘密鍵ファイルパス（オプション） |
| `cert`     | string | TLS 証明書ファイルパス（オプション） |

#### `mcpServers.<name>`

| フィールド     | 型                | 説明                                                      |
| -------------- | ----------------- | --------------------------------------------------------- |
| `transport`    | string            | MCP バックエンド用トランスポート（`http` または `stdio`） |
| `url`          | string            | HTTP トランスポートのエンドポイント                       |
| `command`      | string            | stdio トランスポートのコマンド                            |
| `args`         | []string          | stdio コマンドの引数                                      |
| `env`          | map[string]string | stdio プロセスの環境変数                                  |
| `spec`         | string            | OpenAPI/Swagger 仕様ファイルのパスまたは URL              |
| `baseURL`      | string            | OpenAPI モードでの API ベース URL                         |
| `extraHeaders` | map[string]string | API リクエストに追加するヘッダー                          |
| `authValue`    | object            | 簡易認証設定（`header`, `prefix`, `value`）               |
| `oauth2`       | object            | OAuth 2.1 設定（下記参照）                                |

#### `mcpServers.<name>.oauth2`

| フィールド     | 型       | 説明                     |
| -------------- | -------- | ------------------------ |
| `clientID`     | string   | クライアント ID          |
| `clientSecret` | string   | クライアントシークレット |
| `authURL`      | string   | Authorization Endpoint   |
| `tokenURL`     | string   | Token Endpoint           |
| `scopes`       | []string | リクエストするスコープ   |

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

## HTTP エンドポイント

Manifold が公開する HTTP エンドポイントの一覧です。

### MCP

| メソッド | パス                 | 説明                       |
| -------- | -------------------- | -------------------------- |
| `POST`   | `/mcp/{server_name}` | MCP リクエスト（JSON-RPC） |

### OAuth 2.1

| メソッド | パス                                                        | 説明                            |
| -------- | ----------------------------------------------------------- | ------------------------------- |
| `GET`    | `/.well-known/oauth-authorization-server/mcp/{server_name}` | Authorization Server メタデータ |
| `GET`    | `/.well-known/oauth-protected-resource/mcp/{server_name}`   | Protected Resource メタデータ   |
| `GET`    | `/{server_name}/auth/login`                                 | ログインページへリダイレクト    |
| `GET`    | `/{server_name}/auth/callback`                              | OAuth コールバック              |
| `POST`   | `/{server_name}/auth/token`                                 | トークン発行                    |
| `POST`   | `/{server_name}/auth/clients`                               | クライアント動的登録 (RFC 7591) |

## 開発

### テスト

```bash
make test
```

### Lint

```bash
make lint
```

## インスピレーション

このプロジェクトは [LiteLLM](https://github.com/BerriAI/litellm) からインスピレーションを受けています。

LiteLLM が「多数の LLM プロバイダーへの統一インターフェース」を提供するように、Manifold は「多数の MCP サーバー / REST API へのゲートウェイ」を単一の MCP インターフェースで提供することを目指しています。

## ライセンス

MIT License
