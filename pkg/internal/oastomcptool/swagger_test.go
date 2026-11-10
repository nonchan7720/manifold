package oastomcptool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/stretchr/testify/require"
)

// --- normalizeSwaggerJSON ---

func TestNormalizeSwaggerJSON_RemovesBoolRequired(t *testing.T) {
	input := `{
		"definitions": {
			"Pet": {
				"required": true,
				"properties": {
					"name": {"type": "string"}
				}
			}
		}
	}`

	result, err := normalizeSwaggerJSON([]byte(input))
	require.NoError(t, err)

	var normalized map[string]any
	err = json.Unmarshal(result, &normalized)
	require.NoError(t, err)

	defs := normalized["definitions"].(map[string]any)
	pet := defs["Pet"].(map[string]any)
	_, hasRequired := pet["required"]
	require.False(t, hasRequired, "boolean required should be removed from schema")
}

func TestNormalizeSwaggerJSON_KeepsStringArrayRequired(t *testing.T) {
	input := `{
		"definitions": {
			"Pet": {
				"required": ["name", "id"],
				"properties": {
					"name": {"type": "string"},
					"id": {"type": "integer"}
				}
			}
		}
	}`

	result, err := normalizeSwaggerJSON([]byte(input))
	require.NoError(t, err)

	var normalized map[string]any
	err = json.Unmarshal(result, &normalized)
	require.NoError(t, err)

	defs := normalized["definitions"].(map[string]any)
	pet := defs["Pet"].(map[string]any)
	required, hasRequired := pet["required"]
	require.True(t, hasRequired)
	require.IsType(t, []any{}, required)
}

func TestNormalizeSwaggerJSON_KeepsParameterRequired(t *testing.T) {
	input := `{
		"parameters": {
			"petId": {
				"in": "path",
				"required": true,
				"name": "petId",
				"type": "integer"
			}
		}
	}`

	result, err := normalizeSwaggerJSON([]byte(input))
	require.NoError(t, err)

	var normalized map[string]any
	err = json.Unmarshal(result, &normalized)
	require.NoError(t, err)

	params := normalized["parameters"].(map[string]any)
	petId := params["petId"].(map[string]any)
	required, hasRequired := petId["required"]
	require.True(t, hasRequired, "parameter required should not be removed")
	require.Equal(t, true, required)
}

func TestNormalizeSwaggerJSON_InvalidJSON(t *testing.T) {
	_, err := normalizeSwaggerJSON([]byte("not valid json"))
	require.Error(t, err)
}

func TestNormalizeSwaggerJSON_YAML_UnquotedResponseCode(t *testing.T) {
	// "200:" is a bare integer YAML map key (not valid JSON) — yaml.v3
	// decodes it into map[interface{}]interface{}, which is exactly what
	// the oasdiff/yaml conversion path exists to turn into map[string]any.
	input := `
definitions:
  Pet:
    required: true
    properties:
      name:
        type: string
responses:
  200:
    description: ok
`
	result, err := normalizeSwaggerJSON([]byte(input))
	require.NoError(t, err)

	var normalized map[string]any
	require.NoError(t, json.Unmarshal(result, &normalized))

	responses := normalized["responses"].(map[string]any)
	require.Contains(t, responses, "200")

	defs := normalized["definitions"].(map[string]any)
	pet := defs["Pet"].(map[string]any)
	_, hasRequired := pet["required"]
	require.False(t, hasRequired, "boolean required should be removed from schema")
}

func TestNormalizeSwaggerJSON_YAML_NonObjectTopLevel(t *testing.T) {
	// A bare YAML scalar decodes fine but isn't a spec — the JSON error from
	// the first (failed) attempt should still be returned, not swallowed by
	// the loosely-typed YAML fallback.
	_, err := normalizeSwaggerJSON([]byte("just a scalar string"))
	require.Error(t, err)
}

// --- removeBoolRequiredFromSchemas ---

func TestRemoveBoolRequiredFromSchemas_Array(t *testing.T) {
	input := []any{
		map[string]any{
			"required": true,
			"name":     "item",
		},
	}
	removeBoolRequiredFromSchemas(input)
	item := input[0].(map[string]any)
	_, hasRequired := item["required"]
	require.False(t, hasRequired)
}

func TestRemoveBoolRequiredFromSchemas_Nested(t *testing.T) {
	input := map[string]any{
		"outer": map[string]any{
			"required": false,
			"inner": map[string]any{
				"required": true,
			},
		},
	}
	removeBoolRequiredFromSchemas(input)
	outer := input["outer"].(map[string]any)
	_, hasOuterRequired := outer["required"]
	require.False(t, hasOuterRequired)
	inner := outer["inner"].(map[string]any)
	_, hasInnerRequired := inner["required"]
	require.False(t, hasInnerRequired)
}

// --- GetBaseUrlFromSwagger ---

func TestGetBaseUrlFromSwagger_WithHostAndScheme(t *testing.T) {
	spec := &openapi2.T{
		Host:     "api.example.com",
		Schemes:  []string{"https"},
		BasePath: "/v2",
	}
	got := GetBaseUrlFromSwagger(t.Context(), spec, "")
	require.Equal(t, "https://api.example.com/v2", got)
}

func TestGetBaseUrlFromSwagger_WithHostNoScheme(t *testing.T) {
	spec := &openapi2.T{
		Host:     "api.example.com",
		BasePath: "/v1",
	}
	got := GetBaseUrlFromSwagger(t.Context(), spec, "")
	require.Equal(t, "https://api.example.com/v1", got)
}

func TestGetBaseUrlFromSwagger_WithHTTPScheme(t *testing.T) {
	spec := &openapi2.T{
		Host:     "localhost",
		Schemes:  []string{"http"},
		BasePath: "/api",
	}
	got := GetBaseUrlFromSwagger(t.Context(), spec, "")
	require.Equal(t, "http://localhost/api", got)
}

func TestGetBaseUrlFromSwagger_NoHost_DeriveFromPath(t *testing.T) {
	spec := &openapi2.T{}
	got := GetBaseUrlFromSwagger(t.Context(), spec, "https://example.com/swagger.json")
	require.Equal(t, "https://example.com", got)
}

func TestGetBaseUrlFromSwagger_NoHost_NoPath(t *testing.T) {
	spec := &openapi2.T{}
	got := GetBaseUrlFromSwagger(t.Context(), spec, "")
	require.Equal(t, "", got)
}

// --- LoadSwaggerSpec ---

func TestLoadSwaggerSpec(t *testing.T) {
	spec, err := LoadSwaggerSpec(context.Background(), "../mcpsrv/fixtures/petstore_swagger.json")
	require.NoError(t, err)
	require.NotNil(t, spec)
	require.NotEmpty(t, spec.Paths)
}

func TestLoadSwaggerSpec_NotFound(t *testing.T) {
	_, err := LoadSwaggerSpec(context.Background(), "nonexistent_swagger.json")
	require.Error(t, err)
}

func TestParseSwaggerSpec_YAML(t *testing.T) {
	input := `
swagger: "2.0"
info:
  title: Inline
  version: "1.0.0"
paths:
  /ping:
    get:
      operationId: ping
      responses:
        200:
          description: ok
`
	spec, err := ParseSwaggerSpec(context.Background(), []byte(input))
	require.NoError(t, err)
	require.NotNil(t, spec)
	pathItem, ok := spec.Paths["/ping"]
	require.True(t, ok)
	require.NotNil(t, pathItem.Get)
	require.Equal(t, "ping", pathItem.Get.OperationID)
}

// --- resolveSwaggerParamRef ---

func TestResolveSwaggerParamRef_NoRef(t *testing.T) {
	p := &openapi2.Parameter{Name: "id", In: "path"}
	spec := &openapi2.T{}
	got := resolveSwaggerParamRef(p, spec)
	require.Equal(t, p, got)
}

func TestResolveSwaggerParamRef_WithRef(t *testing.T) {
	resolved := &openapi2.Parameter{Name: "petId", In: "path"}
	spec := &openapi2.T{
		Parameters: map[string]*openapi2.Parameter{
			"petId": resolved,
		},
	}
	p := &openapi2.Parameter{Ref: "#/parameters/petId"}
	got := resolveSwaggerParamRef(p, spec)
	require.Equal(t, resolved, got)
}

func TestResolveSwaggerParamRef_RefNotFound(t *testing.T) {
	spec := &openapi2.T{}
	p := &openapi2.Parameter{Ref: "#/parameters/nonexistent"}
	got := resolveSwaggerParamRef(p, spec)
	require.Nil(t, got)
}

// --- resolveSwaggerSchemaRef ---

func TestResolveSwaggerSchemaRef_Nil(t *testing.T) {
	spec := &openapi2.T{}
	got := resolveSwaggerSchemaRef(nil, spec)
	require.Nil(t, got)
}

func TestResolveSwaggerSchemaRef_NoRef(t *testing.T) {
	schema := &openapi2.Schema{}
	ref := &openapi2.SchemaRef{Value: schema}
	spec := &openapi2.T{}
	got := resolveSwaggerSchemaRef(ref, spec)
	require.Equal(t, schema, got)
}

func TestResolveSwaggerSchemaRef_WithRef(t *testing.T) {
	petSchema := &openapi2.Schema{}
	spec := &openapi2.T{
		Definitions: map[string]*openapi2.SchemaRef{
			"Pet": {Value: petSchema},
		},
	}
	ref := &openapi2.SchemaRef{Ref: "#/definitions/Pet"}
	got := resolveSwaggerSchemaRef(ref, spec)
	require.Equal(t, petSchema, got)
}

func TestResolveSwaggerSchemaRef_EscapedName(t *testing.T) {
	slashSchema := &openapi2.Schema{}
	tildeSchema := &openapi2.Schema{}
	spec := &openapi2.T{
		Definitions: map[string]*openapi2.SchemaRef{
			"attachments/v1": {Value: slashSchema},
			"a~b":            {Value: tildeSchema},
		},
	}
	require.Equal(t, slashSchema,
		resolveSwaggerSchemaRef(&openapi2.SchemaRef{Ref: "#/definitions/attachments~1v1"}, spec))
	require.Equal(t, tildeSchema,
		resolveSwaggerSchemaRef(&openapi2.SchemaRef{Ref: "#/definitions/a~0b"}, spec))
}

func TestResolveSwaggerSchemaRef_RefNotFound(t *testing.T) {
	spec := &openapi2.T{}
	ref := &openapi2.SchemaRef{Ref: "#/definitions/NotExist"}
	got := resolveSwaggerSchemaRef(ref, spec)
	require.Nil(t, got)
}

// --- BuildInputSchemaSwagger ---

func TestBuildInputSchemaSwagger_PathAndQuery(t *testing.T) {
	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{Name: "petId", In: "path", Required: true},
			{Name: "status", In: "query"},
		},
	}
	spec := &openapi2.T{}

	schema := BuildInputSchemaSwagger(op, nil, spec)
	require.Equal(t, "object", schema["type"])

	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, props, "petId")
	require.Contains(t, props, "status")

	required := schema["required"].([]string)
	require.Contains(t, required, "petId")
}

func TestBuildInputSchemaSwagger_BodyParam(t *testing.T) {
	bodySchema := &openapi2.Schema{
		Properties: map[string]*openapi2.SchemaRef{
			"name": {Value: &openapi2.Schema{}},
		},
	}
	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{
				Name:     "body",
				In:       "body",
				Required: true,
				Schema:   &openapi2.SchemaRef{Value: bodySchema},
			},
		},
	}
	spec := &openapi2.T{}

	schema := BuildInputSchemaSwagger(op, nil, spec)
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, props, "body")

	required := schema["required"].([]string)
	require.Contains(t, required, "body")
}

// --- CreateToolFunctionSwagger ---

func TestCreateToolFunctionSwagger_GET(t *testing.T) {
	t.Setenv("TEST", "true")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/pets/99", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":99}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{Name: "petId", In: "path"},
		},
	}
	spec := &openapi2.T{}

	fn := CreateToolFunctionSwagger(
		http.DefaultClient,
		"/pets/{petId}",
		"get",
		op,
		nil,
		spec,
		srv.URL,
		nil,
		false,
	)
	result, _, err := fn(context.Background(), map[string]any{"petId": "99"})
	require.NoError(t, err)
	require.Contains(t, string(result), "99")
}

func TestCreateToolFunctionSwagger_POST_Body(t *testing.T) {
	t.Setenv("TEST", "true")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":1}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{
				Name: "body",
				In:   "body",
				Schema: &openapi2.SchemaRef{
					Value: &openapi2.Schema{},
				},
			},
		},
	}
	spec := &openapi2.T{}

	fn := CreateToolFunctionSwagger(
		http.DefaultClient,
		"/pets",
		"post",
		op,
		nil,
		spec,
		srv.URL,
		nil,
		false,
	)
	result, _, err := fn(context.Background(), map[string]any{
		"body": map[string]any{"name": "Buddy"},
	})
	require.NoError(t, err)
	require.Contains(t, string(result), "1")
}

func TestCreateToolFunctionSwagger_WithQueryParam(t *testing.T) {
	t.Setenv("TEST", "true")
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query().Get("status")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[]`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{Name: "status", In: "query"},
		},
	}
	spec := &openapi2.T{}

	fn := CreateToolFunctionSwagger(
		http.DefaultClient,
		"/pets",
		"get",
		op,
		nil,
		spec,
		srv.URL,
		nil,
		false,
	)
	result, _, err := fn(context.Background(), map[string]any{"status": "sold"})
	require.NoError(t, err)
	require.Equal(t, "sold", capturedQuery)
	require.NotEmpty(t, result)
}

func TestCreateToolFunctionSwagger_UnsupportedMethod(t *testing.T) {
	op := &openapi2.Operation{}
	spec := &openapi2.T{}

	fn := CreateToolFunctionSwagger(
		http.DefaultClient,
		"/resource",
		"UNKNOWN",
		op,
		nil,
		spec,
		"http://example.com",
		nil,
		false,
	)
	_, _, err := fn(context.Background(), map[string]any{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported HTTP method")
}

func TestCreateToolFunctionSwagger_InvalidPathParam(t *testing.T) {
	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{Name: "id", In: "path"},
		},
	}
	spec := &openapi2.T{}

	fn := CreateToolFunctionSwagger(
		http.DefaultClient,
		"/items/{id}",
		"get",
		op,
		nil,
		spec,
		"http://example.com",
		nil,
		false,
	)
	_, _, err := fn(context.Background(), map[string]any{"id": "../evil"})
	require.Error(t, err)
}

func TestCreateToolFunctionSwagger_HTTPError(t *testing.T) {
	t.Setenv("TEST", "true")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`bad request`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi2.Operation{}
	spec := &openapi2.T{}

	fn := CreateToolFunctionSwagger(
		http.DefaultClient,
		"/resource",
		"get",
		op,
		nil,
		spec,
		srv.URL,
		nil,
		false,
	)
	// OpenAPI 3.x と同じく、400以上のステータスはエラーにならず、レスポンスボディをそのまま返す
	result, _, err := fn(context.Background(), map[string]any{})
	require.NoError(t, err)
	require.Equal(t, "bad request", string(result))
}

func TestCreateToolFunctionSwagger_HTTPError_EmptyBodyUsesStatusText(t *testing.T) {
	t.Setenv("TEST", "true")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	fn := CreateToolFunctionSwagger(
		http.DefaultClient, "/resource", "get", &openapi2.Operation{}, nil, &openapi2.T{},
		srv.URL, nil, false,
	)
	result, _, err := fn(context.Background(), map[string]any{})
	require.NoError(t, err)
	require.Equal(t, http.StatusText(http.StatusNotFound), string(result))
}

func TestCreateToolFunctionSwagger_AuthOverrideFromContext(t *testing.T) {
	t.Setenv("TEST", "true")
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi2.Operation{}
	spec := &openapi2.T{}

	fn := CreateToolFunctionSwagger(
		http.DefaultClient,
		"/resource",
		"get",
		op,
		nil,
		spec,
		srv.URL,
		map[string]string{
			"Authorization": "Bearer static",
		},
		false,
	)

	ctx := contexts.ToRequestAuthHeader(context.Background(), "override")
	result, _, err := fn(ctx, map[string]any{})
	require.NoError(t, err)
	require.Equal(t, "Bearer override", capturedAuth)
	require.NotEmpty(t, result)
}

func TestCreateToolFunctionSwagger_DELETE(t *testing.T) {
	t.Setenv("TEST", "true")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	op := &openapi2.Operation{}
	spec := &openapi2.T{}

	fn := CreateToolFunctionSwagger(
		http.DefaultClient,
		"/resource/1",
		"delete",
		op,
		nil,
		spec,
		srv.URL,
		nil,
		false,
	)
	result, _, err := fn(context.Background(), map[string]any{})
	require.NoError(t, err)
	_ = result
}

func TestCreateToolFunctionSwagger_PATCH(t *testing.T) {
	t.Setenv("TEST", "true")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPatch, r.Method)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"updated":true}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{
				Name: "body",
				In:   "body",
				Schema: &openapi2.SchemaRef{
					Value: &openapi2.Schema{},
				},
			},
		},
	}
	spec := &openapi2.T{}

	fn := CreateToolFunctionSwagger(
		http.DefaultClient,
		"/resource/1",
		"patch",
		op,
		nil,
		spec,
		srv.URL,
		nil,
		false,
	)
	result, _, err := fn(context.Background(), map[string]any{
		"body": map[string]any{"name": "updated"},
	})
	require.NoError(t, err)
	require.Contains(t, string(result), "updated")
}

// --- describeSchemaFieldsSwagger (nested objects, arrays) ---

func TestBuildInputSchemaSwagger_BodyWithNestedObject(t *testing.T) {
	// ネストされたオブジェクトを持つbodyパラメータ
	nestedSchema := &openapi2.Schema{
		Properties: map[string]*openapi2.SchemaRef{
			"street": {Value: &openapi2.Schema{}},
			"city":   {Value: &openapi2.Schema{}},
		},
	}
	bodySchema := &openapi2.Schema{
		Required: []string{"address"},
		Properties: map[string]*openapi2.SchemaRef{
			"address": {
				Value: &openapi2.Schema{
					Description: "User address",
					Properties:  nestedSchema.Properties,
				},
			},
		},
	}
	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{
				Name:   "body",
				In:     "body",
				Schema: &openapi2.SchemaRef{Value: bodySchema},
			},
		},
	}
	spec := &openapi2.T{}

	schema := BuildInputSchemaSwagger(op, nil, spec)
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, props, "body")
	bodyProp := props["body"].(map[string]any)
	// descriptionにネストされたフィールドの説明が含まれる
	require.NotEmpty(t, bodyProp["description"])
}

func TestBuildInputSchemaSwagger_BodyWithArrayOfObjects(t *testing.T) {
	// 配列型のプロパティを持つbodyパラメータ
	itemSchema := &openapi2.Schema{
		Properties: map[string]*openapi2.SchemaRef{
			"id": {Value: &openapi2.Schema{}},
		},
	}
	bodySchema := &openapi2.Schema{
		Properties: map[string]*openapi2.SchemaRef{
			"items": {
				Value: &openapi2.Schema{
					Items: &openapi2.SchemaRef{Value: itemSchema},
				},
			},
		},
	}
	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{
				Name:   "body",
				In:     "body",
				Schema: &openapi2.SchemaRef{Value: bodySchema},
			},
		},
	}
	spec := &openapi2.T{}

	schema := BuildInputSchemaSwagger(op, nil, spec)
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, props, "body")
}

func TestBuildInputSchemaSwagger_BodyWithRequiredDescription(t *testing.T) {
	// required フィールド付きのボディ
	bodySchema := &openapi2.Schema{
		Required: []string{"name", "email"},
		Properties: map[string]*openapi2.SchemaRef{
			"name":  {Value: &openapi2.Schema{Description: "Full name"}},
			"email": {Value: &openapi2.Schema{Description: "Email address"}},
		},
	}
	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{
				Name:        "body",
				In:          "body",
				Description: "User registration data",
				Required:    true,
				Schema:      &openapi2.SchemaRef{Value: bodySchema},
			},
		},
	}
	spec := &openapi2.T{}

	schema := BuildInputSchemaSwagger(op, nil, spec)
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, props, "body")
}

// --- mergeSwaggerParams ---

func TestMergeSwaggerParams_PathItemOnly(t *testing.T) {
	pathItemParams := []*openapi2.Parameter{
		{Name: "version", In: "path"},
	}
	op := &openapi2.Operation{
		Parameters: nil,
	}
	spec := &openapi2.T{}

	result := mergeSwaggerParams(op, pathItemParams, spec)
	require.Len(t, result, 1)
	require.Equal(t, "version", result[0].Name)
}

func TestMergeSwaggerParams_OperationOverridesPathItem(t *testing.T) {
	pathItemParams := []*openapi2.Parameter{
		{Name: "limit", In: "query"},
	}
	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{Name: "limit", In: "query"}, // 同じ (in, name) なので上書き
		},
	}
	spec := &openapi2.T{}

	result := mergeSwaggerParams(op, pathItemParams, spec)
	// 重複なく1つだけ
	require.Len(t, result, 1)
}

func TestMergeSwaggerParams_BothDistinct(t *testing.T) {
	pathItemParams := []*openapi2.Parameter{
		{Name: "apiVersion", In: "path"},
	}
	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{Name: "status", In: "query"},
		},
	}
	spec := &openapi2.T{}

	result := mergeSwaggerParams(op, pathItemParams, spec)
	require.Len(t, result, 2)
}

func TestMergeSwaggerParams_NilResolve(t *testing.T) {
	// $ref が解決できない場合はスキップ
	pathItemParams := []*openapi2.Parameter{
		{Ref: "#/parameters/DoesNotExist"},
	}
	op := &openapi2.Operation{}
	spec := &openapi2.T{}

	result := mergeSwaggerParams(op, pathItemParams, spec)
	require.Empty(t, result)
}

// --- extractParametersSwagger ---

func TestExtractParametersSwagger_FormDataFile(t *testing.T) {
	// file タイプの formData → isMultipart=true
	fileType := openapi3.Types{"file"}
	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{Name: "upload", In: "formData", Type: &fileType},
		},
	}
	spec := &openapi2.T{}

	ep := extractParametersSwagger(op, nil, spec)
	require.Contains(t, ep.formParams, "upload")
	require.True(t, ep.isMultipart)
}

// --- extractParametersSwagger: type: file の isFile 判定 ---

func TestExtractParametersSwagger_FormDataFile_SetsIsFile(t *testing.T) {
	fileType := openapi3.Types{"file"}
	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{Name: "upload", In: "formData", Type: &fileType},
			{Name: "caption", In: "formData"},
		},
	}
	spec := &openapi2.T{}

	ep := extractParametersSwagger(op, nil, spec)
	require.True(t, ep.formParams["upload"].isFile)
	require.False(t, ep.formParams["caption"].isFile)
}

// --- BuildInputSchemaSwagger: type: file スキーマ（openapi.go の buildFormPropertySchema と同じヘルパーを使う） ---

func TestBuildInputSchemaSwagger_FormDataFile(t *testing.T) {
	fileType := openapi3.Types{"file"}
	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{Name: "upload", In: "formData", Type: &fileType, Description: "Avatar image"},
		},
	}
	spec := &openapi2.T{}

	schema := BuildInputSchemaSwagger(op, nil, spec)
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	uploadProp := props["upload"].(map[string]any)
	// バリデーションするクライアントが object 形式（{url}/{base64}/{text}/{content}）を
	// 誤って拒否しないよう、実行時に受理される両方の形を oneOf [string, object] で表現する
	// （openapi.go の buildFormPropertySchema と同じ binaryFieldMCPSchema ヘルパーを使う）。
	oneOf, ok := uploadProp["oneOf"].([]any)
	require.True(t, ok)
	require.Len(t, oneOf, 2)
	stringBranch := oneOf[0].(map[string]any)
	require.Equal(t, "string", stringBranch["type"])
	objectBranch := oneOf[1].(map[string]any)
	require.Equal(t, "object", objectBranch["type"])
	objectProps := objectBranch["properties"].(map[string]any)
	require.Contains(t, objectProps, "url")
	require.Contains(t, objectProps, "base64")
	require.Contains(t, objectProps, "text")
	require.Contains(t, objectProps, "content")
	require.Contains(t, objectProps, "filename")
	require.Contains(t, objectProps, "contentType")

	meta, ok := uploadProp["_meta"].(map[string]any)
	require.True(t, ok)
	manifold := meta["manifold"].(map[string]any)
	require.Equal(t, true, manifold["file"])

	// description は元の説明のまま変更せず、base64/URL の案内は _meta.manifold.fileInputHint で伝える
	desc, ok := uploadProp["description"].(string)
	require.True(t, ok)
	require.Equal(t, "Avatar image", desc)
	hint, ok := manifold["fileInputHint"].(string)
	require.True(t, ok)
	require.Contains(t, hint, "base64")
	require.Contains(t, hint, "URL")
}

func TestBuildInputSchemaSwagger_FormDataNonFile_Unchanged(t *testing.T) {
	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{Name: "caption", In: "formData", Description: "Caption text"},
		},
	}
	spec := &openapi2.T{}

	schema := BuildInputSchemaSwagger(op, nil, spec)
	props := schema["properties"].(map[string]any)
	captionProp := props["caption"].(map[string]any)
	require.Equal(t, "Caption text", captionProp["description"])
}

// --- CreateToolFunctionSwagger: multipart ファイル送信（base64 / URL） ---

func TestCreateToolFunctionSwagger_MultipartFileBase64(t *testing.T) {
	t.Setenv("TEST", "true")
	content := []byte("{\"text\":\"hello swagger file\"}")
	var capturedFilename string
	var capturedContent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(10<<20))
		f, hdr, err := r.FormFile("upload")
		require.NoError(t, err)
		defer f.Close() //nolint: errcheck
		capturedFilename = hdr.Filename
		capturedContent, err = io.ReadAll(f)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	fileType := openapi3.Types{"file"}
	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{Name: "upload", In: "formData", Type: &fileType},
		},
	}
	spec := &openapi2.T{}

	fn := CreateToolFunctionSwagger(
		http.DefaultClient,
		"/upload",
		"post",
		op,
		nil,
		spec,
		srv.URL,
		nil,
		false,
	)
	_, _, err := fn(context.Background(), map[string]any{
		"upload": base64.StdEncoding.EncodeToString(content),
	})
	require.NoError(t, err)
	require.Equal(t, "file.json", capturedFilename)
	require.Equal(t, content, capturedContent)
}

func TestCreateToolFunctionSwagger_MultipartFileFromURL(t *testing.T) {
	t.Setenv("TEST", "true")
	// テスト用のダウンロード先は httptest のループバック http サーバーなので AllowLocal を有効化する。
	setFileFetchConfigForTest(t, config.FileFetchConfig{AllowLocal: true})
	content := []byte("swagger streamed file")
	fileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.WriteHeader(http.StatusOK)
		w.Write(content) //nolint: errcheck
	}))
	defer fileSrv.Close()

	var capturedFilename string
	var capturedContentType string
	var capturedContent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(10<<20))
		f, hdr, err := r.FormFile("upload")
		require.NoError(t, err)
		defer f.Close() //nolint: errcheck
		capturedFilename = hdr.Filename
		capturedContentType = hdr.Header.Get("Content-Type")
		capturedContent, err = io.ReadAll(f)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	fileType := openapi3.Types{"file"}
	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{Name: "upload", In: "formData", Type: &fileType},
		},
	}
	spec := &openapi2.T{}

	fn := CreateToolFunctionSwagger(
		http.DefaultClient,
		"/upload",
		"post",
		op,
		nil,
		spec,
		srv.URL,
		nil,
		false,
	)
	_, _, err := fn(context.Background(), map[string]any{
		"upload": fileSrv.URL + "/files/report.pdf?sig=abc123",
	})
	require.NoError(t, err)
	require.Equal(t, "report.pdf", capturedFilename)
	require.Equal(t, "application/pdf", capturedContentType)
	require.Equal(t, content, capturedContent)
}

func TestCreateToolFunctionSwagger_MultipartWithNonFileField(t *testing.T) {
	t.Setenv("TEST", "true")
	// file と通常フィールドが混在する場合、通常フィールドは従来通りWriteFieldされる
	var capturedCaption string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(10<<20))
		capturedCaption = r.FormValue("caption")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	fileType := openapi3.Types{"file"}
	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{Name: "upload", In: "formData", Type: &fileType},
			{Name: "caption", In: "formData"},
		},
	}
	spec := &openapi2.T{}

	fn := CreateToolFunctionSwagger(
		http.DefaultClient,
		"/upload",
		"post",
		op,
		nil,
		spec,
		srv.URL,
		nil,
		false,
	)
	_, _, err := fn(context.Background(), map[string]any{
		"upload":  base64.StdEncoding.EncodeToString([]byte("data")),
		"caption": "hello",
	})
	require.NoError(t, err)
	require.Equal(t, "hello", capturedCaption)
}

func TestCreateToolFunctionSwagger_PUT(t *testing.T) {
	t.Setenv("TEST", "true")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPut, r.Method)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi2.Operation{}
	spec := &openapi2.T{}

	fn := CreateToolFunctionSwagger(
		http.DefaultClient,
		"/resource/1",
		"put",
		op,
		nil,
		spec,
		srv.URL,
		nil,
		false,
	)
	result, _, err := fn(context.Background(), map[string]any{})
	require.NoError(t, err)
	require.NotEmpty(t, result)
}

func TestCreateToolFunctionSwagger_FormData(t *testing.T) {
	t.Setenv("TEST", "true")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{Name: "username", In: "formData"},
			{Name: "password", In: "formData"},
		},
	}
	spec := &openapi2.T{}

	fn := CreateToolFunctionSwagger(
		http.DefaultClient,
		"/login",
		"post",
		op,
		nil,
		spec,
		srv.URL,
		nil,
		false,
	)
	result, _, err := fn(context.Background(), map[string]any{
		"username": "alice",
		"password": "secret",
	})
	require.NoError(t, err)
	require.NotEmpty(t, result)
}

// --- Swagger 2 JSON body: format: binary ---

func swaggerBinaryProp() *openapi2.SchemaRef {
	return &openapi2.SchemaRef{
		Value: &openapi2.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
	}
}

func swaggerStringProp() *openapi2.SchemaRef {
	return &openapi2.SchemaRef{Value: &openapi2.Schema{Type: &openapi3.Types{"string"}}}
}

func swaggerJSONBodyOp(schema *openapi2.SchemaRef) *openapi2.Operation {
	return &openapi2.Operation{
		Parameters: []*openapi2.Parameter{{Name: "body", In: "body", Schema: schema}},
	}
}

// swaggerJSONBodyCapture はアップストリームが受け取った JSON ボディを capture へ格納する httptest サーバーを返す。
func swaggerJSONBodyCapture(t *testing.T, capture *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(capture))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	t.Cleanup(srv.Close)
	return srv
}

func callSwaggerJSONBody(
	t *testing.T,
	op *openapi2.Operation,
	spec *openapi2.T,
	baseURL string,
	body map[string]any,
) error {
	t.Helper()
	fn := CreateToolFunctionSwagger(
		http.DefaultClient,
		"/upload",
		"post",
		op,
		nil,
		spec,
		baseURL,
		nil,
		false,
	)
	_, _, err := fn(context.Background(), map[string]any{"body": body})
	return err
}

func swaggerFileServer(t *testing.T, content []byte) *httptest.Server {
	t.Helper()
	// テスト用のダウンロード先は httptest のループバックなので AllowLocal を有効化する。
	setFileFetchConfigForTest(t, config.FileFetchConfig{AllowLocal: true})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content) //nolint: errcheck
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCreateToolFunctionSwagger_JSONBody_BinaryBase64(t *testing.T) {
	t.Setenv("TEST", "true")
	var got map[string]any
	srv := swaggerJSONBodyCapture(t, &got)
	op := swaggerJSONBodyOp(swaggerSchemaRefForTest{props: map[string]*openapi2.SchemaRef{
		"file": swaggerBinaryProp(), "name": swaggerStringProp(),
	}}.ref())
	b64 := base64.StdEncoding.EncodeToString([]byte("hello"))
	require.NoError(t, callSwaggerJSONBody(t, op, &openapi2.T{}, srv.URL,
		map[string]any{"file": b64, "name": "n"}))
	require.Equal(t, b64, got["file"])
	require.Equal(t, "n", got["name"])
}

func TestCreateToolFunctionSwagger_JSONBody_BinaryFromURLAndObject(t *testing.T) {
	t.Setenv("TEST", "true")
	content := []byte("swagger json file")
	fileSrv := swaggerFileServer(t, content)
	var got map[string]any
	srv := swaggerJSONBodyCapture(t, &got)
	op := swaggerJSONBodyOp(swaggerSchemaRefForTest{props: map[string]*openapi2.SchemaRef{
		"file": swaggerBinaryProp(),
	}}.ref())

	require.NoError(t, callSwaggerJSONBody(t, op, &openapi2.T{}, srv.URL,
		map[string]any{"file": fileSrv.URL + "/a.bin"}))
	require.Equal(t, base64.StdEncoding.EncodeToString(content), got["file"])

	got = nil
	require.NoError(t, callSwaggerJSONBody(t, op, &openapi2.T{}, srv.URL,
		map[string]any{"file": map[string]any{"url": fileSrv.URL + "/a.bin"}}))
	require.Equal(t, base64.StdEncoding.EncodeToString(content), got["file"])
}

func TestCreateToolFunctionSwagger_JSONBody_MultipleBinaryFields(t *testing.T) {
	t.Setenv("TEST", "true")
	content := []byte("from url")
	fileSrv := swaggerFileServer(t, content)
	var got map[string]any
	srv := swaggerJSONBodyCapture(t, &got)
	op := swaggerJSONBodyOp(swaggerSchemaRefForTest{props: map[string]*openapi2.SchemaRef{
		"front": swaggerBinaryProp(), "back": swaggerBinaryProp(), "title": swaggerStringProp(),
	}}.ref())
	b64 := base64.StdEncoding.EncodeToString([]byte("raw"))
	require.NoError(t, callSwaggerJSONBody(t, op, &openapi2.T{}, srv.URL, map[string]any{
		"front": b64, "back": fileSrv.URL + "/b", "title": "t",
	}))
	require.Equal(t, b64, got["front"])
	require.Equal(t, base64.StdEncoding.EncodeToString(content), got["back"])
	require.Equal(t, "t", got["title"])
}

func TestCreateToolFunctionSwagger_JSONBody_NestedArrayNullAndRef(t *testing.T) {
	t.Setenv("TEST", "true")
	content := []byte("nested")
	fileSrv := swaggerFileServer(t, content)
	want := base64.StdEncoding.EncodeToString(content)
	var got map[string]any
	srv := swaggerJSONBodyCapture(t, &got)

	objType := openapi3.Types{"object"}
	arrType := openapi3.Types{"array"}
	spec := &openapi2.T{Definitions: map[string]*openapi2.SchemaRef{
		"Attachment": {Value: &openapi2.Schema{
			Type: &objType,
			Properties: map[string]*openapi2.SchemaRef{
				"data": swaggerBinaryProp(), "label": swaggerStringProp(),
			},
		}},
	}}
	body := &openapi2.SchemaRef{Value: &openapi2.Schema{
		Type: &objType,
		Properties: map[string]*openapi2.SchemaRef{
			"nested": {Value: &openapi2.Schema{
				Type:       &objType,
				Properties: map[string]*openapi2.SchemaRef{"doc": swaggerBinaryProp()},
			}},
			"files": {Value: &openapi2.Schema{Type: &arrType, Items: swaggerBinaryProp()}},
			"att":   {Ref: "#/definitions/Attachment"},
			"opt":   swaggerBinaryProp(),
			"plain": swaggerStringProp(),
		},
	}}
	op := swaggerJSONBodyOp(body)
	require.NoError(t, callSwaggerJSONBody(t, op, spec, srv.URL, map[string]any{
		"nested": map[string]any{"doc": fileSrv.URL + "/x"},
		"files":  []any{fileSrv.URL + "/1", map[string]any{"url": fileSrv.URL + "/2"}},
		"att":    map[string]any{"data": fileSrv.URL + "/3", "label": "l"},
		"opt":    nil,
		"plain":  fileSrv.URL + "/not-resolved",
	}))
	require.Equal(t, want, got["nested"].(map[string]any)["doc"])
	require.Equal(t, []any{want, want}, got["files"])
	att := got["att"].(map[string]any)
	require.Equal(t, want, att["data"])
	require.Equal(t, "l", att["label"])
	v, ok := got["opt"]
	require.True(t, ok)
	require.Nil(t, v)
	require.Equal(t, fileSrv.URL+"/not-resolved", got["plain"])
}

func TestBuildInputSchemaSwagger_BodyBinaryProperties(t *testing.T) {
	objType := openapi3.Types{"object"}
	arrType := openapi3.Types{"array"}
	spec := &openapi2.T{Definitions: map[string]*openapi2.SchemaRef{
		"Att": {Value: &openapi2.Schema{
			Type:       &objType,
			Properties: map[string]*openapi2.SchemaRef{"data": swaggerBinaryProp()},
		}},
	}}
	op := swaggerJSONBodyOp(&openapi2.SchemaRef{Value: &openapi2.Schema{
		Type: &objType,
		Properties: map[string]*openapi2.SchemaRef{
			"file":  swaggerBinaryProp(),
			"files": {Value: &openapi2.Schema{Type: &arrType, Items: swaggerBinaryProp()}},
			"att":   {Ref: "#/definitions/Att"},
			"name":  swaggerStringProp(),
		},
	}})
	schema := BuildInputSchemaSwagger(op, nil, spec)
	body := schema["properties"].(map[string]any)["body"].(map[string]any)
	props := body["properties"].(map[string]any)

	isFileMeta := func(m map[string]any) bool {
		meta, _ := m["_meta"].(map[string]any)
		mf, _ := meta["manifold"].(map[string]any)
		return mf["file"] == true
	}
	file := props["file"].(map[string]any)
	require.NotNil(t, file["oneOf"])
	require.True(t, isFileMeta(file))

	files := props["files"].(map[string]any)
	require.Equal(t, "array", files["type"])
	require.True(t, isFileMeta(files["items"].(map[string]any)))

	att := props["att"].(map[string]any)
	data := att["properties"].(map[string]any)["data"].(map[string]any)
	require.True(t, isFileMeta(data))

	// 非 binary も OpenAPI 3 の JSON ボディと同じ形（_meta 付き）になる
	require.Equal(
		t,
		map[string]any{"type": "string", "description": "", "_meta": map[string]any{}},
		props["name"],
	)
}

// swaggerSchemaRefForTest は object スキーマの SchemaRef を組み立てる小さなヘルパー。
type swaggerSchemaRefForTest struct {
	props map[string]*openapi2.SchemaRef
}

func (s swaggerSchemaRefForTest) ref() *openapi2.SchemaRef {
	return &openapi2.SchemaRef{Value: &openapi2.Schema{
		Type:       &openapi3.Types{"object"},
		Properties: s.props,
	}}
}

// --- describeSchemaFieldsSwagger ---

func TestDescribeSchemaFieldsSwagger_SelfReferencingDefinition(t *testing.T) {
	// 自己参照する definitions（Node.parent / Node.children が Node を指す）でも
	// 無限再帰（stack overflow）にならず、再訪したところで打ち切って要約を返す。
	ref := func() *openapi2.SchemaRef { return &openapi2.SchemaRef{Ref: "#/definitions/Node"} }
	node := &openapi2.Schema{
		Type: &openapi3.Types{openapi3.TypeObject},
		Properties: openapi2.Schemas{
			"name":   {Value: &openapi2.Schema{Type: &openapi3.Types{openapi3.TypeString}}},
			"parent": ref(),
			"children": {Value: &openapi2.Schema{
				Type:  &openapi3.Types{openapi3.TypeArray},
				Items: ref(),
			}},
		},
	}
	spec := &openapi2.T{Definitions: openapi2.Schemas{"Node": {Value: node}}}

	got := describeSchemaFieldsSwagger(node, spec)
	require.Equal(t, "children (array of object); name (string); parent (object)", got)
}

func TestBuildInputSchemaSwagger_SelfReferencingBodyDefinition(t *testing.T) {
	// 実際の取り込み経路（ParseSwaggerSpec → BuildInputSchemaSwagger）でも、自己参照する
	// definitions の body が無限再帰にならず、フィールドの要約付きの description を返す。
	raw := []byte(`{
  "swagger": "2.0",
  "info": {"title": "tree", "version": "1.0"},
  "paths": {
    "/nodes": {
      "post": {
        "operationId": "createNode",
        "parameters": [
          {"name": "body", "in": "body", "schema": {"$ref": "#/definitions/Node"}}
        ],
        "responses": {"200": {"description": "ok"}}
      }
    }
  },
  "definitions": {
    "Node": {
      "type": "object",
      "properties": {
        "name": {"type": "string"},
        "parent": {"$ref": "#/definitions/Node"},
        "children": {"type": "array", "items": {"$ref": "#/definitions/Node"}}
      }
    }
  }
}`)
	spec, err := ParseSwaggerSpec(t.Context(), raw)
	require.NoError(t, err)
	op := spec.Paths["/nodes"].Post
	require.NotNil(t, op)

	schema := BuildInputSchemaSwagger(op, nil, spec)
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	bodyProp, ok := props["body"].(map[string]any)
	require.True(t, ok)
	require.Equal(
		t,
		"Request body. JSON object with fields: {children (array of object); name (string); parent (object)}",
		bodyProp["description"],
	)
	bodyProps, ok := bodyProp["properties"].(map[string]any)
	require.True(t, ok)
	require.Len(t, bodyProps, 3)
	for name, typ := range map[string]string{"name": "string", "parent": "object", "children": "array"} {
		field, ok := bodyProps[name].(map[string]any)
		require.True(t, ok, name)
		require.Equal(t, typ, field["type"], name)
	}
}

// --- "[" "]" を含む名前のサニタイズ（OpenAPI 3.x の query パラメータと同じ規則） ---

// mcpPropertyNamePattern は MCP クライアント（Anthropic API など）が input schema の
// プロパティ名に課すパターン。
var mcpPropertyNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,64}$`)

func swaggerBracketOperation() *openapi2.Operation {
	str := &openapi3.Types{"string"}
	return &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{Name: "id[]", In: "path", Required: true, Type: str},
			{Name: "tag[]", In: "query", Type: str},
			{Name: "filter[status]", In: "query", Required: true, Type: str},
			{
				Name:     "payload[]",
				In:       "body",
				Required: true,
				Schema: &openapi2.SchemaRef{
					Value: &openapi2.Schema{
						Type: &openapi3.Types{"object"},
						Properties: map[string]*openapi2.SchemaRef{
							"ids[]": {Value: &openapi2.Schema{
								Type:  &openapi3.Types{"array"},
								Items: &openapi2.SchemaRef{Value: &openapi2.Schema{Type: str}},
							}},
							"name": {Value: &openapi2.Schema{Type: str}},
							"nested": {Value: &openapi2.Schema{
								Type: &openapi3.Types{"object"},
								Properties: map[string]*openapi2.SchemaRef{
									"opt[key]": {Value: &openapi2.Schema{Type: str}},
								},
							}},
						},
					},
				},
			},
		},
	}
}

func TestBuildInputSchemaSwagger_BracketNamesSanitized(t *testing.T) {
	schema := BuildInputSchemaSwagger(swaggerBracketOperation(), nil, &openapi2.T{})

	props := schema["properties"].(map[string]any)
	for name := range props {
		require.Regexp(t, mcpPropertyNamePattern, name)
	}
	require.Contains(t, props, "id_")
	require.Contains(t, props, "tag_")
	require.Contains(t, props, "filter_status")
	require.Contains(t, props, "payload_")
	require.ElementsMatch(t, []string{"id_", "filter_status", "payload_"}, schema["required"])

	bodyProps := props["payload_"].(map[string]any)["properties"].(map[string]any)
	for name := range bodyProps {
		require.Regexp(t, mcpPropertyNamePattern, name)
	}
	require.Contains(t, bodyProps, "ids_")
	require.Contains(t, bodyProps, "name")
	require.Contains(t, bodyProps, "nested")
	require.Equal(t, "array", bodyProps["ids_"].(map[string]any)["type"])
}

func TestBuildInputSchemaSwagger_FormDataBracketNamesSanitized(t *testing.T) {
	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{Name: "ids[]", In: "formData", Required: true, Type: &openapi3.Types{"string"}},
			{Name: "files[]", In: "formData", Type: &openapi3.Types{"file"}},
		},
	}
	schema := BuildInputSchemaSwagger(op, nil, &openapi2.T{})

	props := schema["properties"].(map[string]any)
	require.Len(t, props, 2)
	require.Contains(t, props, "ids_")
	require.Contains(t, props, "files_")
	require.Equal(t, []string{"ids_"}, schema["required"])
}

func TestCreateToolFunctionSwagger_BracketNamesRestored(t *testing.T) {
	t.Setenv("TEST", "true")
	var gotPath string
	var gotQuery url.Values
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	fn := CreateToolFunctionSwagger(
		http.DefaultClient,
		"/items/{id[]}",
		"post",
		swaggerBracketOperation(),
		nil,
		&openapi2.T{},
		srv.URL,
		nil,
		false,
	)
	_, _, err := fn(context.Background(), map[string]any{
		"id_":           "42",
		"tag_":          "a",
		"filter_status": "open",
		"payload_": map[string]any{
			"ids_": []any{"1", "2"},
			"name": "x",
			// ネストしたプロパティはスキーマに展開されないので、呼び出し側は説明文にある
			// 元の名前で渡す。サニタイズ後の名前で渡された場合も元の名前へ戻す。
			"nested": map[string]any{"opt_key": "v"},
		},
	})
	require.NoError(t, err)

	require.Equal(t, "/items/42", gotPath)
	require.Equal(t, url.Values{"tag[]": {"a"}, "filter[status]": {"open"}}, gotQuery)
	require.Equal(t, map[string]any{
		"ids[]":  []any{"1", "2"},
		"name":   "x",
		"nested": map[string]any{"opt[key]": "v"},
	}, gotBody)
}

// スキーマ上の名前ではなく元の名前（"ids[]"）で渡された body のキーは、そのまま上流へ送る。
func TestCreateToolFunctionSwagger_BodyOriginalBracketKeyPassesThrough(t *testing.T) {
	t.Setenv("TEST", "true")
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	fn := CreateToolFunctionSwagger(
		http.DefaultClient,
		"/items/{id[]}",
		"post",
		swaggerBracketOperation(),
		nil,
		&openapi2.T{},
		srv.URL,
		nil,
		false,
	)
	_, _, err := fn(context.Background(), map[string]any{
		"id_":      "42",
		"payload_": map[string]any{"ids[]": []any{"1"}},
	})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"ids[]": []any{"1"}}, gotBody)
}

func TestCreateToolFunctionSwagger_FormDataBracketNamesRestored(t *testing.T) {
	t.Setenv("TEST", "true")
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotForm = r.PostForm
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{Name: "ids[]", In: "formData", Type: &openapi3.Types{"string"}},
			{Name: "name", In: "formData", Type: &openapi3.Types{"string"}},
		},
	}
	fn := CreateToolFunctionSwagger(
		http.DefaultClient, "/items", "post", op, nil, &openapi2.T{}, srv.URL, nil, false,
	)
	_, _, err := fn(context.Background(), map[string]any{"ids_": "1", "name": "x"})
	require.NoError(t, err)
	require.Equal(t, url.Values{"ids[]": {"1"}, "name": {"x"}}, gotForm)
}

func TestCreateToolFunctionSwagger_MultipartBracketNameRestored(t *testing.T) {
	t.Setenv("TEST", "true")
	var gotFields []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(1<<20))
		for name := range r.MultipartForm.File {
			gotFields = append(gotFields, name)
		}
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{Name: "files[]", In: "formData", Type: &openapi3.Types{"file"}},
		},
	}
	fn := CreateToolFunctionSwagger(
		http.DefaultClient, "/upload", "post", op, nil, &openapi2.T{}, srv.URL, nil, false,
	)
	_, _, err := fn(context.Background(), map[string]any{
		"files_": base64.StdEncoding.EncodeToString([]byte("hello")),
	})
	require.NoError(t, err)
	require.Equal(t, []string{"files[]"}, gotFields)
}

// --- GET / DELETE のリクエストボディ ---

func TestCreateToolFunctionSwagger_BodyOnGETAndDELETE(t *testing.T) {
	t.Setenv("TEST", "true")
	for _, method := range []string{"get", "delete"} {
		t.Run(method, func(t *testing.T) {
			var gotMethod, gotContentType string
			var gotBody []byte
			srv := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotMethod = r.Method
					gotContentType = r.Header.Get("Content-Type")
					gotBody, _ = io.ReadAll(r.Body)
					w.Write([]byte(`{}`)) //nolint: errcheck
				}),
			)
			defer srv.Close()

			op := &openapi2.Operation{
				Parameters: []*openapi2.Parameter{
					{Name: "q", In: "query"},
					{Name: "body", In: "body", Schema: &openapi2.SchemaRef{
						Value: &openapi2.Schema{},
					}},
				},
			}
			fn := CreateToolFunctionSwagger(
				http.DefaultClient, "/search", method, op, nil, &openapi2.T{}, srv.URL, nil, false,
			)

			_, _, err := fn(context.Background(), map[string]any{
				"q":    "x",
				"body": map[string]any{"filter": "open"},
			})
			require.NoError(t, err)
			require.Equal(t, strings.ToUpper(method), gotMethod)
			require.Equal(t, "application/json", gotContentType)
			require.JSONEq(t, `{"filter":"open"}`, string(gotBody))

			// body を渡さなければ、ボディも Content-Type も送らない。
			_, _, err = fn(context.Background(), map[string]any{"q": "x"})
			require.NoError(t, err)
			require.Empty(t, gotContentType)
			require.Empty(t, gotBody)
		})
	}
}

func TestCreateToolFunctionSwagger_FormDataOnGET(t *testing.T) {
	t.Setenv("TEST", "true")
	var gotContentType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{{Name: "name", In: "formData"}},
	}
	fn := CreateToolFunctionSwagger(
		http.DefaultClient, "/search", "get", op, nil, &openapi2.T{}, srv.URL, nil, false,
	)

	_, _, err := fn(context.Background(), map[string]any{"name": "x"})
	require.NoError(t, err)
	require.Equal(t, "application/x-www-form-urlencoded", gotContentType)
	require.Equal(t, "name=x", string(gotBody))

	// 値が 1 つも無い GET では、空のフォームボディ（と Content-Type）を付けない。
	_, _, err = fn(context.Background(), map[string]any{})
	require.NoError(t, err)
	require.Empty(t, gotContentType)
	require.Empty(t, gotBody)
}

// --- OpenAPI 3.x と揃えた挙動 ---

// JSON ボディのプロパティは OpenAPI 3.x と同じく、ネストした object・配列の items・enum・
// required まで input schema に展開する。
func TestBuildInputSchemaSwagger_BodyRichSchemaMatchesOpenAPI(t *testing.T) {
	str := &openapi3.Types{"string"}
	spec := &openapi2.T{
		Definitions: map[string]*openapi2.SchemaRef{
			"Owner": {Value: &openapi2.Schema{
				Type:     &openapi3.Types{"object"},
				Required: []string{"email"},
				Properties: map[string]*openapi2.SchemaRef{
					"email":   {Value: &openapi2.Schema{Type: str}},
					"roles[]": {Value: &openapi2.Schema{Type: str}},
				},
			}},
		},
	}
	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{{
			Name: "body",
			In:   "body",
			Schema: &openapi2.SchemaRef{Value: &openapi2.Schema{
				Type:     &openapi3.Types{"object"},
				Required: []string{"name", "tags[]"},
				Properties: map[string]*openapi2.SchemaRef{
					"name":   {Value: &openapi2.Schema{Type: str}},
					"status": {Value: &openapi2.Schema{Type: str, Enum: []any{"open", "closed"}}},
					"tags[]": {Value: &openapi2.Schema{
						Type:  &openapi3.Types{"array"},
						Items: &openapi2.SchemaRef{Value: &openapi2.Schema{Type: str}},
					}},
					"owner": {Ref: "#/definitions/Owner"},
				},
			}},
		}},
	}

	schema := BuildInputSchemaSwagger(op, nil, spec)
	body := schema["properties"].(map[string]any)["body"].(map[string]any)
	require.Equal(t, []string{"name", "tags_"}, body["required"])

	props := body["properties"].(map[string]any)
	require.Equal(t, []any{"open", "closed"}, props["status"].(map[string]any)["enum"])

	tags := props["tags_"].(map[string]any)
	require.Equal(t, "array", tags["type"])
	require.Equal(t, "string", tags["items"].(map[string]any)["type"])

	owner := props["owner"].(map[string]any)
	require.Equal(t, "object", owner["type"])
	require.Equal(t, []string{"email"}, owner["required"])
	ownerProps := owner["properties"].(map[string]any)
	require.Contains(t, ownerProps, "email")
	require.Contains(t, ownerProps, "roles_")
}

func TestBuildInputSchemaSwagger_FormDataArrayAndEnum(t *testing.T) {
	str := &openapi3.Types{"string"}
	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{
				Name:  "tags",
				In:    "formData",
				Type:  &openapi3.Types{"array"},
				Items: &openapi2.SchemaRef{Value: &openapi2.Schema{Type: str}},
			},
			{Name: "status", In: "formData", Type: str, Enum: []any{"open", "closed"}},
		},
	}
	props := BuildInputSchemaSwagger(op, nil, &openapi2.T{})["properties"].(map[string]any)

	tags := props["tags"].(map[string]any)
	require.Equal(t, "array", tags["type"])
	require.Equal(t, "string", tags["items"].(map[string]any)["type"])
	require.Equal(t, []any{"open", "closed"}, props["status"].(map[string]any)["enum"])
}

func TestCreateToolFunctionSwagger_FormDataArrayCollectionFormat(t *testing.T) {
	t.Setenv("TEST", "true")
	tests := []struct {
		collectionFormat string
		want             []string
	}{
		{"", []string{"a,b"}},
		{"csv", []string{"a,b"}},
		{"ssv", []string{"a b"}},
		{"tsv", []string{"a\tb"}},
		{"pipes", []string{"a|b"}},
		{"multi", []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run("format="+tt.collectionFormat, func(t *testing.T) {
			var gotForm url.Values
			srv := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.NoError(t, r.ParseForm())
					gotForm = r.PostForm
					w.Write([]byte(`{}`)) //nolint: errcheck
				}),
			)
			defer srv.Close()

			op := &openapi2.Operation{
				Parameters: []*openapi2.Parameter{{
					Name:             "tags[]",
					In:               "formData",
					Type:             &openapi3.Types{"array"},
					CollectionFormat: tt.collectionFormat,
					Items: &openapi2.SchemaRef{
						Value: &openapi2.Schema{Type: &openapi3.Types{"string"}},
					},
				}},
			}
			fn := CreateToolFunctionSwagger(
				http.DefaultClient, "/items", "post", op, nil, &openapi2.T{}, srv.URL, nil, false,
			)
			_, _, err := fn(context.Background(), map[string]any{"tags_": []any{"a", "b"}})
			require.NoError(t, err)
			require.Equal(t, url.Values{"tags[]": tt.want}, gotForm)
		})
	}
}

func TestCreateToolFunctionSwagger_MultipartArrayCollectionFormat(t *testing.T) {
	t.Setenv("TEST", "true")
	var gotValues map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(1<<20))
		gotValues = r.MultipartForm.Value
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	strItems := &openapi2.SchemaRef{Value: &openapi2.Schema{Type: &openapi3.Types{"string"}}}
	op := &openapi2.Operation{
		Parameters: []*openapi2.Parameter{
			{Name: "file", In: "formData", Type: &openapi3.Types{"file"}},
			{Name: "csv", In: "formData", Type: &openapi3.Types{"array"}, Items: strItems},
			{
				Name:             "multi",
				In:               "formData",
				Type:             &openapi3.Types{"array"},
				Items:            strItems,
				CollectionFormat: "multi",
			},
		},
	}
	fn := CreateToolFunctionSwagger(
		http.DefaultClient, "/upload", "post", op, nil, &openapi2.T{}, srv.URL, nil, false,
	)
	_, _, err := fn(context.Background(), map[string]any{
		"file":  base64.StdEncoding.EncodeToString([]byte("hello")),
		"csv":   []any{"a", "b"},
		"multi": []any{"c", "d"},
	})
	require.NoError(t, err)
	require.Equal(t, map[string][]string{"csv": {"a,b"}, "multi": {"c", "d"}}, gotValues)
}

func swaggerResponses(status string, schema *openapi2.SchemaRef) *openapi2.Responses {
	responses := openapi2.NewResponsesWithCapacity(1)
	responses.Set(status, &openapi2.Response{Schema: schema})
	return responses
}

func TestResponseIsBinarySwagger(t *testing.T) {
	fileSchema := &openapi2.SchemaRef{Value: &openapi2.Schema{Type: &openapi3.Types{"file"}}}
	binarySchema := &openapi2.SchemaRef{
		Value: &openapi2.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
	}
	objectSchema := &openapi2.SchemaRef{Value: &openapi2.Schema{Type: &openapi3.Types{"object"}}}
	spec := &openapi2.T{Definitions: map[string]*openapi2.SchemaRef{"Blob": binarySchema}}

	tests := []struct {
		name string
		op   *openapi2.Operation
		want bool
	}{
		{"nil operation", nil, false},
		{"no responses", &openapi2.Operation{}, false},
		{"type file", &openapi2.Operation{Responses: swaggerResponses("200", fileSchema)}, true},
		{
			"format binary",
			&openapi2.Operation{Responses: swaggerResponses("201", binarySchema)},
			true,
		},
		{
			"ref to binary",
			&openapi2.Operation{Responses: swaggerResponses(
				"200", &openapi2.SchemaRef{Ref: "#/definitions/Blob"},
			)},
			true,
		},
		{
			"json object",
			&openapi2.Operation{Responses: swaggerResponses("200", objectSchema)},
			false,
		},
		{
			"binary only on error status",
			&openapi2.Operation{Responses: swaggerResponses("500", fileSchema)},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ResponseIsBinarySwagger(tt.op, spec))
		})
	}
}

func TestCreateToolFunctionSwagger_BinaryResponseIsBase64Encoded(t *testing.T) {
	t.Setenv("TEST", "true")
	payload := []byte{0x89, 'P', 'N', 'G', 0x00, 0xff}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fail") != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"not found"}`)) //nolint: errcheck
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(payload) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi2.Operation{Parameters: []*openapi2.Parameter{{Name: "fail", In: "query"}}}
	fn := CreateToolFunctionSwagger(
		http.DefaultClient, "/image", "get", op, nil, &openapi2.T{}, srv.URL, nil, true,
	)

	body, contentType, err := fn(context.Background(), map[string]any{})
	require.NoError(t, err)
	require.Equal(t, "image/png", contentType)
	require.Equal(t, base64.URLEncoding.EncodeToString(payload), string(body))

	// エラー応答は base64 化せず、生のまま返す。
	body, contentType, err = fn(context.Background(), map[string]any{"fail": "1"})
	require.NoError(t, err)
	require.Equal(t, "application/json", contentType)
	require.JSONEq(t, `{"error":"not found"}`, string(body))
}
