package mcpsrv

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/services/guardrail"
	"github.com/stretchr/testify/require"
)

// fakeGuardrail は呼び出しを記録し、入力の種類（request / response）ごとに
// fn の結果を返す Guardrail。
type fakeGuardrail struct {
	mu       sync.Mutex
	requests []guardrail.Request
	fn       func(req guardrail.Request) (guardrail.Result, error)
}

func (g *fakeGuardrail) Apply(_ context.Context, req guardrail.Request) (guardrail.Result, error) {
	g.mu.Lock()
	g.requests = append(g.requests, req)
	g.mu.Unlock()
	return g.fn(req)
}

// maskEmail は "@" を含むテキストのメールアドレスを [EMAIL] に書き換える。
func maskEmail(req guardrail.Request) (guardrail.Result, error) {
	out := make([]string, len(req.Texts))
	changed := false
	for i, text := range req.Texts {
		out[i] = strings.ReplaceAll(text, "a@example.com", "[EMAIL]")
		changed = changed || out[i] != text
	}
	if !changed {
		return guardrail.Result{Action: guardrail.ActionNone}, nil
	}
	return guardrail.Result{Action: guardrail.ActionIntervened, Texts: out}, nil
}

func guardrailTestClient(
	t *testing.T, g guardrail.Guardrail, preCall, postCall bool, calls *atomic.Int32,
) *mcp.ClientSession {
	t.Helper()
	srv := newToolTestServer(t, calls, "getpet")
	srv.AddReceivingMiddleware(NewGuardrailMiddleware("petstore", "pets", g,
		GuardrailOptions{PreCall: preCall, PostCall: postCall}))
	return connectTestClient(t, t.Context(), srv)
}

// pre_call: 書き換えた引数でバックエンドを呼ぶ。
func TestGuardrailMiddleware_PreCallRewritesArguments(t *testing.T) {
	g := &fakeGuardrail{fn: maskEmail}
	cs := guardrailTestClient(t, g, true, false, nil)

	got := callText(t, cs, "getpet", map[string]any{"email": "a@example.com"})
	require.Equal(t, `getpet:{"email":"[EMAIL]"}`, got)

	require.Len(t, g.requests, 1)
	require.Equal(t, guardrail.InputTypeRequest, g.requests[0].InputType)
	require.JSONEq(t, `{"email":"a@example.com"}`, g.requests[0].Texts[0])
	require.Equal(t, map[string]any{
		"server": "petstore", "service": "pets", "method": "tools/call", "tool": "getpet",
	}, g.requests[0].Params)
}

// post_call: 結果のテキストを書き換えて返す。
func TestGuardrailMiddleware_PostCallRewritesResult(t *testing.T) {
	g := &fakeGuardrail{fn: maskEmail}
	cs := guardrailTestClient(t, g, false, true, nil)

	got := callText(t, cs, "getpet", map[string]any{"email": "a@example.com"})
	require.Equal(t, `getpet:{"email":"[EMAIL]"}`, got)
	require.Len(t, g.requests, 1)
	require.Equal(t, guardrail.InputTypeResponse, g.requests[0].InputType)
}

// 変更が無ければそのまま通す。両方有効なら前後で1回ずつ呼ぶ。
func TestGuardrailMiddleware_None(t *testing.T) {
	g := &fakeGuardrail{fn: maskEmail}
	cs := guardrailTestClient(t, g, true, true, nil)
	require.Equal(t, `getpet:{"id":1}`, callText(t, cs, "getpet", map[string]any{"id": 1}))
	require.Len(t, g.requests, 2)
}

// BLOCKED は理由付きで拒否し、pre_call ならバックエンドを呼ばない。
func TestGuardrailMiddleware_Blocked(t *testing.T) {
	blocked := func(guardrail.Request) (guardrail.Result, error) {
		return guardrail.Result{
			Action:        guardrail.ActionBlocked,
			BlockedReason: "contains secrets",
		}, nil
	}
	for _, tc := range []struct {
		name              string
		preCall, postCall bool
		wantBackendCalls  int32
	}{
		{name: "pre_call", preCall: true, wantBackendCalls: 0},
		{name: "post_call", postCall: true, wantBackendCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			cs := guardrailTestClient(
				t,
				&fakeGuardrail{fn: blocked},
				tc.preCall,
				tc.postCall,
				&calls,
			)
			_, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
				Name: "getpet", Arguments: map[string]any{"id": 1},
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), "blocked by guardrail: contains secrets")
			require.Equal(t, tc.wantBackendCalls, calls.Load())
		})
	}
}

// ガードレールが判定できなければ拒否する（fail closed）。理由は呼び出し元に返さない。
func TestGuardrailMiddleware_FailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func(guardrail.Request) (guardrail.Result, error)
	}{
		{name: "error", fn: func(guardrail.Request) (guardrail.Result, error) {
			return guardrail.Result{}, errors.New("guardrail down at 10.0.0.1")
		}},
		{name: "arguments rewritten into non-object", fn: func(guardrail.Request) (guardrail.Result, error) {
			return guardrail.Result{Action: guardrail.ActionIntervened, Texts: []string{`"x"`}}, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			cs := guardrailTestClient(t, &fakeGuardrail{fn: tc.fn}, true, false, &calls)
			_, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
				Name: "getpet", Arguments: map[string]any{"id": 1},
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), "guardrail check failed")
			require.NotContains(t, err.Error(), "10.0.0.1")
			require.Zero(t, calls.Load())
		})
	}
}

// post_call は StructuredContent も書き換え、テキスト以外の Content は通す。
func TestGuardrailMiddleware_PostCallStructuredContent(t *testing.T) {
	g := &fakeGuardrail{fn: maskEmail}
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
	srv.AddTool(
		&mcp.Tool{Name: "whoami", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.ImageContent{Data: []byte("img"), MIMEType: "image/png"},
					&mcp.TextContent{Text: "mail a@example.com"},
				},
				StructuredContent: map[string]any{"email": "a@example.com"},
			}, nil
		},
	)
	srv.AddReceivingMiddleware(
		NewGuardrailMiddleware("svc", "svc", g, GuardrailOptions{PostCall: true}),
	)
	cs := connectTestClient(t, t.Context(), srv)

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "whoami"})
	require.NoError(t, err)
	require.Len(t, res.Content, 2)
	require.IsType(t, &mcp.ImageContent{}, res.Content[0])
	require.Equal(t, "mail [EMAIL]", res.Content[1].(*mcp.TextContent).Text)
	require.Equal(t, map[string]any{"email": "[EMAIL]"}, res.StructuredContent)
	require.Equal(
		t,
		[]string{"mail a@example.com", `{"email":"a@example.com"}`},
		g.requests[0].Texts,
	)
}

// tools/call 以外は素通しする。
func TestGuardrailMiddleware_OtherMethods(t *testing.T) {
	g := &fakeGuardrail{fn: maskEmail}
	cs := guardrailTestClient(t, g, true, true, nil)
	_, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Empty(t, g.requests)
}

// fileResultTestServer は、テキスト以外の Content（画像・音声・埋め込みリソース）を
// 返すツール files と、テキストと blob を返すリソース file:///doc を持つサーバー。
func fileResultTestServer(
	t *testing.T, g guardrail.Guardrail, opts GuardrailOptions, reads *atomic.Int32,
) *mcp.ClientSession {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
	srv.AddTool(
		&mcp.Tool{Name: "files", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{
				&mcp.ImageContent{Data: []byte("img"), MIMEType: "image/png"},
				&mcp.AudioContent{Data: []byte("wav"), MIMEType: "audio/wav"},
				&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
					URI: "file:///note.txt", MIMEType: "text/plain", Text: "mail a@example.com",
				}},
				&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
					URI: "file:///doc.pdf", MIMEType: "application/pdf", Blob: []byte("%PDF"),
				}},
			}}, nil
		},
	)
	srv.AddResource(
		&mcp.Resource{URI: "file:///doc", Name: "doc"},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			if reads != nil {
				reads.Add(1)
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
				{URI: "file:///doc", MIMEType: "text/plain", Text: "mail a@example.com"},
				{URI: "file:///doc#pdf", MIMEType: "application/pdf", Blob: []byte("%PDF")},
			}}, nil
		},
	)
	srv.AddReceivingMiddleware(NewGuardrailMiddleware("svc", "svc", g, opts))
	return connectTestClient(t, t.Context(), srv)
}

// post_call は埋め込みリソースのテキストも書き換える。files が無効なら、画像・音声・
// blob はガードレールへ送らずに通す。
func TestGuardrailMiddleware_PostCallEmbeddedResourceText(t *testing.T) {
	g := &fakeGuardrail{fn: maskEmail}
	cs := fileResultTestServer(t, g, GuardrailOptions{PostCall: true}, nil)

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "files"})
	require.NoError(t, err)
	require.Len(t, res.Content, 4)
	require.Equal(t, []byte("img"), res.Content[0].(*mcp.ImageContent).Data)
	require.Equal(t, []byte("wav"), res.Content[1].(*mcp.AudioContent).Data)
	note := res.Content[2].(*mcp.EmbeddedResource).Resource
	require.Equal(t, "mail [EMAIL]", note.Text)
	require.Equal(t, "file:///note.txt", note.URI)
	require.Equal(t, []byte("%PDF"), res.Content[3].(*mcp.EmbeddedResource).Resource.Blob)

	require.Len(t, g.requests, 1)
	require.Equal(t, []string{"mail a@example.com"}, g.requests[0].Texts)
	require.Empty(t, g.requests[0].Files)
}

// files が有効なら、画像・音声・埋め込みリソースの blob をガードレールへ送る。
func TestGuardrailMiddleware_PostCallFiles(t *testing.T) {
	g := &fakeGuardrail{fn: maskEmail}
	cs := fileResultTestServer(t, g,
		GuardrailOptions{PostCall: true, Files: true, MaxFileBytes: 1024}, nil)

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "files"})
	require.NoError(t, err)
	require.Equal(t, "mail [EMAIL]", res.Content[2].(*mcp.EmbeddedResource).Resource.Text)
	// ファイルは書き換えないので、そのまま返る。
	require.Equal(t, []byte("img"), res.Content[0].(*mcp.ImageContent).Data)
	require.Equal(t, []byte("%PDF"), res.Content[3].(*mcp.EmbeddedResource).Resource.Blob)

	require.Len(t, g.requests, 1)
	require.Equal(t, []guardrail.File{
		{MIMEType: "image/png", Data: []byte("img")},
		{MIMEType: "audio/wav", Data: []byte("wav")},
		{URI: "file:///doc.pdf", MIMEType: "application/pdf", Data: []byte("%PDF")},
	}, g.requests[0].Files)
}

// ファイルの合計が上限を超える結果は検査できないので、ガードレールを呼ばずに拒否する。
func TestGuardrailMiddleware_FilesOverLimit(t *testing.T) {
	g := &fakeGuardrail{fn: maskEmail}
	// img(3) + wav(3) + %PDF(4) = 10 バイト。
	cs := fileResultTestServer(t, g,
		GuardrailOptions{PostCall: true, ResourceRead: true, Files: true, MaxFileBytes: 9}, nil)

	_, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "files"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "blocked by guardrail: files exceed the size limit")
	require.Empty(t, g.requests)

	// 上限ちょうどまでは通る（resources/read の blob は 4 バイト）。
	_, err = cs.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "file:///doc"})
	require.NoError(t, err)
	require.Len(t, g.requests, 1)
}

// resource_read: resources/read のテキストを書き換え、files が有効なら blob も送る。
func TestGuardrailMiddleware_ResourceRead(t *testing.T) {
	g := &fakeGuardrail{fn: maskEmail}
	cs := fileResultTestServer(t, g,
		GuardrailOptions{ResourceRead: true, Files: true, MaxFileBytes: 1024}, nil)

	res, err := cs.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "file:///doc"})
	require.NoError(t, err)
	require.Len(t, res.Contents, 2)
	require.Equal(t, "mail [EMAIL]", res.Contents[0].Text)
	require.Equal(t, "file:///doc", res.Contents[0].URI)
	require.Equal(t, []byte("%PDF"), res.Contents[1].Blob)

	require.Len(t, g.requests, 1)
	req := g.requests[0]
	require.Equal(t, guardrail.InputTypeResponse, req.InputType)
	require.Equal(t, []string{"mail a@example.com"}, req.Texts)
	require.Equal(t, []guardrail.File{
		{URI: "file:///doc#pdf", MIMEType: "application/pdf", Data: []byte("%PDF")},
	}, req.Files)
	require.Equal(t, map[string]any{
		"server": "svc", "service": "svc", "method": "resources/read", "uri": "file:///doc",
	}, req.Params)

	// files が無効なら blob は送らない。
	g2 := &fakeGuardrail{fn: maskEmail}
	cs2 := fileResultTestServer(t, g2, GuardrailOptions{ResourceRead: true}, nil)
	res, err = cs2.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "file:///doc"})
	require.NoError(t, err)
	require.Equal(t, "mail [EMAIL]", res.Contents[0].Text)
	require.Empty(t, g2.requests[0].Files)
}

// resource_read: BLOCKED は理由付きで拒否し、ガードレールが判定できなければ
// 理由を返さずに拒否する（fail closed）。
func TestGuardrailMiddleware_ResourceReadRejects(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fn      func(guardrail.Request) (guardrail.Result, error)
		wantErr string
	}{
		{name: "blocked", fn: func(guardrail.Request) (guardrail.Result, error) {
			return guardrail.Result{
				Action: guardrail.ActionBlocked, BlockedReason: "prompt injection",
			}, nil
		}, wantErr: "blocked by guardrail: prompt injection"},
		{name: "guardrail error", fn: func(guardrail.Request) (guardrail.Result, error) {
			return guardrail.Result{}, errors.New("guardrail down at 10.0.0.1")
		}, wantErr: "guardrail check failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := fileResultTestServer(t, &fakeGuardrail{fn: tc.fn},
				GuardrailOptions{ResourceRead: true}, nil)
			_, err := cs.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "file:///doc"})
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
			require.NotContains(t, err.Error(), "10.0.0.1")
		})
	}
}

// resource_read を指定しなければ、resources/read は検査せずに通す。
func TestGuardrailMiddleware_ResourceReadDisabled(t *testing.T) {
	g := &fakeGuardrail{fn: maskEmail}
	var reads atomic.Int32
	cs := fileResultTestServer(t, g,
		GuardrailOptions{PreCall: true, PostCall: true, Files: true, MaxFileBytes: 1024}, &reads)

	res, err := cs.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "file:///doc"})
	require.NoError(t, err)
	require.Equal(t, "mail a@example.com", res.Contents[0].Text)
	require.EqualValues(t, 1, reads.Load())
	require.Empty(t, g.requests)
}

// 書き換えは複製に対して行い、バックエンドから受け取った result 自体は変えない。
func TestGuardrailPostCall_DoesNotMutateResult(t *testing.T) {
	embedded := &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
		URI: "file:///note.txt", Text: "mail a@example.com",
	}}
	text := &mcp.TextContent{Text: "to a@example.com"}
	result := &mcp.CallToolResult{
		Content:           []mcp.Content{embedded, text},
		StructuredContent: map[string]any{"email": "a@example.com"},
	}

	got, err := guardrailPostCall(t.Context(), &fakeGuardrail{fn: maskEmail},
		map[string]any{}, GuardrailOptions{PostCall: true}, result)
	require.NoError(t, err)
	rewritten := got.(*mcp.CallToolResult)
	require.Equal(t, "mail [EMAIL]", rewritten.Content[0].(*mcp.EmbeddedResource).Resource.Text)
	require.Equal(t, "to [EMAIL]", rewritten.Content[1].(*mcp.TextContent).Text)
	require.Equal(t, map[string]any{"email": "[EMAIL]"}, rewritten.StructuredContent)

	require.Same(t, embedded, result.Content[0])
	require.Equal(t, "mail a@example.com", embedded.Resource.Text)
	require.Equal(t, "to a@example.com", text.Text)
	require.Equal(t, map[string]any{"email": "a@example.com"}, result.StructuredContent)
}
