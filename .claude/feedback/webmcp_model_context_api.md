---
name: webmcp-model-context-api
description: WebMCP の API は document.modelContext が正。navigator.modelContext は非推奨エイリアス
type: feedback
count: 1
---

WebMCP に言及するドキュメント・コード・設計では `document.modelContext` を正とする。
`navigator.modelContext` は非推奨エイリアスなので、言及する場合は「非推奨」と明記する。
仕様の動きが速い領域（WebMCP 等）では、調査時に deprecation の記述を確認し、
知識として馴染みのある古い API 名をそのまま書かない。

**言い訳:** 調査した検索結果に「document.modelContext が本体で navigator は非推奨エイリアス」という情報が含まれていたのに、広く知られていた `navigator.modelContext` の名前をそのまま設計文書に書いてしまった。

**指摘事項:** 調査で得た最新情報を書き出し時に反映すること。検索結果に deprecation 情報があれば、それが成果物に反映されているか確認する。

**Why:** 非推奨 API を正として文書化すると、Phase 2 の拡張実装で古い API を実装してしまう。

**How to apply:** WebMCP / modelContext に言及するドキュメント・設計・実装（特にブラウザ拡張の content script）を書くとき。
