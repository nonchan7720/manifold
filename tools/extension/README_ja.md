# Manifold WebMCP Bridge（ブラウザ拡張）

[English](README.md) | 日本語

Web ページが登録する [WebMCP](https://webmachinelearning.github.io/webmcp/) ツール
（`document.modelContext`）を、Manifold の reverse connection gateway を経由してサーバーサイド
の AI エージェントから呼び出せるようにする Chrome MV3 拡張です。プロトコルの全体設計は
[docs/design/webmcp-reverse-gateway.ja.md](../../docs/design/webmcp-reverse-gateway.ja.md) を参照
してください。この拡張はそこで定義された **pairing + `type: static`** の MVP を実装しています。

## 仕組み

- **background/**（service worker）が edge エンドポイント（例:
  `ws://localhost:8081/edge/ws`）への WebSocket 接続を1本保持し、first-message 認証・heartbeat・
  指数バックオフでの再接続を行います。`ready` フレームを受け取るたびに、サーバーが許可した
  origin だけを対象にした動的コンテンツスクリプト
  （`chrome.scripting.registerContentScripts`）を登録します。
- **content/**（`manifest.json` には静的宣言せず動的登録）は、ページ内の WebMCP サーバーへ
  [`@mcp-b/transports`](https://github.com/WebMCP-org/npm-packages) の `TabClientTransport` で、
  background service worker へは `ExtensionClientTransport` で接続します。両者の間で素の MCP
  JSON-RPC メッセージを中継するだけで、内容を解釈・加工しません。ページ側の WebMCP サーバーは
  content script より後（遅延読み込みされたチャンクの中など）に起動することがあるため、
  `TabClientTransport` のハンドシェイクはトランスポート自身の一回限りの check-ready に頼らず、
  バックオフ付きでリトライします（`connectWithRetry.ts`）。
- **content/nativeAdapter.ts**（ページの MAIN world に動的登録）は、`@mcp-b/global` のような
  postMessage サーバーを伴わない、ネイティブの `document.modelContext` だけを持つページに対応
  します。詳細は下記の [ネイティブ WebMCP ページ対応](#ネイティブ-webmcp-ページ対応) を参照して
  ください。
- **popup/** では edge URL の設定、ペアリングコード→edge token の交換
  （`POST {edge origin}/edge/pair`）、接続状態と接続中タブの origin 一覧の表示、ログアウト
  （保存済み edge token の破棄）ができます。

## ビルド

```bash
pnpm install
pnpm build       # 型チェック後に dist/ をビルド
pnpm test        # vitest
pnpm dev         # vite の watch ビルド（自動リロードはしないので、拡張は手動でリロードしてください）
```

## 拡張の読み込み方

1. `pnpm build`
2. `chrome://extensions` を開き、「デベロッパーモード」を有効化
3. 「パッケージ化されていない拡張機能を読み込む」→ `tools/extension/dist` を選択

## ペアリング手順

1. `gateway.edge` を設定した状態で Manifold を起動します（設定例は
   [docs/design/webmcp-reverse-gateway.ja.md](../../docs/design/webmcp-reverse-gateway.ja.md#設定)
   を参照。ローカル・単一ユーザー用途では `gateway.edge.pairing.type: static` を指定します。
   static はローカル専用です — edge エンドポイントを公開ネットワークに晒さないでください）。
2. reverse サーバーの `create_pairing_code` ツールを呼び出し（Manifold 経由で接続した MCP
   クライアントなどから）、短命のコードを取得します。
3. 拡張のポップアップを開き、edge の WebSocket URL（例: `ws://localhost:8081/edge/ws`）とペア
   リングコードを入力して送信します。ポップアップがコードを edge token に交換し、
   `chrome.storage.local` に保存します。
4. ペアリング後、サーバーが許可した origin のタブを開いたままにしておけば、拡張が自動的に
   ブリッジします。ポップアップの「Log out」で保存済みトークンを破棄できます。

## ネイティブ WebMCP ページ対応

一部のページは、`@mcp-b/global` のような `postMessage` サーバーを介さず、Chromium のネイティブ
producer API ——[WebMCP 仕様](https://webmachinelearning.github.io/webmcp/)準拠の
`document.modelContext.getTools()` と、仕様外の Chrome 固有拡張である `executeTool()`
（呼び出し前に feature detection が必要）—— をそのまま公開しています。
`content/nativeAdapter.ts` はこのケースに対応するため、isolated world のブリッジスクリプトと
同じ許可 origin に対して、ページの **MAIN world**（`chrome.scripting` の `world: "MAIN"`）へ
動的登録されます。

- `document.modelContext` を [`@mcp-b/webmcp-ts-sdk`](https://www.npmjs.com/package/@mcp-b/webmcp-ts-sdk)
  の `BrowserMcpServer`（`{ native: document.modelContext }`）でラップします。既存の
  native/polyfill な `modelContext` を MCP サーバーとしてミラーする、SDK 自身がサポートする方法
  であり、`getTools`/`executeTool` の橋渡しを自前で書く必要はありませんでした。
- ラップしたサーバーは、`@mcp-b/global` 自身が使うのと同じ `mcp-default` チャンネルの
  `TabServerTransport` で公開するため、`content/index.ts` 側の `TabClientTransport` は
  isolated world のプロトコルを変更せずに到達できます。
- `document.modelContext` が既に `BrowserMcpServer` インスタンスである場合（SDK の
  `SERVER_MARKER_PROPERTY` マーカーで検知）は何もしません。これは `@mcp-b/global` が存在する
  ケースに該当し、同じチャンネルに競合する2つ目の `TabServerTransport` が立つのを防ぎます。
- **制約**: 初回の一覧と、その後 *追加* されたツールのみ反映されます（ネイティブの
  `toolchange` イベントで `syncNativeTools()` を再実行）。アダプタの初回同期後に
  `document.modelContext` から *削除* されたツールは、ブリッジ先のサーバーからは削除されません
  ——これはこの拡張が上乗せした制約ではなく、`BrowserMcpServer.syncNativeTools()` 自体の制約です
  （不足分の backfill のみを行うため）。

## 既知の制約（MVP スコープ）

- `host_permissions` は `<all_urls>` です。許可 origin の集合はサーバーの `ready` フレームでしか
  分からず実行時に決まるため、事前に狭い静的パーミッションを宣言できません。origin が判明した
  時点で `chrome.permissions.request` 等を使って絞り込むのは今後の課題とします。
- ペアリング前から開いていたタブも、次の遷移（フルナビゲーションまたは SPA のルート変更）か
  popup の Reconnect ボタンで自動的に再ブリッジされます。手動でのリロードは不要です。
- `forwardAuth` の edge モードはこの拡張では未実装です（pairing モードのみ）。
- ページの WebMCP サーバー（polyfill・ネイティブいずれも）が 30 秒経っても準備できない場合、
  `connectWithRetry` は諦めます。そのタブは単純にブリッジされません。
- ネイティブアダプタ固有の制約（初回同期後のツール削除が反映されない件）は上記
  [ネイティブ WebMCP ページ対応](#ネイティブ-webmcp-ページ対応) を参照してください。

## 使用パッケージ

- [`@mcp-b/transports`](https://www.npmjs.com/package/@mcp-b/transports) — `TabClientTransport`
  （content script ↔ ページ）、`TabServerTransport`（ネイティブアダプタ ↔ ページ）、
  `ExtensionClientTransport`/`ExtensionServerTransport`（content script ↔ background）。
  いずれの境界も自前の `postMessage` / `chrome.runtime.Port` プロトコルを書く必要が
  ありませんでした。
- [`@mcp-b/webmcp-ts-sdk`](https://www.npmjs.com/package/@mcp-b/webmcp-ts-sdk) — `BrowserMcpServer`。
  ネイティブアダプタが、ツール一覧・実行の橋渡しを自前で書かずにネイティブの
  `document.modelContext` を MCP サーバーへミラーするために使用しています。
- [`vite-plugin-web-extension`](https://www.npmjs.com/package/vite-plugin-web-extension) — MV3
  ビルド（manifest 起点のマルチエントリバンドル）。
- [`vitest`](https://vitest.dev/) + `jsdom` — ユニットテスト。

## 参考リンク

- [WebMCP 仕様](https://webmachinelearning.github.io/webmcp/) — W3C Web Machine Learning Community Group（Draft Community Group Report）
- [WebMCP | AI on Chrome](https://developer.chrome.com/docs/ai/webmcp) — Chrome の実装ドキュメント（フラグ・オリジントライアル・実装例）
- [Join the WebMCP origin trial](https://developer.chrome.com/blog/ai-webmcp-origin-trial) — Chrome オリジントライアルの案内
