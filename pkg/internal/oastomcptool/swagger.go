package oastomcptool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/nonchan7720/manifold/pkg/internal/api"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
)

// LoadSwaggerSpec loads a Swagger 2.x spec from a file path or URL.
func LoadSwaggerSpec(specPath string) (*openapi2.T, error) {
	data, err := FetchSpecBytes(specPath)
	if err != nil {
		return nil, err
	}
	// Some real-world Swagger specs use "required": false/true on property schemas,
	// which is invalid for openapi2.Schema (expects []string). Normalize before parsing.
	data, err = normalizeSwaggerJSON(data)
	if err != nil {
		return nil, err
	}
	var spec openapi2.T
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}
	return &spec, nil
}

// normalizeSwaggerJSON removes boolean "required" fields from schema objects.
// Parameter objects (identified by having an "in" key) are left untouched.
func normalizeSwaggerJSON(data []byte) ([]byte, error) {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	removeBoolRequiredFromSchemas(raw)
	return json.Marshal(raw)
}

// removeBoolRequiredFromSchemas walks the JSON tree and removes "required": bool
// from any map that is not a parameter object (parameter objects have an "in" key).
func removeBoolRequiredFromSchemas(v any) {
	switch m := v.(type) {
	case map[string]any:
		// Parameter objects always have "in". Schema objects do not.
		if _, hasIn := m["in"]; !hasIn {
			if _, isBool := m["required"].(bool); isBool {
				delete(m, "required")
			}
		}
		for _, val := range m {
			removeBoolRequiredFromSchemas(val)
		}
	case []any:
		for _, elem := range m {
			removeBoolRequiredFromSchemas(elem)
		}
	}
}

// GetBaseUrlFromSwagger extracts base URL from a Swagger 2.x typed spec.
func GetBaseUrlFromSwagger(spec *openapi2.T, specPath string) string {
	if spec.Host != "" {
		scheme := "https"
		if len(spec.Schemes) > 0 {
			scheme = spec.Schemes[0]
		}
		return fmt.Sprintf("%s://%s%s", scheme, spec.Host, spec.BasePath)
	}
	return deriveBaseUrlFromSpecPath(specPath)
}

// resolveSwaggerParamRef resolves a $ref parameter to the concrete *openapi2.Parameter.
// In Swagger 2.x, refs look like "#/parameters/Name" and resolve against spec.Parameters.
func resolveSwaggerParamRef(p *openapi2.Parameter, spec *openapi2.T) *openapi2.Parameter {
	if p.Ref == "" {
		return p
	}
	name := strings.TrimPrefix(p.Ref, "#/parameters/")
	if resolved, ok := spec.Parameters[name]; ok {
		return resolved
	}
	return nil
}

// resolveSwaggerSchemaRef resolves a $ref SchemaRef to the concrete *openapi2.Schema.
// In Swagger 2.x, refs look like "#/definitions/Name" and resolve against spec.Definitions.
func resolveSwaggerSchemaRef(ref *openapi2.SchemaRef, spec *openapi2.T) *openapi2.Schema {
	if ref == nil {
		return nil
	}
	if ref.Ref == "" {
		return ref.Value
	}
	name := strings.TrimPrefix(ref.Ref, "#/definitions/")
	if resolved, ok := spec.Definitions[name]; ok {
		return resolved.Value
	}
	return nil
}

// mergeSwaggerParams merges path-level and operation-level parameters.
// Operation-level parameters win when the same (in, name) combination appears in both.
func mergeSwaggerParams(operation *openapi2.Operation, pathItemParams []*openapi2.Parameter, spec *openapi2.T) []*openapi2.Parameter {
	type paramKey struct{ in, name string }
	opKeys := map[paramKey]bool{}
	for _, p := range operation.Parameters {
		r := resolveSwaggerParamRef(p, spec)
		if r != nil {
			opKeys[paramKey{r.In, r.Name}] = true
		}
	}

	var result []*openapi2.Parameter
	for _, p := range pathItemParams {
		r := resolveSwaggerParamRef(p, spec)
		if r == nil {
			continue
		}
		if !opKeys[paramKey{r.In, r.Name}] {
			result = append(result, r)
		}
	}
	for _, p := range operation.Parameters {
		r := resolveSwaggerParamRef(p, spec)
		if r != nil {
			result = append(result, r)
		}
	}
	return result
}

// extract_parameters_swagger extracts parameter names from a Swagger 2.x operation.
// Path-level parameters are merged with operation-level (operation wins on conflict).
func extract_parameters_swagger(operation *openapi2.Operation, pathItemParams []*openapi2.Parameter, spec *openapi2.T) (path_params, query_params, body_params, form_params []string, is_multipart bool) {
	path_params = []string{}
	query_params = []string{}
	body_params = []string{}
	form_params = []string{}
	is_multipart = false

	merged := mergeSwaggerParams(operation, pathItemParams, spec)
	for _, p := range merged {
		switch p.In {
		case "path":
			path_params = append(path_params, p.Name)
		case "query":
			query_params = append(query_params, p.Name)
		case "body":
			body_params = append(body_params, p.Name)
		case "formData":
			form_params = append(form_params, p.Name)
			// type: file requires multipart/form-data
			if p.Type != nil && p.Type.Is("file") {
				is_multipart = true
			}
		}
	}
	return
}

// describe_schema_fields_swagger recursively builds a human-readable field summary from a
// Swagger 2.x schema. $refs are resolved via spec.Definitions.
func describe_schema_fields_swagger(schema *openapi2.Schema, spec *openapi2.T) string {
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
		prop := resolveSwaggerSchemaRef(propRef, spec)
		if prop == nil {
			continue
		}

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
			if nested := describe_schema_fields_swagger(prop, spec); nested != "" {
				parts = append(parts, fmt.Sprintf("%s (%s)%s -> {%s}", name, meta, fieldDesc, nested))
				continue
			}
		}

		if typ == "array" && prop.Items != nil {
			itemSchema := resolveSwaggerSchemaRef(prop.Items, spec)
			if itemSchema != nil {
				itemType := schemaTypeStr(itemSchema.Type)
				if itemType == "" {
					itemType = "object"
				}
				if itemType == "object" {
					if nested := describe_schema_fields_swagger(itemSchema, spec); nested != "" {
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
		}

		parts = append(parts, fmt.Sprintf("%s (%s)%s", name, meta, fieldDesc))
	}

	return strings.Join(parts, "; ")
}

// build_body_description_swagger constructs a detailed description for a Swagger 2.x body parameter.
func build_body_description_swagger(base_desc string, schema *openapi2.Schema, spec *openapi2.T) string {
	if base_desc == "" {
		base_desc = "Request body"
	}
	fields := describe_schema_fields_swagger(schema, spec)
	if fields == "" {
		return base_desc + ". Pass a JSON object."
	}
	return fmt.Sprintf("%s. JSON object with fields: {%s}", base_desc, fields)
}

// BuildInputSchemaSwagger builds MCP input schema from a Swagger 2.x operation.
func BuildInputSchemaSwagger(operation *openapi2.Operation, pathItemParams []*openapi2.Parameter, spec *openapi2.T) map[string]any {
	properties := map[string]any{}
	required := []string{}

	merged := mergeSwaggerParams(operation, pathItemParams, spec)
	for _, p := range merged {
		if in := p.In; in == "body" {
			// Body parameter: "schema" holds the full object definition.
			var bodySchema *openapi2.Schema
			if p.Schema != nil {
				bodySchema = resolveSwaggerSchemaRef(p.Schema, spec)
			}
			bodyProps := map[string]any{}
			desc := p.Description
			if bodySchema != nil {
				for propName, propRef := range bodySchema.Properties {
					prop := resolveSwaggerSchemaRef(propRef, spec)
					if prop == nil {
						continue
					}
					propType := schemaTypeStr(prop.Type)
					if propType == "" {
						propType = "string"
					}
					bodyProps[propName] = map[string]any{
						"type":        propType,
						"description": prop.Description,
					}
				}
				properties[p.Name] = map[string]any{
					"type":        "object",
					"description": build_body_description_swagger(desc, bodySchema, spec),
					"properties":  bodyProps,
				}
			} else {
				properties[p.Name] = map[string]any{
					"type":        "object",
					"description": desc,
					"properties":  bodyProps,
				}
			}
		} else {
			// Non-body: "type" is directly on the parameter.
			paramType := schemaTypeStr(p.Type)
			if paramType == "" {
				paramType = "string"
			}
			properties[p.Name] = map[string]any{
				"type":        paramType,
				"description": p.Description,
			}
		}
		if p.Required {
			required = append(required, p.Name)
		}
	}

	return map[string]any{
		"type":       "object",
		"properties": properties,
		"required":   required,
	}
}

// CreateToolFunctionSwagger creates a tool function for a Swagger 2.x operation.
func CreateToolFunctionSwagger(
	path string,
	method string,
	operation *openapi2.Operation,
	pathItemParams []*openapi2.Parameter,
	spec *openapi2.T,
	base_url string,
	headers map[string]string,
) ToolFunc {
	if headers == nil {
		headers = map[string]string{}
	}

	path_params, query_params, body_params, form_params, is_multipart := extract_parameters_swagger(operation, pathItemParams, spec)
	original_method := strings.ToLower(method)

	tool_function := func(ctx context.Context, input map[string]any) (string, error) {
		effective_headers := map[string]string{}
		maps.Copy(effective_headers, headers)
		override_auth := contexts.FromRequestAuthHeader(ctx)
		if override_auth != "" {
			effective_headers["Authorization"] = override_auth
		}

		_url := base_url + path

		for _, param_name := range path_params {
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
		for _, param_name := range query_params {
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

		if len(form_params) > 0 {
			if is_multipart {
				var buf bytes.Buffer
				writer := multipart.NewWriter(&buf)
				for _, param_name := range form_params {
					if v := input[param_name]; v != nil && fmt.Sprintf("%v", v) != "" {
						if err := writer.WriteField(param_name, fmt.Sprintf("%v", v)); err != nil {
							return "", fmt.Errorf("error writing multipart field %s: %w", param_name, err)
						}
					}
				}
				writer.Close() // nolint: errcheck
				bodyBytes = buf.Bytes()
				bodyContentType = writer.FormDataContentType()
			} else {
				formValues := url.Values{}
				for _, param_name := range form_params {
					if v := input[param_name]; v != nil && fmt.Sprintf("%v", v) != "" {
						formValues.Set(param_name, fmt.Sprintf("%v", v))
					}
				}
				bodyBytes = []byte(formValues.Encode())
				bodyContentType = "application/x-www-form-urlencoded"
			}
		} else if len(body_params) > 0 {
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
				for _, param_name := range body_params {
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
		defer response.Body.Close() // nolint: errcheck

		respBody, err := io.ReadAll(response.Body)
		if err != nil {
			return "", fmt.Errorf("error reading response: %w", err)
		}
		// 400 以上はエラーとして返す
		if response.StatusCode >= 400 {
			if len(respBody) == 0 {
				respBody = []byte(http.StatusText(response.StatusCode))
			}
			return "", fmt.Errorf("%s", respBody)
		}
		return string(respBody), nil
	}

	return tool_function
}
