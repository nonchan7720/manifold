package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/toolsearch"
	"go.opentelemetry.io/otel/attribute"
)

// toolSearchName は全 mcpServers 合計のツール数が閾値を超えたときに公開される
// 合成ツールの名前。
const toolSearchName = "tool_search"

// methodListTools は go-sdk v1.6.1 が "tools/list" のメソッド名定数を非公開にしているため、
// パッケージ内で文字列リテラルを定数化したもの。
const methodListTools = "tools/list"

// digestDescriptionMaxRunes は tool_search の description に含める各ツールの説明文の
// 上限文字数（rune 単位）。超過分は "..." を付けて切り詰める。
const digestDescriptionMaxRunes = 200

// toolSearchDef は tool_search ツールの定義（name/description/inputSchema）を返す。
// cfg.ResultFormat が claude の場合、検索結果が Claude API 互換の tool_reference
// ブロックで返ることを description に明記する。また、catalog の現在の登録状況から
// 「このエンドポイントに何件・どんなツールがあるか」のダイジェストを description 末尾に
// 動的に含め、クライアント LLM が検索クエリを組み立てる手掛かりにする。
func toolSearchDef(serverName string, cfg config.ToolSearchConfig, catalog *toolsearch.Catalog) *mcp.Tool {
	description := fmt.Sprintf(
		"Search for available tools registered on the %q MCP endpoint. "+
			"The tools/list response for this endpoint has been replaced by this single "+
			"tool because the number of registered tools exceeds the configured threshold. "+
			"Call this tool with a query to find matching tools, then call the returned "+
			"tool name directly via tools/call using its inputSchema.",
		serverName,
	)
	if cfg.ResultFormat == config.ToolSearchResultFormatClaude {
		description += " Results are returned as tool_reference blocks " +
			`({"type":"tool_reference","tool_name":"..."}), ` +
			"compatible with the Claude API's Tool Search Tool custom implementation contract."
	}
	description += toolSearchDigest(serverName, cfg, catalog)
	return &mcp.Tool{
		Name:        toolSearchName,
		Description: description,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search text matched against tool names, descriptions, argument names, and argument descriptions.",
				},
				"method": map[string]any{
					"type":        "string",
					"enum":        []string{string(toolsearch.MethodBM25), string(toolsearch.MethodRegexp), string(toolsearch.MethodFuzzy)},
					"default":     string(toolsearch.MethodBM25),
					"description": "Search algorithm: bm25 (default, ranked full-text), regexp (case-insensitive pattern match), or fuzzy (subsequence match).",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results to return.",
				},
			},
			"required": []string{"query"},
		},
	}
}

// toolSearchDigest は catalog の serverName スコープの登録状況から、tool_search の
// description に付加する人間可読なダイジェスト文を構築する。登録ツールについて
// "- name: description" 形式（description が空のツールは "- name" のみ）の行を
// ツール名のアルファベット順で列挙する。各ツールの説明は digestDescriptionMaxRunes
// （rune 単位）で切り詰め、超過時は "..." を付与する。登録ツールが 0 件の場合は
// 空文字を返し、呼び出し元は description にダイジェストを付加しない。
//
// cfg.DigestMaxTools が正数かつ登録ツール数を下回る場合、先頭（名前順）N 件のみを列挙し、
// ヘッダ行に "(showing first N)" を付けて省略があったことを明示する。cfg.DigestMaxTools が
// -1（または 0 以下の値全般。ValidateWithContext を経由していれば -1 以外の 0 以下は
// 到達し得ないが、防御的に「全件表示」として扱う）の場合、あるいは登録ツール数以上の場合は
// 全件を省略なしで列挙する。
//
// 出力例（省略なし）:
//
//	 This endpoint currently has 3 searchable tools:
//	- addPet: Add a new pet to the store
//	- deletePet: Delete a pet
//	- createOrder
//
// 出力例（digestMaxTools=2 で 5 件登録されている場合）:
//
//	 This endpoint currently has 5 searchable tools (showing first 2):
//	- addPet: Add a new pet to the store
//	- cancelOrder: Cancel an order
func toolSearchDigest(serverName string, cfg config.ToolSearchConfig, catalog *toolsearch.Catalog) string {
	entries := catalog.Digest(serverName)
	total := len(entries)
	if total == 0 {
		return ""
	}

	shown := entries
	truncated := false
	if cfg.DigestMaxTools > 0 && cfg.DigestMaxTools < total {
		shown = entries[:cfg.DigestMaxTools]
		truncated = true
	}

	unit := "tools"
	if total == 1 {
		unit = "tool"
	}

	var b strings.Builder
	if truncated {
		fmt.Fprintf(&b, " This endpoint currently has %d searchable %s (showing first %d):", total, unit, len(shown))
	} else {
		fmt.Fprintf(&b, " This endpoint currently has %d searchable %s:", total, unit)
	}
	for _, e := range shown {
		b.WriteString("\n- ")
		b.WriteString(e.Name)
		if e.Description != "" {
			b.WriteString(": ")
			b.WriteString(truncateRunes(e.Description, digestDescriptionMaxRunes))
		}
	}
	return b.String()
}

// truncateRunes は s を高々 maxRunes rune に切り詰める。バイト単位ではなく rune 単位で
// 扱うため、CJK などのマルチバイト文字が境界で壊れることはない。切り詰めが発生した場合は
// 末尾に "..." を付与する。
func truncateRunes(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "..."
}

// toolSearchArgs は tool_search 呼び出しの引数。
type toolSearchArgs struct {
	Query  string `json:"query"`
	Method string `json:"method"`
	Limit  int    `json:"limit"`
}

// registerToolSearch は tool_search ツールを srv に登録する。実ツールと同様に常時登録し、
// 表示の出し分けは hideToolsMiddleware が担う。
func registerToolSearch(srv *mcp.Server, serverName string, catalog *toolsearch.Catalog, cfg config.ToolSearchConfig) {
	srv.AddTool(toolSearchDef(serverName, cfg, catalog), func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctx = trace.StartSpan(ctx, "mcpsrv/registerToolSearch/Handler", attribute.String("server-name", serverName))
		// 検索エラー（不正な method / 不正な regexp パターンなど）は tools/call のプロトコル
		// エラーではなく tool error（CallToolResult.IsError）として返す必要があるため、
		// ここでの traceErr はトレース用のローカル変数に留め、named return は使わない
		// （named return を使うと defer 内での代入がハンドラの実際の戻り値エラーになってしまい、
		// ToolHandler の契約上プロトコルエラー扱いになるため）。
		var traceErr error
		defer func() { trace.EndSpan(ctx, traceErr) }()

		var args toolSearchArgs
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			traceErr = err
			resp := &mcp.CallToolResult{}
			resp.SetError(err)
			return resp, nil
		}

		limit := args.Limit
		if limit <= 0 {
			limit = cfg.DefaultLimit
		}

		defs, err := catalog.Search(serverName, args.Query, toolsearch.Method(args.Method), limit)
		var result mcp.CallToolResult
		if err != nil {
			traceErr = err
			result.SetError(err)
			return &result, nil
		}

		formatted, err := toolsearch.FormatResults(toolsearch.ResultFormat(cfg.ResultFormat), defs)
		if err != nil {
			traceErr = err
			result.SetError(err)
			return &result, nil
		}

		data, err := json.MarshalIndent(formatted, "", "  ")
		if err != nil {
			traceErr = err
			result.SetError(err)
			return &result, nil
		}
		result.Content = []mcp.Content{&mcp.TextContent{Text: string(data)}}
		result.StructuredContent = json.RawMessage(data)
		return &result, nil
	})
}

// hideToolsMiddleware は tools/list をインターセプトし、catalog の合計ツール数が閾値を
// 超えている場合は tool_search のみを返し（next は呼ばない）、超えていない場合は next の
// 結果から tool_search を除外して返す。tools/call はどちらの状態でも next にそのまま委譲する
// ため、非表示中の実ツールも呼び出せる。
func hideToolsMiddleware(serverName string, catalog *toolsearch.Catalog, cfg config.ToolSearchConfig) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != methodListTools {
				return next(ctx, method, req)
			}

			if catalog.Total() > cfg.Threshold {
				return &mcp.ListToolsResult{Tools: []*mcp.Tool{toolSearchDef(serverName, cfg, catalog)}}, nil
			}

			result, err := next(ctx, method, req)
			if err != nil {
				return result, err
			}
			ltr, ok := result.(*mcp.ListToolsResult)
			if !ok {
				return result, nil
			}
			ltr.Tools = filterOutToolSearch(ltr.Tools)
			return ltr, nil
		}
	}
}

// filterOutToolSearch は tools 一覧から合成ツール tool_search を取り除く。
func filterOutToolSearch(tools []*mcp.Tool) []*mcp.Tool {
	filtered := make([]*mcp.Tool, 0, len(tools))
	for _, t := range tools {
		if t.Name == toolSearchName {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}
