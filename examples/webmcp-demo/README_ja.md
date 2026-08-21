# WebMCP デモページ

[English](README.md) | 日本語

[WebMCP](https://webmachinelearning.github.io/webmcp/) ツールをいくつか `document.modelContext`
に登録するだけの最小限の Web ページです。実際に WebMCP を実装したサイトを用意しなくても、
[Manifold WebMCP ブラウザ拡張](../../tools/extension/) の動作を一通り確認できます。

WebMCP をまだネイティブ実装していないブラウザ向けに、
[`@mcp-b/global`](https://www.npmjs.com/package/@mcp-b/global) で `document.modelContext` を
ポリフィルしています。

## 登録されているツール

| ツール | 内容 |
| --- | --- |
| `echo` | 渡された `message` をそのままテキストで返す |
| `get_current_time` | 現在時刻を ISO-8601 形式の文字列で返す |
| `increment_counter` / `decrement_counter` | ページ内カウンタを増減（`by` 省略時は 1）し、新しい値を返す |

## 実行方法

```bash
pnpm install
pnpm dev     # http://localhost:5173
```

タブを開いたまま [ブラウザ拡張](../../tools/extension/) を Manifold とペアリングし、
`origin: http://localhost:5173` を指定した reverse `mcpServer` を用意すれば、
`/mcp/{server_name}` 経由でエージェントからこれらのツールを呼び出せます。
