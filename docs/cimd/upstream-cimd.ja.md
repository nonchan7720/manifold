# 上流向け CIMD（OAuth Client ID Metadata Document）

[English](upstream-cimd.md)

Manifold が OAuth クライアントとして上流 MCP サーバーへ接続するとき、Dynamic Client Registration（DCR）の代わりに Client ID Metadata Document（CIMD）で自分を名乗れます。ドキュメント自体は Manifold では配信しません。S3 や CDN などの公開 HTTPS URL に置き、その URL を設定で渡します。

## 設定

サーバー定義の直下に `cimdClientID` を書きます。`oauth2` の中には書かないでください。`oauth2` を設定すると、そのサーバーの OAuth 自動検出が止まり、`cimdClientID` が前提にしている検出が働かなくなります。

```yaml
mcpServers:
  github:
    description: GitHub
    transport: http
    url: https://mcp.example.com/mcp
    cimdClientID: https://cdn.example.com/manifold/github.json
```

- 値は `https://` の URL である必要があります。`transport: http` のサーバーでのみ有効で、`oauth2` とは併用できません。
- 上流の認可サーバーが `client_id_metadata_document_supported: true` を返す場合、この URL を `client_id` に使い、クライアントシークレットは使いません。DCR より優先され、上流に registration endpoint が無くても動作します。
- それ以外（未指定、または上流が CIMD 非対応）は従来どおり DCR にフォールバックします。後者では警告ログを出します。
- ドキュメントの URL を使った場合、検出ログに `cimd=true` が付きます。

## ドキュメントのテンプレート

```json
{
  "client_id": "https://cdn.example.com/manifold/github.json",
  "client_name": "manifold",
  "redirect_uris": ["https://manifold.example.com/github/auth/callback"],
  "grant_types": ["authorization_code", "refresh_token"],
  "response_types": ["code"],
  "token_endpoint_auth_method": "none"
}
```

`redirect_uris` には `<baseURL>/<server>/auth/callback` を含めます。`<baseURL>` は Manifold の外部から見えるベース URL、`<server>` は `mcpServers` のキーです。

## 設置手順

ドキュメントは上流の認可サーバーが取得するため、次を満たす必要があります。

- 署名なしの公開 `GET` で取得できること（認証不要）。
- リダイレクトせず直接返すこと。
- `Content-Type: application/json` で返すこと。
- ドキュメント内の `client_id` が、設置先の URL（および `cimdClientID`）と文字列として完全一致すること。
- Manifold のベース URL やサーバー名を変えたら `redirect_uris` も更新すること。
