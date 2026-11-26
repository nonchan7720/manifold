package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

const issuesJSON = `{"total": 2, "items": [` +
	`{"id": 12345678901234567890, "title": "Fix login", "state": "open", "body": "long text"},` +
	`{"id": 2, "title": "Add docs", "state": "closed", "body": "more text"}]}`

// resultFilterBackend は、結果の絞り込みのテスト用のバックエンド。ツールごとに決まった
// 結果を返し、受け取った引数を覚える。
type resultFilterBackend struct {
	srv   *mcp.Server
	calls atomic.Int32

	mu   sync.Mutex
	args map[string]string
}

func (b *resultFilterBackend) lastArgs(tool string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.args[tool]
}

func (b *resultFilterBackend) add(tool *mcp.Tool, result func() *mcp.CallToolResult) {
	if tool.InputSchema == nil {
		tool.InputSchema = map[string]any{"type": "object"}
	}
	b.srv.AddTool(
		tool,
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			b.calls.Add(1)
			b.mu.Lock()
			b.args[req.Params.Name] = string(req.Params.Arguments)
			b.mu.Unlock()
			return result(), nil
		},
	)
}

func textResult(texts ...string) func() *mcp.CallToolResult {
	return func() *mcp.CallToolResult {
		res := &mcp.CallToolResult{}
		for _, text := range texts {
			res.Content = append(res.Content, &mcp.TextContent{Text: text})
		}
		return res
	}
}

func newResultFilterBackend(t *testing.T, cfg *config.ResultFilterConfig) *resultFilterBackend {
	t.Helper()
	b := &resultFilterBackend{
		srv:  mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.1"}, nil),
		args: map[string]string{},
	}
	b.add(&mcp.Tool{Name: "list_issues"}, textResult(issuesJSON))
	b.add(&mcp.Tool{Name: "structured"}, func() *mcp.CallToolResult {
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: issuesJSON}},
			StructuredContent: json.RawMessage(issuesJSON),
		}
	})
	b.add(&mcp.Tool{Name: "structured_only"}, func() *mcp.CallToolResult {
		return &mcp.CallToolResult{StructuredContent: json.RawMessage(issuesJSON)}
	})
	b.add(&mcp.Tool{Name: "summary_and_structured"}, func() *mcp.CallToolResult {
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: "2 issues found"}},
			StructuredContent: map[string]any{"total": 2},
		}
	})
	b.add(&mcp.Tool{Name: "plain_text"}, textResult("just some text"))
	b.add(&mcp.Tool{Name: "per_item"}, textResult(
		`{"id": 1, "name": "a", "extra": true}`, "a note", `{"id": 2, "name": "b", "extra": false}`,
	))
	b.add(&mcp.Tool{Name: "mixed"}, func() *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: `[{"id": 1, "x": 1}, {"id": 2, "x": 2}]`},
			&mcp.ImageContent{Data: []byte("png"), MIMEType: "image/png"},
			&mcp.ResourceLink{URI: "https://example.com/file", Name: "file"},
		}}
	})
	b.add(&mcp.Tool{Name: "fails"}, func() *mcp.CallToolResult {
		res := &mcp.CallToolResult{}
		res.SetError(errBoom)
		return res
	})
	b.srv.AddReceivingMiddleware(newResultFilterMiddleware("test", cfg, createPairingCodeToolName))
	return b
}

func enabledResultFilter() *config.ResultFilterConfig {
	return &config.ResultFilterConfig{Enabled: true}
}

func callTool(
	t *testing.T, cs *mcp.ClientSession, name string, args map[string]any,
) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	return res
}

func contentTexts(t *testing.T, res *mcp.CallToolResult) []string {
	t.Helper()
	var texts []string
	for _, c := range res.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			texts = append(texts, text.Text)
		}
	}
	return texts
}

func structuredJSON(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res.StructuredContent == nil {
		return ""
	}
	raw, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	return string(raw)
}

func TestResultFilterMiddleware_DisabledIsNoMiddleware(t *testing.T) {
	require.Nil(t, newResultFilterMiddleware("test", nil))
	require.Nil(t, newResultFilterMiddleware("test", &config.ResultFilterConfig{}))
	require.Nil(t, newResultFilterMiddleware("test", &config.ResultFilterConfig{
		Argument: "_q", Include: []string{"*"},
	}))
}

func TestResultFilterMiddleware_ToolsList(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
	handler := func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	}
	objectSchema := map[string]any{"type": "object"}
	srv.AddTool(&mcp.Tool{
		Name: "list_issues",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"state": map[string]any{"type": "string"}},
			"required":   []any{"state"},
		},
		OutputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"total": map[string]any{"type": "integer"}},
			"required":   []any{"total"},
		},
	}, handler)
	srv.AddTool(&mcp.Tool{Name: "no_properties", InputSchema: objectSchema}, handler)
	srv.AddTool(&mcp.Tool{
		Name: "raw_schema", InputSchema: json.RawMessage(`{"type":"object","properties":{"a":{}}}`),
	}, handler)
	srv.AddTool(&mcp.Tool{
		Name: "delete_issue", InputSchema: objectSchema, OutputSchema: objectSchema,
	}, handler)
	srv.AddTool(&mcp.Tool{
		Name: "show_chart", InputSchema: objectSchema,
		Meta: mcp.Meta{mcpAppsMetaKey: map[string]any{"resourceUri": "ui://chart"}},
	}, handler)
	srv.AddTool(&mcp.Tool{Name: createPairingCodeToolName, InputSchema: objectSchema}, handler)
	srv.AddReceivingMiddleware(newResultFilterMiddleware("test", &config.ResultFilterConfig{
		Enabled: true, Exclude: []string{"delete_*"},
	}, createPairingCodeToolName))
	cs := connectTestClient(t, t.Context(), srv)

	res, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	tools := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		tools[tool.Name] = tool
	}
	properties := func(name string) map[string]any {
		schema, ok := tools[name].InputSchema.(map[string]any)
		require.True(t, ok, name)
		props, _ := schema["properties"].(map[string]any)
		return props
	}

	// 対象のツールには式の引数が足され、元のプロパティと required は残る。
	argument := map[string]any{"type": "string", "description": resultFilterDescription}
	require.Equal(t, argument, properties("list_issues")[config.DefaultResultFilterArgument])
	require.Contains(t, properties("list_issues"), "state")
	require.Equal(t, []any{"state"}, tools["list_issues"].InputSchema.(map[string]any)["required"])
	require.Equal(t, argument, properties("no_properties")[config.DefaultResultFilterArgument])
	require.Equal(t, argument, properties("raw_schema")[config.DefaultResultFilterArgument])
	require.Contains(t, properties("raw_schema"), "a")
	// 結果の形は式が決めるので、outputSchema は広告しない。
	require.Nil(t, tools["list_issues"].OutputSchema)

	// exclude したツール、UI を持つツール、ゲートウェイ自身のツールはそのまま。
	for _, name := range []string{"delete_issue", "show_chart", createPairingCodeToolName} {
		require.NotContains(t, properties(name), config.DefaultResultFilterArgument, name)
	}
	require.NotNil(t, tools["delete_issue"].OutputSchema)

	// SDK が保持する登録済みのツールは書き換えない（2 回目の一覧も同じ）。
	again, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, res.Tools, again.Tools)
}

// バックエンドのツールが同じ名前の引数を持つ場合はゲートウェイの引数が優先され、
// 警告を 1 度だけ出す。argument を変えれば衝突しない。
func TestResultFilterMiddleware_ToolsList_ShadowsExistingArgument(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	newServer := func(cfg *config.ResultFilterConfig) *mcp.ClientSession {
		srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
		srv.AddTool(&mcp.Tool{
			Name: "wp_posts",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"_jmespath": map[string]any{"type": "integer", "description": "theirs"},
					"page":      map[string]any{"type": "integer"},
				},
				"required": []any{"_jmespath", "page"},
			},
		}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})
		srv.AddReceivingMiddleware(newResultFilterMiddleware("wordpress", cfg))
		return connectTestClient(t, t.Context(), srv)
	}
	schemaOf := func(cs *mcp.ClientSession) map[string]any {
		res, err := cs.ListTools(t.Context(), nil)
		require.NoError(t, err)
		require.Len(t, res.Tools, 1)
		schema, ok := res.Tools[0].InputSchema.(map[string]any)
		require.True(t, ok)
		return schema
	}

	cs := newServer(enabledResultFilter())
	schema := schemaOf(cs)
	props, _ := schema["properties"].(map[string]any)
	require.Equal(t, map[string]any{"type": "string", "description": resultFilterDescription},
		props["_jmespath"])
	require.Equal(t, []any{"page"}, schema["required"])
	schemaOf(cs)
	require.Equal(t, 1, strings.Count(logs.String(), "the result filter takes it over"))
	require.Contains(t, logs.String(), "tool=wp_posts")
	require.Contains(t, logs.String(), "server=wordpress")

	logs.Reset()
	renamed := schemaOf(newServer(&config.ResultFilterConfig{Enabled: true, Argument: "_query"}))
	props, _ = renamed["properties"].(map[string]any)
	require.Equal(t, map[string]any{"type": "integer", "description": "theirs"}, props["_jmespath"])
	require.Contains(t, props, "_query")
	require.Equal(t, []any{"_jmespath", "page"}, renamed["required"])
	require.Empty(t, logs.String())
}

func TestResultFilterMiddleware_Call(t *testing.T) {
	b := newResultFilterBackend(t, enabledResultFilter())
	cs := connectTestClient(t, t.Context(), b.srv)

	t.Run("without the argument the call is forwarded untouched", func(t *testing.T) {
		res := callTool(t, cs, "list_issues", map[string]any{"state": "open"})
		require.Equal(t, []string{issuesJSON}, contentTexts(t, res))
		require.JSONEq(t, `{"state":"open"}`, b.lastArgs("list_issues"))
	})

	t.Run("the expression is applied and not forwarded", func(t *testing.T) {
		res := callTool(t, cs, "list_issues", map[string]any{
			"state":     "open",
			"_jmespath": "items[?state=='open'].{id: id, title: title}",
		})
		require.False(t, res.IsError)
		// float64 では表せない ID も桁を保つ。
		require.Equal(t, []string{`[{"id":12345678901234567890,"title":"Fix login"}]`},
			contentTexts(t, res))
		require.JSONEq(t, `{"state":"open"}`, b.lastArgs("list_issues"))
	})

	t.Run("only the expression leaves an empty object for the backend", func(t *testing.T) {
		res := callTool(t, cs, "list_issues", map[string]any{"_jmespath": "length(items)"})
		require.Equal(t, []string{"2"}, contentTexts(t, res))
		require.Equal(t, `{}`, b.lastArgs("list_issues"))
	})

	t.Run("an object result is returned as structuredContent too", func(t *testing.T) {
		res := callTool(t, cs, "structured", map[string]any{
			"_jmespath": "{total: total, titles: items[].title}",
		})
		const want = `{"total":2,"titles":["Fix login","Add docs"]}`
		require.Equal(t, []string{want}, contentTexts(t, res))
		require.JSONEq(t, want, structuredJSON(t, res))
	})

	t.Run("a result that is not an object has no structuredContent", func(t *testing.T) {
		res := callTool(t, cs, "structured", map[string]any{"_jmespath": "items[].id"})
		require.Equal(t, []string{`[12345678901234567890,2]`}, contentTexts(t, res))
		require.Nil(t, res.StructuredContent)
	})

	t.Run("structuredContent without a text gets the result as a text", func(t *testing.T) {
		res := callTool(t, cs, "structured_only", map[string]any{"_jmespath": "items[0].title"})
		require.Equal(t, []string{`"Fix login"`}, contentTexts(t, res))
		require.Nil(t, res.StructuredContent)
	})

	t.Run("a text that is not JSON is kept next to the filtered JSON", func(t *testing.T) {
		res := callTool(t, cs, "summary_and_structured", map[string]any{"_jmespath": "total"})
		require.Equal(t, []string{"2 issues found", "2"}, contentTexts(t, res))
		require.Nil(t, res.StructuredContent)
	})

	t.Run("every JSON text is filtered on its own", func(t *testing.T) {
		res := callTool(t, cs, "per_item", map[string]any{"_jmespath": "{id: id, name: name}"})
		require.Equal(t, []string{`{"id":1,"name":"a"}`, "a note", `{"id":2,"name":"b"}`},
			contentTexts(t, res))
	})

	t.Run("other content is left alone", func(t *testing.T) {
		res := callTool(t, cs, "mixed", map[string]any{"_jmespath": "[].id"})
		require.Len(t, res.Content, 3)
		require.Equal(t, []string{`[1,2]`}, contentTexts(t, res))
		image, ok := res.Content[1].(*mcp.ImageContent)
		require.True(t, ok)
		require.Equal(t, "png", string(image.Data))
		_, ok = res.Content[2].(*mcp.ResourceLink)
		require.True(t, ok)
	})

	t.Run("a result that is not JSON is returned with a note", func(t *testing.T) {
		res := callTool(t, cs, "plain_text", map[string]any{"_jmespath": "items[].id"})
		require.False(t, res.IsError)
		require.Equal(t, []string{
			"just some text", "_jmespath was not applied: the result is not JSON.",
		}, contentTexts(t, res))
	})

	t.Run("a null result describes the shape of the result", func(t *testing.T) {
		res := callTool(t, cs, "list_issues", map[string]any{"_jmespath": "issues[].id"})
		require.False(t, res.IsError)
		require.Equal(t, []string{
			"null",
			"_jmespath returned null. The result is an object with keys: total, items.",
		}, contentTexts(t, res))
	})

	t.Run("an empty or null expression is removed and nothing is filtered", func(t *testing.T) {
		for _, expression := range []any{"", "  ", nil} {
			res := callTool(t, cs, "list_issues", map[string]any{"_jmespath": expression, "a": 1})
			require.Equal(t, []string{issuesJSON}, contentTexts(t, res))
			require.JSONEq(t, `{"a":1}`, b.lastArgs("list_issues"))
		}
	})

	t.Run("an error result of the tool is not filtered", func(t *testing.T) {
		res := callTool(t, cs, "fails", map[string]any{"_jmespath": "items[].id"})
		require.True(t, res.IsError)
		require.Equal(t, []string{"boom"}, contentTexts(t, res))
	})
}

// 式の誤りは、バックエンドを呼ぶ前にツールのエラーとして返す。
func TestResultFilterMiddleware_Call_InvalidExpression(t *testing.T) {
	b := newResultFilterBackend(t, enabledResultFilter())
	cs := connectTestClient(t, t.Context(), b.srv)

	tests := []struct {
		name       string
		expression any
		want       string
	}{
		{"syntax error", "items[?state = 'open']",
			"invalid _jmespath: unexpected '=' (use '==' to compare) at offset 13. " +
				"The tool was not called."},
		{"unknown function", "count(items)",
			"invalid _jmespath: unknown function count() at offset 0. The tool was not called."},
		{"not a string", []any{"id", "title"},
			"_jmespath must be a string (a JMESPath expression). The tool was not called."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := callTool(t, cs, "list_issues", map[string]any{"_jmespath": tt.expression})
			require.True(t, res.IsError)
			require.Equal(t, []string{tt.want}, contentTexts(t, res))
			// SDK の組み込みハンドラを通らない結果にも resultType が付く。
			require.Equal(t, resultTypeComplete, resultTypeOf(t, res))
		})
	}
	require.Zero(t, b.calls.Load())
}

// 呼び出しは成功したが式を評価できなかった場合は、結果を返さずにエラーにする。
func TestResultFilterMiddleware_Call_EvaluationError(t *testing.T) {
	b := newResultFilterBackend(t, enabledResultFilter())
	cs := connectTestClient(t, t.Context(), b.srv)

	res := callTool(t, cs, "list_issues", map[string]any{"_jmespath": "length(total)"})
	require.True(t, res.IsError)
	require.Equal(t, []string{
		"_jmespath could not be evaluated: length(): argument 1 must be a string, an array or " +
			"an object, got number. The tool call itself completed, so do not repeat it for its " +
			"side effects. The result is an object with keys: total, items.",
	}, contentTexts(t, res))
	require.Equal(t, resultTypeComplete, resultTypeOf(t, res))
	require.Equal(t, int32(1), b.calls.Load())

	// 結果を膨らませる式は評価の途中で止まる。
	bomb := "@" + strings.Repeat("|[@,@][]", 40)
	res = callTool(t, cs, "mixed", map[string]any{"_jmespath": bomb})
	require.True(t, res.IsError)
	require.Contains(t, contentTexts(t, res)[0], "too expensive to evaluate")
}

// include / exclude に合わないツールやゲートウェイ自身のツールでは、同じ名前の引数も
// バックエンドのものとしてそのまま渡す。
func TestResultFilterMiddleware_Call_ToolsOutOfScope(t *testing.T) {
	b := newResultFilterBackend(t, &config.ResultFilterConfig{
		Enabled: true, Include: []string{"list_*", "struct*"}, Exclude: []string{"structured_only"},
	})
	b.add(&mcp.Tool{Name: createPairingCodeToolName}, textResult(`{"code": "1234"}`))
	cs := connectTestClient(t, t.Context(), b.srv)

	for _, tool := range []string{"per_item", "structured_only", createPairingCodeToolName} {
		before := contentTexts(t, callTool(t, cs, tool, nil))
		res := callTool(t, cs, tool, map[string]any{"_jmespath": "id"})
		require.Equal(t, before, contentTexts(t, res), tool)
		require.JSONEq(t, `{"_jmespath":"id"}`, b.lastArgs(tool), tool)
	}
	res := callTool(t, cs, "structured", map[string]any{"_jmespath": "total"})
	require.Equal(t, []string{"2"}, contentTexts(t, res))
}

func TestResultFilterMiddleware_Call_CustomArgument(t *testing.T) {
	b := newResultFilterBackend(t, &config.ResultFilterConfig{Enabled: true, Argument: "_query"})
	cs := connectTestClient(t, t.Context(), b.srv)

	res := callTool(t, cs, "list_issues", map[string]any{"_query": "total", "_jmespath": "x"})
	require.Equal(t, []string{"2"}, contentTexts(t, res))
	// 既定の名前の引数は、ここではバックエンドの引数。
	require.JSONEq(t, `{"_jmespath":"x"}`, b.lastArgs("list_issues"))
}

// 極端に大きな JSON は、木に展開せずに断る。
func TestResultFilterRun_ResultTooLarge(t *testing.T) {
	run := &resultFilterRun{}
	_, ok, err := run.filter(make([]byte, maxResultFilterBytes+1))
	require.False(t, ok)
	require.ErrorContains(t, err, "the result is too large to filter")
	require.Zero(t, run.documents)
}

// tools.resultFilter は tools.include / overrides と同じく元のツール名で絞り、
// 外側の層（監査ログ、ここでは authz の位置に置いた記録用のミドルウェア）は
// 呼び出し元が送った式と、絞り込み後の結果を見る。
func TestServerToolMiddlewares_ResultFilter(t *testing.T) {
	var (
		audit    bytes.Buffer
		seenArgs atomic.Value
		seenText atomic.Value
	)
	recorder := func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			res, err := next(ctx, method, req)
			if params, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok {
				seenArgs.Store(string(params.Arguments))
				if result, ok := res.(*mcp.CallToolResult); ok && len(result.Content) > 0 {
					if text, ok := result.Content[0].(*mcp.TextContent); ok {
						seenText.Store(text.Text)
					}
				}
			}
			return res, err
		}
	}
	p := newPetstoreTestMCPServer(t, newAuditLoggerTo(&audit, config.AuthzHeaders{}, true),
		func(server *config.Server) {
			server.Tools.ResultFilter = &config.ResultFilterConfig{
				Enabled: true, Include: []string{"getpetbyid"},
			}
		})
	srv, err := p.s.Server("petstore")
	require.NoError(t, err)
	srv.AddReceivingMiddleware(recorder)
	cs := p.connect(t, "caller-token")

	res, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	withArgument := map[string]bool{}
	for _, tool := range res.Tools {
		schema, _ := tool.InputSchema.(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		_, withArgument[tool.Name] = props[config.DefaultResultFilterArgument]
	}
	// include は元の名前（getpetbyid）に照合し、一覧には公開名（get_pet）で載る。
	require.Equal(t, map[string]bool{"get_pet": true, "findpetsbystatus": false}, withArgument)

	result := callTool(t, cs, "get_pet", map[string]any{
		"petId": 1, "_jmespath": "{name: name, path: path}",
	})
	require.False(t, result.IsError)
	const want = `{"name":"doggie","path":"/pet/1"}`
	require.Equal(t, []string{want}, contentTexts(t, result))
	require.JSONEq(t, want, structuredJSON(t, result))
	require.Equal(t, want, seenText.Load())
	require.JSONEq(t, `{"petId":1,"_jmespath":"{name: name, path: path}"}`,
		seenArgs.Load().(string))

	lines := decodeAuditLines(t, &audit)
	require.Len(t, lines, 1)
	require.Equal(t, "get_pet", lines[0]["tool"])
	require.Equal(t, AuditOutcomeSuccess, lines[0]["outcome"])
	require.Equal(t, map[string]any{"petId": float64(1), "_jmespath": "{name: name, path: path}"},
		lines[0]["arguments"])
}
