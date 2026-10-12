package mcpsrv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/services/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPromptDocument = `
prompts:
  - name: summarize_pet
    title: Summarize a pet
    description: Summarize a pet's profile
    arguments:
      - name: petId
        description: ID of the pet
        required: true
      - name: tone
    messages:
      - role: user
        content: "Summarize the pet {{.petId}}.{{if .tone}} Tone: {{.tone}}.{{end}}"
      - role: assistant
        content: "Looking up pet {{index . \"petId\"}}."
`

func TestParsePromptDocument(t *testing.T) {
	set, err := parsePromptDocument([]byte(testPromptDocument))
	require.NoError(t, err)
	require.Len(t, set.list, 1)
	p := set.list[0]
	require.Equal(t, "summarize_pet", p.Name)
	require.Equal(t, "Summarize a pet", p.Title)
	require.Len(t, p.Arguments, 2)
	require.True(t, p.Arguments[0].Required)
	require.False(t, p.Arguments[1].Required)

	// JSON も受け付ける。
	set, err = parsePromptDocument([]byte(
		`{"prompts":[{"name":"a","messages":[{"role":"user","content":"hi"}]}]}`,
	))
	require.NoError(t, err)
	require.Len(t, set.list, 1)

	// prompts が無いドキュメントは空の一覧。
	set, err = parsePromptDocument([]byte("prompts: []\n"))
	require.NoError(t, err)
	require.Empty(t, set.list)
}

func TestParsePromptDocument_Invalid(t *testing.T) {
	for _, tc := range []struct {
		name    string
		doc     string
		wantErr string
	}{
		{
			name:    "missing name",
			doc:     "prompts:\n  - messages: [{role: user, content: hi}]\n",
			wantErr: "name is required",
		},
		{
			name:    "missing messages",
			doc:     "prompts:\n  - name: a\n",
			wantErr: "messages is required",
		},
		{
			name: "duplicate name",
			doc: "prompts:\n" +
				"  - {name: a, messages: [{role: user, content: hi}]}\n" +
				"  - {name: a, messages: [{role: user, content: hi}]}\n",
			wantErr: `duplicate prompt name "a"`,
		},
		{
			name: "duplicate argument",
			doc: "prompts:\n  - name: a\n    arguments: [{name: x}, {name: x}]\n" +
				"    messages: [{role: user, content: hi}]\n",
			wantErr: `duplicate argument "x"`,
		},
		{
			name:    "bad role",
			doc:     "prompts:\n  - {name: a, messages: [{role: system, content: hi}]}\n",
			wantErr: "role must be user or assistant",
		},
		{
			name:    "bad template",
			doc:     "prompts:\n  - {name: a, messages: [{role: user, content: \"{{.x\"}]}\n",
			wantErr: "messages[0]",
		},
		{
			name:    "not yaml",
			doc:     "prompts: [",
			wantErr: "decode prompts document",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parsePromptDocument([]byte(tc.doc))
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestCompiledPrompt_Render(t *testing.T) {
	set, err := parsePromptDocument([]byte(testPromptDocument))
	require.NoError(t, err)
	p := set.byName["summarize_pet"]

	res, err := p.render(map[string]string{"petId": "42", "tone": "friendly"})
	require.NoError(t, err)
	require.Equal(t, "Summarize a pet's profile", res.Description)
	require.Len(t, res.Messages, 2)
	require.Equal(t, mcp.Role("user"), res.Messages[0].Role)
	require.Equal(
		t,
		"Summarize the pet 42. Tone: friendly.",
		res.Messages[0].Content.(*mcp.TextContent).Text,
	)
	require.Equal(t, mcp.Role("assistant"), res.Messages[1].Role)
	require.Equal(t, "Looking up pet 42.", res.Messages[1].Content.(*mcp.TextContent).Text)

	// 任意引数は省略でき、空文字列として展開される。
	res, err = p.render(map[string]string{"petId": "42"})
	require.NoError(t, err)
	require.Equal(t, "Summarize the pet 42.", res.Messages[0].Content.(*mcp.TextContent).Text)

	// 必須引数が無ければ invalid params。
	_, err = p.render(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing required arguments: petId")
}

// connectOpenAPIGateway は prompts を持つ OpenAPI モードのサーバーをゲートウェイとして
// 立てて接続する。
func connectOpenAPIGateway(t *testing.T, server *config.Server) *mcp.ClientSession {
	t.Helper()
	server.Name = "petstore"
	server.Spec = "fixtures/petstore_oas.json"
	server.BaseURL = "https://petstore.example.com"
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(
		config.Servers{"petstore": server},
		storage.NewContentManagementService(u, storage.NewNoopUploader()),
	)
	require.NoError(t, s.Init(context.Background()))
	t.Cleanup(s.Close)
	srv, err := s.Server("petstore")
	require.NoError(t, err)
	gateway := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	))
	t.Cleanup(gateway.Close)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "host", Version: "0.0.1"}, nil).Connect(
		context.Background(), &mcp.StreamableClientTransport{Endpoint: gateway.URL}, nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// prompts.url は要求のたびに取得し、headers・authValue を付けて取りに行く。
func TestOpenAPIPrompts_URL(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	var (
		doc      atomic.Value
		requests atomic.Int32
	)
	doc.Store(testPromptDocument)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("X-Api-Key") != "secret" || r.Header.Get("X-Tenant") != "acme" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(doc.Load().(string)))
	}))
	t.Cleanup(backend.Close)

	cs := connectOpenAPIGateway(t, &config.Server{
		Prompts:      &config.PromptsConfig{URL: backend.URL + "/prompts.yaml"},
		AuthValue:    &config.AuthValue{Header: "X-Api-Key", Value: "secret"},
		ExtraHeaders: map[string]string{"X-Tenant": "acme"},
	})
	ctx := context.Background()

	require.NotNil(t, cs.InitializeResult().Capabilities.Prompts)
	// prompts を設定してもツールは従来どおり公開される。
	tools, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	require.NotEmpty(t, tools.Tools)

	list, err := cs.ListPrompts(ctx, nil)
	require.NoError(t, err)
	require.Len(t, list.Prompts, 1)
	require.Equal(t, "summarize_pet", list.Prompts[0].Name)

	got, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: "summarize_pet", Arguments: map[string]string{"petId": "7"},
	})
	require.NoError(t, err)
	require.Equal(t, "Summarize the pet 7.", got.Messages[0].Content.(*mcp.TextContent).Text)

	_, err = cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "summarize_pet"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing required arguments")

	_, err = cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "missing"})
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown prompt "missing"`)

	// バックエンドの更新は次の要求からそのまま反映される。
	doc.Store("prompts:\n  - {name: other, messages: [{role: user, content: hi}]}\n")
	list, err = cs.ListPrompts(ctx, nil)
	require.NoError(t, err)
	require.Len(t, list.Prompts, 1)
	require.Equal(t, "other", list.Prompts[0].Name)
	require.GreaterOrEqual(t, requests.Load(), int32(5))
}

// prompts.url の取得に失敗したら、prompts/list はエラーを返す。
func TestOpenAPIPrompts_URL_FetchError(t *testing.T) {
	t.Setenv("TEST", "true")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(backend.Close)

	cs := connectOpenAPIGateway(t, &config.Server{
		Prompts: &config.PromptsConfig{URL: backend.URL},
	})
	_, err := cs.ListPrompts(context.Background(), nil)
	require.Error(t, err)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func promptNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	list, err := cs.ListPrompts(context.Background(), nil)
	require.NoError(t, err)
	names := make([]string, 0, len(list.Prompts))
	for _, p := range list.Prompts {
		names = append(names, p.Name)
	}
	return names
}

// prompts.file は変更を監視して読み直す。不正な内容に変わった場合は直前の
// prompts を返し続ける。
func TestOpenAPIPrompts_File_ReloadsOnWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompts.yaml")
	writeFile(t, path, testPromptDocument)

	cs := connectOpenAPIGateway(t, &config.Server{
		Prompts: &config.PromptsConfig{File: path},
	})
	require.Equal(t, []string{"summarize_pet"}, promptNames(t, cs))

	writeFile(t, path, "prompts:\n  - {name: updated, messages: [{role: user, content: hi}]}\n")
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{"updated"}, promptNames(t, cs))
	}, 5*time.Second, 20*time.Millisecond)

	writeFile(t, path, "prompts: [")
	time.Sleep(10 * promptFileReloadDelay)
	require.Equal(t, []string{"updated"}, promptNames(t, cs))
}

// Kubernetes の ConfigMap のマウントと同じ、..data シンボリックリンクの
// 張り替えによる更新を拾う。
func TestOpenAPIPrompts_File_ConfigMapSymlinkSwap(t *testing.T) {
	dir := t.TempDir()
	v1 := filepath.Join(dir, "..2026_10_12_00_00_00.000000001")
	require.NoError(t, os.Mkdir(v1, 0o700))
	writeFile(t, filepath.Join(v1, "prompts.yaml"), testPromptDocument)
	require.NoError(t, os.Symlink(filepath.Base(v1), filepath.Join(dir, "..data")))
	require.NoError(t, os.Symlink(
		filepath.Join("..data", "prompts.yaml"), filepath.Join(dir, "prompts.yaml"),
	))

	cs := connectOpenAPIGateway(t, &config.Server{
		Prompts: &config.PromptsConfig{File: filepath.Join(dir, "prompts.yaml")},
	})
	require.Equal(t, []string{"summarize_pet"}, promptNames(t, cs))

	// kubelet と同じく、新しいディレクトリを作って ..data を rename で差し替える。
	v2 := filepath.Join(dir, "..2026_10_12_00_01_00.000000002")
	require.NoError(t, os.Mkdir(v2, 0o700))
	writeFile(t, filepath.Join(v2, "prompts.yaml"),
		"prompts:\n  - {name: from_configmap, messages: [{role: user, content: hi}]}\n")
	require.NoError(t, os.Symlink(filepath.Base(v2), filepath.Join(dir, "..data_tmp")))
	require.NoError(t, os.Rename(filepath.Join(dir, "..data_tmp"), filepath.Join(dir, "..data")))
	require.NoError(t, os.RemoveAll(v1))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{"from_configmap"}, promptNames(t, cs))
	}, 5*time.Second, 20*time.Millisecond)
}

// 起動時に prompts.file を読めなければ Init が失敗する。
func TestOpenAPIPrompts_File_InitFailsWhenUnreadable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content *string
	}{
		{name: "missing"},
		{name: "invalid", content: new("prompts: [")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "prompts.yaml")
			if tc.content != nil {
				writeFile(t, path, *tc.content)
			}
			u, _ := url.Parse("https://example.com")
			s := NewMCPServer(config.Servers{"petstore": &config.Server{
				Name:    "petstore",
				Spec:    "fixtures/petstore_oas.json",
				BaseURL: "https://petstore.example.com",
				Prompts: &config.PromptsConfig{File: path},
			}}, storage.NewContentManagementService(u, storage.NewNoopUploader()))
			err := s.Init(context.Background())
			require.Error(t, err)
			require.Contains(t, err.Error(), "prompts.file")
			s.Close()
		})
	}
}

// OpenAPI モードの prompts にも、ゲートウェイの authz がツールと同じ判定で掛かる。
func TestOpenAPIPrompts_Authz(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompts.yaml")
	writeFile(t, path, testPromptDocument+
		"  - {name: secret_howto, messages: [{role: user, content: secret}]}\n")
	d := &fakeDecider{
		allowResult: false,
		allowedToolsResult: []authz.ToolRef{
			{Server: "petstore", Service: "petstore", Name: "summarize_pet"},
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(config.Servers{"petstore": &config.Server{
		Name:    "petstore",
		Spec:    "fixtures/petstore_oas.json",
		BaseURL: "https://petstore.example.com",
		Prompts: &config.PromptsConfig{File: path},
	}}, storage.NewContentManagementService(u, storage.NewNoopUploader()),
		WithServerMiddleware(func(name string) []mcp.Middleware {
			return []mcp.Middleware{
				NewAuthzMiddleware(name, name, d, testAuthzHeaders(), nil),
			}
		}))
	require.NoError(t, s.Init(context.Background()))
	t.Cleanup(s.Close)
	srv, err := s.Server("petstore")
	require.NoError(t, err)
	gateway := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	))
	t.Cleanup(gateway.Close)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "host", Version: "0.0.1"}, nil).Connect(
		context.Background(), &mcp.StreamableClientTransport{
			Endpoint:   gateway.URL,
			HTTPClient: &http.Client{Transport: &headerRoundTripper{headers: identityHeaders()}},
		}, nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	require.Equal(t, []string{"summarize_pet"}, promptNames(t, cs))

	_, err = cs.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "secret_howto"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool not allowed by policy")
	require.Equal(t, "secret_howto", d.allowCalls[0].t.Name)
}
