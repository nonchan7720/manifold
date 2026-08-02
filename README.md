# Manifold

**One interface. Many connections. Manifold.**

[![CI](https://github.com/nonchan7720/manifold/actions/workflows/ci.yaml/badge.svg)](https://github.com/nonchan7720/manifold/actions/workflows/ci.yaml)
[![Release](https://img.shields.io/github/v/release/nonchan7720/manifold)](https://github.com/nonchan7720/manifold/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/nonchan7720/manifold)](https://goreportcard.com/report/github.com/nonchan7720/manifold)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

English | [日本語](README.ja.md)

Manifold is a gateway that acts as an MCP server while connecting to multiple external MCP servers and OpenAPI / Swagger-compliant REST APIs on the backend.

## Why "Manifold"?

The name **Manifold** comes from an engine's **intake manifold**.

An intake manifold is the component that distributes air and fuel evenly and efficiently from a single inlet to multiple cylinders. We named this project **Manifold** because its structure is similar.

| Engine manifold        | This project                         |
| ---------------------- | ------------------------------------ |
| Single inlet           | Requests from MCP clients            |
| Distribution / routing | Protocol conversion / routing        |
| To multiple cylinders  | To multiple external MCP / REST APIs |

## Architecture

```text
MCP Client
    │
    ▼
┌─────────────┐
│   Manifold  │   ← this server
└─────────────┘
    │       │
    ▼       ▼
External  OpenAPI / Swagger
MCP       REST API Server
Server
```

## Features

- **OpenAPI / Swagger → MCP conversion**: Automatically generates MCP tools from OpenAPI 3.x / Swagger 2.x specifications
- **MCP backend aggregation**: Transparent reverse proxy to external MCP servers
- **Built-in OAuth 2.1 server**: Authorization server with PKCE (S256) support
- **Pluggable backend authentication**: Choose one of static header (`authValue`) / OAuth 2.0 (`oauth2`) / API key Token Exchange (`tokenExchange`)
- **Resource links**: Stores binary content from tool responses in S3 and returns download URLs (resource links)
- **Lazy connection**: Connects to backends on first request (no backend dependency at gateway startup)
- **Selectable storage**: Session / token management backed by Redis or SQLite
- **OpenTelemetry support**: OTLP export of traces, metrics, and logs (metrics also support Prometheus-style pull)

## Requirements

- Go 1.26+
- Redis or SQLite (for session management)

## Installation

### Download binary

Download the latest binary from [Releases](https://github.com/nonchan7720/manifold/releases).

### Build from source

```bash
git clone https://github.com/nonchan7720/manifold.git
cd manifold
go build -o manifold .
```

### Docker

```bash
docker pull ghcr.io/nonchan7720/manifold:latest
```

## Usage

### Start the gateway

```bash
# Run the binary
manifold gateway

# Specify a config file explicitly (-c / --config, config name without extension)
manifold gateway -c config

# Run from source
go run main.go gateway

# Docker (working directory is /home/nonroot)
docker run -p 9999:9999 \
  -v $(pwd)/config.yaml:/home/nonroot/config.yaml \
  ghcr.io/nonchan7720/manifold:latest
```

### Docker Compose (development)

Starts a development environment including Redis.

```bash
docker compose up -d
```

Ready-to-run configuration examples are available in the [`examples/`](examples/) directory.

## Configuration

Place a configuration file (`config.yaml`) in the current directory or in a `config/` subdirectory.
Configuration values support environment variable expansion in the form `${VAR}` or `${VAR:-default}`.

### Connecting to an MCP backend

Expose an external MCP server through Manifold.

```yaml
gateway:
  port: 9999
  # openssl rand -base64 32
  encryptKey: ${ENCRYPT_KEY}

mcpServers:
  my-mcp-server:
    description: External MCP server
    transport: http
    url: http://localhost:8080/mcp

sqlite:
  path: ./tmp/manifold.db
```

### Connecting to an OpenAPI / Swagger backend

Automatically generate MCP tools from an OpenAPI specification.

```yaml
gateway:
  port: 9999
  encryptKey: ${ENCRYPT_KEY}

mcpServers:
  my-api:
    description: Sample REST API
    spec: https://example.com/api/openapi.json
    baseURL: https://example.com
```

### OpenAPI backend with OAuth 2.0 authentication

```yaml
gateway:
  port: 9999
  encryptKey: ${ENCRYPT_KEY}

mcpServers:
  my-api:
    description: OAuth-protected API
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

### Configuration reference

#### `gateway`

| フィールド   | 型     | 説明                                                                                                             |
| ------------ | ------ | ---------------------------------------------------------------------------------------------------------------- |
| `port`       | int    | リスニングポート（デフォルト: 8081）                                                                             |
| `key`        | string | TLS 秘密鍵ファイルパス（オプション）                                                                             |
| `cert`       | string | TLS 証明書ファイルパス（オプション）                                                                             |
| `encryptKey` | string | トークン暗号化キー（**必須**）。base64 エンコードした 32 バイトの AES-256 キー。`openssl rand -base64 32` で生成 |
| `toolSearch` | object | ツール検索フォールバックの設定（下記参照）                                                                       |

#### `gateway.toolSearch`

接続先の MCP サーバー / OpenAPI から生成されるツールの合計数が多い場合（例: OpenAPI スペック由来で数百ツール）、クライアントの `tools/list` 応答が肥大化して LLM のコンテキストを圧迫します。この機能が有効なとき、**全 `mcpServers` 合計のツール数が `threshold` を超えると**、各エンドポイント（`/mcp/{server_name}`）の `tools/list` は合成ツール `tool_search` 1 件のみを返すようになります。クライアントは `tool_search` を呼び出して一致するツールの完全な定義（`name` / `description` / `inputSchema`）を JSON で受け取り、そのまま `tools/call` で実ツールを直接呼び出せます（実ツールは常に内部登録済みのため、`tools/list` から隠れている間も `tools/call` は成功します）。

| フィールド       | 型     | 説明                                                                                                                                                                                                                                                                               |
| ---------------- | ------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `threshold`      | int    | 全 `mcpServers` 合計のツール数がこの値を超えると `tool_search` に切り替わる。0 または未指定でデフォルト 100                                                                                                                                                                        |
| `defaultLimit`   | int    | `tool_search` 呼び出し時に `limit` 未指定の場合に返す検索結果件数。0 または未指定でデフォルト 10                                                                                                                                                                                   |
| `resultFormat`   | string | `tool_search` の検索結果の出力フォーマット。`default` または `claude`。空または未指定でデフォルト `default`（下記参照）                                                                                                                                                            |
| `digestMaxTools` | int    | `tool_search` の `description` ダイジェストに列挙するツール数の上限。**-1**（未指定時のデフォルト）で全ツールを列挙。**0 は設定エラー**（バリデーションで拒否）。正数 N を指定すると先頭 N 件（名前順）のみを列挙し、実際のツール数が N 以下ならそのまま全件を表示する（下記参照） |

`tool_search` は呼び出し時に検索アルゴリズムを `method` パラメータで選べます（デフォルトは `bm25`）。いずれのアルゴリズムも、検索対象は**ツール名・ツールの説明・引数名（inputSchema の properties のキー）・引数の説明**です（ネストしたオブジェクトや配列の要素内の引数も再帰的に対象になります）。これは Claude API の Tool Search Tool が定める "tool names, descriptions, argument names, and argument descriptions" という検索対象の規約と同じです。

| `method` | 説明                                                                                                                                                                |
| -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `bm25`   | （デフォルト）BM25 スコアリングによるランク付き全文検索。CJK（漢字/ひらがな/カタカナ/ハングル）はバイグラムでトークン化されるため、日本語などの説明文でも検索できる |
| `regexp` | 大文字小文字を区別しない正規表現による一致検索                                                                                                                      |
| `fuzzy`  | 曖昧一致（サブシーケンス）検索                                                                                                                                      |

`resultFormat` は検索結果 JSON の形を選びます:

| `resultFormat` | 説明                                                                                                                                                                                                                                                                                                                                        |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `default`      | （デフォルト）従来どおり、一致したツールの完全な定義（`name` / `description` / `inputSchema`）の配列を返す                                                                                                                                                                                                                                  |
| `claude`       | [Claude API の Tool Search Tool のカスタム検索実装規約](https://platform.claude.com/docs/en/agents-and-tools/tool-use/tool-search-tool#custom-tool-search-implementation) に準拠した `tool_reference` ブロック（`{"type": "tool_reference", "tool_name": "..."}`）の配列を返す。Claude API 側がこのブロックを完全なツール定義に自動展開する |

いずれのフォーマットでもヒット 0 件時は `null` ではなく空配列 `[]` を返します。

注意点:

- MCP バックエンドは認証コンテキストが必要なため遅延接続（初回リクエスト時接続）のままです。そのため合計ツール数は、OpenAPI 由来のツール（起動時に確定）と MCP バックエンド由来のツール（初回接続時に加算）とで動的に増加し、閾値超過のタイミングも起動後に変化しえます。
- ツール数の集計は全 `mcpServers` 合計、`tool_search` の検索対象（スコープ）はエンドポイント（`mcpServers.<name>`）単位です。
- `tool_search` の `description` には、そのエンドポイントに登録されているツール（`digestMaxTools` が -1 または未指定なら全ツール、正数 N なら先頭 N 件）のツール名と説明（アルファベット順、"- name: description" 形式、説明は各ツールにつき 200 文字までで切り詰め、超過分は "..." を付与）のダイジェストが動的に含まれます。これにより、クライアント LLM は `tools/list` から実ツール一覧が見えなくても「このエンドポイントにどんな種類のツールがあるか」を把握でき、検索クエリを組み立てる手掛かりになります。このダイジェストは `tools/list` 応答のたびに再構築されるため、遅延接続でツールが増えれば内容も追随します。`digestMaxTools` で件数上限を設けた場合、実際のツール数がそれを超えていればヘッダ行に "(showing first N)" のように省略があったことが明示されます。全ツール（または上限 N 件）分を列挙するため、登録ツール数が多いエンドポイントでは `tool_search` 自体の `description` がかなり大きくなる点に注意してください（`digestMaxTools` はこれを抑える手段として使えます）。

```yaml
gateway:
  toolSearch:
    threshold: 100
    defaultLimit: 10
    resultFormat: default # または claude
    digestMaxTools: -1 # 全ツールを列挙（デフォルト）。正数 N で先頭 N 件に制限。0 は設定エラー
```

#### `mcpServers.<name>`

Server names (`<name>`) are used in URL paths, so only alphanumerics, `_`, and `-` are allowed.

| Field           | Type              | Description                                                          |
| --------------- | ----------------- | -------------------------------------------------------------------- |
| `description`   | string            | Server description (**required**; included in `/mcp/list` responses) |
| `transport`     | string            | Transport for MCP backends (`http` or `stdio`)                       |
| `url`           | string            | Endpoint for the HTTP transport                                      |
| `command`       | string            | Command for the stdio transport                                      |
| `args`          | []string          | Arguments for the stdio command                                      |
| `env`           | map[string]string | Environment variables for the stdio process                          |
| `spec`          | string            | Path or URL of an OpenAPI/Swagger specification                      |
| `baseURL`       | string            | API base URL in OpenAPI mode (required when `spec` is set)           |
| `headers`       | map[string]string | Extra headers added to API requests                                  |
| `authValue`     | object            | Static authentication settings (`header`, `prefix`, `value`)         |
| `oauth2`        | object            | OAuth 2.0 settings (see below)                                       |
| `tokenExchange` | object            | Token Exchange settings (see below)                                  |

`authValue` / `oauth2` / `tokenExchange` are mutually exclusive; only one may be configured at a time.

#### `mcpServers.<name>.oauth2`

| Field          | Type     | Description                                         |
| -------------- | -------- | --------------------------------------------------- |
| `clientID`     | string   | Client ID (**required**)                            |
| `clientSecret` | string   | Client secret (**required**)                        |
| `authURL`      | string   | Authorization endpoint (**required**; absolute URL) |
| `tokenURL`     | string   | Token endpoint (**required**; absolute URL)         |
| `scopes`       | []string | Scopes to request                                   |

#### `mcpServers.<name>.tokenExchange`

Exchanges the API key received from the client for an OAuth token at the specified token exchange endpoint, and uses it for backend requests. Exchange results are cached, and rate limits (429) are respected.

| Field | Type   | Description                                                |
| ----- | ------ | ---------------------------------------------------------- |
| `url` | string | Absolute URL of the token exchange endpoint (**required**) |

#### `redis`

| Field          | Type     | Description                                           |
| -------------- | -------- | ----------------------------------------------------- |
| `url`          | string   | Redis URL (e.g. `redis://user:pass@localhost:6379/0`) |
| `addrs`        | []string | List of host:port pairs (for Cluster/Sentinel)        |
| `user`         | string   | Username                                              |
| `password`     | string   | Password                                              |
| `db`           | int      | Database number                                       |
| `master_name`  | string   | Sentinel master name                                  |
| `tls`          | bool     | Enable TLS                                            |
| `cluster_mode` | bool     | Enable Cluster mode                                   |

#### `sqlite`

| Field  | Type   | Description                                   |
| ------ | ------ | --------------------------------------------- |
| `path` | string | Database file path (`:memory:` for in-memory) |

Either `redis` or `sqlite` must be configured.

#### `storage`

Stores content included in OpenAPI/Swagger tool responses (images, binaries, etc.) in external storage and returns resource links (download URLs). When unset, no storage is used.

| Field          | Type   | Description                                                                                |
| -------------- | ------ | ------------------------------------------------------------------------------------------ |
| `type`         | string | Storage type. Currently only `s3` is supported                                             |
| `hostURL`      | string | Host for download URLs (when set, content is served via Manifold's `/media/download/{id}`) |
| `s3.bucket`    | string | S3 bucket name (required when `type: s3`)                                                  |
| `s3.keyPrefix` | string | S3 object key prefix (required when `type: s3`)                                            |

```yaml
storage:
  type: s3
  hostURL: https://manifold.example.com
  s3:
    bucket: my-bucket
    keyPrefix: manifold/media
```

#### `fileFetch`

When a URL is passed to a file input field of an OpenAPI/Swagger tool, Manifold downloads the file from that URL. As an SSRF countermeasure, connections to private/loopback/link-local IPs and the `http://` scheme are rejected by default.

| Field          | Type     | Description                                                                                               |
| -------------- | -------- | --------------------------------------------------------------------------------------------------------- |
| `allowLocal`   | bool     | Allow connections to private/loopback IPs and `http://` (for testing with local stacks; default: `false`) |
| `allowedHosts` | []string | Allowlist of hosts (hostname, or `host:port`). Empty allows all hosts (private IP blocking still applies) |
| `maxSize`      | int64    | Maximum bytes for downloaded/base64/text content. 0 or unset defaults to 524288000 (500 MiB)              |

Each field can also be overridden via environment variables (`FILEFETCH_MAXSIZE`, `FILEFETCH_ALLOWLOCAL`, `FILEFETCH_ALLOWEDHOSTS`).

```yaml
fileFetch:
  allowLocal: false
  maxSize: 524288000 # 500MiB
  # allowedHosts:
  #   - example.com
  #   - files.example.com:8443
```

#### `telemetry`

Output settings for traces, metrics, and logs via OpenTelemetry.

| Field             | Type   | Description                                                                   |
| ----------------- | ------ | ----------------------------------------------------------------------------- |
| `serviceName`     | string | Service name                                                                  |
| `environment`     | string | Environment name (`deployment.environment` attribute)                         |
| `gzipCompression` | bool   | Gzip compression for OTLP export                                              |
| `trace`           | object | Trace settings (`enabled`, `http`, `grpc`)                                    |
| `metrics`         | object | Metrics settings (`enabled`, `exporterType`: `push` / `pull`, `http`, `grpc`) |
| `logs`            | object | Log settings (`enabled`, `http`, `grpc`)                                      |

For the `http` / `grpc` exporters, specify `addr` (host:port) or `url`. `grpc` also accepts `insecure`. With `metrics.exporterType: pull`, Prometheus-format metrics are exposed at the `/metrics` endpoint instead of OTLP push.

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

## HTTP endpoints

The HTTP endpoints exposed by Manifold.

### MCP

| Method | Path                 | Description                                      |
| ------ | -------------------- | ------------------------------------------------ |
| `POST` | `/mcp/{server_name}` | MCP requests (Streamable HTTP)                   |
| `GET`  | `/mcp/list`          | List registered servers (names and descriptions) |

### OAuth 2.1

| Method | Path                                                        | Description                            |
| ------ | ----------------------------------------------------------- | -------------------------------------- |
| `GET`  | `/.well-known/oauth-authorization-server/mcp/{server_name}` | Authorization Server metadata          |
| `GET`  | `/.well-known/oauth-protected-resource/mcp/{server_name}`   | Protected Resource metadata            |
| `GET`  | `/{server_name}/auth/login`                                 | Redirect to the login page             |
| `GET`  | `/{server_name}/auth/callback`                              | OAuth callback                         |
| `POST` | `/{server_name}/auth/token`                                 | Token issuance                         |
| `POST` | `/{server_name}/auth/clients`                               | Dynamic client registration (RFC 7591) |
| `GET`  | `/authorize`, `/callback`                                   | Aliases without a server name          |
| `POST` | `/token`, `/register`                                       | Aliases without a server name          |

### Other

| Method | Path                   | Description                                                           |
| ------ | ---------------------- | --------------------------------------------------------------------- |
| `GET`  | `/media/download/{id}` | Download stored content (only when `storage.hostURL` is set)          |
| `GET`  | `/metrics`             | Prometheus metrics (only when `telemetry.metrics.exporterType: pull`) |

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for how to set up a development environment and submit changes.

### Test

```bash
make test
```

### Lint

```bash
make lint
```

## Inspiration

This project is inspired by the **Agent / MCP Gateway** of [LiteLLM](https://github.com/BerriAI/litellm).

Just as LiteLLM's MCP Gateway provides a unified access point to multiple MCP servers, Manifold aims to be a gateway that connects a single MCP interface to many MCP servers / REST APIs.

## License

MIT License
