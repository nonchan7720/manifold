---
name: webmcp-e2e
description: WebMCP reverse connection gateway（pairing + static モード）の E2E 結合確認を実行する。Manifold 起動 → デモページ → 拡張入り Chromium → ペアリング → tools/call 検証 → タブクローズ時エラー確認までを通し、スクリーンショット証跡を残す。「webmcp の E2E」「reverse gateway の動作確認」「拡張の結合テスト」で使用。
---

# WebMCP Reverse Gateway E2E 結合確認

設計文書: `docs/design/webmcp-reverse-gateway.ja.md`

## 前提

- `tools/extension/`、`examples/webmcp-demo/`、`.claude/skills/webmcp-e2e/scripts/` はいずれも pnpm パッケージ（`pnpm install` 済みであること。`scripts/` は `@modelcontextprotocol/sdk` と `playwright` の実行用依存を持つ）
- ポート: Manifold = 9999、デモページ = 5173（変更したら以降のコマンド・config を合わせる）

## 手順

### 1. 拡張をビルド

```bash
cd tools/extension && pnpm install && pnpm build   # dist/ が生成される
cd .claude/skills/webmcp-e2e/scripts && pnpm install   # e2e.mjs / create-pairing-code.mjs 用
```

### 2. Manifold を起動

```bash
mkdir -p examples/webmcp-reverse/tmp    # sqlite.path: ./tmp/manifold.db 用
ENCRYPT_KEY=$(openssl rand -base64 32) go run . gateway --config examples/webmcp-reverse/config.yaml
```

- config は `examples/webmcp-reverse/config.yaml`（`gateway.port: 9999`、edge auth は pairing + static、origin は `http://localhost:5173`）

### 3. デモページを起動

```bash
cd examples/webmcp-demo && pnpm dev    # http://localhost:5173
```

`document.modelContext` に echo / get_current_time / increment_counter / decrement_counter が登録される。

### 4. ペアリングコードを取得

```bash
node .claude/skills/webmcp-e2e/scripts/create-pairing-code.mjs
```

- `edge.pairing.type: static`（このスキルの config）では reverse サーバーへの `/mcp/{name}` に JWT ミドルウェア自体が掛からないため、Authorization ヘッダーは不要（スクリプトも送らない）
- reverse サーバーはタブ未接続でも `create_pairing_code` ツールだけは公開している（ベースサーバーが起動時に構築される）

### 5. 拡張入り Chromium を起動してペアリング

Playwright でヘッドフル Chromium を `--load-extension=tools/extension/dist` 付きで起動し、拡張 popup で edge URL（`ws://localhost:9999/edge/ws`）とコードを入力してペアリング。

### 6. 検証（`scripts/e2e.mjs` が一括実行）

```bash
node .claude/skills/webmcp-e2e/scripts/e2e.mjs
```

| 検証項目 | 期待結果 |
| --- | --- |
| タブ接続後の tools/list | `create_pairing_code, echo, get_current_time, increment_counter, decrement_counter` |
| `echo("...")` | 入力文字列がそのまま返る |
| `increment_counter(by: 3)` | `"3"` が返り、ページ内カウンタ表示も一致 |
| タブを閉じて tools/call | `isError: true` + 「対象アプリのタブが開かれていません。…」の案内文 |

## 証跡（必須）

各ステップのスクリーンショットを `${CLAUDE_PROJECT_DIR}/.claude/scratchpad/webmcp-e2e/` に保存し、報告でパス一覧を提示する。口頭の「確認済み」だけで終わらせない。最低限: popup ペアリング前後、デモページのカウンタ変化前後、タブクローズ後の popup。

## 既知のハマりどころ

- **`/edge/ws` が 501 になる場合**: `middleware.Logging` の ResponseWriter ラップが `Hijack()` を隠すため WebSocket が確立できない。`pkg/cmd/server.go` の `newHTTPHandler` が `/edge/ws` を Logging バイパスで配線しているのが正。この配線を崩さないこと
- **ペアリング後に WS 接続が始まらない場合**: background が `chrome.storage.onChanged` を購読して token 保存を検知する実装（`tools/extension/src/background/app.ts`）に依存。popup での保存後、自動で接続が始まるのが正常
- **MV3 service worker の suspend**: heartbeat は 20 秒間隔（30 秒未満必須）
- 拡張のテスト・型チェックは `.claude/gate.yaml` の hook（`pnpm test` / `pnpm typecheck`）が Edit 時に自動実行される
