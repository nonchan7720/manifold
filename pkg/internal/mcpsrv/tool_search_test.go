package mcpsrv

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/toolsearch"
	"github.com/stretchr/testify/require"
)

// buildToolSearchTestServer は tool_search 登録 + 出し分けミドルウェアを備えたテスト用サーバーと
// 対応する catalog を構築する。呼び出し元が addRealTool で実ツールを追加する。
func buildToolSearchTestServer(serverName string, cfg config.ToolSearchConfig) (*mcp.Server, *toolsearch.Catalog) {
	srv := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: "test"}, &mcp.ServerOptions{})
	catalog := toolsearch.NewCatalog()
	registerToolSearch(srv, serverName, catalog, cfg)
	srv.AddReceivingMiddleware(hideToolsMiddleware(serverName, catalog, cfg))
	return srv, catalog
}

// addRealTool は registerAPI が行うのと同じように、実ツールをサーバーと catalog の両方に登録する。
func addRealTool(srv *mcp.Server, catalog *toolsearch.Catalog, serverName, name, description string) {
	schema := map[string]any{"type": "object"}
	srv.AddTool(&mcp.Tool{Name: name, Description: description, InputSchema: schema}, func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok:" + name}}}, nil
	})
	catalog.Add(serverName, toolsearch.ToolDef{Name: name, Description: description, InputSchema: schema})
}

// connectInMemory はサーバーを in-memory transport 経由でクライアントに接続する。
func connectInMemory(t *testing.T, ctx context.Context, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	_, err := srv.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func toolNames(tools []*mcp.Tool) []string {
	names := make([]string, len(tools))
	for i, tt := range tools {
		names[i] = tt.Name
	}
	return names
}

func TestHideToolsMiddleware_BelowThreshold_RealToolsVisible(t *testing.T) {
	ctx := t.Context()
	cfg := config.ToolSearchConfig{Threshold: 100, DefaultLimit: 10}
	srv, catalog := buildToolSearchTestServer("petstore", cfg)
	addRealTool(srv, catalog, "petstore", "list_pets", "list pet inventory")
	addRealTool(srv, catalog, "petstore", "get_pet", "get a pet by id")

	session := connectInMemory(t, ctx, srv)
	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	names := toolNames(result.Tools)
	require.ElementsMatch(t, []string{"list_pets", "get_pet"}, names)
	require.NotContains(t, names, toolSearchName)
}

func TestHideToolsMiddleware_AboveThreshold_OnlyToolSearchVisible(t *testing.T) {
	ctx := t.Context()
	cfg := config.ToolSearchConfig{Threshold: 1, DefaultLimit: 10}
	srv, catalog := buildToolSearchTestServer("petstore", cfg)
	addRealTool(srv, catalog, "petstore", "list_pets", "list pet inventory")
	addRealTool(srv, catalog, "petstore", "get_pet", "get a pet by id")

	session := connectInMemory(t, ctx, srv)
	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	require.Len(t, result.Tools, 1)
	require.Equal(t, toolSearchName, result.Tools[0].Name)
	require.Empty(t, result.NextCursor)
}

func TestHideToolsMiddleware_AboveThreshold_HiddenRealToolStillCallable(t *testing.T) {
	ctx := t.Context()
	cfg := config.ToolSearchConfig{Threshold: 1, DefaultLimit: 10}
	srv, catalog := buildToolSearchTestServer("petstore", cfg)
	addRealTool(srv, catalog, "petstore", "list_pets", "list pet inventory")
	addRealTool(srv, catalog, "petstore", "get_pet", "get a pet by id")

	session := connectInMemory(t, ctx, srv)

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_pets"})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "ok:list_pets", text.Text)
}

func TestToolSearch_CallToolSearch_DefaultMethodBM25_RoundTripsDefinitions(t *testing.T) {
	ctx := t.Context()
	cfg := config.ToolSearchConfig{Threshold: 1, DefaultLimit: 10}
	srv, catalog := buildToolSearchTestServer("petstore", cfg)
	addRealTool(srv, catalog, "petstore", "list_pets", "list pet inventory")
	addRealTool(srv, catalog, "petstore", "get_pet", "get a pet by id")

	session := connectInMemory(t, ctx, srv)

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolSearchName,
		Arguments: map[string]any{"query": "pet"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, res.Content, 1)

	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)

	var defs []toolsearch.ToolDef
	require.NoError(t, json.Unmarshal([]byte(text.Text), &defs))
	require.Len(t, defs, 2)

	var gotNames []string
	for _, d := range defs {
		gotNames = append(gotNames, d.Name)
	}
	require.ElementsMatch(t, []string{"list_pets", "get_pet"}, gotNames)

	// StructuredContent にも同じ内容がラウンドトリップしていること
	structured, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var structuredDefs []toolsearch.ToolDef
	require.NoError(t, json.Unmarshal(structured, &structuredDefs))
	require.Len(t, structuredDefs, 2)
}

func TestToolSearch_NoMatch_ReturnsEmptyArrayJSON(t *testing.T) {
	// bm25 / regexp / fuzzy のいずれのヒット 0 件経路でも、default / claude どちらの
	// ResultFormat でも null ではなく空配列 [] を返す。
	for _, format := range []string{config.ToolSearchResultFormatDefault, config.ToolSearchResultFormatClaude} {
		for _, method := range []string{"bm25", "regexp", "fuzzy"} {
			t.Run(format+"_"+method, func(t *testing.T) {
				ctx := t.Context()
				cfg := config.ToolSearchConfig{Threshold: 1, DefaultLimit: 10, ResultFormat: format}
				srv, catalog := buildToolSearchTestServer("petstore", cfg)
				addRealTool(srv, catalog, "petstore", "list_pets", "list pet inventory")

				session := connectInMemory(t, ctx, srv)

				res, err := session.CallTool(ctx, &mcp.CallToolParams{
					Name:      toolSearchName,
					Arguments: map[string]any{"query": "nomatchquery", "method": method},
				})
				require.NoError(t, err)
				require.False(t, res.IsError)
				require.Len(t, res.Content, 1)

				text, ok := res.Content[0].(*mcp.TextContent)
				require.True(t, ok)
				require.JSONEq(t, "[]", text.Text)

				structured, err := json.Marshal(res.StructuredContent)
				require.NoError(t, err)
				require.JSONEq(t, "[]", string(structured))
			})
		}
	}
}

func TestToolSearch_ClaudeFormat_ReturnsToolReferenceBlocks(t *testing.T) {
	ctx := t.Context()
	cfg := config.ToolSearchConfig{Threshold: 1, DefaultLimit: 10, ResultFormat: config.ToolSearchResultFormatClaude}
	srv, catalog := buildToolSearchTestServer("petstore", cfg)
	addRealTool(srv, catalog, "petstore", "list_pets", "list pet inventory")
	addRealTool(srv, catalog, "petstore", "get_pet", "get a pet by id")

	session := connectInMemory(t, ctx, srv)

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolSearchName,
		Arguments: map[string]any{"query": "pet"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, res.Content, 1)

	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)

	var refs []toolsearch.ToolReference
	require.NoError(t, json.Unmarshal([]byte(text.Text), &refs))
	require.Len(t, refs, 2)

	var gotNames []string
	for _, r := range refs {
		require.Equal(t, "tool_reference", r.Type)
		gotNames = append(gotNames, r.ToolName)
	}
	require.ElementsMatch(t, []string{"list_pets", "get_pet"}, gotNames)

	// StructuredContent にも同じ内容がラウンドトリップしていること
	structured, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var structuredRefs []toolsearch.ToolReference
	require.NoError(t, json.Unmarshal(structured, &structuredRefs))
	require.Len(t, structuredRefs, 2)
}

func TestToolSearchDef_Description_MentionsToolReferenceForClaudeFormat(t *testing.T) {
	catalog := toolsearch.NewCatalog()
	def := toolSearchDef("petstore", config.ToolSearchConfig{ResultFormat: config.ToolSearchResultFormatClaude}.WithDefaults(), catalog)
	require.Contains(t, def.Description, "tool_reference")
}

func TestToolSearchDef_Description_DefaultFormat_NoToolReferenceMention(t *testing.T) {
	catalog := toolsearch.NewCatalog()
	def := toolSearchDef("petstore", config.ToolSearchConfig{}.WithDefaults(), catalog)
	require.NotContains(t, def.Description, "tool_reference")
}

// --- tool_search: description のカタログダイジェスト ---

func TestToolSearchDef_Description_EmptyCatalog_OmitsDigest(t *testing.T) {
	catalog := toolsearch.NewCatalog()
	def := toolSearchDef("petstore", config.ToolSearchConfig{}.WithDefaults(), catalog)
	require.NotContains(t, def.Description, "searchable tool")
}

func TestToolSearchDef_Description_IncludesDigest(t *testing.T) {
	catalog := toolsearch.NewCatalog()
	catalog.Add("petstore",
		toolsearch.ToolDef{Name: "addPet", Description: "Add a new pet to the store"},
		toolsearch.ToolDef{Name: "deletePet", Description: "Delete a pet"},
		toolsearch.ToolDef{Name: "createOrder"},
	)

	def := toolSearchDef("petstore", config.ToolSearchConfig{}.WithDefaults(), catalog)
	require.Contains(t, def.Description, "3 searchable tools:")
	require.Contains(t, def.Description, "- addPet: Add a new pet to the store")
	require.Contains(t, def.Description, "- deletePet: Delete a pet")
	// description が空のツールは "- name" のみ（コロン以降なし）
	require.Contains(t, def.Description, "- createOrder\n")
}

func TestToolSearchDef_Description_NoDescription_OmitsColonPart(t *testing.T) {
	catalog := toolsearch.NewCatalog()
	catalog.Add("petstore", toolsearch.ToolDef{Name: "addPet"})

	def := toolSearchDef("petstore", config.ToolSearchConfig{}.WithDefaults(), catalog)
	require.Contains(t, def.Description, "- addPet")
	require.NotContains(t, def.Description, "addPet:")
}

func TestToolSearchDef_Description_ScopedToServer(t *testing.T) {
	catalog := toolsearch.NewCatalog()
	catalog.Add("petstore", toolsearch.ToolDef{Name: "addPet"})
	catalog.Add("otherserver", toolsearch.ToolDef{Name: "unrelatedTool"})

	def := toolSearchDef("petstore", config.ToolSearchConfig{}.WithDefaults(), catalog)
	require.Contains(t, def.Description, "1 searchable tool:")
	require.NotContains(t, def.Description, "unrelatedTool")
}

// --- tool_search: digestMaxTools によるダイジェスト件数の上限 ---

func newFiveToolCatalog() *toolsearch.Catalog {
	catalog := toolsearch.NewCatalog()
	catalog.Add("petstore",
		toolsearch.ToolDef{Name: "toolA"},
		toolsearch.ToolDef{Name: "toolB"},
		toolsearch.ToolDef{Name: "toolC"},
		toolsearch.ToolDef{Name: "toolD"},
		toolsearch.ToolDef{Name: "toolE"},
	)
	return catalog
}

func TestToolSearchDef_Description_DigestMaxTools_TruncatesAndNotesOmission(t *testing.T) {
	catalog := newFiveToolCatalog()

	def := toolSearchDef("petstore", config.ToolSearchConfig{DigestMaxTools: 2}.WithDefaults(), catalog)
	require.Contains(t, def.Description, "5 searchable tools")
	require.Contains(t, def.Description, "- toolA")
	require.Contains(t, def.Description, "- toolB")
	// 上限 2 件のみで、それ以外のツール名は含まれない
	require.NotContains(t, def.Description, "toolC")
	require.NotContains(t, def.Description, "toolD")
	require.NotContains(t, def.Description, "toolE")
	// 省略があったことが LLM に伝わる表現が含まれる
	require.Contains(t, def.Description, "showing first 2")
}

func TestToolSearchDef_Description_DigestMaxTools_AtOrAboveTotal_ShowsAllNoOmissionNotice(t *testing.T) {
	catalog := newFiveToolCatalog()

	def := toolSearchDef("petstore", config.ToolSearchConfig{DigestMaxTools: 10}.WithDefaults(), catalog)
	require.Contains(t, def.Description, "5 searchable tools")
	for _, name := range []string{"toolA", "toolB", "toolC", "toolD", "toolE"} {
		require.Contains(t, def.Description, "- "+name)
	}
	require.NotContains(t, def.Description, "showing first")
}

func TestToolSearchDef_Description_DigestMaxTools_ExactlyTotal_ShowsAllNoOmissionNotice(t *testing.T) {
	catalog := newFiveToolCatalog()

	def := toolSearchDef("petstore", config.ToolSearchConfig{DigestMaxTools: 5}.WithDefaults(), catalog)
	for _, name := range []string{"toolA", "toolB", "toolC", "toolD", "toolE"} {
		require.Contains(t, def.Description, "- "+name)
	}
	require.NotContains(t, def.Description, "showing first")
}

func TestToolSearchDef_Description_DigestMaxTools_NegativeOne_ShowsAll(t *testing.T) {
	catalog := newFiveToolCatalog()

	def := toolSearchDef("petstore", config.ToolSearchConfig{DigestMaxTools: -1}.WithDefaults(), catalog)
	for _, name := range []string{"toolA", "toolB", "toolC", "toolD", "toolE"} {
		require.Contains(t, def.Description, "- "+name)
	}
	require.NotContains(t, def.Description, "showing first")
}

func TestToolSearchDef_Description_LongDescription_TruncatedAt200RunesWithEllipsis(t *testing.T) {
	catalog := toolsearch.NewCatalog()
	longDesc := strings.Repeat("a", 250)
	catalog.Add("petstore", toolsearch.ToolDef{Name: "bigTool", Description: longDesc})

	def := toolSearchDef("petstore", config.ToolSearchConfig{}.WithDefaults(), catalog)
	require.Contains(t, def.Description, "- bigTool: "+strings.Repeat("a", 200)+"...")
	require.NotContains(t, def.Description, strings.Repeat("a", 201))
}

func TestToolSearchDef_Description_Exactly200Runes_NoTruncation(t *testing.T) {
	catalog := toolsearch.NewCatalog()
	desc200 := strings.Repeat("b", 200)
	catalog.Add("petstore", toolsearch.ToolDef{Name: "exactTool", Description: desc200})

	def := toolSearchDef("petstore", config.ToolSearchConfig{}.WithDefaults(), catalog)
	require.Contains(t, def.Description, "- exactTool: "+desc200)
	require.NotContains(t, def.Description, "exactTool: "+desc200+"...")
}

func TestToolSearchDef_Description_199Runes_NoTruncation(t *testing.T) {
	catalog := toolsearch.NewCatalog()
	desc199 := strings.Repeat("c", 199)
	catalog.Add("petstore", toolsearch.ToolDef{Name: "shortTool", Description: desc199})

	def := toolSearchDef("petstore", config.ToolSearchConfig{}.WithDefaults(), catalog)
	require.Contains(t, def.Description, "- shortTool: "+desc199)
	require.NotContains(t, def.Description, desc199+"...")
}

func TestToolSearchDef_Description_201Runes_TruncatesAndAppendsEllipsis(t *testing.T) {
	catalog := toolsearch.NewCatalog()
	desc201 := strings.Repeat("d", 201)
	catalog.Add("petstore", toolsearch.ToolDef{Name: "overTool", Description: desc201})

	def := toolSearchDef("petstore", config.ToolSearchConfig{}.WithDefaults(), catalog)
	require.Contains(t, def.Description, "- overTool: "+strings.Repeat("d", 200)+"...")
}

func TestToolSearchDef_Description_CJK200RuneBoundary_TruncatesByRuneNotByte(t *testing.T) {
	catalog := toolsearch.NewCatalog()
	// 日本語（マルチバイト文字）でも 200 "文字"（rune）単位で切り詰められ、
	// バイト境界で文字が壊れないこと。
	cjkDesc := strings.Repeat("あ", 210)
	catalog.Add("petstore", toolsearch.ToolDef{Name: "cjkTool", Description: cjkDesc})

	def := toolSearchDef("petstore", config.ToolSearchConfig{}.WithDefaults(), catalog)
	require.True(t, utf8.ValidString(def.Description))
	require.Contains(t, def.Description, "- cjkTool: "+strings.Repeat("あ", 200)+"...")
}

func TestHideToolsMiddleware_AboveThreshold_ToolSearchDescription_IncludesDigest(t *testing.T) {
	ctx := t.Context()
	cfg := config.ToolSearchConfig{Threshold: 1, DefaultLimit: 10}
	srv, catalog := buildToolSearchTestServer("petstore", cfg)
	addRealTool(srv, catalog, "petstore", "list_pets", "list pet inventory")
	addRealTool(srv, catalog, "petstore", "get_pet", "get a pet by id")

	session := connectInMemory(t, ctx, srv)
	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, result.Tools, 1)

	desc := result.Tools[0].Description
	require.Contains(t, desc, "2 searchable tools:")
	require.Contains(t, desc, "- list_pets: list pet inventory")
	require.Contains(t, desc, "- get_pet: get a pet by id")
}

func TestHideToolsMiddleware_DynamicDigest_NewSessionReflectsAddedTools(t *testing.T) {
	ctx := t.Context()
	cfg := config.ToolSearchConfig{Threshold: 1, DefaultLimit: 10}
	srv, catalog := buildToolSearchTestServer("petstore", cfg)
	addRealTool(srv, catalog, "petstore", "list_pets", "list pet inventory")
	addRealTool(srv, catalog, "petstore", "get_pet", "get a pet by id")

	firstSession := connectInMemory(t, ctx, srv)
	firstResult, err := firstSession.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Contains(t, firstResult.Tools[0].Description, "2 searchable tools:")

	// 遅延接続などによりカタログにツールが追加される
	addRealTool(srv, catalog, "petstore", "cancel_pet", "cancel a pet order")

	secondSession := connectInMemory(t, ctx, srv)
	secondResult, err := secondSession.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Contains(t, secondResult.Tools[0].Description, "3 searchable tools:")
	require.Contains(t, secondResult.Tools[0].Description, "- cancel_pet: cancel a pet order")
}

func TestToolSearch_RegexpInvalidPattern_ReturnsToolError(t *testing.T) {
	ctx := t.Context()
	cfg := config.ToolSearchConfig{Threshold: 1, DefaultLimit: 10}
	srv, catalog := buildToolSearchTestServer("petstore", cfg)
	addRealTool(srv, catalog, "petstore", "list_pets", "list pet inventory")

	session := connectInMemory(t, ctx, srv)

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolSearchName,
		Arguments: map[string]any{"query": "(unterminated", "method": "regexp"},
	})
	// プロトコルエラーではなく tool error として返る
	require.NoError(t, err)
	require.True(t, res.IsError)
}

func TestToolSearch_MethodAndLimit_TableDriven(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		limit     int
		query     string
		wantNames []string
	}{
		{
			name:      "bm25_explicit",
			method:    "bm25",
			query:     "pet",
			wantNames: []string{"list_pets", "get_pet", "cancel_pet"},
		},
		{
			name:      "regexp_explicit",
			method:    "regexp",
			query:     "^get_",
			wantNames: []string{"get_pet"},
		},
		{
			name:      "fuzzy_explicit",
			method:    "fuzzy",
			query:     "listpets",
			wantNames: []string{"list_pets"},
		},
		{
			name:      "limit_truncates",
			method:    "regexp",
			query:     "pet",
			limit:     1,
			wantNames: []string{"list_pets"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			cfg := config.ToolSearchConfig{Threshold: 1, DefaultLimit: 10}
			srv, catalog := buildToolSearchTestServer("petstore", cfg)
			addRealTool(srv, catalog, "petstore", "list_pets", "list pet inventory")
			addRealTool(srv, catalog, "petstore", "get_pet", "get a pet by id")
			addRealTool(srv, catalog, "petstore", "cancel_pet", "cancel a pet order")

			session := connectInMemory(t, ctx, srv)

			args := map[string]any{"query": tt.query, "method": tt.method}
			if tt.limit > 0 {
				args["limit"] = tt.limit
			}
			res, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name:      toolSearchName,
				Arguments: args,
			})
			require.NoError(t, err)
			require.False(t, res.IsError)

			text, ok := res.Content[0].(*mcp.TextContent)
			require.True(t, ok)
			var defs []toolsearch.ToolDef
			require.NoError(t, json.Unmarshal([]byte(text.Text), &defs))

			var gotNames []string
			for _, d := range defs {
				gotNames = append(gotNames, d.Name)
			}
			if tt.name == "limit_truncates" {
				require.Len(t, gotNames, tt.limit)
			} else {
				require.ElementsMatch(t, tt.wantNames, gotNames)
			}
		})
	}
}

func TestHideToolsMiddleware_DynamicThresholdChange_NewSessionReflectsChange(t *testing.T) {
	ctx := t.Context()
	cfg := config.ToolSearchConfig{Threshold: 5, DefaultLimit: 10}
	srv, catalog := buildToolSearchTestServer("petstore", cfg)
	addRealTool(srv, catalog, "petstore", "list_pets", "list pet inventory")
	addRealTool(srv, catalog, "petstore", "get_pet", "get a pet by id")

	// 閾値未満: 最初のセッションでは実ツールが見える
	firstSession := connectInMemory(t, ctx, srv)
	result, err := firstSession.ListTools(ctx, nil)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"list_pets", "get_pet"}, toolNames(result.Tools))

	// 遅延接続などにより合計ツール数が閾値を超える
	for i := range 5 {
		addRealTool(srv, catalog, "petstore", "extra_tool_"+string(rune('a'+i)), "extra")
	}
	require.Greater(t, catalog.Total(), cfg.Threshold)

	// 新規セッションでは tool_search のみが見える
	secondSession := connectInMemory(t, ctx, srv)
	result2, err := secondSession.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, result2.Tools, 1)
	require.Equal(t, toolSearchName, result2.Tools[0].Name)
}
