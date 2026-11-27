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

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/jmespath"
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
	// バックエンドの SDK を通った結果のように、テキストと structuredContent でキーの順序と
	// 数値の表記が違う。
	b.add(&mcp.Tool{Name: "reordered"}, func() *mcp.CallToolResult {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: `{"b": {"y": 2, "x": 1.0}, "a": 1}`},
			},
			StructuredContent: map[string]any{"a": 1, "b": map[string]any{"x": 1, "y": 2}},
		}
	})
	b.add(&mcp.Tool{Name: "summary_and_structured"}, func() *mcp.CallToolResult {
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: "2 issues found"}},
			StructuredContent: map[string]any{"total": 2},
		}
	})
	// テキストの JSON と structuredContent が別の内容。
	b.add(&mcp.Tool{Name: "different_documents"}, func() *mcp.CallToolResult {
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: `{"summary": "ok"}`}},
			StructuredContent: json.RawMessage(`{"items": [1, 2]}`),
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

// allResultFilterFeatures は、全ツールで条件と位置の構文も使える設定。
func allResultFilterFeatures() *config.ResultFilterConfig {
	return &config.ResultFilterConfig{
		Enabled: true,
		Features: config.ResultFilterFeatures{
			Conditions: []string{"*"}, Indexing: []string{"*"},
		},
	}
}

func callTool(
	t *testing.T, cs *mcp.ClientSession, name string, args any,
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

func toolProperties(t *testing.T, tool *mcp.Tool) map[string]any {
	t.Helper()
	schema, ok := tool.InputSchema.(map[string]any)
	require.True(t, ok, tool.Name)
	props, _ := schema["properties"].(map[string]any)
	return props
}

func listedTools(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	res, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	tools := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		tools[tool.Name] = tool
	}
	return tools
}

func TestResultFilterMiddleware_DisabledIsNoMiddleware(t *testing.T) {
	require.Nil(t, newResultFilterMiddleware("test", nil))
	require.Nil(t, newResultFilterMiddleware("test", &config.ResultFilterConfig{}))
	require.Nil(t, newResultFilterMiddleware("test", &config.ResultFilterConfig{
		Argument: "_q", Include: []string{"*"},
		Features: config.ResultFilterFeatures{Conditions: []string{"*"}},
	}))
}

func TestResultFilterDescription(t *testing.T) {
	const prefix = "Optional JMESPath expression. When set, the tool returns only the " +
		"expression's result instead of the full JSON, e.g. "
	tests := []struct {
		name     string
		features jmespath.Features
		want     string
	}{
		{"field selection only", jmespath.Features{},
			prefix + `"items[].{id: id, title: title}". Supported: field selection. ` +
				"Not supported: [?conditions], [index], slices, functions."},
		{"conditions", jmespath.Features{Conditions: true},
			prefix + `"items[?state=='open'].{id: id, title: title}". ` +
				"Supported: field selection, [?conditions]. " +
				"Not supported: [index], slices, functions."},
		{"indexing", jmespath.Features{Indexing: true},
			prefix + `"items[].{id: id, title: title}". ` +
				"Supported: field selection, [index] and [start:stop] slices. " +
				"Not supported: [?conditions], functions."},
		{"both", jmespath.Features{Conditions: true, Indexing: true},
			prefix + `"items[?state=='open'].{id: id, title: title}". ` +
				"Supported: field selection, [?conditions], [index] and [start:stop] slices. " +
				"Not supported: functions."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, resultFilterDescription(tt.features))
			// 説明に載せた例は、その構文の設定で解釈できる。
			example := strings.Split(tt.want, `"`)[1]
			_, err := jmespath.Compile(example, tt.features)
			require.NoError(t, err)
		})
	}
}

func TestResultFilterMiddleware_ToolsList(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
	handler := func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	}
	objectSchema := map[string]any{"type": "object"}
	outputSchema := map[string]any{
		"type":        "object",
		"description": "A page of issues",
		"properties":  map[string]any{"total": map[string]any{"type": "integer"}},
		"required":    []any{"total"},
	}
	srv.AddTool(&mcp.Tool{
		Name: "list_issues",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"state": map[string]any{"type": "string"}},
			"required":   []any{"state"},
		},
		OutputSchema: outputSchema,
	}, handler)
	srv.AddTool(&mcp.Tool{Name: "search_issues", InputSchema: objectSchema}, handler)
	srv.AddTool(&mcp.Tool{Name: "get_issue", InputSchema: objectSchema}, handler)
	srv.AddTool(&mcp.Tool{
		Name: "raw_schema", InputSchema: json.RawMessage(`{"type":"object","properties":{"a":{}}}`),
	}, handler)
	srv.AddTool(&mcp.Tool{
		Name: "delete_issue", InputSchema: objectSchema, OutputSchema: outputSchema,
	}, handler)
	srv.AddTool(&mcp.Tool{
		Name: "show_chart", InputSchema: objectSchema,
		Meta: mcp.Meta{mcpAppsMetaKey: map[string]any{"resourceUri": "ui://chart"}},
	}, handler)
	srv.AddTool(&mcp.Tool{Name: createPairingCodeToolName, InputSchema: objectSchema}, handler)
	srv.AddReceivingMiddleware(newResultFilterMiddleware("test", &config.ResultFilterConfig{
		Enabled: true,
		Exclude: []string{"delete_*"},
		Features: config.ResultFilterFeatures{
			Conditions: []string{"list_*", "search_*"},
			Indexing:   []string{"list_*", "get_*", "delete_*"},
		},
	}, createPairingCodeToolName))
	cs := connectTestClient(t, t.Context(), srv)
	tools := listedTools(t, cs)

	// 対象のツールには式の引数が足され、元のプロパティと required は残る。説明は、その
	// ツールで有効な構文に合わせる。
	argument := func(features jmespath.Features) map[string]any {
		return map[string]any{"type": "string", "description": resultFilterDescription(features)}
	}
	const name = config.DefaultResultFilterArgument
	require.Equal(t, argument(jmespath.Features{Conditions: true, Indexing: true}),
		toolProperties(t, tools["list_issues"])[name])
	require.Contains(t, toolProperties(t, tools["list_issues"]), "state")
	require.Equal(t, []any{"state"}, tools["list_issues"].InputSchema.(map[string]any)["required"])
	require.Equal(t, argument(jmespath.Features{Conditions: true}),
		toolProperties(t, tools["search_issues"])[name])
	require.Equal(t, argument(jmespath.Features{Indexing: true}),
		toolProperties(t, tools["get_issue"])[name])
	require.Equal(t, argument(jmespath.Features{}), toolProperties(t, tools["raw_schema"])[name])
	require.Contains(t, toolProperties(t, tools["raw_schema"]), "a")

	// outputSchema は残す。元のスキーマは読めるまま、どんな結果でも検証を通る形になる。
	require.Equal(t, map[string]any{
		"type":        "object",
		"description": "A page of issues",
		"anyOf":       []any{outputSchema, map[string]any{}},
	}, tools["list_issues"].OutputSchema)

	// exclude したツール、UI を持つツール、ゲートウェイ自身のツールはそのまま。
	for _, tool := range []string{"delete_issue", "show_chart", createPairingCodeToolName} {
		require.NotContains(t, toolProperties(t, tools[tool]), name, tool)
	}
	require.Equal(t, outputSchema, tools["delete_issue"].OutputSchema)

	// SDK が保持する登録済みのツールは書き換えない（2 回目の一覧も同じ）。
	require.Equal(t, tools, listedTools(t, cs))
}

// permissiveOutputSchema が返すスキーマは、元のスキーマを読める形で持ち、参照が切れず、
// 絞り込む前の結果も、絞り込んだ結果（オブジェクト、または {"result": ...}）も通す。
func TestPermissiveOutputSchema(t *testing.T) {
	validate := func(t *testing.T, schema any, instances ...string) {
		t.Helper()
		raw, err := json.Marshal(schema)
		require.NoError(t, err)
		var s jsonschema.Schema
		require.NoError(t, json.Unmarshal(raw, &s))
		resolved, err := s.Resolve(nil)
		require.NoError(t, err, "the schema does not resolve: %s", raw)
		for _, instance := range instances {
			var v any
			require.NoError(t, json.Unmarshal([]byte(instance), &v))
			require.NoError(t, resolved.Validate(v), "%s does not accept %s", raw, instance)
		}
	}
	decode := func(t *testing.T, schema string) map[string]any {
		t.Helper()
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(schema), &m))
		return m
	}
	const filtered = `{"n": "not the declared type"}`

	t.Run("object schema", func(t *testing.T) {
		const original = `{"type":"object","title":"Issue","description":"An issue",
			"properties":{"id":{"type":"integer"},"title":{"type":"string"}},
			"required":["id","title"],"additionalProperties":false}`
		got := permissiveOutputSchema(json.RawMessage(original))
		require.Equal(t, map[string]any{
			"type": "object", "title": "Issue", "description": "An issue",
			"anyOf": []any{decode(t, original), map[string]any{}},
		}, got)
		validate(t, got, `{"id":1,"title":"a"}`, filtered, `{"id":"1"}`, `{"result":[1,2]}`, `{}`)
	})

	// pydantic などが出す形。定義は最上位へ移すので、参照は切れない。
	t.Run("$defs are hoisted", func(t *testing.T) {
		const original = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object",
			"properties":{"owner":{"$ref":"#/$defs/User"}},"required":["owner"],
			"$defs":{"User":{"type":"object","properties":{"team":{"$ref":"#/$defs/Team"}},"required":["team"]},
				"Team":{"type":"object"}}}`
		got, ok := permissiveOutputSchema(json.RawMessage(original)).(map[string]any)
		require.True(t, ok)
		require.Equal(t, "https://json-schema.org/draft/2020-12/schema", got["$schema"])
		require.Contains(t, got["$defs"], "User")
		nested, ok := got["anyOf"].([]any)[0].(map[string]any)
		require.True(t, ok)
		require.NotContains(t, nested, "$defs")
		require.NotContains(t, nested, "$schema")
		require.Contains(t, nested, "properties")
		validate(t, got, `{"owner":{"team":{}}}`, filtered, `{"owner":1}`)
	})

	t.Run("definitions are hoisted", func(t *testing.T) {
		const original = `{"type":"object","properties":{"old":{"$ref":"#/definitions/Legacy"}},
			"required":["old"],"definitions":{"Legacy":{"type":"string"}}}`
		got, ok := permissiveOutputSchema(json.RawMessage(original)).(map[string]any)
		require.True(t, ok)
		require.Contains(t, got["definitions"], "Legacy")
		require.NotContains(t, got["anyOf"].([]any)[0], "definitions")
		validate(t, got, `{"old":"x"}`, filtered, `{"old":1}`)
	})

	// $id があるスキーマは、それ自体が参照の基点になるので、中身を動かさない。
	t.Run("a schema with $id is embedded as it is", func(t *testing.T) {
		const original = `{"$id":"https://example.com/issue.json","type":"object",
			"properties":{"owner":{"$ref":"#/$defs/User"}},"$defs":{"User":{"type":"object"}}}`
		got, ok := permissiveOutputSchema(json.RawMessage(original)).(map[string]any)
		require.True(t, ok)
		require.NotContains(t, got, "$defs")
		require.Equal(t, []any{decode(t, original), map[string]any{}}, got["anyOf"])
		validate(t, got, `{"owner":{}}`, filtered)
	})

	// 定義以外を指す参照は移せないので、元のスキーマは載せない。
	t.Run("a pointer reference outside the definitions", func(t *testing.T) {
		const original = `{"type":"object","description":"Tree",
			"properties":{"value":{"type":"integer"},"children":{"type":"array","items":{"$ref":"#/properties/value"}}}}`
		got := permissiveOutputSchema(json.RawMessage(original))
		require.Equal(t, map[string]any{"type": "object", "description": "Tree"}, got)
		validate(t, got, `{"value":1,"children":[2]}`, filtered)
	})

	// 自分自身（"#"）を指す再帰は、最上位が何でも通すスキーマになるだけで切れない。
	t.Run("a recursive reference to the root", func(t *testing.T) {
		const original = `{"type":"object","properties":{"children":{"type":"array","items":{"$ref":"#"}}}}`
		got := permissiveOutputSchema(json.RawMessage(original))
		validate(t, got, `{"children":[{"children":[]}]}`, filtered)
	})

	// オブジェクト以外を返すと宣言したスキーマには、type を付けない。
	t.Run("a schema that is not of type object", func(t *testing.T) {
		got, ok := permissiveOutputSchema(map[string]any{
			"type": "array", "items": map[string]any{"type": "integer"},
		}).(map[string]any)
		require.True(t, ok)
		require.NotContains(t, got, "type")
		validate(t, got, `[1,2]`, `{"result":[1]}`, `"text"`)
	})

	t.Run("a boolean schema is returned as it is", func(t *testing.T) {
		require.Equal(t, true, permissiveOutputSchema(true))
		require.Equal(t, json.RawMessage(`false`), permissiveOutputSchema(json.RawMessage(`false`)))
	})
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
		schema, ok := listedTools(t, cs)["wp_posts"].InputSchema.(map[string]any)
		require.True(t, ok)
		return schema
	}

	cs := newServer(&config.ResultFilterConfig{Enabled: true})
	schema := schemaOf(cs)
	props, _ := schema["properties"].(map[string]any)
	require.Equal(t, map[string]any{
		"type": "string", "description": resultFilterDescription(jmespath.Features{}),
	}, props["_jmespath"])
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
	b := newResultFilterBackend(t, allResultFilterFeatures())
	cs := connectTestClient(t, t.Context(), b.srv)

	t.Run("without the argument the call is forwarded untouched", func(t *testing.T) {
		res := callTool(t, cs, "list_issues", map[string]any{"state": "open"})
		require.Equal(t, []string{issuesJSON}, contentTexts(t, res))
		require.JSONEq(t, `{"state":"open"}`, b.lastArgs("list_issues"))
	})

	t.Run("the expression selects fields and is not forwarded", func(t *testing.T) {
		res := callTool(t, cs, "list_issues", map[string]any{
			"state":     "open",
			"_jmespath": "items[].{id: id, title: title}",
		})
		require.False(t, res.IsError)
		// float64 では表せない ID も桁を保つ。
		require.Equal(t, []string{
			`[{"id":12345678901234567890,"title":"Fix login"},{"id":2,"title":"Add docs"}]`,
		}, contentTexts(t, res))
		require.Nil(t, res.StructuredContent)
		require.JSONEq(t, `{"state":"open"}`, b.lastArgs("list_issues"))
	})

	t.Run("conditions and slices", func(t *testing.T) {
		res := callTool(t, cs, "list_issues", map[string]any{
			// 絞った結果の先頭を取るには、パイプで射影を止めてからスライスする。
			"_jmespath": "items[?state=='open' && id > `12345678901234567889`] | [:1].title",
		})
		require.Equal(t, []string{`["Fix login"]`}, contentTexts(t, res))
		require.Equal(t, `{}`, b.lastArgs("list_issues"))
	})

	// キーは JSON としてエスケープして書いても同じキー。
	t.Run("an escaped argument name is the same argument", func(t *testing.T) {
		res := callTool(t, cs, "list_issues",
			json.RawMessage(`{"state":"open","`+`\`+`u005fjmespath":"total"}`))
		require.Equal(t, []string{"2"}, contentTexts(t, res))
		require.JSONEq(t, `{"state":"open"}`, b.lastArgs("list_issues"))
	})

	t.Run("an object result is returned as structuredContent too", func(t *testing.T) {
		res := callTool(t, cs, "structured", map[string]any{
			"_jmespath": "{total: total, titles: items[].title}",
		})
		const want = `{"total":2,"titles":["Fix login","Add docs"]}`
		require.Equal(t, []string{want}, contentTexts(t, res))
		require.JSONEq(t, want, structuredJSON(t, res))
	})

	// outputSchema を持つツールは structuredContent を省略できないので、オブジェクトで
	// ない結果は包んで返す。
	t.Run("a result that is not an object is wrapped in structuredContent", func(t *testing.T) {
		res := callTool(t, cs, "structured", map[string]any{"_jmespath": "items[].title"})
		require.Equal(t, []string{`["Fix login","Add docs"]`}, contentTexts(t, res))
		require.JSONEq(t, `{"result":["Fix login","Add docs"]}`, structuredJSON(t, res))
	})

	t.Run("structuredContent without a text gets the result as a text", func(t *testing.T) {
		res := callTool(t, cs, "structured_only", map[string]any{"_jmespath": "items[0].title"})
		require.Equal(t, []string{`"Fix login"`}, contentTexts(t, res))
		require.JSONEq(t, `{"result":"Fix login"}`, structuredJSON(t, res))
	})

	// キーの順序や数値の表記が違うだけなら、同じ結果を 2 度は返さない。
	t.Run("a text equal to structuredContent is not repeated", func(t *testing.T) {
		res := callTool(t, cs, "reordered", map[string]any{"_jmespath": "b"})
		require.Equal(t, []string{`{"y":2,"x":1.0}`}, contentTexts(t, res))
		require.JSONEq(t, `{"x":1,"y":2}`, structuredJSON(t, res))
	})

	t.Run("a text that is not JSON is kept next to the filtered JSON", func(t *testing.T) {
		res := callTool(t, cs, "summary_and_structured", map[string]any{"_jmespath": "total"})
		require.Equal(t, []string{"2 issues found", "2"}, contentTexts(t, res))
		require.JSONEq(t, `{"result":2}`, structuredJSON(t, res))
	})

	// テキストの JSON には無いフィールドでも、structuredContent から選んだ結果は返る。
	t.Run("structuredContent that differs from the text is returned as a text", func(t *testing.T) {
		res := callTool(t, cs, "different_documents", map[string]any{"_jmespath": "items"})
		require.Equal(t, []string{"null", "[1,2]"}, contentTexts(t, res))
		require.JSONEq(t, `{"result":[1,2]}`, structuredJSON(t, res))
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
		const hint = "_jmespath returned null. The result is an object with keys: total, items."
		res := callTool(t, cs, "list_issues", map[string]any{"_jmespath": "issues[].id"})
		require.False(t, res.IsError)
		require.Equal(t, []string{"null", hint}, contentTexts(t, res))

		// structuredContent しか読まない呼び出し元にも、手がかりが届く。
		res = callTool(t, cs, "structured", map[string]any{"_jmespath": "issues[].id"})
		require.Equal(t, []string{"null", hint}, contentTexts(t, res))
		require.JSONEq(t, `{"result":null,"hint":"`+hint+`"}`, structuredJSON(t, res))
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

// 条件で絞る構文と、位置・件数で絞る構文は、設定で有効にしたツールでだけ使える。
// 使えない構文の式は、バックエンドを呼ぶ前に断る。
func TestResultFilterMiddleware_Call_FeaturesPerTool(t *testing.T) {
	b := newResultFilterBackend(t, &config.ResultFilterConfig{
		Enabled: true,
		Features: config.ResultFilterFeatures{
			Conditions: []string{"list_*", "structured"},
			Indexing:   []string{"list_*", "per_item"},
		},
	})
	cs := connectTestClient(t, t.Context(), b.srv)
	const (
		condition = "items[?state=='open'].title"
		slice     = "items[:1].title"
		fields    = "items[].title"
	)
	const (
		conditionsOff = `invalid _jmespath: "[?" cannot be used: filtering by a condition ` +
			"([?...] with comparison and logical operators) is not enabled at offset 5. " +
			"The tool was not called."
		indexingOff = "invalid _jmespath: indexes and slices ([0], [:5]) are not enabled " +
			"at offset 5. The tool was not called."
	)
	tests := []struct {
		tool       string
		expression string
		want       string
		wantErr    string
	}{
		// list_issues は両方、structured は条件だけ、structured_only はどちらも無効。
		{tool: "list_issues", expression: condition, want: `["Fix login"]`},
		{tool: "list_issues", expression: slice, want: `["Fix login"]`},
		{tool: "structured", expression: condition, want: `["Fix login"]`},
		{tool: "structured", expression: slice, wantErr: indexingOff},
		{tool: "structured_only", expression: condition, wantErr: conditionsOff},
		{tool: "structured_only", expression: slice, wantErr: indexingOff},
		// フィールドの選択は、どのツールでも使える。
		{tool: "structured_only", expression: fields, want: `["Fix login","Add docs"]`},
	}
	for _, tt := range tests {
		t.Run(tt.tool+" "+tt.expression, func(t *testing.T) {
			before := b.calls.Load()
			res := callTool(t, cs, tt.tool, map[string]any{"_jmespath": tt.expression})
			if tt.wantErr != "" {
				require.True(t, res.IsError)
				require.Equal(t, []string{tt.wantErr}, contentTexts(t, res))
				require.Equal(t, before, b.calls.Load(), "the backend must not be called")
				return
			}
			require.False(t, res.IsError, contentTexts(t, res))
			require.Equal(t, tt.want, contentTexts(t, res)[0])
		})
	}
}

// 式の誤りは、バックエンドを呼ぶ前にツールのエラーとして返す。
func TestResultFilterMiddleware_Call_InvalidExpression(t *testing.T) {
	b := newResultFilterBackend(t, allResultFilterFeatures())
	cs := connectTestClient(t, t.Context(), b.srv)

	tests := []struct {
		name       string
		expression any
		want       string
	}{
		{"syntax error", "items[?state = 'open']",
			"invalid _jmespath: unexpected '=' (use '==' to compare) at offset 13. " +
				"The tool was not called."},
		{"function", "length(items)",
			"invalid _jmespath: functions are not supported (length()): an expression can only " +
				"select and filter at offset 0. The tool was not called."},
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

// 呼び出しは成功したが式を評価できなかった場合（結果を膨らませる式が上限で止まった
// 場合）は、結果を返さずにエラーにする。
func TestResultFilterMiddleware_Call_EvaluationError(t *testing.T) {
	b := newResultFilterBackend(t, &config.ResultFilterConfig{Enabled: true})
	cs := connectTestClient(t, t.Context(), b.srv)

	bomb := "@" + strings.Repeat("|[@,@][]", 40)
	res := callTool(t, cs, "mixed", map[string]any{"_jmespath": bomb})
	require.True(t, res.IsError)
	require.Equal(t, []string{
		"_jmespath could not be evaluated: the expression is too expensive to evaluate on this " +
			"result. The tool call itself completed, so do not repeat it for its side effects. " +
			"The result is an array of 2 elements; its first element is an object with keys: id, x.",
	}, contentTexts(t, res))
	require.Equal(t, resultTypeComplete, resultTypeOf(t, res))
	require.Equal(t, int32(1), b.calls.Load())
}

// include / exclude に合わないツールやゲートウェイ自身のツールでは、同じ名前の引数も
// バックエンドのものとしてそのまま渡す。
func TestResultFilterMiddleware_Call_ToolsOutOfScope(t *testing.T) {
	b := newResultFilterBackend(t, &config.ResultFilterConfig{
		Enabled: true, Include: []string{"list_*", "struct*"}, Exclude: []string{"structured_only"},
		// features に挙げても、対象外のツールでは何も起きない。
		Features: config.ResultFilterFeatures{Conditions: []string{"*"}, Indexing: []string{"*"}},
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

func TestStructuredResult(t *testing.T) {
	// オブジェクトは式が選んだ形のまま返す。
	require.JSONEq(t, `{"id":1}`, string(structuredResult([]byte(`{"id":1}`), "")))
	require.JSONEq(t, `{"id":1}`, string(structuredResult([]byte(`{"id":1}`), "a hint")))
	// それ以外は包む。
	require.JSONEq(t, `{"result":[1,2]}`, string(structuredResult([]byte(`[1,2]`), "")))
	require.JSONEq(t, `{"result":12345678901234567890}`,
		string(structuredResult([]byte(`12345678901234567890`), "")))
	require.JSONEq(t, `{"result":null,"hint":"a \"hint\""}`,
		string(structuredResult([]byte(`null`), `a "hint"`)))
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

	withArgument := map[string]bool{}
	for name, tool := range listedTools(t, cs) {
		_, withArgument[name] = toolProperties(t, tool)[config.DefaultResultFilterArgument]
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
