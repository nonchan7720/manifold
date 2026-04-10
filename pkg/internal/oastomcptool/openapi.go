package oastomcptool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/nonchan7720/manifold/pkg/internal/api"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
)

type ToolFunc func(ctx context.Context, input map[string]any) (string, error)

// MCPToolRegistry defines the interface for the global MCP tool registry
type MCPToolRegistry interface {
	RegisterTool(name, description string, input_schema map[string]any, handler func(context.Context, map[string]any) (string, error))
}

// getHttpClient returns an HTTP client (simulates litellm's getHttpClient)
func getHttpClient() *http.Client {
	return &http.Client{}
}

func sanitize_path_parameter_value(param_value any, param_name string) (string, error) {
	if param_value == nil {
		return "", nil
	}

	value_str := fmt.Sprintf("%v", param_value)
	if value_str == "" {
		return "", nil
	}

	normalized_value := strings.ReplaceAll(value_str, "\\", "/")
	if strings.Contains(normalized_value, "/") {
		return "", fmt.Errorf("path parameter '%s' must not contain path separators", param_name)
	}

	// Simulates: any(part in {".", ".."} for part in PurePosixPath(normalized_value).parts)
	for part := range strings.SplitSeq(normalized_value, "/") {
		if part == "." || part == ".." {
			return "", fmt.Errorf("path parameter '%s' cannot include '.' or '..' segments", param_name)
		}
	}

	return url.PathEscape(value_str), nil
}

// FetchSpecBytes fetches spec bytes from a file path or URL.
func FetchSpecBytes(ctx context.Context, specPath string) ([]byte, error) {
	if strings.HasPrefix(specPath, "http://") || strings.HasPrefix(specPath, "https://") {
		client := getHttpClient()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, specPath, nil)
		if err != nil {
			return nil, err
		}
		r, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer r.Body.Close() //nolint: errcheck
		if r.StatusCode >= 400 {
			return nil, fmt.Errorf("HTTP error: %d %s", r.StatusCode, r.Status)
		}
		return io.ReadAll(r.Body)
	}
	if _, err := os.Stat(specPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("OpenAPI spec not found at %s", specPath)
	}
	return os.ReadFile(specPath) //nolint: gosec
}

// LoadOpenapiSpec loads spec as raw map (Deprecated: use LoadOpenAPI3Spec or LoadSwaggerSpec).
func LoadOpenapiSpec(ctx context.Context, filepath string) (map[string]any, error) {
	data, err := FetchSpecBytes(ctx, filepath)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// LoadOpenAPI3Spec loads an OpenAPI 3.x spec with automatic $ref resolution.
func LoadOpenAPI3Spec(specPath string) (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	if strings.HasPrefix(specPath, "http://") || strings.HasPrefix(specPath, "https://") {
		u, err := url.Parse(specPath)
		if err != nil {
			return nil, err
		}
		return loader.LoadFromURI(u)
	}
	return loader.LoadFromFile(specPath)
}

// GetBaseUrl extracts base URL from raw OpenAPI spec map.
func GetBaseUrl(spec map[string]any, spec_path string) string {
	// OpenAPI 3.x
	if servers, ok := spec["servers"].([]any); ok && len(servers) > 0 { //nolint: nestif
		if server, ok := servers[0].(map[string]any); ok {
			if server_url, ok := server["url"].(string); ok {
				if strings.HasPrefix(server_url, "/") && spec_path != "" {
					if strings.HasPrefix(spec_path, "http://") || strings.HasPrefix(spec_path, "https://") {
						parsed, err := url.Parse(spec_path)
						if err == nil {
							base_domain := fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
							full_base_url := base_domain + server_url
							slog.Info(fmt.Sprintf(
								"OpenAPI spec has relative server URL '%s'. Deriving base from spec_path: %s",
								server_url, full_base_url,
							))
							return full_base_url
						}
					}
				}
				return server_url
			}
		}
	}
	// OpenAPI 2.x (Swagger)
	if host, ok := spec["host"].(string); ok {
		scheme := "https"
		if schemes, ok := spec["schemes"].([]any); ok && len(schemes) > 0 {
			if s, ok := schemes[0].(string); ok {
				scheme = s
			}
		}
		base_path := ""
		if bp, ok := spec["basePath"].(string); ok {
			base_path = bp
		}
		return fmt.Sprintf("%s://%s%s", scheme, host, base_path)
	}
	return deriveBaseUrlFromSpecPath(spec_path)
}

// GetBaseUrlFromOpenAPI3 extracts base URL from an OpenAPI 3.x typed spec.
func GetBaseUrlFromOpenAPI3(spec *openapi3.T, specPath string) string {
	if len(spec.Servers) > 0 { //nolint: nestif
		serverURL := spec.Servers[0].URL
		if strings.HasPrefix(serverURL, "/") && specPath != "" {
			if strings.HasPrefix(specPath, "http://") || strings.HasPrefix(specPath, "https://") {
				parsed, err := url.Parse(specPath)
				if err == nil {
					baseDomain := fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
					fullBaseURL := baseDomain + serverURL
					slog.Info(fmt.Sprintf(
						"OpenAPI spec has relative server URL '%s'. Deriving base from spec_path: %s",
						serverURL, fullBaseURL,
					))
					return fullBaseURL
				}
			}
		}
		return serverURL
	}
	return deriveBaseUrlFromSpecPath(specPath)
}

func deriveBaseUrlFromSpecPath(spec_path string) string {
	if spec_path == "" {
		return ""
	}
	if !strings.HasPrefix(spec_path, "http://") && !strings.HasPrefix(spec_path, "https://") {
		return ""
	}
	for _, suffix := range []string{"/openapi.json", "/openapi.yaml", "/swagger.json", "/swagger.yaml"} {
		if strings.HasSuffix(spec_path, suffix) {
			base_url := spec_path[:len(spec_path)-len(suffix)]
			slog.Info(fmt.Sprintf("No server info in OpenAPI spec. Using derived base URL: %s", base_url))
			return base_url
		}
	}
	parts := strings.Split(spec_path, "/")
	last := parts[len(parts)-1]
	if strings.HasSuffix(last, ".json") || strings.HasSuffix(last, ".yaml") || strings.HasSuffix(last, ".yml") {
		base_url := strings.Join(parts[:len(parts)-1], "/")
		slog.Info(fmt.Sprintf("No server info in OpenAPI spec. Using derived base URL: %s", base_url))
		return base_url
	}
	return ""
}

// schemaTypeStr returns the first type string from an openapi3.Types (which is []string).
func schemaTypeStr(t *openapi3.Types) string {
	if t == nil || len(*t) == 0 {
		return ""
	}
	return (*t)[0]
}

type extractParameter struct {
	pathParams  []string
	queryParams []string
	bodyParams  []string
	formParams  []string
	isMultipart bool
}

// extractParameters は、OpenAPI 3.x のオペレーションからパラメータ名を取り出す。
func extractParameters(operation *openapi3.Operation) extractParameter { //nolint: gocyclo
	var (
		pathParams  = []string{}
		queryParams = []string{}
		bodyParams  = []string{}
		formParams  = []string{}
		isMultipart = false
	)

	for _, paramRef := range operation.Parameters {
		p := paramRef.Value
		switch p.In {
		case "path":
			pathParams = append(pathParams, p.Name)
		case "query":
			queryParams = append(queryParams, p.Name)
		case "body":
			bodyParams = append(bodyParams, p.Name)
		}
	}

	if operation.RequestBody != nil && operation.RequestBody.Value != nil {
		content := operation.RequestBody.Value.Content
		switch {
		case content["application/json"] != nil:
			bodyParams = append(bodyParams, "body")
		case content["application/x-www-form-urlencoded"] != nil:
			mt := content["application/x-www-form-urlencoded"]
			if mt.Schema != nil && mt.Schema.Value != nil {
				for name := range mt.Schema.Value.Properties {
					formParams = append(formParams, name)
				}
			}
		case content["multipart/form-data"] != nil:
			isMultipart = true
			mt := content["multipart/form-data"]
			if mt.Schema != nil && mt.Schema.Value != nil {
				for name := range mt.Schema.Value.Properties {
					formParams = append(formParams, name)
				}
			}
		}
	}
	return extractParameter{
		pathParams:  pathParams,
		queryParams: queryParams,
		bodyParams:  bodyParams,
		formParams:  formParams,
		isMultipart: isMultipart,
	}
}

// describe_schema_fields_openapi recursively builds a human-readable field summary from an
// OpenAPI 3.x schema. Since Loader auto-resolves $refs, propRef.Value is always populated.
func describe_schema_fields_openapi(schema *openapi3.Schema) string { //nolint: gocyclo
	bodyProps := schema.Properties
	if len(bodyProps) == 0 {
		return ""
	}

	localRequired := map[string]bool{}
	for _, r := range schema.Required {
		localRequired[r] = true
	}

	// Sort field names for deterministic output
	fieldNames := make([]string, 0, len(bodyProps))
	for name := range bodyProps {
		fieldNames = append(fieldNames, name)
	}
	for i := 1; i < len(fieldNames); i++ {
		for j := i; j > 0 && fieldNames[j] < fieldNames[j-1]; j-- {
			fieldNames[j], fieldNames[j-1] = fieldNames[j-1], fieldNames[j]
		}
	}

	parts := make([]string, 0, len(fieldNames))
	for _, name := range fieldNames {
		propRef := bodyProps[name]
		if propRef == nil || propRef.Value == nil {
			continue
		}
		prop := propRef.Value

		typ := schemaTypeStr(prop.Type)
		if typ == "" {
			if len(prop.Properties) > 0 {
				typ = "object"
			} else {
				typ = "string"
			}
		}
		meta := typ
		if localRequired[name] {
			meta += ", required"
		}
		fieldDesc := ""
		if prop.Description != "" {
			fieldDesc = ": " + prop.Description
		}

		if typ == "object" {
			if nested := describe_schema_fields_openapi(prop); nested != "" {
				parts = append(parts, fmt.Sprintf("%s (%s)%s -> {%s}", name, meta, fieldDesc, nested))
				continue
			}
		}

		if typ == "array" && prop.Items != nil && prop.Items.Value != nil { //nolint: nestif
			itemSchema := prop.Items.Value
			itemType := schemaTypeStr(itemSchema.Type)
			if itemType == "" {
				itemType = "object"
			}
			if itemType == "object" {
				if nested := describe_schema_fields_openapi(itemSchema); nested != "" {
					arrayMeta := "array of object"
					if localRequired[name] {
						arrayMeta += ", required"
					}
					parts = append(parts, fmt.Sprintf("%s (%s)%s -> [{%s}]", name, arrayMeta, fieldDesc, nested))
					continue
				}
			}
			meta = "array of " + itemType
			if localRequired[name] {
				meta += ", required"
			}
		}

		parts = append(parts, fmt.Sprintf("%s (%s)%s", name, meta, fieldDesc))
	}

	return strings.Join(parts, "; ")
}

// build_body_description_openapi constructs a detailed description for an OpenAPI 3.x requestBody.
func build_body_description_openapi(base_desc string, schema *openapi3.Schema) string {
	if base_desc == "" {
		base_desc = "Request body"
	}
	fields := describe_schema_fields_openapi(schema)
	if fields == "" {
		return base_desc + ". Pass a JSON object."
	}
	return fmt.Sprintf("%s. JSON object with fields: {%s}", base_desc, fields)
}

// BuildInputSchema builds MCP input schema from an OpenAPI 3.x operation.
func BuildInputSchema(operation *openapi3.Operation) map[string]any { //nolint: gocyclo
	properties := map[string]any{}
	required := []string{}

	for _, paramRef := range operation.Parameters {
		p := paramRef.Value
		paramType := "string"
		if p.Schema != nil && p.Schema.Value != nil {
			if t := schemaTypeStr(p.Schema.Value.Type); t != "" {
				paramType = t
			}
		}
		properties[p.Name] = map[string]any{
			"type":        paramType,
			"description": p.Description,
		}
		if p.Required {
			required = append(required, p.Name)
		}
	}

	if operation.RequestBody != nil && operation.RequestBody.Value != nil { //nolint: nestif
		rb := operation.RequestBody.Value
		baseDesc := rb.Description
		if baseDesc == "" {
			baseDesc = "Request body"
		}
		content := rb.Content

		if mt := content["application/json"]; mt != nil && mt.Schema != nil && mt.Schema.Value != nil {
			schema := mt.Schema.Value
			bodyProps := map[string]any{}
			for propName, propRef := range schema.Properties {
				if propRef == nil || propRef.Value == nil {
					continue
				}
				prop := propRef.Value
				propType := "string"
				if t := schemaTypeStr(prop.Type); t != "" {
					propType = t
				}
				bodyProps[propName] = map[string]any{
					"type":        propType,
					"description": prop.Description,
				}
			}
			properties["body"] = map[string]any{
				"type":        "object",
				"description": build_body_description_openapi(baseDesc, schema),
				"properties":  bodyProps,
			}
			if rb.Required {
				required = append(required, "body")
			}
		} else {
			// Form content types: each schema property becomes a top-level input field.
			var formSchema *openapi3.Schema
			if mt := content["application/x-www-form-urlencoded"]; mt != nil && mt.Schema != nil && mt.Schema.Value != nil {
				formSchema = mt.Schema.Value
			} else if mt := content["multipart/form-data"]; mt != nil && mt.Schema != nil && mt.Schema.Value != nil {
				formSchema = mt.Schema.Value
			}
			if formSchema != nil {
				schemaRequired := map[string]bool{}
				for _, r := range formSchema.Required {
					schemaRequired[r] = true
				}
				for propName, propRef := range formSchema.Properties {
					if propRef == nil || propRef.Value == nil {
						continue
					}
					prop := propRef.Value
					propType := "string"
					if t := schemaTypeStr(prop.Type); t != "" {
						propType = t
					}
					properties[propName] = map[string]any{
						"type":        propType,
						"description": prop.Description,
					}
					if schemaRequired[propName] {
						required = append(required, propName)
					}
				}
			}
		}
	}

	return map[string]any{
		"type":       "object",
		"properties": properties,
		"required":   required,
	}
}

// CreateToolFunction creates a tool function for an OpenAPI 3.x operation.
func CreateToolFunction( //nolint: gocyclo
	path string,
	method string,
	operation *openapi3.Operation,
	base_url string,
	headers map[string]string,
) ToolFunc {
	if headers == nil {
		headers = map[string]string{}
	}

	extractParameter := extractParameters(operation)
	original_method := strings.ToLower(method)

	tool_function := func(ctx context.Context, input map[string]any) (string, error) {
		effective_headers := map[string]string{}
		maps.Copy(effective_headers, headers)
		override_auth := contexts.FromRequestAuthHeader(ctx)
		if override_auth != "" {
			effective_headers["Authorization"] = override_auth
		}

		_url := base_url + path

		for _, param_name := range extractParameter.pathParams {
			param_value := input[param_name]
			if param_value != nil && param_value != "" {
				safe_value, err := sanitize_path_parameter_value(param_value, param_name)
				if err != nil {
					return "", fmt.Errorf("invalid path parameter: %w", err)
				}
				_url = strings.ReplaceAll(_url, "{"+param_name+"}", safe_value)
				_url = strings.ReplaceAll(_url, "{{"+param_name+"}}", safe_value)
			}
		}

		params := map[string]any{}
		for _, param_name := range extractParameter.queryParams {
			param_value := input[param_name]
			if param_value != nil && param_value != "" {
				params[param_name] = param_value
			}
		}

		client := getHttpClient()

		parsedURL, err := url.Parse(_url)
		if err != nil {
			return "", fmt.Errorf("error parsing URL: %w", err)
		}
		q := parsedURL.Query()
		for k, v := range params {
			q.Set(k, fmt.Sprintf("%v", v))
		}
		parsedURL.RawQuery = q.Encode()
		finalURL := parsedURL.String()

		var bodyBytes []byte
		var bodyContentType string

		if len(extractParameter.formParams) > 0 { //nolint: nestif
			if extractParameter.isMultipart {
				var buf bytes.Buffer
				writer := multipart.NewWriter(&buf)
				for _, param_name := range extractParameter.formParams {
					if v := input[param_name]; v != nil && fmt.Sprintf("%v", v) != "" {
						if err := writer.WriteField(param_name, fmt.Sprintf("%v", v)); err != nil {
							return "", fmt.Errorf("error writing multipart field %s: %w", param_name, err)
						}
					}
				}
				writer.Close() //nolint: errcheck
				bodyBytes = buf.Bytes()
				bodyContentType = writer.FormDataContentType()
			} else {
				formValues := url.Values{}
				for _, param_name := range extractParameter.formParams {
					if v := input[param_name]; v != nil && fmt.Sprintf("%v", v) != "" {
						formValues.Set(param_name, fmt.Sprintf("%v", v))
					}
				}
				bodyBytes = []byte(formValues.Encode())
				bodyContentType = "application/x-www-form-urlencoded"
			}
		} else if len(extractParameter.bodyParams) > 0 {
			isEmpty := func(v any) bool {
				if v == nil {
					return true
				}
				if m, ok := v.(map[string]any); ok && len(m) == 0 {
					return true
				}
				if s, ok := v.(string); ok && s == "" {
					return true
				}
				return false
			}

			body_value := input["body"]
			if isEmpty(body_value) {
				for _, param_name := range extractParameter.bodyParams {
					bv := input[param_name]
					if !isEmpty(bv) {
						body_value = bv
						break
					}
				}
			}

			var json_body map[string]any
			if bv, ok := body_value.(map[string]any); ok {
				json_body = bv
			} else if !isEmpty(body_value) {
				if s, ok := body_value.(string); ok {
					if err := json.Unmarshal([]byte(s), &json_body); err != nil {
						json_body = map[string]any{"data": body_value}
					}
				} else {
					json_body = map[string]any{"data": body_value}
				}
			}

			if json_body != nil {
				bodyBytes, err = json.Marshal(json_body)
				if err != nil {
					return "", fmt.Errorf("error marshaling request body: %w", err)
				}
				bodyContentType = "application/json"
			}
		}

		var response *http.Response
		switch original_method {
		case "get":
			response, err = api.DoRequest(ctx, client, finalURL, "get", false, bodyBytes, bodyContentType, effective_headers)
		case "post":
			response, err = api.DoRequest(ctx, client, finalURL, "post", true, bodyBytes, bodyContentType, effective_headers)
		case "put":
			response, err = api.DoRequest(ctx, client, finalURL, "put", true, bodyBytes, bodyContentType, effective_headers)
		case "delete":
			response, err = api.DoRequest(ctx, client, finalURL, "delete", false, bodyBytes, bodyContentType, effective_headers)
		case "patch":
			response, err = api.DoRequest(ctx, client, finalURL, "patch", true, bodyBytes, bodyContentType, effective_headers)
		default:
			return "", fmt.Errorf("unsupported HTTP method: %s", original_method)
		}

		if err != nil {
			return "", fmt.Errorf("error making request: %w", err)
		}
		defer response.Body.Close() //nolint: errcheck

		respBody, err := io.ReadAll(response.Body)
		if err != nil {
			return "", fmt.Errorf("error reading response: %w", err)
		}
		// 400 以上はエラーとして返す
		if response.StatusCode >= 400 {
			if len(respBody) == 0 {
				respBody = []byte(http.StatusText(response.StatusCode))
			}
		}
		return string(respBody), nil
	}

	return tool_function
}
