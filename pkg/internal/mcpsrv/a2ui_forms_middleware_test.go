package mcpsrv

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/services/authz"
	"github.com/stretchr/testify/require"
)

// a2uiOrdersAPI は fixtures/a2ui_orders_oas.json の API。受けたリクエストを記録する。
type a2uiOrdersAPI struct {
	mu    sync.Mutex
	calls []a2uiOrdersCall
}

type a2uiOrdersCall struct {
	method string
	path   string
	body   string
}

func (a *a2uiOrdersAPI) recorded() []a2uiOrdersCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]a2uiOrdersCall(nil), a.calls...)
}

func newA2UIOrdersAPI(t *testing.T) (*httptest.Server, *a2uiOrdersAPI) {
	t.Helper()
	api := &a2uiOrdersAPI{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		api.mu.Lock()
		api.calls = append(api.calls, a2uiOrdersCall{r.Method, r.URL.Path, string(body)})
		api.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"o-1"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, api
}

// newA2UIFormsServer は orders の OpenAPI サーバーを a2ui の設定付きで立てる。
func newA2UIFormsServer(
	t *testing.T, a2ui *config.A2UIConfig, opts ...Option,
) (*MCPServer, *a2uiOrdersAPI) {
	t.Helper()
	t.Setenv("TEST", "true")
	backend, api := newA2UIOrdersAPI(t)
	servers := config.Servers{"orders": &config.Server{
		Name:    "orders",
		Spec:    "fixtures/a2ui_orders_oas.json",
		BaseURL: backend.URL,
		A2UI:    a2ui,
	}}
	mediaURL, err := url.Parse("https://media.example.com")
	require.NoError(t, err)
	s := NewMCPServer(servers,
		storage.NewContentManagementService(mediaURL, storage.NewNoopUploader()), opts...)
	require.NoError(t, s.Init(t.Context()))
	t.Cleanup(s.Close)
	return s, api
}

// newA2UIFormsSession は newA2UIFormsServer のサーバーへインメモリ接続した
// セッションを返す。
func newA2UIFormsSession(
	t *testing.T, a2ui *config.A2UIConfig,
) (*mcp.ClientSession, *a2uiOrdersAPI) {
	t.Helper()
	s, api := newA2UIFormsServer(t, a2ui)
	return connectInMemory(t, s, "orders"), api
}

func a2uiDeclaredMeta(catalogs ...string) mcp.Meta {
	ids := make([]any, 0, len(catalogs))
	for _, c := range catalogs {
		ids = append(ids, c)
	}
	return mcp.Meta{"a2ui": map[string]any{
		"rendererCapabilities": map[string]any{
			"1.0": map[string]any{"supportedCatalogIds": ids},
		},
	}}
}

func toolByName(tools []*mcp.Tool, name string) *mcp.Tool {
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	return nil
}

func TestA2UIForms_DisabledByDefault(t *testing.T) {
	session, api := newA2UIFormsSession(t, nil)
	require.Nil(t, session.InitializeResult().Capabilities.Resources)

	// 申告があっても、設定が無ければ何も変えない。
	listed, err := session.ListTools(t.Context(), &mcp.ListToolsParams{
		Meta: a2uiDeclaredMeta(a2uiBasicCatalogID),
	})
	require.NoError(t, err)
	require.Equal(t, []string{"createorder", "deleteorder", "getorder"}, sortedToolNames(listed))
	require.Empty(t, toolByName(listed.Tools, "createorder").Meta)

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "createorder",
		Arguments: map[string]any{"body": map[string]any{"title": "desk", "quantity": 1}},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, api.recorded(), 1)
}

func sortedToolNames(listed *mcp.ListToolsResult) []string {
	names := slices.Clone(toolNames(listed.Tools))
	slices.Sort(names)
	return names
}

func TestA2UIForms_CallerWithoutA2UIIsUnchanged(t *testing.T) {
	session, api := newA2UIFormsSession(t, &config.A2UIConfig{Forms: true})
	require.NotNil(t, session.InitializeResult().Capabilities.Resources)
	// SDK の既定の広告（tools.listChanged）を保つ。
	require.True(t, session.InitializeResult().Capabilities.Tools.ListChanged)

	listed, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, []string{"createorder", "deleteorder", "getorder"}, sortedToolNames(listed))
	require.Empty(t, toolByName(listed.Tools, "createorder").Meta)

	// 申告していても、必要なカタログ（basic）が無ければ非対応として扱う。
	listed, err = session.ListTools(t.Context(), &mcp.ListToolsParams{
		Meta: a2uiDeclaredMeta("https://example.com/catalogs/custom.json"),
	})
	require.NoError(t, err)
	require.Len(t, listed.Tools, 3)

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "createorder",
		Arguments: map[string]any{"body": map[string]any{"title": "desk", "quantity": 1}},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	calls := api.recorded()
	require.Len(t, calls, 1)
	require.Equal(t, http.MethodPost, calls[0].method)
}

func TestA2UIForms_DeclaredCaller(t *testing.T) {
	session, api := newA2UIFormsSession(t, &config.A2UIConfig{Forms: true})
	meta := a2uiDeclaredMeta(a2uiBasicCatalogID)

	listed, err := session.ListTools(t.Context(), &mcp.ListToolsParams{Meta: meta})
	require.NoError(t, err)
	require.Equal(t,
		[]string{
			"createorder", "createorder__submit",
			"deleteorder", "deleteorder__submit",
			"getorder",
		},
		sortedToolNames(listed))

	create := toolByName(listed.Tools, "createorder")
	require.Equal(t, map[string]any{
		"resourceUri": "a2ui://form/createorder",
		"mimeType":    "application/a2ui+json",
	}, create.Meta["ui"])
	submit := toolByName(listed.Tools, "createorder__submit")
	require.Empty(t, submit.Meta)
	require.Equal(t, create.InputSchema, submit.InputSchema)
	require.NotNil(t, submit.Annotations)
	require.False(t, submit.Annotations.ReadOnlyHint)
	// 読み取り専用のツールにはフォームを付けない。
	require.Empty(t, toolByName(listed.Tools, "getorder").Meta)

	// 読み取り専用のツールは、対応した呼び出し元でもそのまま実行する。
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Meta: meta, Name: "getorder", Arguments: map[string]any{"orderId": "o-1"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, api.recorded(), 1)

	// 書き込み系のツールは API を実行せず、フォームの初期値を返す。
	res, err = session.CallTool(t.Context(), &mcp.CallToolParams{
		Meta: meta,
		Name: "createorder",
		Arguments: map[string]any{
			"body": map[string]any{"title": "desk", "quantity": 2, "priority": "high"},
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, api.recorded(), 1, "the API must not be called before the form is submitted")
	require.Equal(t, map[string]any{
		"resourceUri": "a2ui://form/createorder",
		"mimeType":    "application/a2ui+json",
	}, res.Meta["ui"])
	require.Len(t, res.Content, 2)
	require.Contains(t, res.Content[0].(*mcp.TextContent).Text, "NOT been sent")
	embedded := res.Content[1].(*mcp.EmbeddedResource)
	require.Equal(t, "a2ui://form/createorder/data", embedded.Resource.URI)
	require.Equal(t, "application/a2ui+json", embedded.Resource.MIMEType)
	require.Equal(t, []mcp.Role{"user"}, embedded.Annotations.Audience)
	var messages []map[string]any
	require.NoError(t, json.Unmarshal([]byte(embedded.Resource.Text), &messages))
	require.Len(t, messages, 1)
	require.Equal(t, "1.0", messages[0]["version"])
	update := messages[0]["updateDataModel"].(map[string]any)
	require.Equal(t, "manifold-form-createorder", update["surfaceId"])
	require.Equal(t, "/", update["path"])
	body := update["value"].(map[string]any)["input"].(map[string]any)["body"].(map[string]any)
	require.Equal(t, "desk", body["title"])
	require.Equal(t, "2", body["quantity"])
	require.Equal(t, []any{"high"}, body["priority"])
	require.Equal(t, map[string]any{"city": "", "zip": ""}, body["address"])
}

func TestA2UIForms_Resources(t *testing.T) {
	session, _ := newA2UIFormsSession(t, &config.A2UIConfig{Forms: true})

	listed, err := session.ListResources(t.Context(), nil)
	require.NoError(t, err)
	uris := map[string]string{}
	for _, r := range listed.Resources {
		uris[r.URI] = r.MIMEType
	}
	require.Equal(t, map[string]string{
		"a2ui://form/createorder": "application/a2ui+json",
		"a2ui://form/deleteorder": "application/a2ui+json",
	}, uris)
	require.Equal(t, "public", listed.CacheScope)

	read, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{
		URI: "a2ui://form/createorder",
	})
	require.NoError(t, err)
	require.Len(t, read.Contents, 1)
	require.Equal(t, "application/a2ui+json", read.Contents[0].MIMEType)
	var template []any
	require.NoError(t, json.Unmarshal([]byte(read.Contents[0].Text), &template))
	require.Len(t, template, 2)
	surface := template[0].(map[string]any)["createSurface"].(map[string]any)
	require.Equal(t, "manifold-form-createorder", surface["surfaceId"])
	comps := a2uiTemplateComponents(t, template)
	require.Equal(t, map[string]any{"@path": "/input/body/title"},
		a2uiComponentByLabel(t, comps, "title *")["value"])

	// フォームを持たないツール・存在しないツール・データ用の URI は読めない。
	for _, uri := range []string{
		"a2ui://form/getorder",
		"a2ui://form/unknown",
		"a2ui://form/createorder/data",
		"a2ui://other/createorder",
	} {
		_, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: uri})
		require.Error(t, err, uri)
	}
}

func TestA2UIForms_SubmitExecutesTheTool(t *testing.T) {
	// assumeSupport: 申告しない呼び出し元も対応とみなす。
	session, api := newA2UIFormsSession(t,
		&config.A2UIConfig{Forms: true, AssumeSupport: true})

	listed, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.NotNil(t, toolByName(listed.Tools, "createorder__submit"))

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "createorder",
		Arguments: map[string]any{"body": map[string]any{"title": "desk"}},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Empty(t, api.recorded())

	// フォームは数値を文字列で、単一選択を配列で、未入力を空文字で送ってくる。
	res, err = session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "createorder__submit",
		Arguments: map[string]any{
			"body": map[string]any{
				"title":    "desk",
				"quantity": "2",
				"price":    "",
				"priority": []any{"high"},
				"urgent":   true,
				"dueDate":  "",
				"tags":     []any{},
				"address":  map[string]any{"city": "Osaka", "zip": ""},
				"extra":    "",
			},
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "%v", res.Content)
	calls := api.recorded()
	require.Len(t, calls, 1)
	require.Equal(t, http.MethodPost, calls[0].method)
	require.Equal(t, "/orders", calls[0].path)
	require.JSONEq(t, `{
		"title": "desk", "quantity": 2, "priority": "high", "urgent": true,
		"address": {"city": "Osaka"}
	}`, calls[0].body)

	// 型に直せない値は、API を呼ばずにツールのエラーで返す。
	res, err = session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "createorder__submit",
		Arguments: map[string]any{"body": map[string]any{"title": "desk", "quantity": "two"}},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.Content[0].(*mcp.TextContent).Text, "body.quantity: must be an integer")
	require.Len(t, api.recorded(), 1)

	// フォームを持たないツールの __submit は存在しない。
	res, err = session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "getorder__submit", Arguments: map[string]any{"orderId": "o-1"},
	})
	if err == nil {
		require.True(t, res.IsError)
	}
	require.Len(t, api.recorded(), 1)
}

func TestA2UIFormToolFromURI(t *testing.T) {
	require.Equal(t, "a2ui://form/a%2Fb", a2uiFormURI("a/b"))
	tool, ok := a2uiFormToolFromURI("a2ui://form/a%2Fb")
	require.True(t, ok)
	require.Equal(t, "a/b", tool)
	for _, uri := range []string{"", "a2ui://form/", "a2ui://form/a/b", "a2ui://form/%zz", "ui://x"} {
		_, ok := a2uiFormToolFromURI(uri)
		require.False(t, ok, uri)
	}
}

// inlineA2UIBlock は a2ui.inline のツール結果のテキストから <a2ui> ブロックの中身を
// 行ごとに取り出す。
func inlineA2UIBlock(t *testing.T, res *mcp.CallToolResult) []string {
	t.Helper()
	require.Len(t, res.Content, 1)
	text := res.Content[0].(*mcp.TextContent).Text
	_, rest, found := strings.Cut(text, "<a2ui>\n")
	require.True(t, found)
	block, tail, found := strings.Cut(rest, "\n</a2ui>")
	require.True(t, found)
	require.Empty(t, tail, "the block must end the text")
	return strings.Split(block, "\n")
}

func TestA2UIForms_Inline(t *testing.T) {
	session, api := newA2UIFormsSession(t,
		&config.A2UIConfig{Forms: true, AssumeSupport: true, Inline: true})

	call := func() *mcp.CallToolResult {
		res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "createorder",
			Arguments: map[string]any{"body": map[string]any{"title": "desk", "quantity": 2}},
		})
		require.NoError(t, err)
		require.False(t, res.IsError)
		return res
	}
	res := call()
	require.Empty(t, api.recorded(), "the API must not be called before the form is submitted")
	// リソースへのリンクは付けない（ツール結果だけでフォームが完結する）。
	require.NotContains(t, res.Meta, "ui")
	text := res.Content[0].(*mcp.TextContent).Text
	require.Contains(t, text, "NOT been sent")
	require.Contains(t, text, "createorder__submit")

	// フォーム全体が A2UI Express の <a2ui> ブロックで返る。
	lines := inlineA2UIBlock(t, res)
	require.Regexp(t, `^surface\("manifold-form-createorder-[A-Z0-9]{8}"\)$`, lines[0])
	require.Contains(t, lines, `$/input/body/title = "desk"`)
	require.Contains(t, lines, `$/input/body/quantity = "2"`)
	require.Contains(t, lines, `$/input/body/address/city = ""`)
	require.Contains(t, lines, `$/input/body/urgent = false`)
	require.Contains(t, lines[len(lines)-1], "root = Card(child=")
	var submit string
	for _, line := range lines {
		if strings.Contains(line, " = Button(") {
			submit = line
		}
	}
	// 送信ボタンは UI から MCP を呼ばず、イベントを発火するだけ。
	require.Contains(t, submit, `action=Event("createorder__submit", {body: $/input/body})`)

	// 同じツールのフォームをもう一度出しても、surfaceId は重ならない。
	require.NotEqual(t, lines[0], inlineA2UIBlock(t, call())[0])

	// 送信イベントの context（データモデルの /input/body）をそのまま引数にして実行できる。
	submitted, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "createorder__submit",
		Arguments: map[string]any{"body": map[string]any{
			"title": "desk", "quantity": "2", "price": "", "priority": []any{},
			"urgent": false, "dueDate": "", "tags": []any{}, "extra": "",
			"address": map[string]any{"city": "", "zip": ""},
		}},
	})
	require.NoError(t, err)
	require.False(t, submitted.IsError, "%v", submitted.Content)
	calls := api.recorded()
	require.Len(t, calls, 1)
	require.JSONEq(t, `{"title": "desk", "quantity": 2, "urgent": false}`, calls[0].body)
}

// authz は、フォームの送信（<tool>__submit）を実際に実行されるツール（<tool>）として
// 判定する。拒否されたツールにはフォームも送信用ツールも出さない。
func TestA2UIForms_Authz(t *testing.T) {
	d := &denyToolDecider{deny: "deleteorder"}
	s, api := newA2UIFormsServer(t,
		&config.A2UIConfig{Forms: true, AssumeSupport: true},
		WithServerMiddleware(func(name string) []mcp.Middleware {
			return []mcp.Middleware{NewAuthzMiddleware(name, name, d, testAuthzHeaders(), nil)}
		}),
	)
	srv, err := s.Server("orders")
	require.NoError(t, err)
	// authz は呼び出し元を HTTP ヘッダーから識別するので、Streamable HTTP で繋ぐ。
	httpSrv := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	))
	t.Cleanup(httpSrv.Close)
	headers := http.Header{}
	headers.Set("x-user-id", "alice")
	headers.Set("x-user-groups", "writers")
	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "0.0.1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   httpSrv.URL,
		HTTPClient: &http.Client{Transport: &headerRoundTripper{headers: headers}},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	lastDecision := func() authz.ToolRef {
		require.NotEmpty(t, d.calls)
		return d.calls[len(d.calls)-1]
	}

	// 拒否されたツールは、本体もフォームの送信用ツールも一覧に出ない。
	listed, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t,
		[]string{"createorder", "createorder__submit", "getorder"}, sortedToolNames(listed))

	// フォームのテンプレートも、一覧できるツールの分だけ。
	resources, err := session.ListResources(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, resources.Resources, 1)
	require.Equal(t, "a2ui://form/createorder", resources.Resources[0].URI)
	_, err = session.ReadResource(t.Context(), &mcp.ReadResourceParams{
		URI: "a2ui://form/deleteorder",
	})
	require.Error(t, err)

	// 拒否されたツールの送信用ツールは存在しない扱いになり、API に届かない
	// （呼び出し元に見えないツールにはフォームが無いので、<tool> へ書き換えない）。
	_, err = session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "deleteorder__submit", Arguments: map[string]any{"orderId": "o-1"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown tool")
	// 本体の呼び出しも、フォームを返さずに拒否される。
	_, err = session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "deleteorder", Arguments: map[string]any{"orderId": "o-1"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool not allowed by policy")
	require.Empty(t, api.recorded())

	// 許可されたツールは、フォームを返し（API は呼ばない）、送信で実行される。
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "createorder",
		Arguments: map[string]any{"body": map[string]any{"title": "desk", "quantity": 1}},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Contains(t, res.Content[0].(*mcp.TextContent).Text, "NOT been sent")
	require.Empty(t, api.recorded())

	d.calls = nil
	res, err = session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "createorder__submit",
		Arguments: map[string]any{"body": map[string]any{"title": "desk", "quantity": "1"}},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "%v", res.Content)
	// 判定は送信用ツールの名前ではなく、実行されるツールの名前で行われる。
	require.Equal(t,
		authz.ToolRef{Server: "orders", Service: "orders", Name: "createorder"}, lastDecision())
	for _, call := range d.calls {
		require.NotContains(t, call.Name, a2uiSubmitSuffix)
	}
	calls := api.recorded()
	require.Len(t, calls, 1)
	require.JSONEq(t, `{"title": "desk", "quantity": 1}`, calls[0].body)
}
