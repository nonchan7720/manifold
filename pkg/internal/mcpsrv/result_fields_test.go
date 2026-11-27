package mcpsrv

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/internal/jmespath"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
	"github.com/stretchr/testify/require"
)

const resultFieldsOpenAPISpec = `{"openapi":"3.0.3","info":{"title":"t","version":"1"},"paths":{
  "/pets":{"get":{"operationId":"listPets","responses":{"200":{"description":"ok","content":{"application/json":{"schema":{
    "type":"array","items":{"$ref":"#/components/schemas/Pet"}}}}}}}},
  "/pets/{id}":{
    "get":{"operationId":"getPet","parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"integer"}}],
      "responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Pet"}}}}}},
    "delete":{"operationId":"deletePet","parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"integer"}}],
      "responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Pet"}}}}}}},
  "/hal":{"get":{"operationId":"listHal","responses":{"200":{"description":"ok","content":{"application/hal+json":{"schema":{
    "type":"array","items":{"$ref":"#/components/schemas/Pet"}}}}}}}},
  "/photo":{"get":{"operationId":"getPhoto","responses":{"200":{"description":"ok","content":{"image/png":{"schema":{"type":"string","format":"binary"}}}}}}},
  "/ping":{"get":{"operationId":"getPing","responses":{"204":{"description":"no content"}}}}
},"components":{"schemas":{"Pet":{"type":"object","properties":{
  "id":{"type":"integer"},"name":{"type":"string"},
  "tags":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"}}}}}}}}}`

const resultFieldsSwaggerSpec = `{"swagger":"2.0","info":{"title":"t","version":"1"},"host":"api.example.com","schemes":["https"],"paths":{
  "/pets":{"get":{"operationId":"listPets","produces":["application/json"],"responses":{"200":{"description":"ok","schema":{
    "type":"array","items":{"$ref":"#/definitions/Pet"}}}}}},
  "/pets/{id}":{"get":{"operationId":"getPet","parameters":[{"name":"id","in":"path","required":true,"type":"integer"}],
    "responses":{"200":{"description":"ok","schema":{"$ref":"#/definitions/Pet"}}}}},
  "/photo":{"get":{"operationId":"getPhoto","produces":["image/png"],"responses":{"200":{"description":"ok","schema":{"type":"file"}}}}}
},"definitions":{"Pet":{"type":"object","properties":{
  "id":{"type":"integer"},"name":{"type":"string"},
  "tags":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"}}}}}}}}`

const petResultFields = "id, name, tags[]{id, name}"

func buildResultFieldsCatalog(
	t *testing.T, spec, baseURL string, opts ...RegisterOpenAPIOption,
) *MCPToolRegistry {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spec.json")
	require.NoError(t, os.WriteFile(path, []byte(spec), 0o600))
	source, err := oastomcptool.LoadSpecSource(t.Context(), path)
	require.NoError(t, err)
	registry, err := BuildCatalog(t.Context(), &http.Client{}, source, baseURL, nil, opts...)
	require.NoError(t, err)
	return registry
}

// registeredResultFields は、登録時にツールへ持たせたフィールド一覧を返す。
func registeredResultFields(t *testing.T, r *MCPToolRegistry, name string) string {
	t.Helper()
	tool := findTool(t, r, name)
	require.NotNil(t, tool, name)
	fields, _ := takeResultFields(&tool.tool)
	return fields
}

// requireNoResultFieldsMeta は、_meta にフィールド一覧が載っていないことを確かめる
// （_meta.manifold の他の項目、たとえば binaryResponse はあってよい）。
func requireNoResultFieldsMeta(t *testing.T, meta mcp.Meta, tool string) {
	t.Helper()
	manifoldMeta, _ := meta[manifoldMetaKey].(map[string]any)
	require.NotContains(t, manifoldMeta, resultFieldsMetaKey, tool)
}

// spec のレスポンス定義から、ツールが実際に返す JSON のフィールド一覧を作る。
func TestBuildCatalog_ResultFields(t *testing.T) {
	enabled := &config.ResultFilterConfig{Enabled: true, Exclude: []string{"delete*"}}

	t.Run("openapi3", func(t *testing.T) {
		r := buildResultFieldsCatalog(
			t, resultFieldsOpenAPISpec, "https://api.example.com", WithResultFilter(enabled))
		require.Equal(t, petResultFields, registeredResultFields(t, r, "getpet"))
		// application/json の配列は {"items": [...]} に包んで返すので、一覧もその形にする。
		require.Equal(t, "items[]{"+petResultFields+"}", registeredResultFields(t, r, "listpets"))
		// +json の配列は包まない。
		require.Equal(t, "[]{"+petResultFields+"}", registeredResultFields(t, r, "listhal"))
		// 絞り込みの対象外のツール、JSON を返さないツールには持たせない。
		for _, name := range []string{"deletepet", "getphoto", "getping"} {
			require.Empty(t, registeredResultFields(t, r, name), name)
		}
		// バイナリを返すツールの目印は、そのまま残る。
		require.True(t, toolBinaryResponse(findTool(t, r, "getphoto").tool))
	})

	t.Run("swagger2", func(t *testing.T) {
		r := buildResultFieldsCatalog(t, resultFieldsSwaggerSpec, "", WithResultFilter(enabled))
		require.Equal(t, petResultFields, registeredResultFields(t, r, "getpet"))
		require.Equal(t, "items[]{"+petResultFields+"}", registeredResultFields(t, r, "listpets"))
		require.Empty(t, registeredResultFields(t, r, "getphoto"))
	})

	// 結果の絞り込みが無効なら、どのツールの _meta にも何も足さない（取り除く
	// ミドルウェアが入らないため）。
	for name, opts := range map[string][]RegisterOpenAPIOption{
		"no option":    nil,
		"nil config":   {WithResultFilter(nil)},
		"not enabled":  {WithResultFilter(&config.ResultFilterConfig{})},
		"tools config": {WithToolsConfig(&config.ToolsConfig{ResultFilter: enabled})},
	} {
		t.Run(name, func(t *testing.T) {
			r := buildResultFieldsCatalog(
				t,
				resultFieldsOpenAPISpec,
				"https://api.example.com",
				opts...)
			for _, tool := range r.ListTools() {
				requireNoResultFieldsMeta(t, tool.tool.Meta, tool.tool.Name)
			}
		})
	}
}

func TestResponseResultFields_Limit(t *testing.T) {
	props := make([]string, 0, 400)
	for i := range 400 {
		props = append(props, `"field_`+strings.Repeat("x", 8)+string(rune('a'+i%26))+
			string(rune('a'+i/26))+`":{"type":"string"}`)
	}
	spec := `{"openapi":"3.0.3","info":{"title":"t","version":"1"},"paths":{"/big":{"get":{"operationId":"getBig",
	  "responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{` +
		strings.Join(
			props,
			",",
		) + `}}}}}}}}}}`
	r := buildResultFieldsCatalog(t, spec, "https://api.example.com",
		WithResultFilter(&config.ResultFilterConfig{Enabled: true}))
	fields := registeredResultFields(t, r, "getbig")
	require.LessOrEqual(t, len(fields), maxResultFieldsBytes)
	require.Greater(t, len(fields), maxResultFieldsBytes-32)
	require.True(t, strings.HasSuffix(fields, ", ..."), fields)
}

// takeResultFields は _meta.manifold.resultFields だけを取り除き、他の _meta は残す。
// 登録済みのツールは書き換えない。
func TestTakeResultFields(t *testing.T) {
	tool := &Tool{}
	WithRegisterToolMeta(map[string]any{"other": 1})(tool)
	WithRegisterToolResultFields("")(tool)
	require.NotContains(t, tool.tool.Meta, manifoldMetaKey)
	fields, same := takeResultFields(&tool.tool)
	require.Empty(t, fields)
	require.Same(t, &tool.tool, same)

	WithRegisterToolResultFields("id, name")(tool)
	fields, stripped := takeResultFields(&tool.tool)
	require.Equal(t, "id, name", fields)
	require.Equal(t, mcp.Meta{"other": 1}, stripped.Meta)
	require.Equal(t, mcp.Meta{
		"other": 1, manifoldMetaKey: map[string]any{resultFieldsMetaKey: "id, name"},
	}, tool.tool.Meta)

	// _meta.manifold の他の項目は残る。
	both := &Tool{}
	WithRegisterToolMeta(
		map[string]any{manifoldMetaKey: map[string]any{"binaryResponse": true}},
	)(
		both,
	)
	WithRegisterToolResultFields("id")(both)
	fields, stripped = takeResultFields(&both.tool)
	require.Equal(t, "id", fields)
	require.Equal(
		t,
		mcp.Meta{manifoldMetaKey: map[string]any{"binaryResponse": true}},
		stripped.Meta,
	)
	require.True(t, toolBinaryResponse(both.tool))

	// 一覧だけなら _meta ごと無くなる。文字列でない値は使わないが、取り除く。
	only := &mcp.Tool{Meta: mcp.Meta{manifoldMetaKey: map[string]any{resultFieldsMetaKey: 42}}}
	fields, stripped = takeResultFields(only)
	require.Empty(t, fields)
	require.Nil(t, stripped.Meta)

	for _, meta := range []mcp.Meta{nil, {manifoldMetaKey: "text"}, {manifoldMetaKey: map[string]any{}}} {
		unrelated := &mcp.Tool{Meta: meta}
		fields, same := takeResultFields(unrelated)
		require.Empty(t, fields)
		require.Same(t, unrelated, same)
	}
}

// spec から作ったツールを結果の絞り込みつきで配信すると、式の引数の説明にフィールド一覧が
// 載り、その一覧のとおりに書いた式で絞り込める。
func TestResultFilter_OpenAPIResultFields(t *testing.T) {
	t.Setenv("TEST", "true")
	const pets = `[{"id":1,"name":"a","tags":[{"id":7,"name":"x"}]},{"id":2,"name":"b","tags":[]}]`
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/pets" {
			_, _ = w.Write([]byte(pets))
			return
		}
		_, _ = w.Write([]byte(`{"id":1,"name":"a","tags":[{"id":7,"name":"x"}]}`))
	}))
	t.Cleanup(api.Close)

	cfg := &config.ResultFilterConfig{Enabled: true, Exclude: []string{"delete*"}}
	registry := buildResultFieldsCatalog(t, resultFieldsOpenAPISpec, api.URL, WithResultFilter(cfg))
	srv := mcp.NewServer(&mcp.Implementation{Name: "petstore", Version: "0.0.1"}, nil)
	srv.AddReceivingMiddleware(newResultFilterMiddleware("petstore", cfg))
	mediaURL, err := url.Parse("https://media.example.com")
	require.NoError(t, err)
	attachTools(srv, registry,
		storage.NewContentManagementService(mediaURL, storage.NewNoopUploader()))
	cs := connectTestClient(t, t.Context(), srv)

	tools := listedTools(t, cs)
	description := func(tool string) string {
		arg, ok := toolProperties(t, tools[tool])[config.DefaultResultFilterArgument].(map[string]any)
		require.True(t, ok, tool)
		desc, _ := arg["description"].(string)
		return desc
	}
	require.Equal(
		t,
		resultFilterDescription(jmespath.Features{}, petResultFields),
		description("getpet"),
	)
	require.True(t, strings.HasSuffix(description("listpets"),
		" Result fields: items[]{id, name, tags[]{id, name}}."), description("listpets"))
	// JSON を返さないツールにも引数は足すが、一覧は無い。
	require.Equal(t, resultFilterDescription(jmespath.Features{}, ""), description("getping"))
	// 一覧は _meta には出さない。対象外のツールも同じ。
	for name, tool := range tools {
		requireNoResultFieldsMeta(t, tool.Meta, name)
	}
	require.NotContains(
		t,
		toolProperties(t, tools["deletepet"]),
		config.DefaultResultFilterArgument,
	)

	res := callTool(t, cs, "listpets", map[string]any{
		"_jmespath": "items[].{id: id, tags: tags[].name}",
	})
	require.False(t, res.IsError, "%+v", res.Content)
	require.Equal(t, []string{`[{"id":1,"tags":["x"]},{"id":2,"tags":[]}]`}, contentTexts(t, res))

	res = callTool(t, cs, "getpet", map[string]any{"id": 1, "_jmespath": "{name: name}"})
	require.False(t, res.IsError, "%+v", res.Content)
	require.Equal(t, []string{`{"name":"a"}`}, contentTexts(t, res))
}
