package oastomcptool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi3"
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

	fn := CreateToolFunctionSwagger("/pets/{petId}", "get", op, nil, spec, srv.URL, nil)
	result, err := fn(context.Background(), map[string]any{"petId": "99"})
	require.NoError(t, err)
	require.Contains(t, result, "99")
}

func TestCreateToolFunctionSwagger_POST_Body(t *testing.T) {
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

	fn := CreateToolFunctionSwagger("/pets", "post", op, nil, spec, srv.URL, nil)
	result, err := fn(context.Background(), map[string]any{
		"body": map[string]any{"name": "Buddy"},
	})
	require.NoError(t, err)
	require.Contains(t, result, "1")
}

func TestCreateToolFunctionSwagger_WithQueryParam(t *testing.T) {
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

	fn := CreateToolFunctionSwagger("/pets", "get", op, nil, spec, srv.URL, nil)
	result, err := fn(context.Background(), map[string]any{"status": "sold"})
	require.NoError(t, err)
	require.Equal(t, "sold", capturedQuery)
	require.NotEmpty(t, result)
}

func TestCreateToolFunctionSwagger_UnsupportedMethod(t *testing.T) {
	op := &openapi2.Operation{}
	spec := &openapi2.T{}

	fn := CreateToolFunctionSwagger("/resource", "UNKNOWN", op, nil, spec, "http://example.com", nil)
	_, err := fn(context.Background(), map[string]any{})
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

	fn := CreateToolFunctionSwagger("/items/{id}", "get", op, nil, spec, "http://example.com", nil)
	_, err := fn(context.Background(), map[string]any{"id": "../evil"})
	require.Error(t, err)
}

func TestCreateToolFunctionSwagger_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`bad request`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi2.Operation{}
	spec := &openapi2.T{}

	fn := CreateToolFunctionSwagger("/resource", "get", op, nil, spec, srv.URL, nil)
	_, err := fn(context.Background(), map[string]any{})
	// Swaggerのツールは400以上をエラーとして返す
	require.Error(t, err)
}

func TestCreateToolFunctionSwagger_AuthOverrideFromContext(t *testing.T) {
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi2.Operation{}
	spec := &openapi2.T{}

	fn := CreateToolFunctionSwagger("/resource", "get", op, nil, spec, srv.URL, map[string]string{
		"Authorization": "Bearer static",
	})

	ctx := contexts.ToRequestAuthHeader(context.Background(), "Bearer override")
	result, err := fn(ctx, map[string]any{})
	require.NoError(t, err)
	require.Equal(t, "Bearer override", capturedAuth)
	require.NotEmpty(t, result)
}

func TestCreateToolFunctionSwagger_DELETE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	op := &openapi2.Operation{}
	spec := &openapi2.T{}

	fn := CreateToolFunctionSwagger("/resource/1", "delete", op, nil, spec, srv.URL, nil)
	result, err := fn(context.Background(), map[string]any{})
	require.NoError(t, err)
	_ = result
}

func TestCreateToolFunctionSwagger_PATCH(t *testing.T) {
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

	fn := CreateToolFunctionSwagger("/resource/1", "patch", op, nil, spec, srv.URL, nil)
	result, err := fn(context.Background(), map[string]any{
		"body": map[string]any{"name": "updated"},
	})
	require.NoError(t, err)
	require.Contains(t, result, "updated")
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

func TestCreateToolFunctionSwagger_PUT(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPut, r.Method)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi2.Operation{}
	spec := &openapi2.T{}

	fn := CreateToolFunctionSwagger("/resource/1", "put", op, nil, spec, srv.URL, nil)
	result, err := fn(context.Background(), map[string]any{})
	require.NoError(t, err)
	require.NotEmpty(t, result)
}

func TestCreateToolFunctionSwagger_FormData(t *testing.T) {
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

	fn := CreateToolFunctionSwagger("/login", "post", op, nil, spec, srv.URL, nil)
	result, err := fn(context.Background(), map[string]any{
		"username": "alice",
		"password": "secret",
	})
	require.NoError(t, err)
	require.NotEmpty(t, result)
}
