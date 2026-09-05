# DCR クライアントの登録元 MCP サーバーへの束縛

[English](dcr-client-server-binding.md) | 日本語

## 概要

`POST /{server_name}/auth/clients` の動的クライアント登録（RFC 7591）で発行したクライアントは、同じ MCP サーバーの認可エンドポイントでしか使えません。その `client_id` を `GET /{other_server}/auth/login` に渡すと `invalid_client`（401）で拒否されます。

クライアント ID メタデータドキュメント（CIMD）で解決したクライアントは対象外です。登録元サーバーを持たないため、従来どおり MCP サーバーを横断して使えます。

## 束縛が必要な理由

理由は 2 つあり、いずれも控えめなものです。

**登録は発行元の認可サーバーに属するものであるという正しさ。** Manifold は MCP サーバーごとに別の認可サーバーを名乗ります。`{server_name}` のメタデータは `issuer` に `{base_url}/mcp/{server_name}` を、`registration_endpoint` に `/{server_name}/auth/clients` を返します。ある issuer が発行した `client_id` は別の issuer では意味を持たないので、そこで受け付ける理由がありません。

**別サーバーで登録された `client_id` の持ち込みを検知できること。** 拒否時には両方のサーバー名をログへ出力するため、他サーバーの `client_id` を持ち込む動きが黙って通らず監査ログに残ります。

**これ単体では、攻撃者が別サーバーの上流トークンを取得することを防ぎません。** `POST /{server_name}/auth/clients` は全サーバー分が同じ mux に登録されており、その前段の `middleware.MCPServerApp`（`pkg/interfaces/http/middleware/mcp_server.go`）はパスからサーバーを解決して context に入れるだけで、呼び出し元の認証もアクセス制限も行いません。`server-b` を狙う攻撃者は `server-b` の登録エンドポイントで直接登録すればよく、束縛の照合は通ります。「A で登録して B に持ち込む」を塞いでも「最初から B で登録して B を叩く」に置き換えられるだけです。

実効的な制限は `mcpServers.<name>.oauth2.clients` と `unknownClient: reject` です。`resolveUpstreamClient`（`pkg/interfaces/http/auth_handler.go`）は登録元に関係なく、`client_id` がそのマッピングに載っているかだけで判定します。

## 検証する内容

`LoginEndpoint` は、クライアントを解決した直後、`redirect_uri` を照合するより前に、クライアントに記録された登録元サーバーとリクエスト先のサーバー名を比較します。

| クライアントの登録経路 | 記録される `mcp_server_name` | 使える MCP サーバー |
| ---------------------- | ---------------------------- | ------------------- |
| DCR（`source: dcr`） | 登録時に記録されたサーバー | そのサーバーのみ |
| この変更より前に DCR 登録されたもの（`source` が空） | 登録時に記録されたサーバー | そのサーバーのみ |
| CIMD（`source: cimd`） | 設計上つねに空 | すべてのサーバー |

`POST /{server_name}/auth/clients` ではパスのサーバー名が記録されます。サーバー名をパスに持たない `POST /register` は、送信された `client_name` からサーバー名を取り出して記録し、束縛はそのサーバーに対して効きます。

拒否した場合は、監査用に下流の `client_id`・クライアント名・登録元サーバー名・リクエスト先のサーバー名をログへ出力します。理由をクライアントへ返すことはなく、レスポンスボディは `invalid_client` のみです。

`/authorize` エイリアスはパスにサーバー名を含みません。この経路では従来どおりクライアント自身の `mcp_server_name` からサーバーを解決するため、両者は必ず一致し、この検証で拒否されることはありません。CIMD クライアントは `mcp_server_name` を持たないので、そもそも `/authorize` では使えず `/{server_name}/auth/login` を呼ぶ必要があります。

## 影響を受ける構成

RFC 7591 の DCR は、クライアントが認可サーバーごとに登録して `client_id` を受け取り、それを issuer をキーに保持するモデルです。仕様どおりの DCR クライアントがあるサーバーの `client_id` を別のサーバーへ出すことはないため、影響を受ける構成は通常存在しません。1 つの `client_id` を複数の `mcpServers` エントリで使い回している実装だけが `invalid_client`（401）になります。

`unknownClient` の既定値は変更していません。`clients` が非空なら `reject`、空なら `default` のままです。

## 関連

- [下流クライアントの登録](../../README_ja.md#下流クライアントの登録) — DCR・CIMD と、下流クライアントから上流クライアントへのマッピング
