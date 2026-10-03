package mcpsrv

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
)

// Meta tools exposed in place of the real tools when tool search is enabled.
const (
	ToolSearchToolName = "search_tools"
	ToolCallToolName   = "call_tool"
)

// maxToolSearchListPages bounds how many tools/list pages search_tools reads
// from the inner handler, so a backend returning cursors forever can't loop
// the gateway.
const maxToolSearchListPages = 100

// toolSearchResult is one search_tools hit.
type toolSearchResult struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"inputSchema,omitempty"`
}

type toolSearchOutput struct {
	Tools []toolSearchResult `json:"tools"`
	// Total は検索条件に一致したツールの総数（limit で切り詰める前）。
	Total int `json:"total"`
}

type toolSearchArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type toolCallArgs struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func toolSearchMetaTools(maxResults int) []*mcp.Tool {
	return []*mcp.Tool{
		{
			Name: ToolSearchToolName,
			Description: "Search the tools available on this server by keyword. " +
				"Returns matching tools with their input schema. " +
				"Call a returned tool with " + ToolCallToolName + ".",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"query": {
						Type: "string",
						Description: "Keywords describing what you want to do " +
							"(matched against tool names and descriptions). Empty lists every tool.",
					},
					"limit": {
						Type: "integer",
						Description: fmt.Sprintf(
							"Maximum number of tools to return (default %d).", maxResults,
						),
					},
				},
			},
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		},
		{
			Name: ToolCallToolName,
			Description: "Call a tool found with " + ToolSearchToolName +
				". Pass the tool's name and its arguments as an object matching its inputSchema.",
			InputSchema: &jsonschema.Schema{
				Type:     "object",
				Required: []string{"name"},
				Properties: map[string]*jsonschema.Schema{
					"name": {Type: "string", Description: "Name of the tool to call."},
					"arguments": {
						Type:        "object",
						Description: "Arguments for the tool, matching its inputSchema.",
					},
				},
			},
		},
	}
}

// searchTokens splits s into lower-cased words of letters and digits; '_',
// '-', '.' and camelCase boundaries all separate words.
func searchTokens(s string) []string {
	var (
		tokens []string
		cur    []rune
		prev   rune
	)
	flush := func() {
		if len(cur) > 0 {
			tokens = append(tokens, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if unicode.IsUpper(r) && unicode.IsLower(prev) {
				flush()
			}
			cur = append(cur, r)
		default:
			flush()
		}
		prev = r
	}
	flush()
	return tokens
}

// scoreTool ranks tool against the query tokens: a token found in the name
// counts more than one found only in the title or description. 0 means no
// query token matched.
func scoreTool(tool *mcp.Tool, queryTokens []string) int {
	name := strings.ToLower(tool.Name)
	text := strings.ToLower(tool.Title + " " + tool.Description)
	score := 0
	for _, token := range queryTokens {
		switch {
		case strings.Contains(name, token):
			score += 3
		case strings.Contains(text, token):
			score++
		}
	}
	return score
}

// searchTools returns the tools matching query, best match first (ties by
// name), and the number of matches before limit is applied.
func searchTools(tools []*mcp.Tool, query string, limit int) ([]*mcp.Tool, int) {
	queryTokens := searchTokens(query)
	type scored struct {
		tool  *mcp.Tool
		score int
	}
	var hits []scored
	for _, tool := range tools {
		if len(queryTokens) == 0 {
			hits = append(hits, scored{tool: tool})
			continue
		}
		if s := scoreTool(tool, queryTokens); s > 0 {
			hits = append(hits, scored{tool: tool, score: s})
		}
	}
	slices.SortStableFunc(hits, func(a, b scored) int {
		if c := cmp.Compare(b.score, a.score); c != 0 {
			return c
		}
		return strings.Compare(a.tool.Name, b.tool.Name)
	})
	total := len(hits)
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]*mcp.Tool, len(hits))
	for i, hit := range hits {
		out[i] = hit.tool
	}
	return out, total
}

// listAllTools reads every tools/list page from next.
func listAllTools(
	ctx context.Context, next mcp.MethodHandler, req mcp.Request,
) ([]*mcp.Tool, error) {
	session, _ := req.GetSession().(*mcp.ServerSession)
	var (
		tools  []*mcp.Tool
		cursor string
	)
	for range maxToolSearchListPages {
		res, err := next(ctx, authzMethodToolsList, &mcp.ListToolsRequest{
			Session: session,
			Params:  &mcp.ListToolsParams{Cursor: cursor},
			Extra:   req.GetExtra(),
		})
		if err != nil {
			return nil, err
		}
		result, ok := res.(*mcp.ListToolsResult)
		if !ok {
			return nil, fmt.Errorf("unexpected tools/list result %T", res)
		}
		tools = append(tools, result.Tools...)
		if result.NextCursor == "" {
			return tools, nil
		}
		cursor = result.NextCursor
	}
	return tools, nil
}

func toolSearchErrorResult(err error) *mcp.CallToolResult {
	res := &mcp.CallToolResult{}
	res.SetError(err)
	return res
}

func handleSearchTools(
	ctx context.Context,
	next mcp.MethodHandler,
	req mcp.Request,
	params *mcp.CallToolParamsRaw,
	maxResults int,
) (mcp.Result, error) {
	var args toolSearchArgs
	if len(params.Arguments) > 0 {
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			return toolSearchErrorResult(fmt.Errorf("invalid arguments: %w", err)), nil
		}
	}
	limit := args.Limit
	if limit <= 0 {
		limit = maxResults
	}
	tools, err := listAllTools(ctx, next, req)
	if err != nil {
		return nil, err
	}
	hits, total := searchTools(tools, args.Query, limit)
	out := toolSearchOutput{Tools: make([]toolSearchResult, len(hits)), Total: total}
	for i, tool := range hits {
		out.Tools[i] = toolSearchResult{
			Name:        tool.Name,
			Title:       tool.Title,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		}
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(body)}},
		StructuredContent: json.RawMessage(body),
	}, nil
}

func handleCallTool(
	ctx context.Context,
	next mcp.MethodHandler,
	req mcp.Request,
	params *mcp.CallToolParamsRaw,
) (mcp.Result, error) {
	var args toolCallArgs
	if err := json.Unmarshal(params.Arguments, &args); err != nil || args.Name == "" {
		return nil, &jsonrpc.Error{
			Code:    jsonrpc.CodeInvalidParams,
			Message: ToolCallToolName + ` requires {"name": "<tool>", "arguments": {...}}`,
		}
	}
	if args.Name == ToolSearchToolName || args.Name == ToolCallToolName {
		return nil, unknownToolError(args.Name)
	}
	inner := withToolName(req, args.Name)
	innerParams, _ := inner.GetParams().(*mcp.CallToolParamsRaw)
	innerParams.Arguments = args.Arguments
	if len(innerParams.Arguments) == 0 || string(innerParams.Arguments) == "null" {
		innerParams.Arguments = json.RawMessage("{}")
	}
	return next(ctx, authzMethodToolsCall, inner)
}

// newToolSearchMiddleware returns the lazy-loading middleware for cfg, or nil
// when tool search is disabled. tools/list returns only search_tools and
// call_tool; search_tools reads the inner tools/list (so it only ever finds
// tools the caller may see), and call_tool forwards to the inner tools/call
// (so it is authorized and audited like a direct call). A tools/call naming a
// real tool directly is forwarded as is.
//
// It must be the outermost middleware of the server it is added to.
func newToolSearchMiddleware(cfg *config.ToolSearchConfig) mcp.Middleware {
	if !cfg.IsEnabled() {
		return nil
	}
	maxResults := cfg.MaxResultsOrDefault()
	metaTools := toolSearchMetaTools(maxResults)
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case authzMethodToolsList:
				res := &mcp.ListToolsResult{Tools: metaTools}
				normalizeCacheable(&res.Cacheable)
				return res, nil
			case authzMethodToolsCall:
				params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
				if !ok {
					return next(ctx, method, req)
				}
				switch params.Name {
				case ToolSearchToolName:
					return handleSearchTools(ctx, next, req, params, maxResults)
				case ToolCallToolName:
					return handleCallTool(ctx, next, req, params)
				}
				return next(ctx, method, req)
			default:
				return next(ctx, method, req)
			}
		}
	}
}
