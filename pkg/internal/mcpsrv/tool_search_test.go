package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/nonchan7720/manifold/pkg/internal/toolsearch"
	"github.com/stretchr/testify/require"
)

type searchTool struct{ name, desc string }

// newToolSearchTestServer builds a server with the given tools and the full
// ServerToolMiddlewares chain (inner is the authz slot).
func newToolSearchTestServer(
	t *testing.T,
	cfg config.ToolSearchConfig,
	tools *config.ToolsConfig,
	inner []mcp.Middleware,
	audit *AuditLogger,
	defs ...searchTool,
) *mcp.Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "petstore", Version: "test"}, nil)
	for _, d := range defs {
		addSearchTool(srv, d.name, d.desc)
	}
	var server *config.Server
	if tools != nil {
		server = &config.Server{Name: "petstore", Tools: tools}
	}
	srv.AddReceivingMiddleware(
		ServerToolMiddlewares("petstore", server, inner, nil, nil, audit, cfg)...)
	return srv
}

func addSearchTool(srv *mcp.Server, name, desc string) {
	srv.AddTool(
		&mcp.Tool{Name: name, Description: desc, InputSchema: map[string]any{"type": "object"}},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "ok:" + req.Params.Name}},
			}, nil
		},
	)
}

// allowOnlyMiddleware is an authz stand-in: tools/list is narrowed to allowed
// and a tools/call of anything else is denied.
func allowOnlyMiddleware(allowed ...string) mcp.Middleware {
	set := map[string]bool{}
	for _, name := range allowed {
		set[name] = true
	}
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case authzMethodToolsCall:
				params, _ := req.GetParams().(*mcp.CallToolParamsRaw)
				if params == nil || !set[params.Name] {
					return nil, errToolNotAllowedByPolicy
				}
			case authzMethodToolsList:
				res, err := next(ctx, method, req)
				if err != nil {
					return nil, err
				}
				result, _ := res.(*mcp.ListToolsResult)
				filtered := make([]*mcp.Tool, 0, len(result.Tools))
				for _, tool := range result.Tools {
					if set[tool.Name] {
						filtered = append(filtered, tool)
					}
				}
				result.Tools = filtered
				return result, nil
			}
			return next(ctx, method, req)
		}
	}
}

// denyAllMiddleware mimics authz for a caller without an identity.
func denyAllMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method == authzMethodToolsCall || method == authzMethodToolsList {
			return nil, errToolNotAllowedByPolicy
		}
		return next(ctx, method, req)
	}
}

func callToolSearch(t *testing.T, cs *mcp.ClientSession, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: ToolSearchName, Arguments: args})
	require.NoError(t, err)
	return res
}

func searchResultNames(t *testing.T, res *mcp.CallToolResult) []string {
	t.Helper()
	require.False(t, res.IsError)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var defs []toolsearch.ToolDef
	require.NoError(t, json.Unmarshal([]byte(text.Text), &defs))
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	// StructuredContent にも同じ内容がラウンドトリップしていること
	structured, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var structuredDefs []toolsearch.ToolDef
	require.NoError(t, json.Unmarshal(structured, &structuredDefs))
	require.Len(t, structuredDefs, len(defs))
	return names
}

var petTools = []searchTool{
	{"list_pets", "list pet inventory"},
	{"get_pet", "get a pet by id"},
	{"cancel_pet", "cancel a pet order"},
}

// 既定（enabled 未設定）では tool_search は無効で、閾値を超えても tools/list はそのまま。
func TestToolSearch_DisabledByDefault_PassesThrough(t *testing.T) {
	srv := newToolSearchTestServer(
		t, config.ToolSearchConfig{Threshold: 1}, nil, nil, nil, petTools...,
	)
	cs := connectTestClient(t, t.Context(), srv)

	require.ElementsMatch(
		t, []string{"list_pets", "get_pet", "cancel_pet"}, sessionToolNames(t, cs),
	)
	// tool_search は登録されていない
	_, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name: ToolSearchName, Arguments: map[string]any{"query": "pet"},
	})
	require.ErrorContains(t, err, "unknown tool")
	require.Nil(t, newToolSearchMiddleware("petstore", config.ToolSearchConfig{}, nil, nil))
	require.NotNil(
		t,
		newToolSearchMiddleware("petstore", config.ToolSearchConfig{Enabled: true}, nil, nil),
	)
}

func TestToolSearch_BelowThreshold_RealToolsVisible(t *testing.T) {
	srv := newToolSearchTestServer(
		t,
		config.ToolSearchConfig{Enabled: true, Threshold: 100},
		nil,
		nil,
		nil,
		petTools...)
	cs := connectTestClient(t, t.Context(), srv)
	names := sessionToolNames(t, cs)
	require.ElementsMatch(t, []string{"list_pets", "get_pet", "cancel_pet"}, names)
	require.NotContains(t, names, ToolSearchName)
}

func TestToolSearch_AboveThreshold_OnlyToolSearchVisible_HiddenToolsCallable(t *testing.T) {
	srv := newToolSearchTestServer(
		t,
		config.ToolSearchConfig{Enabled: true, Threshold: 1},
		nil,
		nil,
		nil,
		petTools...)
	cs := connectTestClient(t, t.Context(), srv)

	res, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, res.Tools, 1)
	require.Equal(t, ToolSearchName, res.Tools[0].Name)
	require.Empty(t, res.NextCursor)
	require.Contains(t, res.Tools[0].Description, "3 searchable tools:")
	require.Contains(t, res.Tools[0].Description, "- list_pets: list pet inventory")

	require.Equal(t, "ok:list_pets", callText(t, cs, "list_pets", map[string]any{}))
}

func TestToolSearch_DefaultMethodBM25_RoundTripsDefinitions(t *testing.T) {
	srv := newToolSearchTestServer(
		t,
		config.ToolSearchConfig{Enabled: true, Threshold: 1},
		nil,
		nil,
		nil,
		petTools...)
	cs := connectTestClient(t, t.Context(), srv)
	names := searchResultNames(t, callToolSearch(t, cs, map[string]any{"query": "pet"}))
	require.ElementsMatch(t, []string{"list_pets", "get_pet", "cancel_pet"}, names)
}

func TestToolSearch_MethodAndLimit(t *testing.T) {
	tests := []struct {
		name  string
		args  map[string]any
		want  []string
		limit int
	}{
		{
			name: "regexp",
			args: map[string]any{"query": "^get_", "method": "regexp"},
			want: []string{"get_pet"},
		},
		{
			name: "fuzzy",
			args: map[string]any{"query": "listpets", "method": "fuzzy"},
			want: []string{"list_pets"},
		},
		{
			name:  "limit",
			args:  map[string]any{"query": "pet", "method": "regexp", "limit": 1},
			limit: 1,
		},
		{name: "no match", args: map[string]any{"query": "nomatchquery"}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newToolSearchTestServer(
				t,
				config.ToolSearchConfig{Enabled: true, Threshold: 1},
				nil,
				nil,
				nil,
				petTools...)
			cs := connectTestClient(t, t.Context(), srv)
			names := searchResultNames(t, callToolSearch(t, cs, tt.args))
			if tt.limit > 0 {
				require.Len(t, names, tt.limit)
				return
			}
			require.ElementsMatch(t, tt.want, names)
		})
	}
}

func TestToolSearch_NoMatch_ReturnsEmptyArrayJSON(t *testing.T) {
	for _, format := range []string{config.ToolSearchResultFormatDefault, config.ToolSearchResultFormatClaude} {
		for _, method := range []string{"bm25", "regexp", "fuzzy"} {
			t.Run(format+"_"+method, func(t *testing.T) {
				cfg := config.ToolSearchConfig{Enabled: true, Threshold: 1, ResultFormat: format}
				srv := newToolSearchTestServer(t, cfg, nil, nil, nil, petTools...)
				cs := connectTestClient(t, t.Context(), srv)
				res := callToolSearch(
					t,
					cs,
					map[string]any{"query": "nomatchquery", "method": method},
				)
				require.False(t, res.IsError)
				require.JSONEq(t, "[]", res.Content[0].(*mcp.TextContent).Text)
				structured, err := json.Marshal(res.StructuredContent)
				require.NoError(t, err)
				require.JSONEq(t, "[]", string(structured))
			})
		}
	}
}

func TestToolSearch_ClaudeFormat_ReturnsToolReferenceBlocks(t *testing.T) {
	cfg := config.ToolSearchConfig{
		Enabled: true, Threshold: 1, ResultFormat: config.ToolSearchResultFormatClaude,
	}
	srv := newToolSearchTestServer(t, cfg, nil, nil, nil, petTools...)
	cs := connectTestClient(t, t.Context(), srv)

	list, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Contains(t, list.Tools[0].Description, "tool_reference")

	res := callToolSearch(t, cs, map[string]any{"query": "pet"})
	require.False(t, res.IsError)
	var refs []toolsearch.ToolReference
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &refs))
	require.Len(t, refs, 3)
	for _, r := range refs {
		require.Equal(t, "tool_reference", r.Type)
	}
}

func TestToolSearch_RegexpInvalidPattern_ReturnsToolError(t *testing.T) {
	srv := newToolSearchTestServer(
		t,
		config.ToolSearchConfig{Enabled: true, Threshold: 1},
		nil,
		nil,
		nil,
		petTools...)
	cs := connectTestClient(t, t.Context(), srv)
	res := callToolSearch(t, cs, map[string]any{"query": "(unterminated", "method": "regexp"})
	require.True(t, res.IsError)
	res = callToolSearch(t, cs, map[string]any{"query": "pet", "method": "nope"})
	require.True(t, res.IsError)
}

func TestToolSearch_UpstreamToolSearchIsHidden(t *testing.T) {
	tools := append([]searchTool{{ToolSearchName, "upstream tool_search"}}, petTools[:1]...)
	srv := newToolSearchTestServer(
		t,
		config.ToolSearchConfig{Enabled: true, Threshold: 100},
		nil,
		nil,
		nil,
		tools...)
	cs := connectTestClient(t, t.Context(), srv)
	require.Equal(t, []string{"list_pets"}, sessionToolNames(t, cs))
	// 名前が衝突しても合成ツールの方が呼ばれる
	names := searchResultNames(t, callToolSearch(t, cs, map[string]any{"query": "pet"}))
	require.Equal(t, []string{"list_pets"}, names)
}

// --- policy (authz) ---

func TestToolSearch_Authz_OnlyVisibleToolsAreCountedSearchedAndDigested(t *testing.T) {
	var buf bytes.Buffer
	audit := newAuditLoggerTo(&buf, config.AuthzHeaders{}, false)
	srv := newToolSearchTestServer(
		t, config.ToolSearchConfig{Enabled: true, Threshold: 1}, nil,
		[]mcp.Middleware{allowOnlyMiddleware("list_pets", "get_pet")}, audit, petTools...,
	)
	cs := connectTestClient(t, contexts.ToRequestAuthHeader(t.Context(), "alice"), srv)

	list, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, list.Tools, 1)
	require.Equal(t, ToolSearchName, list.Tools[0].Name)
	require.Contains(t, list.Tools[0].Description, "2 searchable tools:")
	require.NotContains(t, list.Tools[0].Description, "cancel_pet")

	names := searchResultNames(t, callToolSearch(t, cs, map[string]any{"query": "pet"}))
	require.ElementsMatch(t, []string{"list_pets", "get_pet"}, names)

	// 隠れているツールの呼び出しもポリシーを通る
	require.Equal(t, "ok:get_pet", callText(t, cs, "get_pet", map[string]any{}))
	_, err = cs.CallTool(
		t.Context(),
		&mcp.CallToolParams{Name: "cancel_pet", Arguments: map[string]any{}},
	)
	require.Error(t, err)

	// tool_search の呼び出しも監査ログに残る
	var tools []string
	for _, line := range decodeAuditLines(t, &buf) {
		tools = append(tools, line["tool"].(string))
	}
	require.Equal(t, []string{ToolSearchName, "get_pet", "cancel_pet"}, tools)
}

func TestToolSearch_Authz_FewVisibleTools_StaysBelowThreshold(t *testing.T) {
	srv := newToolSearchTestServer(
		t, config.ToolSearchConfig{Enabled: true, Threshold: 1}, nil,
		[]mcp.Middleware{allowOnlyMiddleware("list_pets")}, nil, petTools...,
	)
	cs := connectTestClient(t, t.Context(), srv)
	// 3 ツール登録されていても見えるのは 1 つなので閾値を超えず、実ツールが見える
	require.Equal(t, []string{"list_pets"}, sessionToolNames(t, cs))
}

func TestToolSearch_Authz_DeniedCaller_CannotSearch(t *testing.T) {
	srv := newToolSearchTestServer(
		t,
		config.ToolSearchConfig{Enabled: true, Threshold: 1},
		nil,
		[]mcp.Middleware{denyAllMiddleware},
		nil,
		petTools...,
	)
	cs := connectTestClient(t, t.Context(), srv)
	_, err := cs.ListTools(t.Context(), nil)
	require.Error(t, err)
	_, err = cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name: ToolSearchName, Arguments: map[string]any{"query": "pet"},
	})
	require.ErrorContains(t, err, "not allowed by policy")
}

func TestToolSearch_SearchesExposedNamesAfterToolFilter(t *testing.T) {
	tools := &config.ToolsConfig{
		Exclude:   []string{"cancel_pet"},
		Overrides: map[string]config.ToolOverride{"get_pet": {Name: "fetch_pet"}},
	}
	srv := newToolSearchTestServer(
		t,
		config.ToolSearchConfig{Enabled: true, Threshold: 1},
		tools,
		nil,
		nil,
		petTools...)
	cs := connectTestClient(t, t.Context(), srv)
	names := searchResultNames(t, callToolSearch(t, cs, map[string]any{"query": "pet"}))
	require.ElementsMatch(t, []string{"list_pets", "fetch_pet"}, names)
	require.Equal(t, "ok:get_pet", callText(t, cs, "fetch_pet", map[string]any{}))
}

func TestToolSearch_NewToolsShowUpWithoutRestart(t *testing.T) {
	srv := newToolSearchTestServer(
		t,
		config.ToolSearchConfig{Enabled: true, Threshold: 5},
		nil,
		nil,
		nil,
		petTools[:2]...)
	cs := connectTestClient(t, t.Context(), srv)
	require.Len(t, sessionToolNames(t, cs), 2)

	for _, name := range []string{"a", "b", "c", "d"} {
		addSearchTool(srv, "extra_"+name, "extra")
	}
	list, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, list.Tools, 1)
	require.Equal(t, ToolSearchName, list.Tools[0].Name)
	require.Contains(t, list.Tools[0].Description, "6 searchable tools:")
}

// pagedListHandler is an inner tools/list handler serving names pageSize at a
// time with numeric cursors, counting its calls.
func pagedListHandler(names []string, pageSize int, calls *int) mcp.MethodHandler {
	return func(_ context.Context, _ string, req mcp.Request) (mcp.Result, error) {
		*calls++
		start := 0
		if params, _ := req.GetParams().(*mcp.ListToolsParams); params != nil &&
			params.Cursor != "" {
			n, err := strconv.Atoi(params.Cursor)
			if err != nil {
				return nil, err
			}
			start = n
		}
		end := min(start+pageSize, len(names))
		res := &mcp.ListToolsResult{}
		for _, name := range names[start:end] {
			res.Tools = append(res.Tools, &mcp.Tool{
				Name: name, Description: name, InputSchema: map[string]any{"type": "object"},
			})
		}
		if end < len(names) {
			res.NextCursor = strconv.Itoa(end)
		}
		return res, nil
	}
}

func listToolsViaMiddleware(
	t *testing.T, h mcp.MethodHandler, cursor string,
) *mcp.ListToolsResult {
	t.Helper()
	res, err := h(t.Context(), authzMethodToolsList, &mcp.ListToolsRequest{
		Params: &mcp.ListToolsParams{Cursor: cursor},
	})
	require.NoError(t, err)
	list, ok := res.(*mcp.ListToolsResult)
	require.True(t, ok)
	return list
}

// 閾値以下ではクライアントの cursor を無視せず、ページングをそのまま保つ。
func TestToolSearch_BelowThreshold_PreservesPagination(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e"}
	calls := 0
	h := newToolSearchMiddleware(
		"petstore", config.ToolSearchConfig{Enabled: true, Threshold: 10}, nil, nil,
	)(pagedListHandler(names, 2, &calls))

	page1 := listToolsViaMiddleware(t, h, "")
	require.Equal(t, []string{"a", "b"}, toolNames(page1.Tools))
	require.Equal(t, "2", page1.NextCursor)

	// cursor 付きは内部ハンドラへそのまま転送される（全ページを読み直さない）。
	calls = 0
	page2 := listToolsViaMiddleware(t, h, page1.NextCursor)
	require.Equal(t, []string{"c", "d"}, toolNames(page2.Tools))
	require.Equal(t, "4", page2.NextCursor)
	require.Equal(t, 1, calls)

	page3 := listToolsViaMiddleware(t, h, page2.NextCursor)
	require.Equal(t, []string{"e"}, toolNames(page3.Tools))
	require.Empty(t, page3.NextCursor)
}

// 閾値を超えたら cursor なしの tools/list は tool_search のみ（NextCursor なし）。
func TestToolSearch_AboveThreshold_SingleToolSearchPage(t *testing.T) {
	calls := 0
	h := newToolSearchMiddleware(
		"petstore", config.ToolSearchConfig{Enabled: true, Threshold: 3}, nil, nil,
	)(pagedListHandler([]string{"a", "b", "c", "d", "e"}, 2, &calls))

	res := listToolsViaMiddleware(t, h, "")
	require.Equal(t, []string{ToolSearchName}, toolNames(res.Tools))
	require.Empty(t, res.NextCursor)
}

// 閾値以下でも、バックエンドの tool_search は隠す（どのページでも）。
func TestToolSearch_BelowThreshold_PagedUpstreamToolSearchIsHidden(t *testing.T) {
	calls := 0
	h := newToolSearchMiddleware(
		"petstore", config.ToolSearchConfig{Enabled: true, Threshold: 10}, nil, nil,
	)(pagedListHandler([]string{ToolSearchName, "b", "c"}, 2, &calls))

	page1 := listToolsViaMiddleware(t, h, "")
	require.Equal(t, []string{"b"}, toolNames(page1.Tools))
	require.Equal(t, "2", page1.NextCursor)
	page2 := listToolsViaMiddleware(t, h, "2")
	require.Equal(t, []string{"c"}, toolNames(page2.Tools))
}

// バックエンドの tool_search との衝突は WARN をサーバー（ミドルウェア）ごとに
// 1 回だけ出し、衝突が解消されたら再び出す。
func TestDropReservedTool_WarnsOncePerServer(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	tools := []*mcp.Tool{{Name: ToolSearchName}, {Name: "x"}}
	const msg = "collides with the synthetic tool_search"
	a := newToolSearchIndexes("warn-once-a", nil, nil)
	for range 3 {
		got := a.dropReservedTool(t.Context(), tools)
		require.Equal(t, []string{"x"}, toolNames(got))
	}
	require.Equal(t, 1, strings.Count(buf.String(), msg))

	// 別サーバーは別に 1 回。
	b := newToolSearchIndexes("warn-once-b", nil, nil)
	b.dropReservedTool(t.Context(), tools)
	b.dropReservedTool(t.Context(), tools)
	require.Equal(t, 2, strings.Count(buf.String(), msg))
}

func TestToolSearchIndexes_ReservedWarningResetsWhenToolDisappears(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	const msg = "collides with the synthetic tool_search"
	inner := &callerTools{byToken: map[string][]string{"tok": {ToolSearchName, "x"}}}
	c := newToolSearchIndexes("petstore", nil, nil)
	now := time.Now()
	c.now = func() time.Time { return now }
	ctx := withToken(t.Context(), "tok")
	req := &mcp.ListToolsRequest{Params: &mcp.ListToolsParams{}}
	read := func() {
		_, err := c.snapshotFor(ctx, inner.handler, req)
		require.NoError(t, err)
		now = now.Add(2 * toolSearchIndexTTL) // next read goes to the backend
	}

	read()
	read()
	require.Equal(t, 1, strings.Count(buf.String(), msg), "warns once while it stays")

	inner.byToken["tok"] = []string{"x"}
	read()
	require.Equal(t, 1, strings.Count(buf.String(), msg))

	inner.byToken["tok"] = []string{ToolSearchName, "x"}
	read()
	require.Equal(t, 2, strings.Count(buf.String(), msg), "re-added tool warns again")
}

// --- digest ---

func digestDescription(cfg config.ToolSearchConfig, docs ...toolsearch.ToolDef) string {
	return toolSearchDef("petstore", cfg.WithDefaults(), docs).Description
}

func TestToolSearchDef_Digest(t *testing.T) {
	five := []toolsearch.ToolDef{
		{Name: "toolA"}, {Name: "toolB"}, {Name: "toolC"}, {Name: "toolD"}, {Name: "toolE"},
	}

	require.NotContains(t, digestDescription(config.ToolSearchConfig{}), "searchable tool")

	desc := digestDescription(config.ToolSearchConfig{},
		toolsearch.ToolDef{Name: "deletePet", Description: "Delete a pet"},
		toolsearch.ToolDef{Name: "addPet", Description: "Add a new pet to the store"},
		toolsearch.ToolDef{Name: "createOrder"},
	)
	require.Contains(t, desc, "3 searchable tools:")
	require.Contains(
		t,
		desc,
		"- addPet: Add a new pet to the store\n- createOrder\n- deletePet: Delete a pet",
	)
	require.NotContains(t, desc, "createOrder:")

	desc = digestDescription(config.ToolSearchConfig{DigestMaxTools: 2}, five...)
	require.Contains(t, desc, "5 searchable tools (showing first 2):")
	require.Contains(t, desc, "- toolB")
	require.NotContains(t, desc, "toolC")

	for _, n := range []int{-1, 5, 10} {
		desc = digestDescription(config.ToolSearchConfig{DigestMaxTools: n}, five...)
		require.Contains(t, desc, "- toolE", n)
		require.NotContains(t, desc, "showing first", n)
	}
}

// 既定の digestMaxTools は有限で、ツールが多いエンドポイントでも説明文が肥大化しない。
func TestToolSearchDef_Digest_DefaultCapsLargeCatalogs(t *testing.T) {
	cfg := config.ToolSearchConfig{Enabled: true}.WithDefaults()
	docs := make([]toolsearch.ToolDef, 500)
	for i := range docs {
		docs[i] = toolsearch.ToolDef{Name: fmt.Sprintf("tool_%03d", i), Description: "d"}
	}
	desc := digestDescription(cfg, docs...)
	require.Contains(t, desc, "500 searchable tools (showing first 50):")
	require.Equal(t, 50, strings.Count(desc, "\n- "))

	// -1 を明示すれば従来どおり全件。
	cfg.DigestMaxTools = -1
	desc = digestDescription(cfg, docs...)
	require.NotContains(t, desc, "showing first")
	require.Equal(t, 500, strings.Count(desc, "\n- "))
}

func TestToolSearchDef_Digest_TruncatesDescriptionsByRune(t *testing.T) {
	desc := digestDescription(config.ToolSearchConfig{},
		toolsearch.ToolDef{Name: "bigTool", Description: strings.Repeat("a", 250)},
		toolsearch.ToolDef{Name: "exactTool", Description: strings.Repeat("b", 200)},
		toolsearch.ToolDef{Name: "cjkTool", Description: strings.Repeat("あ", 210)},
	)
	require.Contains(t, desc, "- bigTool: "+strings.Repeat("a", 200)+"...")
	require.NotContains(t, desc, strings.Repeat("a", 201))
	require.Contains(t, desc, "- exactTool: "+strings.Repeat("b", 200))
	require.NotContains(t, desc, strings.Repeat("b", 200)+"...")
	require.True(t, utf8.ValidString(desc))
	require.Contains(t, desc, "- cjkTool: "+strings.Repeat("あ", 200)+"...")
}

// --- MCPServer integration ---

func TestMCPServer_ToolSearch_OpenAPIMode(t *testing.T) {
	servers := config.Servers{
		"petstore": {
			Name:    "petstore",
			Spec:    "fixtures/petstore_oas.json",
			BaseURL: "https://petstore.example.com",
		},
	}
	u, _ := url.Parse("https://example.com")
	build := func(cfg config.ToolSearchConfig) *mcp.ClientSession {
		s := NewMCPServer(
			servers,
			storage.NewContentManagementService(u, storage.NewNoopUploader()),
			WithToolSearchConfig(cfg),
		)
		require.NoError(t, s.Init(t.Context()))
		t.Cleanup(s.Close)
		srv, err := s.Server("petstore")
		require.NoError(t, err)
		return connectTestClient(t, t.Context(), srv)
	}

	// 既定の閾値（100）はフィクスチャのツール数（19）を上回るため実ツールがそのまま見える
	names := sessionToolNames(t, build(config.ToolSearchConfig{Enabled: true}))
	require.Len(t, names, 19)
	require.NotContains(t, names, ToolSearchName)

	cs := build(config.ToolSearchConfig{Enabled: true, Threshold: 1})
	require.Equal(t, []string{ToolSearchName}, sessionToolNames(t, cs))
	got := searchResultNames(
		t,
		callToolSearch(t, cs, map[string]any{"query": "pet", "method": "regexp"}),
	)
	require.Contains(t, got, "getpetbyid")
}

func TestListVisibleTools_TooManyPages(t *testing.T) {
	// 何ページ読んでも nextCursor を返し続けるバックエンド。
	endless := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.ListToolsResult{
			Tools:      []*mcp.Tool{{Name: "x", InputSchema: map[string]any{"type": "object"}}},
			NextCursor: "more",
		}, nil
	}
	req := &mcp.ListToolsRequest{Params: &mcp.ListToolsParams{}}
	_, _, err := listVisibleTools(t.Context(), endless, req)
	require.ErrorContains(t, err, "did not finish within")

	// 最後のページで nextCursor が空になれば全件返る。
	pages := 0
	finite := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		pages++
		res := &mcp.ListToolsResult{
			Tools: []*mcp.Tool{{Name: "x", InputSchema: map[string]any{"type": "object"}}},
		}
		if pages < 3 {
			res.NextCursor = "more"
		}
		return res, nil
	}
	_, tools, err := listVisibleTools(t.Context(), finite, req)
	require.NoError(t, err)
	require.Len(t, tools, 3)
}
