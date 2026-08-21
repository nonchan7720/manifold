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
  JSON-RPC メッセージを中継するだけで、内容を解釈・加工しません。
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
   を参照。ローカル・単一ユーザー用途では `gateway.edge.pairing.type: static` を指定します）。
2. reverse サーバーの `create_pairing_code` ツールを呼び出し（Manifold 経由で接続した MCP
   クライアントなどから）、短命のコードを取得します。
3. 拡張のポップアップを開き、edge の WebSocket URL（例: `ws://localhost:8081/edge/ws`）とペア
   リングコードを入力して送信します。ポップアップがコードを edge token に交換し、
   `chrome.storage.local` に保存します。
4. ペアリング後、サーバーが許可した origin のタブを開いたままにしておけば、拡張が自動的に
   ブリッジします。ポップアップの「Log out」で保存済みトークンを破棄できます。

## 既知の制約（MVP スコープ）

- `host_permissions` は `<all_urls>` です。許可 origin の集合はサーバーの `ready` フレームでしか
  分からず実行時に決まるため、事前に狭い静的パーミッションを宣言できません。origin が判明した
  時点で `chrome.permissions.request` 等を使って絞り込むのは今後の課題とします。
- ペアリング前から開いていたタブは、動的登録されたコンテンツスクリプトを反映するためにリロード
  が必要です。`ready` 受信後に開かれた／遷移したタブのみ自動的にブリッジされます。
- `forwardAuth` の edge モードはこの拡張では未実装です（pairing モードのみ）。

## 使用パッケージ

- [`@mcp-b/transports`](https://www.npmjs.com/package/@mcp-b/transports) — `TabClientTransport`
  （content script ↔ ページ）と `ExtensionClientTransport`/`ExtensionServerTransport`
  （content script ↔ background）。どちらの境界も自前の `postMessage` / `chrome.runtime.Port`
  プロトコルを書く必要がありませんでした。
- [`vite-plugin-web-extension`](https://www.npmjs.com/package/vite-plugin-web-extension) — MV3
  ビルド（manifest 起点のマルチエントリバンドル）。
- [`vitest`](https://vitest.dev/) + `jsdom` — ユニットテスト。
