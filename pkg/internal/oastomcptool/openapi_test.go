package oastomcptool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/stretchr/testify/require"
)

// --- sanitize_path_parameter_value ---

func TestSanitizePathParameterValue(t *testing.T) {
	tests := []struct {
		name       string
		paramValue any
		paramName  string
		wantErr    bool
		want       string
	}{
		{"nil value", nil, "id", false, ""},
		{"empty string", "", "id", false, ""},
		{"valid integer string", "123", "id", false, "123"},
		{"valid slug", "my-resource", "slug", false, "my-resource"},
		{"path separator /", "a/b", "id", true, ""},
		{"backslash (converted to /)", "a\\b", "id", true, ""},
		{"single dot segment", ".", "id", true, ""},
		{"double dot segment", "..", "id", true, ""},
		{"url encode spaces", "hello world", "id", false, "hello%20world"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sanitize_path_parameter_value(tt.paramValue, tt.paramName)
			if tt.wantErr {
				require.Error(t, err)
				require.Empty(t, got)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})
	}
}

// --- FetchSpecBytes ---

func TestFetchSpecBytes_LocalFile(t *testing.T) {
	data, err := FetchSpecBytes(context.Background(), "../mcpsrv/fixtures/petstore_oas.json")
	require.NoError(t, err)
	require.NotEmpty(t, data)
}

func TestFetchSpecBytes_LocalFileNotFound(t *testing.T) {
	_, err := FetchSpecBytes(context.Background(), "nonexistent_spec.json")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestFetchSpecBytes_URL_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"openapi":"3.0.0"}`)) //nolint: errcheck
	}))
	defer srv.Close()

	data, err := FetchSpecBytes(context.Background(), srv.URL+"/openapi.json")
	require.NoError(t, err)
	require.Contains(t, string(data), "openapi")
}

func TestFetchSpecBytes_URL_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := FetchSpecBytes(context.Background(), srv.URL+"/notfound")
	require.Error(t, err)
	require.Contains(t, err.Error(), "HTTP error")
}

// --- deriveBaseUrlFromSpecPath ---

func TestDeriveBaseUrlFromSpecPath(t *testing.T) {
	tests := []struct {
		specPath string
		expected string
	}{
		{"", ""},
		{"local/path/openapi.json", ""},
		{"http://example.com/openapi.json", "http://example.com"},
		{"https://example.com/api/swagger.yaml", "https://example.com/api"},
		{"https://example.com/api/swagger.json", "https://example.com/api"},
		{"https://example.com/api/openapi.yaml", "https://example.com/api"},
		{"http://example.com/api/v1/spec.json", "http://example.com/api/v1"},
		{"http://example.com/api/v1/spec.yml", "http://example.com/api/v1"},
		{"http://example.com/no-extension", ""},
	}

	for _, tt := range tests {
		t.Run(tt.specPath, func(t *testing.T) {
			got := deriveBaseUrlFromSpecPath(t.Context(), tt.specPath)
			require.Equal(t, tt.expected, got)
		})
	}
}

// --- GetBaseUrl ---

func TestGetBaseUrl_OpenAPI3_AbsoluteServer(t *testing.T) {
	spec := map[string]any{
		"servers": []any{
			map[string]any{"url": "https://api.example.com"},
		},
	}
	got := GetBaseUrl(t.Context(), spec, "")
	require.Equal(t, "https://api.example.com", got)
}

func TestGetBaseUrl_OpenAPI3_RelativeServer(t *testing.T) {
	spec := map[string]any{
		"servers": []any{
			map[string]any{"url": "/api/v1"},
		},
	}
	got := GetBaseUrl(t.Context(), spec, "https://example.com/openapi.json")
	require.Equal(t, "https://example.com/api/v1", got)
}

func TestGetBaseUrl_Swagger2_WithHost(t *testing.T) {
	spec := map[string]any{
		"host":     "api.example.com",
		"schemes":  []any{"http"},
		"basePath": "/v2",
	}
	got := GetBaseUrl(t.Context(), spec, "")
	require.Equal(t, "http://api.example.com/v2", got)
}

func TestGetBaseUrl_Swagger2_DefaultScheme(t *testing.T) {
	spec := map[string]any{
		"host":     "api.example.com",
		"basePath": "/v1",
	}
	got := GetBaseUrl(t.Context(), spec, "")
	require.Equal(t, "https://api.example.com/v1", got)
}

func TestGetBaseUrl_Fallback(t *testing.T) {
	got := GetBaseUrl(t.Context(), map[string]any{}, "https://example.com/openapi.json")
	require.Equal(t, "https://example.com", got)
}

// --- GetBaseUrlFromOpenAPI3 ---

func TestGetBaseUrlFromOpenAPI3_WithServer(t *testing.T) {
	spec := &openapi3.T{
		Servers: openapi3.Servers{
			{URL: "https://api.example.com/v2"},
		},
	}
	got := GetBaseUrlFromOpenAPI3(t.Context(), spec, "")
	require.Equal(t, "https://api.example.com/v2", got)
}

func TestGetBaseUrlFromOpenAPI3_RelativeServer(t *testing.T) {
	spec := &openapi3.T{
		Servers: openapi3.Servers{
			{URL: "/api/v1"},
		},
	}
	got := GetBaseUrlFromOpenAPI3(t.Context(), spec, "https://example.com/openapi.json")
	require.Equal(t, "https://example.com/api/v1", got)
}

func TestGetBaseUrlFromOpenAPI3_NoServers(t *testing.T) {
	spec := &openapi3.T{}
	got := GetBaseUrlFromOpenAPI3(t.Context(), spec, "https://example.com/openapi.json")
	require.Equal(t, "https://example.com", got)
}

func TestGetBaseUrlFromOpenAPI3_NoServersNoPath(t *testing.T) {
	spec := &openapi3.T{}
	got := GetBaseUrlFromOpenAPI3(t.Context(), spec, "")
	require.Equal(t, "", got)
}

// --- BuildInputSchema ---

func TestBuildInputSchema_WithPathAndQueryParams(t *testing.T) {
	op := &openapi3.Operation{
		Parameters: openapi3.Parameters{
			&openapi3.ParameterRef{
				Value: &openapi3.Parameter{
					Name:     "petId",
					In:       "path",
					Required: true,
					Schema: &openapi3.SchemaRef{
						Value: &openapi3.Schema{Type: &openapi3.Types{"integer"}},
					},
				},
			},
			&openapi3.ParameterRef{
				Value: &openapi3.Parameter{
					Name:     "status",
					In:       "query",
					Required: false,
					Schema: &openapi3.SchemaRef{
						Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
					},
				},
			},
		},
	}

	schema := BuildInputSchema(op)
	require.Equal(t, "object", schema["type"])

	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, props, "petId")
	require.Contains(t, props, "status")

	petIdProp := props["petId"].(map[string]any)
	require.Equal(t, "integer", petIdProp["type"])

	required, ok := schema["required"].([]string)
	require.True(t, ok)
	require.Contains(t, required, "petId")
	require.NotContains(t, required, "status")
}

func TestBuildInputSchema_WithJSONBody(t *testing.T) {
	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Required: true,
				Content: openapi3.Content{
					"application/json": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{
								Properties: openapi3.Schemas{
									"name": &openapi3.SchemaRef{
										Value: &openapi3.Schema{
											Type: &openapi3.Types{"string"},
										},
									},
									"age": &openapi3.SchemaRef{
										Value: &openapi3.Schema{
											Type: &openapi3.Types{"integer"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	schema := BuildInputSchema(op)
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, props, "body")

	bodyProp := props["body"].(map[string]any)
	require.Equal(t, "object", bodyProp["type"])

	required := schema["required"].([]string)
	require.Contains(t, required, "body")
}

func TestBuildInputSchema_WithFormBody(t *testing.T) {
	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Content: openapi3.Content{
					"application/x-www-form-urlencoded": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{
								Required: []string{"username"},
								Properties: openapi3.Schemas{
									"username": &openapi3.SchemaRef{
										Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
									},
									"password": &openapi3.SchemaRef{
										Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	schema := BuildInputSchema(op)
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, props, "username")
	require.Contains(t, props, "password")

	required := schema["required"].([]string)
	require.Contains(t, required, "username")
}

func TestBuildInputSchema_WithMultipartBody(t *testing.T) {
	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Content: openapi3.Content{
					"multipart/form-data": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{
								Properties: openapi3.Schemas{
									"file": &openapi3.SchemaRef{
										Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	schema := BuildInputSchema(op)
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, props, "file")
}

func TestBuildInputSchema_NoParams(t *testing.T) {
	op := &openapi3.Operation{}
	schema := BuildInputSchema(op)
	require.Equal(t, "object", schema["type"])

	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.Empty(t, props)
}

// --- LoadOpenapiSpec ---

func TestLoadOpenapiSpec(t *testing.T) {
	spec, err := LoadOpenapiSpec(context.Background(), "../mcpsrv/fixtures/petstore_oas.json")
	require.NoError(t, err)
	require.NotNil(t, spec)
	// OpenAPI 3.x のspecはopenapi keyを持つ
	_, hasOpenAPI := spec["openapi"]
	require.True(t, hasOpenAPI)
}

func TestLoadOpenapiSpec_NotFound(t *testing.T) {
	_, err := LoadOpenapiSpec(context.Background(), "nonexistent.json")
	require.Error(t, err)
}

// --- LoadOpenAPI3Spec ---

func TestLoadOpenAPI3Spec(t *testing.T) {
	spec, err := LoadOpenAPI3Spec("../mcpsrv/fixtures/petstore_oas.json")
	require.NoError(t, err)
	require.NotNil(t, spec)
	require.NotEmpty(t, spec.OpenAPI)
}

func TestLoadOpenAPI3Spec_NotFound(t *testing.T) {
	_, err := LoadOpenAPI3Spec("nonexistent.json")
	require.Error(t, err)
}

// --- CreateToolFunction ---

func TestCreateToolFunction_GET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/pets/42", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":42}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi3.Operation{
		Parameters: openapi3.Parameters{
			&openapi3.ParameterRef{
				Value: &openapi3.Parameter{
					Name: "petId",
					In:   "path",
				},
			},
		},
	}

	fn := CreateToolFunction("/pets/{petId}", "get", op, srv.URL, nil)
	result, err := fn(context.Background(), map[string]any{"petId": "42"})
	require.NoError(t, err)
	require.Contains(t, result, "42")
}

func TestCreateToolFunction_POST_JSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":1}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Content: openapi3.Content{
					"application/json": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{
								Properties: openapi3.Schemas{
									"name": &openapi3.SchemaRef{
										Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	fn := CreateToolFunction("/pets", "post", op, srv.URL, nil)
	result, err := fn(context.Background(), map[string]any{
		"body": map[string]any{"name": "Fido"},
	})
	require.NoError(t, err)
	require.Contains(t, result, "1")
}

func TestCreateToolFunction_POST_StringBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`"ok"`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Content: openapi3.Content{
					"application/json": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{},
						},
					},
				},
			},
		},
	}

	fn := CreateToolFunction("/pets", "post", op, srv.URL, nil)
	// body が JSON 文字列として渡される
	result, err := fn(context.Background(), map[string]any{
		"body": `{"name":"Fido"}`,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result)
}

func TestCreateToolFunction_WithQueryParams(t *testing.T) {
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query().Get("status")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[]`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi3.Operation{
		Parameters: openapi3.Parameters{
			&openapi3.ParameterRef{
				Value: &openapi3.Parameter{
					Name: "status",
					In:   "query",
				},
			},
		},
	}

	fn := CreateToolFunction("/pets", "get", op, srv.URL, nil)
	result, err := fn(context.Background(), map[string]any{"status": "available"})
	require.NoError(t, err)
	require.Equal(t, "available", capturedQuery)
	require.NotEmpty(t, result)
}

func TestCreateToolFunction_WithAuthHeader(t *testing.T) {
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi3.Operation{}
	fn := CreateToolFunction("/resource", "get", op, srv.URL, map[string]string{
		"Authorization": "Bearer static-token",
	})

	result, err := fn(context.Background(), map[string]any{})
	require.NoError(t, err)
	require.Equal(t, "Bearer static-token", capturedAuth)
	require.NotEmpty(t, result)
}

func TestCreateToolFunction_AuthOverrideFromContext(t *testing.T) {
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi3.Operation{}
	fn := CreateToolFunction("/resource", "get", op, srv.URL, map[string]string{
		"Authorization": "Bearer static-token",
	})

	ctx := contexts.ToRequestAuthHeader(context.Background(), "Bearer override-token")
	result, err := fn(ctx, map[string]any{})
	require.NoError(t, err)
	// コンテキストのトークンで上書きされる
	require.Equal(t, "Bearer override-token", capturedAuth)
	require.NotEmpty(t, result)
}

func TestCreateToolFunction_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`not found`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi3.Operation{}
	fn := CreateToolFunction("/missing", "get", op, srv.URL, nil)

	// 400以上のステータスはエラーにならず、レスポンスボディをそのまま返す
	result, err := fn(context.Background(), map[string]any{})
	require.NoError(t, err)
	require.Contains(t, result, "not found")
}

func TestCreateToolFunction_InvalidPathParam(t *testing.T) {
	op := &openapi3.Operation{
		Parameters: openapi3.Parameters{
			&openapi3.ParameterRef{
				Value: &openapi3.Parameter{
					Name: "id",
					In:   "path",
				},
			},
		},
	}

	fn := CreateToolFunction("/items/{id}", "get", op, "http://example.com", nil)
	// パスパラメータに "/" を含む場合エラー
	_, err := fn(context.Background(), map[string]any{"id": "a/b"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid path parameter")
}

func TestCreateToolFunction_UnsupportedMethod(t *testing.T) {
	op := &openapi3.Operation{}
	fn := CreateToolFunction("/resource", "UNKNOWN", op, "http://example.com", nil)

	_, err := fn(context.Background(), map[string]any{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported HTTP method")
}

func TestCreateToolFunction_FormURLEncoded(t *testing.T) {
	var capturedContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Content: openapi3.Content{
					"application/x-www-form-urlencoded": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{
								Properties: openapi3.Schemas{
									"username": &openapi3.SchemaRef{
										Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	fn := CreateToolFunction("/login", "post", op, srv.URL, nil)
	result, err := fn(context.Background(), map[string]any{"username": "alice"})
	require.NoError(t, err)
	require.Contains(t, capturedContentType, "application/x-www-form-urlencoded")
	require.NotEmpty(t, result)
}

func TestCreateToolFunction_DELETE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	op := &openapi3.Operation{}
	fn := CreateToolFunction("/resource/1", "delete", op, srv.URL, nil)
	result, err := fn(context.Background(), map[string]any{})
	require.NoError(t, err)
	_ = result
}

// --- describe_schema_fields_openapi (indirectly via BuildInputSchema) ---

func TestBuildInputSchema_JSONBody_NestedObject(t *testing.T) {
	// ネストされたオブジェクトをスキーマに含める
	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Required: true,
				Content: openapi3.Content{
					"application/json": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{
								Required: []string{"address"},
								Properties: openapi3.Schemas{
									"address": &openapi3.SchemaRef{
										Value: &openapi3.Schema{
											Type:        &openapi3.Types{"object"},
											Description: "User address",
											Properties: openapi3.Schemas{
												"street": &openapi3.SchemaRef{
													Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
												},
												"city": &openapi3.SchemaRef{
													Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
												},
											},
										},
									},
									"tags": &openapi3.SchemaRef{
										Value: &openapi3.Schema{
											Type: &openapi3.Types{"array"},
											Items: &openapi3.SchemaRef{
												Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	schema := BuildInputSchema(op)
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, props, "body")

	bodyProp := props["body"].(map[string]any)
	require.Contains(t, bodyProp["description"], "address")
}

func TestBuildInputSchema_JSONBody_ArrayOfObjects(t *testing.T) {
	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Content: openapi3.Content{
					"application/json": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{
								Properties: openapi3.Schemas{
									"items": &openapi3.SchemaRef{
										Value: &openapi3.Schema{
											Type: &openapi3.Types{"array"},
											Items: &openapi3.SchemaRef{
												Value: &openapi3.Schema{
													Properties: openapi3.Schemas{
														"id": &openapi3.SchemaRef{
															Value: &openapi3.Schema{Type: &openapi3.Types{"integer"}},
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	schema := BuildInputSchema(op)
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, props, "body")
}

func TestBuildInputSchema_JSONBody_EmptyBody(t *testing.T) {
	// body が nil の場合
	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Content: openapi3.Content{
					"application/json": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{
								// Properties が空
							},
						},
					},
				},
			},
		},
	}

	schema := BuildInputSchema(op)
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, props, "body")
}

// --- BuildInputSchema / describe_schema_fields_openapi: JSON body の allOf 解決 ---

func TestBuildInputSchema_JSONBody_AllOfRoot(t *testing.T) {
	// body スキーマ自体が allOf 合成の場合、トップレベルの Properties がフラット化されていないと
	// bodyProps が空になってしまう。mergeAllOf でトップレベルを解決してからプロパティを読む。
	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Required: true,
				Content: openapi3.Content{
					"application/json": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{
								AllOf: openapi3.SchemaRefs{
									{Value: &openapi3.Schema{
										Required: []string{"name"},
										Properties: openapi3.Schemas{
											"name": &openapi3.SchemaRef{
												Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
											},
										},
									}},
									{Value: &openapi3.Schema{
										Properties: openapi3.Schemas{
											"age": &openapi3.SchemaRef{
												Value: &openapi3.Schema{Type: &openapi3.Types{"integer"}},
											},
										},
									}},
								},
							},
						},
					},
				},
			},
		},
	}

	schema := BuildInputSchema(op)
	props := schema["properties"].(map[string]any)
	require.Contains(t, props, "body")

	bodyProp := props["body"].(map[string]any)
	bodyProps := bodyProp["properties"].(map[string]any)
	require.Contains(t, bodyProps, "name")
	require.Contains(t, bodyProps, "age")

	nameProp := bodyProps["name"].(map[string]any)
	require.Equal(t, "string", nameProp["type"])
	ageProp := bodyProps["age"].(map[string]any)
	require.Equal(t, "integer", ageProp["type"])

	// build_body_description_openapi (describe_schema_fields_openapi 経由) の description にも
	// マージ後のフィールドが反映される
	require.Contains(t, bodyProp["description"], "name")
	require.Contains(t, bodyProp["description"], "age")
}

func TestBuildInputSchema_JSONBody_PropertyAllOf(t *testing.T) {
	// プロパティの値自体が allOf 合成の場合、type/description を直接読むと空になり
	// type が "string" にフォールバックしてしまう。mergeAllOf で解決してから読む。
	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Content: openapi3.Content{
					"application/json": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{
								Properties: openapi3.Schemas{
									"address": &openapi3.SchemaRef{
										Value: &openapi3.Schema{
											AllOf: openapi3.SchemaRefs{
												{Value: &openapi3.Schema{
													Type:        &openapi3.Types{"object"},
													Description: "Composite address",
													Properties: openapi3.Schemas{
														"street": &openapi3.SchemaRef{
															Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
														},
													},
												}},
												{Value: &openapi3.Schema{
													Properties: openapi3.Schemas{
														"city": &openapi3.SchemaRef{
															Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
														},
													},
												}},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	schema := BuildInputSchema(op)
	props := schema["properties"].(map[string]any)
	bodyProp := props["body"].(map[string]any)
	bodyProps := bodyProp["properties"].(map[string]any)
	require.Contains(t, bodyProps, "address")

	addressProp := bodyProps["address"].(map[string]any)
	// allOf のブランチをマージした結果 type: "object" が読み取れる（"string" にフォールバックしない）
	require.Equal(t, "object", addressProp["type"])
	require.Equal(t, "Composite address", addressProp["description"])

	// describe_schema_fields_openapi 側でも allOf が解決され、ネストしたフィールド（street/city）が
	// 説明文に反映される
	require.Contains(t, bodyProp["description"], "address")
	require.Contains(t, bodyProp["description"], "street")
	require.Contains(t, bodyProp["description"], "city")
}

func TestBuildInputSchema_JSONBody_ArrayItemAllOf_DescribedInBodyDescription(t *testing.T) {
	// 配列の items が allOf 合成の場合も describe_schema_fields_openapi がネストしたフィールドを
	// 説明文に含められる
	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Content: openapi3.Content{
					"application/json": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{
								Properties: openapi3.Schemas{
									"items": &openapi3.SchemaRef{
										Value: &openapi3.Schema{
											Type: &openapi3.Types{"array"},
											Items: &openapi3.SchemaRef{
												Value: &openapi3.Schema{
													AllOf: openapi3.SchemaRefs{
														{Value: &openapi3.Schema{
															Properties: openapi3.Schemas{
																"id": &openapi3.SchemaRef{
																	Value: &openapi3.Schema{Type: &openapi3.Types{"integer"}},
																},
															},
														}},
														{Value: &openapi3.Schema{
															Properties: openapi3.Schemas{
																"label": &openapi3.SchemaRef{
																	Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
																},
															},
														}},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	schema := BuildInputSchema(op)
	props := schema["properties"].(map[string]any)
	bodyProp := props["body"].(map[string]any)

	require.Contains(t, bodyProp["description"], "items")
	require.Contains(t, bodyProp["description"], "id")
	require.Contains(t, bodyProp["description"], "label")
}

// --- describe_schema_fields_openapi（直接呼び出し） ---

func TestDescribeSchemaFieldsOpenapi_TopLevelAllOf(t *testing.T) {
	schema := &openapi3.Schema{
		AllOf: openapi3.SchemaRefs{
			{Value: &openapi3.Schema{
				Required: []string{"name"},
				Properties: openapi3.Schemas{
					"name": &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
				},
			}},
			{Value: &openapi3.Schema{
				Properties: openapi3.Schemas{
					"age": &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"integer"}}},
				},
			}},
		},
	}

	got := describe_schema_fields_openapi(schema)
	require.Contains(t, got, "name (string, required)")
	require.Contains(t, got, "age (integer)")
}

func TestDescribeSchemaFieldsOpenapi_PropertyAllOf(t *testing.T) {
	schema := &openapi3.Schema{
		Properties: openapi3.Schemas{
			"address": &openapi3.SchemaRef{
				Value: &openapi3.Schema{
					AllOf: openapi3.SchemaRefs{
						{Value: &openapi3.Schema{
							Type:        &openapi3.Types{"object"},
							Description: "Composite address",
							Properties: openapi3.Schemas{
								"street": &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
							},
						}},
						{Value: &openapi3.Schema{
							Properties: openapi3.Schemas{
								"city": &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
							},
						}},
					},
				},
			},
		},
	}

	got := describe_schema_fields_openapi(schema)
	require.Contains(t, got, "address (object)")
	require.Contains(t, got, "Composite address")
	require.Contains(t, got, "street")
	require.Contains(t, got, "city")
}

func TestDescribeSchemaFieldsOpenapi_ArrayItemAllOf(t *testing.T) {
	schema := &openapi3.Schema{
		Properties: openapi3.Schemas{
			"items": &openapi3.SchemaRef{
				Value: &openapi3.Schema{
					Type: &openapi3.Types{"array"},
					Items: &openapi3.SchemaRef{
						Value: &openapi3.Schema{
							AllOf: openapi3.SchemaRefs{
								{Value: &openapi3.Schema{
									Properties: openapi3.Schemas{
										"id": &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"integer"}}},
									},
								}},
								{Value: &openapi3.Schema{
									Properties: openapi3.Schemas{
										"label": &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
									},
								}},
							},
						},
					},
				},
			},
		},
	}

	got := describe_schema_fields_openapi(schema)
	require.Contains(t, got, "items (array of object)")
	require.Contains(t, got, "id")
	require.Contains(t, got, "label")
}

func TestCreateToolFunction_BodyAsNonMapString(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`"ok"`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Content: openapi3.Content{
					"application/json": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{},
						},
					},
				},
			},
		},
	}

	fn := CreateToolFunction("/pets", "post", op, srv.URL, nil)
	// body が非JSONの文字列（数値など）の場合
	result, err := fn(context.Background(), map[string]any{
		"body": 42,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result)
}

func TestCreateToolFunction_ExtractParameters_BodyInParam(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	// "body" を In: "body" パラメータとして持つ operation（Swagger スタイル）
	op := &openapi3.Operation{
		Parameters: openapi3.Parameters{
			&openapi3.ParameterRef{
				Value: &openapi3.Parameter{
					Name: "myBody",
					In:   "body",
				},
			},
		},
	}

	fn := CreateToolFunction("/resource", "post", op, srv.URL, nil)
	result, err := fn(context.Background(), map[string]any{
		"myBody": map[string]any{"key": "value"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, result)
}

func TestCreateToolFunction_PATCH(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPatch, r.Method)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"updated":true}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Content: openapi3.Content{
					"application/json": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{},
						},
					},
				},
			},
		},
	}

	fn := CreateToolFunction("/resource/1", "patch", op, srv.URL, nil)
	result, err := fn(context.Background(), map[string]any{
		"body": map[string]any{"name": "new-name"},
	})
	require.NoError(t, err)
	require.Contains(t, result, "updated")
}

// --- multipart/form-data スキーマ構築（ネスト・oneOf・allOf・binary） ---

func multipartOperation(schema *openapi3.Schema) *openapi3.Operation {
	return &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Content: openapi3.Content{
					"multipart/form-data": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{Value: schema},
					},
				},
			},
		},
	}
}

// setFileFetchConfigForTest はテスト用にパッケージレベルの fileFetch 設定を変更し、
// テスト終了時に既定値（ゼロ値 = AllowLocal:false, MaxSize:デフォルト）へ戻す。
// パッケージレベル設定はテスト間でグローバルに共有されるため、他のテストを汚染しないよう
// 必ず t.Cleanup で元に戻すこと。
func setFileFetchConfigForTest(t *testing.T, cfg FileFetchConfig) {
	t.Helper()
	SetFileFetchConfig(cfg)
	t.Cleanup(func() {
		SetFileFetchConfig(FileFetchConfig{})
	})
}

func TestBuildInputSchema_Multipart_BinaryFile(t *testing.T) {
	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"file": &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
			},
		},
	})

	schema := BuildInputSchema(op)
	props := schema["properties"].(map[string]any)
	fileProp := props["file"].(map[string]any)
	meta := fileProp["_meta"].(map[string]any)
	manifold := meta["manifold"].(map[string]any)
	require.Equal(t, true, manifold["file"])
}

func TestBuildInputSchema_Multipart_BinaryFile_MetaMentionsURLOption(t *testing.T) {
	// ファイル入力は description ではなく _meta.manifold.fileInputHint 経由で
	// base64 / URL（署名付きURL等）のどちらでも渡せることを案内する。
	// description は仕様上変更しない（自社内エージェントは _meta を解釈できるが、
	// Claude web や CLI 系エージェントは _meta を利用できないため description を汚さない）。
	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"file": &openapi3.SchemaRef{
				Value: &openapi3.Schema{
					Type:        &openapi3.Types{"string"},
					Format:      "binary",
					Description: "Avatar image",
				},
			},
		},
	})

	schema := BuildInputSchema(op)
	props := schema["properties"].(map[string]any)
	fileProp := props["file"].(map[string]any)

	// 元の説明文はそのまま保持され、案内文は付与されない
	desc, ok := fileProp["description"].(string)
	require.True(t, ok)
	require.Equal(t, "Avatar image", desc)
	require.NotContains(t, desc, "base64")
	require.NotContains(t, desc, "URL")

	// 案内文と file フラグは _meta.manifold に格納される
	meta := fileProp["_meta"].(map[string]any)
	manifold := meta["manifold"].(map[string]any)
	require.Equal(t, true, manifold["file"])
	hint, ok := manifold["fileInputHint"].(string)
	require.True(t, ok)
	require.Contains(t, hint, "base64")
	require.Contains(t, hint, "URL")
}

func TestBuildInputSchema_Multipart_BinaryFile_MetaWithoutOriginalDescription(t *testing.T) {
	// 元の description が空の場合も description は空のままで、案内文は _meta にのみ入る
	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"file": &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
			},
		},
	})

	schema := BuildInputSchema(op)
	props := schema["properties"].(map[string]any)
	fileProp := props["file"].(map[string]any)

	desc, ok := fileProp["description"].(string)
	require.True(t, ok)
	require.Empty(t, desc)

	meta := fileProp["_meta"].(map[string]any)
	manifold := meta["manifold"].(map[string]any)
	require.Equal(t, true, manifold["file"])
	hint, ok := manifold["fileInputHint"].(string)
	require.True(t, ok)
	require.Contains(t, hint, "base64")
	require.Contains(t, hint, "URL")
}

func TestBuildInputSchema_Multipart_ArrayOfBinary_ItemsMetaMentionsURLOption(t *testing.T) {
	// array of binary の場合も items 側の description ではなく _meta に案内文が入る
	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"files": &openapi3.SchemaRef{
				Value: &openapi3.Schema{
					Type: &openapi3.Types{"array"},
					Items: &openapi3.SchemaRef{
						Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
					},
				},
			},
		},
	})

	schema := BuildInputSchema(op)
	props := schema["properties"].(map[string]any)
	filesProp := props["files"].(map[string]any)
	items := filesProp["items"].(map[string]any)

	itemDesc, ok := items["description"].(string)
	require.True(t, ok)
	require.Empty(t, itemDesc)

	itemMeta := items["_meta"].(map[string]any)
	itemManifold := itemMeta["manifold"].(map[string]any)
	require.Equal(t, true, itemManifold["file"])
	hint, ok := itemManifold["fileInputHint"].(string)
	require.True(t, ok)
	require.Contains(t, hint, "base64")
	require.Contains(t, hint, "URL")
}

func TestBuildInputSchema_NonBinaryField_DescriptionUnchanged(t *testing.T) {
	// binary でないフィールドには URL/base64 の案内を追加しない
	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"username": &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Description: "User name"},
			},
		},
	})

	schema := BuildInputSchema(op)
	props := schema["properties"].(map[string]any)
	usernameProp := props["username"].(map[string]any)
	require.Equal(t, "User name", usernameProp["description"])
}

func TestBuildInputSchema_Multipart_NestedForm(t *testing.T) {
	op := multipartOperation(&openapi3.Schema{
		Required: []string{"metadata"},
		Properties: openapi3.Schemas{
			"metadata": &openapi3.SchemaRef{
				Value: &openapi3.Schema{
					Type:     &openapi3.Types{"object"},
					Required: []string{"name"},
					Properties: openapi3.Schemas{
						"name": &openapi3.SchemaRef{
							Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
						},
						"thumbnail": &openapi3.SchemaRef{
							Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
						},
					},
				},
			},
		},
	})

	schema := BuildInputSchema(op)
	props := schema["properties"].(map[string]any)

	metadataProp := props["metadata"].(map[string]any)
	require.Equal(t, "object", metadataProp["type"])

	nested := metadataProp["properties"].(map[string]any)
	require.Contains(t, nested, "name")
	require.Contains(t, nested, "thumbnail")

	nestedRequired := metadataProp["required"].([]string)
	require.Contains(t, nestedRequired, "name")

	thumbnail := nested["thumbnail"].(map[string]any)
	meta := thumbnail["_meta"].(map[string]any)
	manifold := meta["manifold"].(map[string]any)
	require.Equal(t, true, manifold["file"])

	required := schema["required"].([]string)
	require.Contains(t, required, "metadata")
}

func TestBuildInputSchema_Multipart_ArrayOfBinary(t *testing.T) {
	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"files": &openapi3.SchemaRef{
				Value: &openapi3.Schema{
					Type: &openapi3.Types{"array"},
					Items: &openapi3.SchemaRef{
						Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
					},
				},
			},
		},
	})

	schema := BuildInputSchema(op)
	props := schema["properties"].(map[string]any)
	filesProp := props["files"].(map[string]any)
	require.Equal(t, "array", filesProp["type"])

	meta := filesProp["_meta"].(map[string]any)
	manifold := meta["manifold"].(map[string]any)
	require.Equal(t, true, manifold["file"])

	items := filesProp["items"].(map[string]any)
	itemMeta := items["_meta"].(map[string]any)
	itemManifold := itemMeta["manifold"].(map[string]any)
	require.Equal(t, true, itemManifold["file"])
}

func TestBuildInputSchema_Multipart_AllOf(t *testing.T) {
	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"doc": &openapi3.SchemaRef{
				Value: &openapi3.Schema{
					AllOf: openapi3.SchemaRefs{
						{Value: &openapi3.Schema{
							Type:     &openapi3.Types{"object"},
							Required: []string{"a"},
							Properties: openapi3.Schemas{
								"a": &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
							},
						}},
						{Value: &openapi3.Schema{
							Properties: openapi3.Schemas{
								"b": &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"integer"}}},
							},
						}},
					},
				},
			},
		},
	})

	schema := BuildInputSchema(op)
	props := schema["properties"].(map[string]any)
	docProp := props["doc"].(map[string]any)
	require.Equal(t, "object", docProp["type"])

	merged := docProp["properties"].(map[string]any)
	require.Contains(t, merged, "a")
	require.Contains(t, merged, "b")

	mergedRequired := docProp["required"].([]string)
	require.Contains(t, mergedRequired, "a")
}

func TestBuildInputSchema_Multipart_AllOfRoot(t *testing.T) {
	// root スキーマ自体が allOf の場合もプロパティが展開される
	op := multipartOperation(&openapi3.Schema{
		AllOf: openapi3.SchemaRefs{
			{Value: &openapi3.Schema{
				Required: []string{"name"},
				Properties: openapi3.Schemas{
					"name": &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
				},
			}},
			{Value: &openapi3.Schema{
				Properties: openapi3.Schemas{
					"file": &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"}},
				},
			}},
		},
	})

	schema := BuildInputSchema(op)
	props := schema["properties"].(map[string]any)
	require.Contains(t, props, "name")
	require.Contains(t, props, "file")

	required := schema["required"].([]string)
	require.Contains(t, required, "name")
}

func TestBuildInputSchema_Multipart_OneOf(t *testing.T) {
	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"source": &openapi3.SchemaRef{
				Value: &openapi3.Schema{
					OneOf: openapi3.SchemaRefs{
						{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
						{Value: &openapi3.Schema{
							Type: &openapi3.Types{"object"},
							Properties: openapi3.Schemas{
								"url": &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
							},
						}},
					},
				},
			},
		},
	})

	schema := BuildInputSchema(op)
	props := schema["properties"].(map[string]any)
	sourceProp := props["source"].(map[string]any)

	oneOf, ok := sourceProp["oneOf"].([]any)
	require.True(t, ok)
	require.Len(t, oneOf, 2)

	second := oneOf[1].(map[string]any)
	require.Equal(t, "object", second["type"])
	require.Contains(t, second["properties"].(map[string]any), "url")
}

// --- extractParameters（ネスト・allOf・binary 検出） ---

func TestExtractParameters_Multipart_NestedFileAndAllOf(t *testing.T) {
	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"file": &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
			},
			"files": &openapi3.SchemaRef{
				Value: &openapi3.Schema{
					Type: &openapi3.Types{"array"},
					Items: &openapi3.SchemaRef{
						Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
					},
				},
			},
			"metadata": &openapi3.SchemaRef{
				Value: &openapi3.Schema{
					Type: &openapi3.Types{"object"},
					Properties: openapi3.Schemas{
						"thumbnail": &openapi3.SchemaRef{
							Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
						},
					},
				},
			},
			"doc": &openapi3.SchemaRef{
				Value: &openapi3.Schema{
					AllOf: openapi3.SchemaRefs{
						{Value: &openapi3.Schema{
							Properties: openapi3.Schemas{
								"attachment": &openapi3.SchemaRef{
									Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
								},
							},
						}},
					},
				},
			},
		},
	})

	extracted := extractParameters(op)
	require.True(t, extracted.isMultipart)

	require.True(t, extracted.formParams["file"].isFile)
	require.True(t, extracted.formParams["files"].isFile)
	require.False(t, extracted.formParams["metadata"].isFile)
	require.True(t, extracted.formParams["metadata"].parameters["thumbnail"].isFile)
	require.True(t, extracted.formParams["doc"].parameters["attachment"].isFile)
}

// --- CreateToolFunction（multipart データの書き込み） ---

func TestCreateToolFunction_Multipart_FileBase64(t *testing.T) {
	content := []byte("{\"text\":\"hello file\"}")

	var capturedFilename string
	var capturedContent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(10<<20))
		f, hdr, err := r.FormFile("file")
		require.NoError(t, err)
		defer f.Close() //nolint: errcheck
		capturedFilename = hdr.Filename
		capturedContent, err = io.ReadAll(f)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"file": &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
			},
		},
	})

	fn := CreateToolFunction("/upload", "post", op, srv.URL, nil)
	_, err := fn(context.Background(), map[string]any{
		"file": base64.StdEncoding.EncodeToString(content),
	})
	require.NoError(t, err)
	require.Equal(t, "file.json", capturedFilename)
	require.Equal(t, content, capturedContent)
}

func TestCreateToolFunction_Multipart_FileObjectValue(t *testing.T) {
	content := []byte("%PDF-1.7 dummy")

	var capturedFilename string
	var capturedContentType string
	var capturedContent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(10<<20))
		f, hdr, err := r.FormFile("file")
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

	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"file": &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
			},
		},
	})

	fn := CreateToolFunction("/upload", "post", op, srv.URL, nil)
	_, err := fn(context.Background(), map[string]any{
		"file": map[string]any{
			"filename":    "report.pdf",
			"contentType": "application/pdf",
			"content":     base64.StdEncoding.EncodeToString(content),
		},
	})
	require.NoError(t, err)
	require.Equal(t, "report.pdf", capturedFilename)
	require.Equal(t, "application/pdf", capturedContentType)
	require.Equal(t, content, capturedContent)
}

func TestCreateToolFunction_Multipart_FileRawString(t *testing.T) {
	// base64 として解釈できない文字列はそのままのバイト列として送信される
	raw := "plain text ###"

	var capturedContent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(10<<20))
		f, _, err := r.FormFile("file")
		require.NoError(t, err)
		defer f.Close() //nolint: errcheck
		capturedContent, err = io.ReadAll(f)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"file": &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
			},
		},
	})

	fn := CreateToolFunction("/upload", "post", op, srv.URL, nil)
	_, err := fn(context.Background(), map[string]any{"file": raw})
	require.NoError(t, err)
	require.Equal(t, []byte(raw), capturedContent)
}

func TestCreateToolFunction_Multipart_MultipleFiles(t *testing.T) {
	var capturedCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(10<<20))
		capturedCount = len(r.MultipartForm.File["files"])
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"files": &openapi3.SchemaRef{
				Value: &openapi3.Schema{
					Type: &openapi3.Types{"array"},
					Items: &openapi3.SchemaRef{
						Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
					},
				},
			},
		},
	})

	fn := CreateToolFunction("/upload", "post", op, srv.URL, nil)
	_, err := fn(context.Background(), map[string]any{
		"files": []any{
			base64.StdEncoding.EncodeToString([]byte("one")),
			base64.StdEncoding.EncodeToString([]byte("two")),
		},
	})
	require.NoError(t, err)
	require.Equal(t, 2, capturedCount)
}

func TestCreateToolFunction_Multipart_NestedObjectAsJSON(t *testing.T) {
	var capturedMetadata string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(10<<20))
		capturedMetadata = r.FormValue("metadata")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"metadata": &openapi3.SchemaRef{
				Value: &openapi3.Schema{
					Type: &openapi3.Types{"object"},
					Properties: openapi3.Schemas{
						"name": &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
					},
				},
			},
		},
	})

	fn := CreateToolFunction("/upload", "post", op, srv.URL, nil)
	_, err := fn(context.Background(), map[string]any{
		"metadata": map[string]any{"name": "foo", "tags": []any{"a", "b"}},
	})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(capturedMetadata), &decoded))
	require.Equal(t, "foo", decoded["name"])
}

func TestCreateToolFunction_FormURLEncoded_ComplexValue(t *testing.T) {
	var capturedFilters string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		capturedFilters = r.FormValue("filters")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Content: openapi3.Content{
					"application/x-www-form-urlencoded": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{
								Properties: openapi3.Schemas{
									"filters": &openapi3.SchemaRef{
										Value: &openapi3.Schema{Type: &openapi3.Types{"object"}},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	fn := CreateToolFunction("/search", "post", op, srv.URL, nil)
	_, err := fn(context.Background(), map[string]any{
		"filters": map[string]any{"status": "active"},
	})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(capturedFilters), &decoded))
	require.Equal(t, "active", decoded["status"])
}

func TestCreateToolFunction_Multipart_FileFromURL(t *testing.T) {
	// 署名付きURLのようにURLが渡された場合はダウンロードしてストリーム書き込みする。
	// テスト用のダウンロード先は httptest のループバック http サーバーなので AllowLocal を有効化する。
	setFileFetchConfigForTest(t, FileFetchConfig{AllowLocal: true})
	content := []byte("streamed file body")
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
		f, hdr, err := r.FormFile("file")
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

	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"file": &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
			},
		},
	})

	fn := CreateToolFunction("/upload", "post", op, srv.URL, nil)
	// 署名付きURL相当（クエリ付き）
	_, err := fn(context.Background(), map[string]any{
		"file": fileSrv.URL + "/files/report.pdf?X-Signature=abc123&Expires=9999999999",
	})
	require.NoError(t, err)
	// filename はURLパスの末尾（クエリは含まない）
	require.Equal(t, "report.pdf", capturedFilename)
	require.Equal(t, "application/pdf", capturedContentType)
	require.Equal(t, content, capturedContent)
}

func TestCreateToolFunction_Multipart_FileFromURL_ObjectValueOverrides(t *testing.T) {
	// オブジェクト形式で content にURLを渡した場合、filename / contentType の明示指定が優先される
	setFileFetchConfigForTest(t, FileFetchConfig{AllowLocal: true})
	content := []byte("object url content")
	fileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write(content) //nolint: errcheck
	}))
	defer fileSrv.Close()

	var capturedFilename string
	var capturedContentType string
	var capturedContent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(10<<20))
		f, hdr, err := r.FormFile("file")
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

	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"file": &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
			},
		},
	})

	fn := CreateToolFunction("/upload", "post", op, srv.URL, nil)
	_, err := fn(context.Background(), map[string]any{
		"file": map[string]any{
			"filename":    "renamed.bin",
			"contentType": "application/pdf",
			"content":     fileSrv.URL + "/download",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "renamed.bin", capturedFilename)
	require.Equal(t, "application/pdf", capturedContentType)
	require.Equal(t, content, capturedContent)
}

func TestCreateToolFunction_Multipart_FileFromURL_HTTPError(t *testing.T) {
	// ダウンロード先が4xx/5xxを返した場合はツールエラーになる
	setFileFetchConfigForTest(t, FileFetchConfig{AllowLocal: true})
	fileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer fileSrv.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"file": &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
			},
		},
	})

	fn := CreateToolFunction("/upload", "post", op, srv.URL, nil)
	_, err := fn(context.Background(), map[string]any{
		"file": fileSrv.URL + "/files/secret.pdf",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "file")
}

// --- fileFetch SSRF 対策（AllowLocal / AllowedHosts / MaxSize） ---

func TestCreateToolFunction_Multipart_FileFromURL_AllowLocalFalse_RejectsHTTP(t *testing.T) {
	// 既定（AllowLocal=false）では http:// URL（httptest のループバックサーバーも含む）を拒否する
	setFileFetchConfigForTest(t, FileFetchConfig{})

	fileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secret")) //nolint: errcheck
	}))
	defer fileSrv.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"file": &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
			},
		},
	})

	fn := CreateToolFunction("/upload", "post", op, srv.URL, nil)
	_, err := fn(context.Background(), map[string]any{
		"file": fileSrv.URL + "/secret.txt",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "http://")
	require.Contains(t, err.Error(), "not allowed")
}

func TestFetchFileFromURL_AllowLocalFalse_RejectsPrivateIP(t *testing.T) {
	// https:// でもプライベート/ループバックIPへの接続は SafeHTTPClient の dialer が拒否する
	// （スキームチェックとは独立した経路であることの確認）
	setFileFetchConfigForTest(t, FileFetchConfig{})

	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secret")) //nolint: errcheck
	}))
	defer tlsSrv.Close()

	_, _, _, err := fetchFileFromURL(context.Background(), tlsSrv.URL+"/secret.txt")
	require.Error(t, err)
	require.Contains(t, err.Error(), "private")
}

func TestFetchFileFromURL_AllowedHosts_AllowsMatchingHost(t *testing.T) {
	fileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok")) //nolint: errcheck
	}))
	defer fileSrv.Close()

	fileSrvURL, err := url.Parse(fileSrv.URL)
	require.NoError(t, err)

	setFileFetchConfigForTest(t, FileFetchConfig{AllowLocal: true, AllowedHosts: []string{fileSrvURL.Host}})

	body, _, _, err := fetchFileFromURL(context.Background(), fileSrv.URL+"/f")
	require.NoError(t, err)
	defer body.Close() //nolint: errcheck
}

func TestFetchFileFromURL_AllowedHosts_DeniesNonMatchingHost(t *testing.T) {
	fileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok")) //nolint: errcheck
	}))
	defer fileSrv.Close()

	setFileFetchConfigForTest(t, FileFetchConfig{AllowLocal: true, AllowedHosts: []string{"example.com"}})

	_, _, _, err := fetchFileFromURL(context.Background(), fileSrv.URL+"/f")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not in the allowed hosts list")
}

func TestFetchFileFromURL_AllowedHosts_DeniesRedirectTarget(t *testing.T) {
	// AllowedHosts はリダイレクト元だけでなく各ホップのリダイレクト先ホストにも適用される
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("target content")) //nolint: errcheck
	}))
	defer targetSrv.Close()

	redirectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetSrv.URL+"/final", http.StatusFound)
	}))
	defer redirectSrv.Close()

	redirectSrvURL, err := url.Parse(redirectSrv.URL)
	require.NoError(t, err)

	// リダイレクト元のホストのみを許可し、リダイレクト先（targetSrv）は許可リストに含めない
	setFileFetchConfigForTest(t, FileFetchConfig{AllowLocal: true, AllowedHosts: []string{redirectSrvURL.Host}})

	_, _, _, err = fetchFileFromURL(context.Background(), redirectSrv.URL+"/start")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not in the allowed hosts list")
}

func TestFetchFileFromURL_MaxSize_ContentLengthExceeded(t *testing.T) {
	// Content-Length が既知の場合はボディを読む前に即座にエラーになる
	fileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, 100)) //nolint: errcheck
	}))
	defer fileSrv.Close()

	setFileFetchConfigForTest(t, FileFetchConfig{AllowLocal: true, MaxSize: 10})

	_, _, _, err := fetchFileFromURL(context.Background(), fileSrv.URL+"/f")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds the maximum allowed size of 10 bytes")
}

func TestFetchFileFromURL_MaxSize_StreamingExceeded(t *testing.T) {
	// Content-Length が不明（chunked）な応答でも、ストリーミング中にサイズ上限が強制される
	fileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for range 5 {
			w.Write(make([]byte, 10)) //nolint: errcheck
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer fileSrv.Close()

	setFileFetchConfigForTest(t, FileFetchConfig{AllowLocal: true, MaxSize: 10})

	body, _, _, err := fetchFileFromURL(context.Background(), fileSrv.URL+"/f")
	// Content-Length 不明なのでヘッダー到達時点ではまだエラーにならない
	require.NoError(t, err)
	defer body.Close() //nolint: errcheck

	_, err = io.ReadAll(body)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds the maximum allowed size")
}

func TestCreateToolFunction_Multipart_FileExplicitURLKey(t *testing.T) {
	// {url: "..."} を明示指定すると fetchFileFromURL 経由で取得する
	setFileFetchConfigForTest(t, FileFetchConfig{AllowLocal: true})
	content := []byte("via explicit url key")
	fileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content) //nolint: errcheck
	}))
	defer fileSrv.Close()

	var capturedContent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(10<<20))
		f, _, err := r.FormFile("file")
		require.NoError(t, err)
		defer f.Close() //nolint: errcheck
		capturedContent, err = io.ReadAll(f)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"file": &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
			},
		},
	})

	fn := CreateToolFunction("/upload", "post", op, srv.URL, nil)
	_, err := fn(context.Background(), map[string]any{
		"file": map[string]any{"url": fileSrv.URL + "/f"},
	})
	require.NoError(t, err)
	require.Equal(t, content, capturedContent)
}

func TestCreateToolFunction_Multipart_FileExplicitURLKey_RejectsNonHTTPValue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"file": &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
			},
		},
	})

	fn := CreateToolFunction("/upload", "post", op, srv.URL, nil)
	_, err := fn(context.Background(), map[string]any{
		"file": map[string]any{"url": "not-a-url"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "url must start with http:// or https://")
}

func TestCreateToolFunction_Multipart_FileExplicitBase64Key(t *testing.T) {
	// {base64: "..."} を明示指定すると必ず base64 としてデコードされる
	content := []byte("via explicit base64 key")
	var capturedContent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(10<<20))
		f, _, err := r.FormFile("file")
		require.NoError(t, err)
		defer f.Close() //nolint: errcheck
		capturedContent, err = io.ReadAll(f)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"file": &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
			},
		},
	})

	fn := CreateToolFunction("/upload", "post", op, srv.URL, nil)
	_, err := fn(context.Background(), map[string]any{
		"file": map[string]any{"base64": base64.StdEncoding.EncodeToString(content)},
	})
	require.NoError(t, err)
	require.Equal(t, content, capturedContent)
}

func TestCreateToolFunction_Multipart_FileExplicitBase64Key_InvalidBase64Errors(t *testing.T) {
	// base64 キーを明示指定した場合、不正な base64 は raw フォールバックせずエラーになる
	// （content/生文字列の場合の従来ヒューリスティックとは異なる挙動）
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"file": &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
			},
		},
	})

	fn := CreateToolFunction("/upload", "post", op, srv.URL, nil)
	_, err := fn(context.Background(), map[string]any{
		"file": map[string]any{"base64": "not valid base64!!!"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid base64")
}

func TestCreateToolFunction_Multipart_FileExplicitBase64Key_MaxSizeExceeded(t *testing.T) {
	setFileFetchConfigForTest(t, FileFetchConfig{MaxSize: 5})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"file": &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
			},
		},
	})

	fn := CreateToolFunction("/upload", "post", op, srv.URL, nil)
	_, err := fn(context.Background(), map[string]any{
		"file": map[string]any{"base64": base64.StdEncoding.EncodeToString([]byte("this is definitely more than five bytes"))},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds the maximum allowed size of 5 bytes")
}

func TestCreateToolFunction_Multipart_FileExplicitTextKey(t *testing.T) {
	// {text: "..."} はデコードせずそのまま bytes として使用される
	text := "raw text content, not decoded as base64"
	var capturedContent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(10<<20))
		f, _, err := r.FormFile("file")
		require.NoError(t, err)
		defer f.Close() //nolint: errcheck
		capturedContent, err = io.ReadAll(f)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"file": &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
			},
		},
	})

	fn := CreateToolFunction("/upload", "post", op, srv.URL, nil)
	_, err := fn(context.Background(), map[string]any{
		"file": map[string]any{"text": text},
	})
	require.NoError(t, err)
	require.Equal(t, []byte(text), capturedContent)
}

func TestCreateToolFunction_Multipart_FileContentLegacy_MaxSizeExceeded(t *testing.T) {
	// content キー（従来の生文字列ヒューリスティック）経路でもサイズ上限が強制される
	setFileFetchConfigForTest(t, FileFetchConfig{MaxSize: 5})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"file": &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
			},
		},
	})

	fn := CreateToolFunction("/upload", "post", op, srv.URL, nil)
	_, err := fn(context.Background(), map[string]any{
		"file": "this raw string is definitely longer than five bytes",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds the maximum allowed size of 5 bytes")
}

func TestCreateToolFunction_Multipart_FileExplicitKeyPriority_URLWinsOverBase64(t *testing.T) {
	// url / base64 / text / content が同時に指定された場合、url が最優先で使われ他は無視される
	setFileFetchConfigForTest(t, FileFetchConfig{AllowLocal: true})
	urlContent := []byte("from url")
	fileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(urlContent) //nolint: errcheck
	}))
	defer fileSrv.Close()

	var capturedContent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(10<<20))
		f, _, err := r.FormFile("file")
		require.NoError(t, err)
		defer f.Close() //nolint: errcheck
		capturedContent, err = io.ReadAll(f)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := multipartOperation(&openapi3.Schema{
		Properties: openapi3.Schemas{
			"file": &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Format: "binary"},
			},
		},
	})

	fn := CreateToolFunction("/upload", "post", op, srv.URL, nil)
	_, err := fn(context.Background(), map[string]any{
		"file": map[string]any{
			"url":    fileSrv.URL + "/f",
			"base64": base64.StdEncoding.EncodeToString([]byte("from base64, should be ignored")),
			"text":   "from text, should be ignored",
		},
	})
	require.NoError(t, err)
	require.Equal(t, urlContent, capturedContent)
}

func TestDecodeFileContent_MaxSize(t *testing.T) {
	// raw テキスト（base64 として解釈できない）は入力文字列長で判定する
	_, err := decodeFileContent("hello world", 5)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds the maximum allowed size of 5 bytes")

	data, err := decodeFileContent("hi", 5)
	require.NoError(t, err)
	require.Equal(t, []byte("hi"), data)

	// base64 は元データの約 4/3 倍に膨らむため、エンコード後の文字列長が上限を超えていても
	// デコード後のサイズが上限内であれば受理されなければならない（上限ぎりぎりのファイルを
	// エンコード前サイズで誤って拒否しないこと）
	encoded := base64.StdEncoding.EncodeToString([]byte("abcdef")) // デコード後 6 bytes、エンコード後 8 文字
	require.Greater(t, len(encoded), 6)
	data, err = decodeFileContent(encoded, 6)
	require.NoError(t, err)
	require.Equal(t, []byte("abcdef"), data)

	// デコード後のサイズが上限を超えていれば、エンコード後の文字列長が粗い上限（4/3倍+4）の
	// 範囲内であってもデコード後サイズで正しく拒否される
	_, err = decodeFileContent(encoded, 5)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds the maximum allowed size of 5 bytes")

	// 極端に長い入力は、base64 デコードを試みる前に粗い上限（4/3倍+4）で弾かれる
	// （巨大な異常入力に対する無駄なデコード用アロケーションの回避）
	hugeEncoded := base64.StdEncoding.EncodeToString(make([]byte, 1000))
	_, err = decodeFileContent(hugeEncoded, 5)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds the maximum allowed size of 5 bytes")
}

func TestIsHostAllowed(t *testing.T) {
	u, err := url.Parse("https://example.com:8443/path")
	require.NoError(t, err)
	require.True(t, isHostAllowed([]string{"example.com"}, u))
	require.True(t, isHostAllowed([]string{"example.com:8443"}, u))
	require.False(t, isHostAllowed([]string{"other.com"}, u))
}

// --- 循環参照スキーマ（自己参照・相互参照）でのスタックオーバーフロー防止 ---
// kin-openapi の Loader は $ref をポインタ循環として解決するため、自己参照/相互参照スキーマが
// 実際に発生しうる（例: node が children: array of node を持つ、A⇄B の相互参照）。
// テストでは Loader を介さず、同じ状況を素朴な Go ポインタ共有で再現する。

// selfReferentialNodeSchema は node.properties.children が array of node（node 自身への
// 自己参照）であるスキーマを返す。
func selfReferentialNodeSchema() *openapi3.Schema {
	node := &openapi3.Schema{
		Type:       &openapi3.Types{"object"},
		Properties: openapi3.Schemas{},
	}
	node.Properties["name"] = &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}}
	node.Properties["children"] = &openapi3.SchemaRef{
		Value: &openapi3.Schema{
			Type:  &openapi3.Types{"array"},
			Items: &openapi3.SchemaRef{Value: node},
		},
	}
	return node
}

// mutuallyReferentialSchemas は a.properties.b == b、b.properties.a == a という
// A→B→A の相互参照スキーマの組を返す。
func mutuallyReferentialSchemas() (a, b *openapi3.Schema) {
	a = &openapi3.Schema{Type: &openapi3.Types{"object"}, Properties: openapi3.Schemas{}}
	b = &openapi3.Schema{Type: &openapi3.Types{"object"}, Properties: openapi3.Schemas{}}
	a.Properties["name"] = &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}}
	a.Properties["b"] = &openapi3.SchemaRef{Value: b}
	b.Properties["label"] = &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}}
	b.Properties["a"] = &openapi3.SchemaRef{Value: a}
	return a, b
}

func TestBuildFormPropertySchema_SelfReferentialSchema(t *testing.T) {
	node := selfReferentialNodeSchema()

	var result map[string]any
	require.NotPanics(t, func() {
		result = buildFormPropertySchema(node)
	})
	require.Equal(t, "object", result["type"])
	props := result["properties"].(map[string]any)
	require.Contains(t, props, "name")
	require.Contains(t, props, "children")
}

func TestBuildFormPropertySchema_MutuallyReferentialSchema(t *testing.T) {
	a, _ := mutuallyReferentialSchemas()

	var result map[string]any
	require.NotPanics(t, func() {
		result = buildFormPropertySchema(a)
	})
	require.Equal(t, "object", result["type"])
	props := result["properties"].(map[string]any)
	require.Contains(t, props, "name")
	require.Contains(t, props, "b")
}

func TestDescribeSchemaFieldsOpenapi_SelfReferentialSchema(t *testing.T) {
	node := selfReferentialNodeSchema()

	var result string
	require.NotPanics(t, func() {
		result = describe_schema_fields_openapi(node)
	})
	require.Contains(t, result, "name")
	require.Contains(t, result, "children")
}

func TestDescribeSchemaFieldsOpenapi_MutuallyReferentialSchema(t *testing.T) {
	a, _ := mutuallyReferentialSchemas()

	var result string
	require.NotPanics(t, func() {
		result = describe_schema_fields_openapi(a)
	})
	require.Contains(t, result, "name")
	require.Contains(t, result, "b")
}

func TestNewFormParameter_SelfReferentialSchema(t *testing.T) {
	node := selfReferentialNodeSchema()

	var fp formParameter
	require.NotPanics(t, func() {
		fp = newFormParameter(node)
	})
	require.Contains(t, fp.parameters, "name")
	require.Contains(t, fp.parameters, "children")
}

func TestNewFormParameter_MutuallyReferentialSchema(t *testing.T) {
	a, _ := mutuallyReferentialSchemas()

	var fp formParameter
	require.NotPanics(t, func() {
		fp = newFormParameter(a)
	})
	require.Contains(t, fp.parameters, "name")
	require.Contains(t, fp.parameters, "b")
}

func TestBuildInputSchema_SelfReferentialSchema_ViaMultipart(t *testing.T) {
	node := selfReferentialNodeSchema()
	op := multipartOperation(node)

	var schema map[string]any
	require.NotPanics(t, func() {
		schema = BuildInputSchema(op)
	})
	props := schema["properties"].(map[string]any)
	require.Contains(t, props, "name")
	require.Contains(t, props, "children")
}

func TestBuildInputSchema_MutuallyReferentialSchema_ViaMultipart(t *testing.T) {
	a, _ := mutuallyReferentialSchemas()
	op := multipartOperation(a)

	var schema map[string]any
	require.NotPanics(t, func() {
		schema = BuildInputSchema(op)
	})
	props := schema["properties"].(map[string]any)
	require.Contains(t, props, "name")
	require.Contains(t, props, "b")
}

func TestBuildInputSchema_SelfReferentialSchema_ViaJSONBody(t *testing.T) {
	node := selfReferentialNodeSchema()
	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Content: openapi3.Content{
					"application/json": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{Value: node},
					},
				},
			},
		},
	}

	var schema map[string]any
	require.NotPanics(t, func() {
		schema = BuildInputSchema(op)
	})
	props := schema["properties"].(map[string]any)
	require.Contains(t, props, "body")
	bodyProp := props["body"].(map[string]any)
	require.Contains(t, bodyProp["description"], "name")
	require.Contains(t, bodyProp["description"], "children")
}

func TestBuildInputSchema_MutuallyReferentialSchema_ViaJSONBody(t *testing.T) {
	a, _ := mutuallyReferentialSchemas()
	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Content: openapi3.Content{
					"application/json": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{Value: a},
					},
				},
			},
		},
	}

	var schema map[string]any
	require.NotPanics(t, func() {
		schema = BuildInputSchema(op)
	})
	props := schema["properties"].(map[string]any)
	require.Contains(t, props, "body")
	bodyProp := props["body"].(map[string]any)
	require.Contains(t, bodyProp["description"], "name")
	require.Contains(t, bodyProp["description"], "b")
}

func TestMergeAllOf_SelfReferentialAllOf(t *testing.T) {
	self := &openapi3.Schema{
		Type:       &openapi3.Types{"object"},
		Properties: openapi3.Schemas{"a": &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}}},
	}
	self.AllOf = openapi3.SchemaRefs{{Value: self}}

	var merged *openapi3.Schema
	require.NotPanics(t, func() {
		merged = mergeAllOf(self)
	})
	require.Contains(t, merged.Properties, "a")
}

func TestMergeAllOf_MutuallyReferentialAllOf(t *testing.T) {
	a := &openapi3.Schema{Properties: openapi3.Schemas{"x": &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}}}}
	b := &openapi3.Schema{Properties: openapi3.Schemas{"y": &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}}}}
	a.AllOf = openapi3.SchemaRefs{{Value: b}}
	b.AllOf = openapi3.SchemaRefs{{Value: a}}

	var merged *openapi3.Schema
	require.NotPanics(t, func() {
		merged = mergeAllOf(a)
	})
	require.Contains(t, merged.Properties, "x")
	require.Contains(t, merged.Properties, "y")
}

// --- sanitizeParamName ---
// MCP の InputSchema プロパティ名は "[" "]" を許容しないため、
// OpenAPI の配列/ネスト形式クエリパラメータ名（例: "tag[]", "filter[status]"）を
// 安全な名前に変換する必要がある。

func TestSanitizeParamName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"no brackets", "status", "status"},
		{"trailing array brackets", "tag[]", "tag_"},
		{"nested key brackets", "filter[status]", "filter_status"},
		{"multiple bracket groups", "a[b][c]", "a_b_c"},
		{"empty string", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, sanitizeParamName(tt.in))
		})
	}
}

// --- BuildInputSchema: パラメータ名の [] サニタイズ ---

func TestBuildInputSchema_QueryParamWithBrackets_IsSanitized(t *testing.T) {
	op := &openapi3.Operation{
		Parameters: openapi3.Parameters{
			&openapi3.ParameterRef{
				Value: &openapi3.Parameter{
					Name: "tag[]",
					In:   "query",
					Schema: &openapi3.SchemaRef{
						Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
					},
				},
			},
		},
	}

	schema := BuildInputSchema(op)
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, props, "tag[]")
	require.Contains(t, props, "tag_")
}

func TestBuildInputSchema_FormParamWithBrackets_IsSanitized(t *testing.T) {
	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Content: openapi3.Content{
					"application/x-www-form-urlencoded": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{
								Required: []string{"filter[status]"},
								Properties: openapi3.Schemas{
									"filter[status]": &openapi3.SchemaRef{
										Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	schema := BuildInputSchema(op)
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, props, "filter[status]")
	require.Contains(t, props, "filter_status")

	required := schema["required"].([]string)
	require.NotContains(t, required, "filter[status]")
	require.Contains(t, required, "filter_status")
}

// --- CreateToolFunction: サニタイズ後の名前 -> 実リクエストでは元の名前を使用 ---

func TestCreateToolFunction_QueryParamWithBrackets_UsesOriginalNameOnWire(t *testing.T) {
	var capturedRawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedRawQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[]`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi3.Operation{
		Parameters: openapi3.Parameters{
			&openapi3.ParameterRef{
				Value: &openapi3.Parameter{
					Name: "tag[]",
					In:   "query",
				},
			},
		},
	}

	fn := CreateToolFunction("/pets", "get", op, srv.URL, nil)
	result, err := fn(context.Background(), map[string]any{"tag_": "cute"})
	require.NoError(t, err)
	require.NotEmpty(t, result)

	q, err := url.ParseQuery(capturedRawQuery)
	require.NoError(t, err)
	require.Equal(t, "cute", q.Get("tag[]"))
}

func TestCreateToolFunction_PathParamWithBrackets_UsesOriginalNameOnWire(t *testing.T) {
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi3.Operation{
		Parameters: openapi3.Parameters{
			&openapi3.ParameterRef{
				Value: &openapi3.Parameter{
					Name: "filter[id]",
					In:   "path",
				},
			},
		},
	}

	fn := CreateToolFunction("/pets/{filter[id]}", "get", op, srv.URL, nil)
	result, err := fn(context.Background(), map[string]any{"filter_id": "42"})
	require.NoError(t, err)
	require.NotEmpty(t, result)
	require.Equal(t, "/pets/42", capturedPath)
}

func TestCreateToolFunction_FormURLEncodedParamWithBrackets_UsesOriginalNameOnWire(t *testing.T) {
	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body) //nolint: errcheck
		capturedBody = string(b)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Content: openapi3.Content{
					"application/x-www-form-urlencoded": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{
								Properties: openapi3.Schemas{
									"tag[]": &openapi3.SchemaRef{
										Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	fn := CreateToolFunction("/pets", "post", op, srv.URL, nil)
	result, err := fn(context.Background(), map[string]any{"tag_": "cute"})
	require.NoError(t, err)
	require.NotEmpty(t, result)

	q, err := url.ParseQuery(capturedBody)
	require.NoError(t, err)
	require.Equal(t, "cute", q.Get("tag[]"))
}

func TestCreateToolFunction_MultipartParamWithBrackets_UsesOriginalNameOnWire(t *testing.T) {
	var capturedFieldNames []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(10<<20))
		for name := range r.MultipartForm.Value {
			capturedFieldNames = append(capturedFieldNames, name)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint: errcheck
	}))
	defer srv.Close()

	op := &openapi3.Operation{
		RequestBody: &openapi3.RequestBodyRef{
			Value: &openapi3.RequestBody{
				Content: openapi3.Content{
					"multipart/form-data": &openapi3.MediaType{
						Schema: &openapi3.SchemaRef{
							Value: &openapi3.Schema{
								Properties: openapi3.Schemas{
									"tag[]": &openapi3.SchemaRef{
										Value: &openapi3.Schema{Type: &openapi3.Types{"string"}},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	fn := CreateToolFunction("/pets", "post", op, srv.URL, nil)
	result, err := fn(context.Background(), map[string]any{"tag_": "cute"})
	require.NoError(t, err)
	require.NotEmpty(t, result)
	require.Contains(t, capturedFieldNames, "tag[]")
}
