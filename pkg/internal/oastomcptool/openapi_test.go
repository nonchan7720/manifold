package oastomcptool

import (
	"context"
	"net/http"
	"net/http/httptest"
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
			got := deriveBaseUrlFromSpecPath(tt.specPath)
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
	got := GetBaseUrl(spec, "")
	require.Equal(t, "https://api.example.com", got)
}

func TestGetBaseUrl_OpenAPI3_RelativeServer(t *testing.T) {
	spec := map[string]any{
		"servers": []any{
			map[string]any{"url": "/api/v1"},
		},
	}
	got := GetBaseUrl(spec, "https://example.com/openapi.json")
	require.Equal(t, "https://example.com/api/v1", got)
}

func TestGetBaseUrl_Swagger2_WithHost(t *testing.T) {
	spec := map[string]any{
		"host":     "api.example.com",
		"schemes":  []any{"http"},
		"basePath": "/v2",
	}
	got := GetBaseUrl(spec, "")
	require.Equal(t, "http://api.example.com/v2", got)
}

func TestGetBaseUrl_Swagger2_DefaultScheme(t *testing.T) {
	spec := map[string]any{
		"host":     "api.example.com",
		"basePath": "/v1",
	}
	got := GetBaseUrl(spec, "")
	require.Equal(t, "https://api.example.com/v1", got)
}

func TestGetBaseUrl_Fallback(t *testing.T) {
	got := GetBaseUrl(map[string]any{}, "https://example.com/openapi.json")
	require.Equal(t, "https://example.com", got)
}

// --- GetBaseUrlFromOpenAPI3 ---

func TestGetBaseUrlFromOpenAPI3_WithServer(t *testing.T) {
	spec := &openapi3.T{
		Servers: openapi3.Servers{
			{URL: "https://api.example.com/v2"},
		},
	}
	got := GetBaseUrlFromOpenAPI3(spec, "")
	require.Equal(t, "https://api.example.com/v2", got)
}

func TestGetBaseUrlFromOpenAPI3_RelativeServer(t *testing.T) {
	spec := &openapi3.T{
		Servers: openapi3.Servers{
			{URL: "/api/v1"},
		},
	}
	got := GetBaseUrlFromOpenAPI3(spec, "https://example.com/openapi.json")
	require.Equal(t, "https://example.com/api/v1", got)
}

func TestGetBaseUrlFromOpenAPI3_NoServers(t *testing.T) {
	spec := &openapi3.T{}
	got := GetBaseUrlFromOpenAPI3(spec, "https://example.com/openapi.json")
	require.Equal(t, "https://example.com", got)
}

func TestGetBaseUrlFromOpenAPI3_NoServersNoPath(t *testing.T) {
	spec := &openapi3.T{}
	got := GetBaseUrlFromOpenAPI3(spec, "")
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
