---
name: ssrf_guard_test_setenv
description: SSRFガード(env.IsLocalOrCIOrTest)によるテスト失敗はt.SetenvでTEST等の環境変数を設定して回避する
type: feedback
count: 1
---

`client.HTTPClient()` が `env.IsLocalOrCIOrTest()` の値によって Safe/Unsafe な Transport を切り替える設計になっている場合、テストの httptest サーバー(127.0.0.1)への接続が SafeHTTPClient にブロックされて失敗するケースがある。この場合、`CreateToolFunctionSwagger` や `fetchFileFromURL` 等にまで `*http.Client` を DI で渡す設計変更は行わず、テスト側で `t.Setenv("TEST", "true")`（または `LOCAL`/`CI`）を設定して SafeHTTPClient を回避する方法を優先する。

**Why:** ユーザーは一度「DI移行を完成させる」を選んだが、直後に「t.Setenvで対応して」と訂正した。DI移行（CreateToolFunctionSwagger・fetchFileFromURL・fetchSpecBytesへのclient引数追加と呼び出し元の修正）は変更範囲が大きく、テストを通すためだけの対応としては過剰。

**How to apply:** [[golang_archtecture]] のようなプロダクションコード構造変更が必要に見える場面でも、テスト実行環境を検出するための環境変数分岐(env.IsLocal/IsCI/IsTest等)が絡む場合は、まずテスト側でその環境変数をt.Setenvでセットして回避できないか検討し、それで解決するならプロダクションコードの構造変更は行わない。
