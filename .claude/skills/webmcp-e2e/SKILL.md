---
name: webmcp-e2e
description: WebMCP reverse connection gateway（pairing + static/remote モード）の E2E 結合確認を実行する。Manifold 起動 → デモページ → 拡張入り Chromium → ペアリング → tools/call 検証 → タブクローズ時エラー確認までを通し、スクリーンショット証跡を残す。remote モードでは自前 JWKS + JWT で identityKey ルーティングの分離も検証する。「webmcp の E2E」「reverse gateway の動作確認」「拡張の結合テスト」で使用。
---

# WebMCP Reverse Gateway E2E 結合確認

設計文書: `docs/design/webmcp-reverse-gateway.ja.md`

## 前提

- `tools/extension/`、`examples/webmcp-demo/`、`.claude/skills/webmcp-e2e/scripts/` はいずれも pnpm パッケージ（`pnpm install` 済みであること。`scripts/` は `@modelcontextprotocol/sdk` と `playwright` の実行用依存を持つ）
- ポート: Manifold = 9999、デモページ = 5173（変更したら以降のコマンド・config を合わせる）

## static モードの手順

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

## remote モードの手順

remote は identityKey を Bearer JWT(自前 JWKS で署名検証)から導出する。JWT を送るのは MCP クライアント(エージェント)側であり、拡張はペアリング(`POST /edge/pair`)のみ static と同じフローで、拡張自体の変更は不要。`scripts/e2e-remote.mjs` が JWKS サーバー起動・Manifold 起動・ペアリング・検証・プロセス終了までを一括で行う。

### 前提

- config は `.claude/skills/webmcp-e2e/config.remote.e2e.yaml`(`edge.pairing.type: remote`。`identities.oauth.jwksURL` は起動時に環境変数 `JWKS_URL` で注入)
- デモページ(`examples/webmcp-demo`、`pnpm dev`)は事前に起動しておくこと。static と共用できる

### 実行

```bash
cd tools/extension && pnpm install && pnpm build   # 拡張は static と共通、未ビルドなら実行
cd .claude/skills/webmcp-e2e/scripts && pnpm install   # jose が追加済み
cd ../../../.. && node .claude/skills/webmcp-e2e/scripts/e2e-remote.mjs
```

`scripts/e2e-remote.mjs` が内部で行うこと:

1. `jwt-helper.mjs` の `startJwksServer` でループバック JWKS サーバーを起動、RS256 鍵ペアを生成
2. `TEST=true`(`client.HTTPClient()` がループバックへの JWKS 取得を許すため必須。`pkg/internal/client/http.go` 参照)で Manifold を `config.remote.e2e.yaml` で起動(`go run` は viper の `SetConfigName` に絶対パスを渡すと解決に失敗するため、config パスは Manifold の cwd からの相対パスで渡す)
3. `sub=e2e-user-a` の JWT で `create_pairing_code` を呼びコード取得 → 拡張でペアリング
4. デモページのタブ接続後、tools/list・echo・increment_counter を検証
5. 別 `sub`(`e2e-user-b`、未ペアリング)の JWT で `tools/list` すると `create_pairing_code` のみ返ることを確認(identityKey ごとのルーティング分離)
6. タブクローズ後の tools/call エラーを確認
7. Manifold プロセスと JWKS サーバーを終了

| 検証項目 | 期待結果 |
| --- | --- |
| user-a: タブ接続後の tools/list | `create_pairing_code, echo, get_current_time, increment_counter, decrement_counter` |
| user-a: `echo("...")` / `increment_counter(by: 3)` | static と同じ |
| user-b(別 identityKey、未ペアリング)の tools/list | `create_pairing_code` のみ |
| user-a: タブを閉じて tools/call | static と同じ |

## 証跡（必須）

各ステップのスクリーンショットを、static は `${CLAUDE_PROJECT_DIR}/.claude/scratchpad/webmcp-e2e/`、remote は `${CLAUDE_PROJECT_DIR}/.claude/scratchpad/webmcp-e2e-remote/` に保存し、報告でパス一覧を提示する。口頭の「確認済み」だけで終わらせない。最低限: popup ペアリング前後、デモページのカウンタ変化前後、タブクローズ後の popup。

## 既知のハマりどころ

- **`/edge/ws` が 501 になる場合**: `middleware.Logging` の `responseWriter` は `http.Hijacker`（WebSocket のアップグレード）と `http.Flusher`（SSE）を常に下層へ委譲する。`/edge/ws` は他のルートと同じ `newHTTPHandler` チェーンを通るので、Logging をバイパスする配線を追加しないこと
- **ペアリング後に WS 接続が始まらない場合**: background が `chrome.storage.onChanged` を購読して token 保存を検知する実装（`tools/extension/src/background/app.ts`）に依存。popup での保存後、自動で接続が始まるのが正常
- **MV3 service worker の suspend**: heartbeat は 20 秒間隔（30 秒未満必須）
- 拡張のテスト・型チェックは `.claude/gate.yaml` の hook（`pnpm test` / `pnpm typecheck`）が Edit 時に自動実行される
- **`go run` の子プロセスが `kill` 後も残ることがある**: `go run` はビルド済みバイナリを子プロセスとして起動するため、親（`go run` 自体）を kill してもバイナリ（`manifold gateway ...`）が生き残り、ポートを掴んだままになる。`ps aux | grep gateway` で確認し、残っていれば別途 kill する
- **`--config` に絶対パスを渡すと解決に失敗する**: `pkg/config.Load` は `viper.SetConfigName(configName)` にそのまま渡すため、絶対パス文字列は「ファイル名」として誤解釈され `Config File "..." Not Found in [...]` になる。カレントディレクトリ（または `go run` の cwd）からの相対パスで渡すこと
