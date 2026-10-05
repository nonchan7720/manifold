package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/toolsearch"
	"go.opentelemetry.io/otel/attribute"
)

// ToolSearchName is the synthetic tool that replaces an endpoint's tools/list
// once the caller can see more than gateway.toolSearch.threshold tools there.
const ToolSearchName = "tool_search"

// digestDescriptionMaxRunes caps each tool's description in the tool_search
// description digest (in runes, so CJK text is cut between characters).
const digestDescriptionMaxRunes = 200

// maxToolSearchListPages bounds how many tools/list pages the middleware reads
// from the inner handler, so a backend returning cursors forever can't loop
// the gateway.
const maxToolSearchListPages = 100

// toolSearchArgs は tool_search 呼び出しの引数。
type toolSearchArgs struct {
	Query  string `json:"query"`
	Method string `json:"method"`
	Limit  int    `json:"limit"`
}

// toolSearchDef builds the tool_search definition for serverName. docs are the
// tools the caller can see; their names and descriptions go into the
// description digest so the model knows what it can search for.
func toolSearchDef(
	serverName string, cfg config.ToolSearchConfig, docs []toolsearch.ToolDef,
) *mcp.Tool {
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
	description += toolSearchDigest(cfg, docs)
	return &mcp.Tool{
		Name:        ToolSearchName,
		Description: description,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type": "string",
					"description": "Search text matched against tool names, descriptions, " +
						"argument names, and argument descriptions.",
				},
				"method": map[string]any{
					"type": "string",
					"enum": []string{
						string(toolsearch.MethodBM25),
						string(toolsearch.MethodRegexp),
						string(toolsearch.MethodFuzzy),
					},
					"default": string(toolsearch.MethodBM25),
					"description": "Search algorithm: bm25 (default, ranked full-text), " +
						"regexp (case-insensitive pattern match), or fuzzy (subsequence match).",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results to return.",
				},
			},
			"required": []string{"query"},
		},
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}
}

// toolSearchDigest builds the human-readable digest appended to tool_search's
// description: one "- name: description" line per visible tool (just "- name"
// without a description), sorted by name, each description cut at
// digestDescriptionMaxRunes. With cfg.DigestMaxTools > 0 and fewer than
// len(docs), only the first N are listed and the header says so. Empty docs
// give "".
//
// 出力例（省略なし）:
//
//	 This endpoint currently has 3 searchable tools:
//	- addPet: Add a new pet to the store
//	- deletePet: Delete a pet
//	- createOrder
func toolSearchDigest(cfg config.ToolSearchConfig, docs []toolsearch.ToolDef) string {
	entries := toolsearch.Digest(docs)
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
		fmt.Fprintf(&b, " This endpoint currently has %d searchable %s (showing first %d):",
			total, unit, len(shown))
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

// truncateRunes cuts s to at most maxRunes runes, appending "..." when it did.
func truncateRunes(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "..."
}

// listVisibleTools reads every tools/list page from next — the handler chain
// inside tool search, so the tool filter and authz have already been applied —
// and returns the first page (for its Cacheable and Meta) and all tools. A
// backend still handing out cursors after maxToolSearchListPages pages is an
// error rather than a silently truncated list, since the threshold, the
// digest and the search would all miss the tools beyond it.
func listVisibleTools(
	ctx context.Context, next mcp.MethodHandler, req mcp.Request,
) (*mcp.ListToolsResult, []*mcp.Tool, error) {
	session, _ := req.GetSession().(*mcp.ServerSession)
	var (
		first  *mcp.ListToolsResult
		tools  []*mcp.Tool
		cursor string
	)
	complete := false
	for range maxToolSearchListPages {
		res, err := next(ctx, authzMethodToolsList, &mcp.ListToolsRequest{
			Session: session,
			Params:  &mcp.ListToolsParams{Cursor: cursor},
			Extra:   req.GetExtra(),
		})
		if err != nil {
			return nil, nil, err
		}
		result, ok := res.(*mcp.ListToolsResult)
		if !ok {
			return nil, nil, fmt.Errorf("unexpected tools/list result %T", res)
		}
		if first == nil {
			first = result
		}
		tools = append(tools, result.Tools...)
		if result.NextCursor == "" {
			complete = true
			break
		}
		cursor = result.NextCursor
	}
	if !complete {
		return nil, nil, fmt.Errorf(
			"tools/list did not finish within %d pages", maxToolSearchListPages,
		)
	}
	return first, tools, nil
}

// dropReservedTool hides a backend tool named tool_search: the synthetic tool
// takes that name, and a tools/call for it never reaches the backend. It logs
// a WARN once per collision (see toolSearchIndexes.reservedWarned).
func (c *toolSearchIndexes) dropReservedTool(ctx context.Context, tools []*mcp.Tool) []*mcp.Tool {
	out := tools[:0:0]
	for _, tool := range tools {
		if tool.Name == ToolSearchName {
			if c.reservedWarned.CompareAndSwap(false, true) {
				slog.WarnContext(ctx,
					"upstream tool name collides with the synthetic tool_search; hiding it",
					slog.String("server", c.serverName))
			}
			continue
		}
		out = append(out, tool)
	}
	return out
}

func toolDefs(tools []*mcp.Tool) []toolsearch.ToolDef {
	docs := make([]toolsearch.ToolDef, len(tools))
	for i, tool := range tools {
		docs[i] = toolsearch.ToolDef{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		}
	}
	return docs
}

func toolErrorResult(err error) *mcp.CallToolResult {
	res := &mcp.CallToolResult{}
	res.SetError(err)
	return res
}

// handleToolSearchList answers tools/list: only tool_search when the caller
// can see more than cfg.Threshold tools, otherwise what the inner handler
// returns, unchanged but for a backend tool named tool_search. Pagination is
// preserved below the threshold: a request carrying a cursor is forwarded
// as is, and one without gets the inner first page with its NextCursor. The
// tools are counted (and digested) from indexes, so within its TTL a caller's
// repeated tools/list doesn't read every inner page again.
func handleToolSearchList(
	ctx context.Context,
	serverName string,
	cfg config.ToolSearchConfig,
	next mcp.MethodHandler,
	indexes *toolSearchIndexes,
	req mcp.Request,
) (mcp.Result, error) {
	if params, ok := req.GetParams().(*mcp.ListToolsParams); ok && params != nil &&
		params.Cursor != "" {
		// Only a page of a list we passed through hands out cursors, so the
		// client is already paging through the real tools.
		res, err := next(ctx, authzMethodToolsList, req)
		if err != nil {
			return nil, err
		}
		if page, ok := res.(*mcp.ListToolsResult); ok {
			out := *page
			out.Tools = indexes.dropReservedTool(ctx, page.Tools)
			if out.Tools == nil {
				out.Tools = []*mcp.Tool{} // avoid JSON null
			}
			normalizeCacheable(&out.Cacheable)
			return &out, nil
		}
		return res, nil
	}
	snap, err := indexes.snapshotFor(ctx, next, req)
	if err != nil {
		return nil, err
	}
	if docs := snap.index.Docs(); len(docs) > cfg.Threshold {
		res := &mcp.ListToolsResult{
			Tools: []*mcp.Tool{toolSearchDef(serverName, cfg, docs)},
		}
		normalizeCacheable(&res.Cacheable)
		return res, nil
	}
	out := *snap.first
	out.Tools = indexes.dropReservedTool(ctx, snap.first.Tools)
	if out.Tools == nil {
		out.Tools = []*mcp.Tool{} // avoid JSON null
	}
	normalizeCacheable(&out.Cacheable)
	return &out, nil
}

// handleToolSearchCall answers a tools/call of tool_search by searching the
// tools the caller can see (read through indexes, which reuses them for a
// short while per caller). Search errors (unknown method, bad regexp) are
// tool errors; an error from the inner tools/list (e.g. authz denying the
// caller) is returned as is.
func handleToolSearchCall(
	ctx context.Context,
	serverName string,
	cfg config.ToolSearchConfig,
	next mcp.MethodHandler,
	indexes *toolSearchIndexes,
	req mcp.Request,
	params *mcp.CallToolParamsRaw,
) (_ mcp.Result, rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/toolSearch/Handler",
		attribute.String("server-name", serverName))
	// 検索エラーは tool error（IsError）で返すため、span に記録するエラーと
	// ハンドラの戻り値のエラーを分ける。
	var traceErr error
	defer func() { trace.EndSpan(ctx, traceErr) }()

	var args toolSearchArgs
	if len(params.Arguments) > 0 {
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			traceErr = err
			return toolErrorResult(fmt.Errorf("invalid arguments: %w", err)), nil
		}
	}
	limit := args.Limit
	if limit <= 0 {
		limit = cfg.DefaultLimit
	}

	snap, err := indexes.snapshotFor(ctx, next, req)
	if err != nil {
		traceErr = err
		return nil, err
	}

	defs, err := snap.index.Search(args.Query, toolsearch.Method(args.Method), limit)
	if err != nil {
		traceErr = err
		return toolErrorResult(err), nil
	}
	formatted, err := toolsearch.FormatResults(toolsearch.ResultFormat(cfg.ResultFormat), defs)
	if err != nil {
		traceErr = err
		return toolErrorResult(err), nil
	}
	data, err := json.MarshalIndent(formatted, "", "  ")
	if err != nil {
		traceErr = err
		return toolErrorResult(err), nil
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(data)}},
		StructuredContent: json.RawMessage(data),
	}, nil
}

// newToolSearchMiddleware returns the tool_search middleware for serverName
// (cfg's zero fields take their defaults), or nil unless cfg.Enabled: without
// it tools/list passes through untouched and a tools/call of tool_search
// reaches the backend like any other name. It sits outside authz and the tool
// filter, so everything it lists, counts and searches is what the caller may
// see; a tools/call for any other tool, hidden or not, passes through to the
// same authz and audit as before. toolCache (may be nil) is only consulted for
// its invalidation generations, see toolSearchIndexes. cfg is used as is: it
// comes from config.Load, which applies the defaults (ToolSearchConfig.WithDefaults). authzKey (may be nil
// when no authz sits inside) keys the index cache by the authz principal.
func newToolSearchMiddleware(
	serverName string, cfg config.ToolSearchConfig, toolCache *ToolCache, authzKey AuthzCacheKeyer,
) mcp.Middleware {
	if !cfg.IsEnabled() {
		return nil
	}
	indexes := newToolSearchIndexes(serverName, toolCache, authzKey)
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case authzMethodToolsList:
				return handleToolSearchList(ctx, serverName, cfg, next, indexes, req)
			case authzMethodToolsCall:
				params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
				if !ok || params.Name != ToolSearchName {
					return next(ctx, method, req)
				}
				return handleToolSearchCall(ctx, serverName, cfg, next, indexes, req, params)
			default:
				return next(ctx, method, req)
			}
		}
	}
}
