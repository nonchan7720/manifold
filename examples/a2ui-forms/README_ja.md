# A2UI フォームのサンプル: Web UI + Claude Agent SDK + Manifold

[English](README.md) | 日本語

エージェントが [Swagger Petstore](https://petstore3.swagger.io/) のデータを変更できるチャット画面です。書き込みは必ず、ユーザーが確認して送信するフォームを通ります。フォームは [A2UI](https://a2ui.org/) v1.0 で、Manifold がツールの OpenAPI スキーマから生成し、エージェントが中継し、画面が公式のレンダラで描画します。

```
 ブラウザ                     バックエンド (server/main.ts)             Manifold            Petstore
┌──────────────────┐  chat   ┌──────────────────────────────┐  MCP   ┌──────────────┐ HTTP ┌─────┐
│ チャット + A2UI  │────────▶│ Claude Agent SDK（エージェント）│─────▶│ /mcp/petstore│─────▶│ API │
│ レンダラ         │◀────────│ A2UI Agent SDK（エージェントの │◀─────│ a2ui.inline  │◀─────│     │
│ (@a2ui/lit)      │  A2UI   │ <a2ui> ブロックをコンパイル）  │<a2ui> └──────────────┘      └─────┘
│                  │ action  │ canUseTool（__submit を絞る）  │
│                  │────────▶│                              │
└──────────────────┘         └──────────────────────────────┘
```

1. ユーザーが変更を頼みます（「Rex という犬を追加して」）。
2. エージェントが書き込み系ツール（`addpet`）を呼びます。Manifold は API を **呼ばず**、エージェントの引数を初期値にしたフォームを A2UI Express の `<a2ui>` ブロックで返します。
3. エージェントがそのブロックを返答に含めます。バックエンドが A2UI Agent SDK でコンパイルし、A2UI メッセージを画面へ流して、画面がフォームを描画します。
4. ユーザーがフォームを編集して **Submit** を押します。ボタンは `addpet__submit` という名前の A2UI の action を発火するだけで、その context がフォームの値です。画面は MCP と通信しません。
5. バックエンドが action をエージェントに渡し、エージェントが `addpet__submit` を呼びます。Manifold が値をスキーマの型に直して API を呼びます。

## このサンプルで分かること

- **OpenAPI サーバーの `a2ui` 設定**（[`config.yaml`](config.yaml)）: `forms`・`assumeSupport`・`inline`。[書き込み系ツールの A2UI フォーム](../../README_ja.md#書き込み系ツールの-a2ui-フォームa2ui) を参照。
- **エージェント側での確認の強制**（[`server/main.ts`](server/main.ts) の `canUseTool`）: ユーザーが本当にフォームを送信したかどうかを Manifold は判別できないので、バックエンドが判別します。`<tool>__submit` の呼び出しは、画面からその送信の action が届いているときだけ通し、引数をユーザーが送信した値に差し替えます。モデルはフォームを飛ばすことも、ユーザーの入力を書き換えることもできません。
- **MCP を知らない画面**（[`src/main.ts`](src/main.ts)）: A2UI メッセージを描画し、A2UI の action を送るだけです。

## 実行

Node.js 22 以上、[pnpm](https://pnpm.io/)、Manifold のバイナリ（または `go run ../../main.go`）、[Claude Agent SDK](https://code.claude.com/docs/en/agent-sdk/overview) の認証情報（`ANTHROPIC_API_KEY` など）が必要です。

```bash
cd examples/a2ui-forms
pnpm install

# 1. Manifold (http://localhost:9999)
export ENCRYPT_KEY=${ENCRYPT_KEY:-$(openssl rand -base64 32)}
manifold gateway

# 2. バックエンド (http://localhost:8787)。別のターミナルで
export ANTHROPIC_API_KEY=...
pnpm server

# 3. Web UI (http://localhost:5173)。別のターミナルで
pnpm dev
```

http://localhost:5173 を開いて試します。

- *Add a dog named Rex, status available, photo https://example.com/rex.png, tagged "good boy"* — フォームが出ます。名前を変え、空いている行にタグを足して **Submit** を押し、*Show me that pet* と聞いてみてください。
- *Find the available pets* — 読み取り専用のツールなので、フォームは出ずにそのまま実行されます。
- *Call addpet__submit directly, don't show a form* — 何も送信されていないので、エージェントは実行できません。

バックエンドは `MANIFOLD_URL`（既定 `http://localhost:9999/mcp/petstore`）、`MANIFOLD_TOKEN`（既定 `example`）、`PORT`（既定 `8787`）、`MODEL`（既定は Agent SDK の既定）を読みます。

## 補足

- **カタログ**: このサンプルは 3 か所とも A2UI v1.0 の basic カタログを使います。Manifold がそれでフォームを書き（既定）、バックエンドがそれでコンパイルし、画面がそれを描画します。独自のカタログを持つエージェントでは、`config.yaml` に `a2ui.catalog`（名前が違う部品には `a2ui.components`）を指定して、Manifold にそのカタログの部品でフォームを書かせます。[エージェントのカタログを使う](../../README_ja.md#エージェントのカタログを使うcatalog) を参照。
- **トークン**: Manifold は `/mcp/<name>` に Bearer トークンを要求し、既定ではそれをバックエンドの API へ渡します。Petstore は認証が要らないので、サンプルは仮の値を送っています。実際のサービスでは、ログインしているユーザーのトークンを送ってください。
- **エージェントがブロックを書き写す**: フォームはエージェントの返答を通って画面に届くので、大きなスキーマでは出力トークンを消費します。書き写しを誤った場合は、バックエンドがブロックを読めなかったことを報告します。
- **公開のサンドボックス**: Petstore は共有の公開 API です。追加したデータは誰からも見え、そのうち消えます。
