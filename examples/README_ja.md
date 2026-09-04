[English](README.md)

# Manifold のサンプル集

Manifold をすぐ試せる設定サンプル集です。各ディレクトリに `config.yaml` と手順を示す README が入っています。

| サンプル | 内容 |
| -------- | ---- |
| [`openapi-backend/`](openapi-backend/) | 公開 OpenAPI 仕様（Swagger Petstore）を MCP ツールに変換する — Manifold を最速で試す方法。`manifold openapi tools` / `generate` の使い方も示す |
| [`mcp-backend/`](mcp-backend/) | 外部 MCP サーバーをプロキシする（stdio / HTTP トランスポート） |
| [`oauth2-backend/`](oauth2-backend/) | OAuth 保護された REST API（Google Calendar）を MCP ツールとして公開する |
| [`opa/`](opa/) | OPA サイドカーで `tools/call` / `tools/list` を呼び出し元のグループ単位で認可する |

## 前提条件

- Manifold のバイナリ（[Releases](https://github.com/nonchan7720/manifold/releases)）、Docker イメージ（`ghcr.io/nonchan7720/manifold:latest`）、またはローカルのチェックアウト（`go run main.go gateway`）
- 暗号化キー。**一度だけ**生成し、安全に保管してください（例: `.env` ファイル）— 保存されたセッションやトークンはこのキーで暗号化されるため、新しいキーを生成するとそれまで保存された内容がすべて無効になります:

```bash
export ENCRYPT_KEY=$(openssl rand -base64 32)
echo "ENCRYPT_KEY=$ENCRYPT_KEY" >> .env   # 次回以降の実行のために保管
```

すべてのサンプルはストレージに SQLite を使うため、Redis は不要です。

## サンプルの実行

```bash
cd examples/openapi-backend
manifold gateway   # ./config.yaml を読み込む。ENCRYPT_KEY の設定が必要
```

または Docker で（コンテナ内の作業ディレクトリは `/home/nonroot`）:

```bash
cd examples/openapi-backend
mkdir -p tmp
docker run -p 9999:9999 \
  -e ENCRYPT_KEY \
  -v $(pwd)/config.yaml:/home/nonroot/config.yaml \
  -v $(pwd)/tmp:/home/nonroot/tmp \
  ghcr.io/nonchan7720/manifold:latest
```

## クライアントを接続する

Manifold は `/mcp/{server_name}` で Streamable HTTP を話します。例えば [Claude Code](https://claude.com/claude-code) の場合:

```bash
claude mcp add --transport http petstore http://localhost:9999/mcp/petstore
```

登録済みサーバーの一覧は次で確認できます:

```bash
curl http://localhost:9999/mcp/list
```
