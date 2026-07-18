package oastomcptool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/internal/api"
	"github.com/nonchan7720/manifold/pkg/internal/client"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
)

type ToolFunc func(ctx context.Context, input map[string]any) (string, error)

// MCPToolRegistry defines the interface for the global MCP tool registry
type MCPToolRegistry interface {
	RegisterTool(name, description string, input_schema map[string]any, handler func(context.Context, map[string]any) (string, error))
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
func FetchSpecBytes(ctx context.Context, specPath string) (_ []byte, rErr error) {
	ctx = trace.StartSpan(ctx, "oastomcptool/FetchSpecBytes")
	defer func() { trace.EndSpan(ctx, rErr) }()

	if strings.HasPrefix(specPath, "http://") || strings.HasPrefix(specPath, "https://") {
		client := client.HTTPClient()
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
	loader.ReadFromURIFunc = openapi3.URIMapCache(openapi3.ReadFromURIs(openapi3.ReadFromHTTP(client.HTTPClient()), openapi3.ReadFromFile))
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
func GetBaseUrl(ctx context.Context, spec map[string]any, spec_path string) string {
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
							slog.InfoContext(ctx, fmt.Sprintf(
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
	return deriveBaseUrlFromSpecPath(ctx, spec_path)
}

// GetBaseUrlFromOpenAPI3 extracts base URL from an OpenAPI 3.x typed spec.
func GetBaseUrlFromOpenAPI3(ctx context.Context, spec *openapi3.T, specPath string) string {
	if len(spec.Servers) > 0 { //nolint: nestif
		serverURL := spec.Servers[0].URL
		if strings.HasPrefix(serverURL, "/") && specPath != "" {
			if strings.HasPrefix(specPath, "http://") || strings.HasPrefix(specPath, "https://") {
				parsed, err := url.Parse(specPath)
				if err == nil {
					baseDomain := fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
					fullBaseURL := baseDomain + serverURL
					slog.InfoContext(ctx, fmt.Sprintf(
						"OpenAPI spec has relative server URL '%s'. Deriving base from spec_path: %s",
						serverURL, fullBaseURL,
					))
					return fullBaseURL
				}
			}
		}
		return serverURL
	}
	return deriveBaseUrlFromSpecPath(ctx, specPath)
}

func deriveBaseUrlFromSpecPath(ctx context.Context, spec_path string) string {
	if spec_path == "" {
		return ""
	}
	if !strings.HasPrefix(spec_path, "http://") && !strings.HasPrefix(spec_path, "https://") {
		return ""
	}
	for _, suffix := range []string{"/openapi.json", "/openapi.yaml", "/swagger.json", "/swagger.yaml"} {
		if strings.HasSuffix(spec_path, suffix) {
			base_url := spec_path[:len(spec_path)-len(suffix)]
			slog.InfoContext(ctx, fmt.Sprintf("No server info in OpenAPI spec. Using derived base URL: %s", base_url))
			return base_url
		}
	}
	parts := strings.Split(spec_path, "/")
	last := parts[len(parts)-1]
	if strings.HasSuffix(last, ".json") || strings.HasSuffix(last, ".yaml") || strings.HasSuffix(last, ".yml") {
		base_url := strings.Join(parts[:len(parts)-1], "/")
		slog.InfoContext(ctx, fmt.Sprintf("No server info in OpenAPI spec. Using derived base URL: %s", base_url))
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

type formParameter struct {
	isFile       bool
	originalName string
	parameters   map[string]formParameter
}

type formParameters map[string]formParameter

type extractParameter struct {
	pathParams   []string
	queryParams  []string
	bodyParams   []string
	formParams   formParameters
	isMultipart  bool
	paramNameMap map[string]string // sanitized name -> original OpenAPI name
}

// sanitizeParamName converts an OpenAPI parameter/property name into one that is
// safe to use as an MCP input schema property name. MCP does not allow "[" or "]"
// in property names, which commonly appear in array/nested query or form parameter
// names (e.g. "tag[]", "filter[status]").
func sanitizeParamName(name string) string {
	if !strings.ContainsAny(name, "[]") {
		return name
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch r {
		case '[':
			b.WriteByte('_')
		case ']':
			// drop
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// schemaIsBinary は format: binary（または binary 要素の array）かどうかを返す。
func schemaIsBinary(schema *openapi3.Schema) bool {
	if schema == nil {
		return false
	}
	if schema.Format == "binary" {
		return true
	}
	if schemaTypeStr(schema.Type) == "array" && schema.Items != nil && schema.Items.Value != nil {
		if schema.Items.Value.Format == "binary" {
			return true
		}
	}
	return false
}

// newFormParameter はフォームスキーマから formParameter を再帰的に構築する。
func newFormParameter(schema *openapi3.Schema) formParameter {
	return newFormParameterVisited(schema, map[*openapi3.Schema]bool{})
}

// newFormParameterVisited は newFormParameter の実体。visited は現在の再帰パス上にあるスキーマ
// ポインタの集合で、自己参照・相互参照スキーマ（kin-openapi の Loader は $ref をポインタ循環と
// して解決するため実際に発生しうる）による無限再帰を防ぐ。同じスキーマへの再訪を検知した場合は
// 空の formParameter を返す（それ以上再帰しない）。
func newFormParameterVisited(schema *openapi3.Schema, visited map[*openapi3.Schema]bool) formParameter { //nolint: gocyclo
	fp := formParameter{parameters: formParameters{}}
	if schema == nil || visited[schema] {
		return fp
	}
	visited[schema] = true
	defer delete(visited, schema)

	fp.isFile = schemaIsBinary(schema)

	for _, ref := range schema.AllOf {
		if ref == nil || ref.Value == nil {
			continue
		}
		branch := newFormParameterVisited(ref.Value, visited)
		maps.Copy(fp.parameters, branch.parameters)
		if branch.isFile {
			fp.isFile = true
		}
	}
	for _, ref := range schema.OneOf {
		if ref == nil || ref.Value == nil {
			continue
		}
		branch := newFormParameterVisited(ref.Value, visited)
		maps.Copy(fp.parameters, branch.parameters)
		if branch.isFile {
			fp.isFile = true
		}
	}
	for _, ref := range schema.AnyOf {
		if ref == nil || ref.Value == nil {
			continue
		}
		branch := newFormParameterVisited(ref.Value, visited)
		maps.Copy(fp.parameters, branch.parameters)
		if branch.isFile {
			fp.isFile = true
		}
	}

	for name, field := range schema.Properties {
		if field != nil {
			child := newFormParameterVisited(field.Value, visited)
			child.originalName = name
			fp.parameters[sanitizeParamName(name)] = child
		}
	}
	return fp
}

// mergeAllOf は allOf の各ブランチをマージした合成スキーマを返す。
// この関数がフラット化するのはトップレベル（および allOf ブランチ直下）の allOf のみで、
// マージ結果の Properties の値に含まれる allOf までは解決しない。そのため呼び出し元が
// 各プロパティを再帰的に処理する際は、階層ごとに mergeAllOf を呼び直す必要がある
// （buildFormPropertySchema がその例）。
func mergeAllOf(prop *openapi3.Schema) *openapi3.Schema {
	return mergeAllOfVisited(prop, map[*openapi3.Schema]bool{})
}

// mergeAllOfVisited は mergeAllOf の実体。kin-openapi の Loader は $ref をポインタ循環として
// 解決するため、allOf のブランチが自己参照・相互参照になっているスキーマが存在しうる。
// visited は現在の再帰パス上にある allOf ブランチのスキーマポインタの集合で、無限再帰を防ぐ。
// 同じスキーマへの再訪（＝循環）を検知した場合、そのブランチはマージ対象からスキップする。
func mergeAllOfVisited(prop *openapi3.Schema, visited map[*openapi3.Schema]bool) *openapi3.Schema {
	merged := *prop
	merged.AllOf = nil

	properties := openapi3.Schemas{}
	maps.Copy(properties, merged.Properties)

	required := append([]string{}, merged.Required...)

	visited[prop] = true
	defer delete(visited, prop)

	for _, ref := range prop.AllOf {
		if ref == nil || ref.Value == nil {
			continue
		}
		sub := ref.Value
		if visited[sub] {
			// 循環参照: このブランチはスキップする
			continue
		}
		if len(sub.AllOf) > 0 {
			sub = mergeAllOfVisited(sub, visited)
		}
		maps.Copy(properties, sub.Properties)
		required = append(required, sub.Required...)
		if merged.Type == nil || len(*merged.Type) == 0 {
			merged.Type = sub.Type
		}
		if merged.Description == "" {
			merged.Description = sub.Description
		}
		if merged.Format == "" {
			merged.Format = sub.Format
		}
	}

	merged.Properties = properties
	merged.Required = required

	return &merged
}

// fileInputHint は、ファイル入力フィールドが base64 文字列または URL（署名付き URL 等）の
// どちらでも受け付けることを MCP クライアント側の LLM が判断できるようにする案内文。
// 明示的に取得元を指定したい場合は {url:"..."} / {base64:"..."} / {text:"..."} /
// {content:...}（従来の自動判定）のいずれかのキーを持つオブジェクトも渡せることを併記する。
const fileInputHint = "Provide the file content as a base64-encoded string, or as a URL (e.g. a presigned URL) to download the file from. " +
	"For explicit control, an object may be passed instead with one of these keys: " +
	`{url:"..."} to download from a URL, {base64:"..."} for base64-encoded content, {text:"..."} for raw text content, ` +
	`or {content:...} for the legacy auto-detected base64/URL form; filename/contentType may be included alongside any of these.`

// buildFormPropertySchema はフォームプロパティを MCP input schema のプロパティへ再帰変換する。
func buildFormPropertySchema(prop *openapi3.Schema) map[string]any {
	return buildFormPropertySchemaVisited(prop, map[*openapi3.Schema]bool{})
}

// buildFormPropertySchemaVisited は buildFormPropertySchema の実体。visited は現在の再帰パス上に
// あるスキーマポインタの集合で、自己参照・相互参照スキーマ（kin-openapi の Loader は $ref を
// ポインタ循環として解決するため実際に発生しうる）による無限再帰を防ぐ。同じスキーマへの再訪を
// 検知した場合、それ以上再帰せず浅いオブジェクト表現を返す。
func buildFormPropertySchemaVisited(prop *openapi3.Schema, visited map[*openapi3.Schema]bool) map[string]any { //nolint: gocyclo
	if prop == nil {
		return map[string]any{"type": "string", "description": "", "_meta": map[string]any{}}
	}
	if visited[prop] {
		// 循環参照: これ以上再帰しない
		return map[string]any{"type": "object", "description": prop.Description, "_meta": map[string]any{}}
	}
	visited[prop] = true
	defer delete(visited, prop)

	if len(prop.AllOf) > 0 {
		prop = mergeAllOf(prop)
	}

	desc := prop.Description
	if ext := prop.Extensions; ext != nil {
		if x, ok := ext["x-mcp"].(map[string]any); ok && x != nil {
			if v, ok := x["description"].(string); ok {
				desc = v
			}
		}
	}

	metadata := map[string]any{}
	if schemaIsBinary(prop) {
		metadata["manifold"] = map[string]any{
			"file":          true,
			"fileInputHint": fileInputHint,
		}
	}

	if len(prop.OneOf) > 0 {
		branches := []any{}
		for _, ref := range prop.OneOf {
			if ref == nil || ref.Value == nil {
				continue
			}
			branches = append(branches, buildFormPropertySchemaVisited(ref.Value, visited))
		}
		return map[string]any{
			"oneOf":       branches,
			"description": desc,
			"_meta":       metadata,
		}
	}
	if len(prop.AnyOf) > 0 {
		branches := []any{}
		for _, ref := range prop.AnyOf {
			if ref == nil || ref.Value == nil {
				continue
			}
			branches = append(branches, buildFormPropertySchemaVisited(ref.Value, visited))
		}
		return map[string]any{
			"anyOf":       branches,
			"description": desc,
			"_meta":       metadata,
		}
	}

	propType := schemaTypeStr(prop.Type)
	if propType == "" {
		if len(prop.Properties) > 0 {
			propType = "object"
		} else {
			propType = "string"
		}
	}

	result := map[string]any{
		"type":        propType,
		"description": desc,
		"_meta":       metadata,
	}

	if propType == "object" && len(prop.Properties) > 0 {
		properties := map[string]any{}
		for propName, propRef := range prop.Properties {
			if propRef == nil || propRef.Value == nil {
				continue
			}
			properties[propName] = buildFormPropertySchemaVisited(propRef.Value, visited)
		}
		result["properties"] = properties

		required := []string{}
		for _, r := range prop.Required {
			if _, ok := prop.Properties[r]; ok {
				required = append(required, r)
			}
		}
		result["required"] = required
	}

	if propType == "array" && prop.Items != nil && prop.Items.Value != nil {
		result["items"] = buildFormPropertySchemaVisited(prop.Items.Value, visited)
	}

	return result
}

// extractParameters は、OpenAPI 3.x のオペレーションからパラメータ名を取り出す。
func extractParameters(operation *openapi3.Operation) extractParameter {
	var (
		pathParams   = []string{}
		queryParams  = []string{}
		bodyParams   = []string{}
		formParams   = formParameters{}
		isMultipart  = false
		paramNameMap = map[string]string{}
	)

	for _, paramRef := range operation.Parameters {
		p := paramRef.Value
		name := sanitizeParamName(p.Name)
		paramNameMap[name] = p.Name
		switch p.In {
		case "path":
			pathParams = append(pathParams, name)
		case "query":
			queryParams = append(queryParams, name)
		case "body":
			bodyParams = append(bodyParams, name)
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
				formParams = newFormParameter(mt.Schema.Value).parameters
			}
		case content["multipart/form-data"] != nil:
			isMultipart = true
			mt := content["multipart/form-data"]
			if mt.Schema != nil && mt.Schema.Value != nil {
				formParams = newFormParameter(mt.Schema.Value).parameters
			}
		}
	}
	return extractParameter{
		pathParams:   pathParams,
		queryParams:  queryParams,
		bodyParams:   bodyParams,
		formParams:   formParams,
		isMultipart:  isMultipart,
		paramNameMap: paramNameMap,
	}
}

// describe_schema_fields_openapi recursively builds a human-readable field summary from an
// OpenAPI 3.x schema. Since Loader auto-resolves $refs, propRef.Value is always populated.
func describe_schema_fields_openapi(schema *openapi3.Schema) string {
	return describeSchemaFieldsOpenapiVisited(schema, map[*openapi3.Schema]bool{})
}

// describeSchemaFieldsOpenapiVisited は describe_schema_fields_openapi の実体。visited は現在の
// 再帰パス上にあるスキーマポインタの集合で、自己参照・相互参照スキーマ（kin-openapi の Loader は
// $ref をポインタ循環として解決するため実際に発生しうる）による無限再帰を防ぐ。同じスキーマへの
// 再訪を検知した場合は空文字列を返し、それ以上再帰しない。
func describeSchemaFieldsOpenapiVisited(schema *openapi3.Schema, visited map[*openapi3.Schema]bool) string { //nolint: gocyclo
	if schema == nil || visited[schema] {
		return ""
	}
	visited[schema] = true
	defer delete(visited, schema)

	if len(schema.AllOf) > 0 {
		schema = mergeAllOf(schema)
	}
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
		if len(prop.AllOf) > 0 {
			prop = mergeAllOf(prop)
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
			if nested := describeSchemaFieldsOpenapiVisited(prop, visited); nested != "" {
				parts = append(parts, fmt.Sprintf("%s (%s)%s -> {%s}", name, meta, fieldDesc, nested))
				continue
			}
		}

		if typ == "array" && prop.Items != nil && prop.Items.Value != nil { //nolint: nestif
			itemSchema := prop.Items.Value
			if len(itemSchema.AllOf) > 0 {
				itemSchema = mergeAllOf(itemSchema)
			}
			itemType := schemaTypeStr(itemSchema.Type)
			if itemType == "" {
				itemType = "object"
			}
			if itemType == "object" {
				if nested := describeSchemaFieldsOpenapiVisited(itemSchema, visited); nested != "" {
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
		name := sanitizeParamName(p.Name)
		properties[name] = map[string]any{
			"type":        paramType,
			"description": p.Description,
		}
		if p.Required {
			required = append(required, name)
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
			if len(schema.AllOf) > 0 {
				schema = mergeAllOf(schema)
			}
			bodyProps := map[string]any{}
			for propName, propRef := range schema.Properties {
				if propRef == nil || propRef.Value == nil {
					continue
				}
				prop := propRef.Value
				if len(prop.AllOf) > 0 {
					prop = mergeAllOf(prop)
				}
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
				if len(formSchema.AllOf) > 0 {
					formSchema = mergeAllOf(formSchema)
				}
				schemaRequired := map[string]bool{}
				for _, r := range formSchema.Required {
					schemaRequired[r] = true
				}
				for propName, propRef := range formSchema.Properties {
					if propRef == nil || propRef.Value == nil {
						continue
					}
					name := sanitizeParamName(propName)
					properties[name] = buildFormPropertySchema(propRef.Value)
					if schemaRequired[propName] {
						required = append(required, name)
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

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, `\"`)

// decodeFileContent はファイル内容を bytes に変換する。base64 として解釈できない文字列は raw bytes として扱う。
// サイズ判定は「実際に使われる表現」のバイト数（base64 ならデコード後、raw テキストならそのままの
// 長さ）に対して maxSize（バイト）を適用する。ただし base64 デコード自体を試みる前に、maxSize を
// base64 化した場合にあり得る最大長（およそ 4/3 倍＋パディング余裕）を超える入力は、無駄な
// デコード処理・アロケーションを避けるため粗く弾く。
func decodeFileContent(v any, maxSize int64) ([]byte, error) {
	switch value := v.(type) {
	case []byte:
		if int64(len(value)) > maxSize {
			return nil, fmt.Errorf("file size %d bytes exceeds the maximum allowed size of %d bytes", len(value), maxSize)
		}
		return value, nil
	case string:
		if maxBase64Len := maxSize*4/3 + 4; int64(len(value)) > maxBase64Len {
			return nil, fmt.Errorf("file size %d bytes exceeds the maximum allowed size of %d bytes", len(value), maxSize)
		}
		if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
			if int64(len(decoded)) > maxSize {
				return nil, fmt.Errorf("file size %d bytes exceeds the maximum allowed size of %d bytes", len(decoded), maxSize)
			}
			return decoded, nil
		}
		if int64(len(value)) > maxSize {
			return nil, fmt.Errorf("file size %d bytes exceeds the maximum allowed size of %d bytes", len(value), maxSize)
		}
		return []byte(value), nil
	default:
		return nil, fmt.Errorf("unsupported file content type %T", v)
	}
}

// fileFetchMaxRedirects は fetchFileFromURL がたどるリダイレクトの最大ホップ数。
const fileFetchMaxRedirects = 10

// checkFileFetchURL は FileFetchConfig に基づき URL のスキームと AllowedHosts を検証する。
// プライベート/ループバック IP への接続拒否自体は client.SafeHTTPClient() の dialer が担うため
// ここでは扱わない（isHostAllowed も参照）。
func checkFileFetchURL(cfg FileFetchConfig, u *url.URL) error {
	switch u.Scheme {
	case "https":
		// 常に許可
	case "http":
		if !cfg.AllowLocal {
			return fmt.Errorf("http:// URLs are not allowed (enable fileFetch.allowLocal for local testing)")
		}
	default:
		return fmt.Errorf("unsupported URL scheme %q: only http/https are allowed", u.Scheme)
	}
	if len(cfg.AllowedHosts) > 0 && !isHostAllowed(cfg.AllowedHosts, u) {
		return fmt.Errorf("host %q is not in the allowed hosts list", u.Host)
	}
	return nil
}

// isHostAllowed は URL のホストが allowed リストに含まれるかを判定する。
// ホスト名単体（ポート無し）とポート付きホストの両方で一致を試みる。
// DNS ホスト名は大文字小文字を区別しないため、比較は case-insensitive で行う。
func isHostAllowed(allowed []string, u *url.URL) bool {
	hostname := u.Hostname()
	hostWithPort := u.Host
	for _, h := range allowed {
		if strings.EqualFold(h, hostname) || strings.EqualFold(h, hostWithPort) {
			return true
		}
	}
	return false
}

// fileTooLargeError は取得/デコードしたファイルが MaxSize を超えた場合のエラー。
type fileTooLargeError struct {
	max int64
}

func (e *fileTooLargeError) Error() string {
	return fmt.Sprintf("file exceeds the maximum allowed size of %d bytes", e.max)
}

// limitedReadCloser は、読み出しバイト数が max を超えようとした時点でエラーを返す io.ReadCloser。
// Content-Length が不明なストリーミング応答（chunked 等）でもサイズ上限を強制するために使う。
// 内部では io.LimitReader(rc, max+1) に読み出しを委譲する（max を 1 バイト超えた時点で
// 検知できるよう、上限+1 バイトまでは素通しで読ませる）。呼び出し側で自前のスライス切り詰めは行わない。
type limitedReadCloser struct {
	r   io.Reader // io.LimitReader(rc, max+1)
	c   io.Closer
	max int64
	n   int64
}

func newLimitedReadCloser(rc io.ReadCloser, maxSize int64) *limitedReadCloser {
	return &limitedReadCloser{r: io.LimitReader(rc, maxSize+1), c: rc, max: maxSize}
}

func (l *limitedReadCloser) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.n += int64(n)
	if l.n > l.max {
		return n, &fileTooLargeError{max: l.max}
	}
	return n, err
}

func (l *limitedReadCloser) Close() error {
	return l.c.Close()
}

// fetchFileFromURL は署名付き URL などからファイルを取得し、ボディをストリームとして返す。
// 戻り値: body, URLパス末尾のファイル名（無ければ空）, レスポンスの Content-Type
//
// パッケージレベルの FileFetchConfig（SetFileFetchConfig で設定）に従い、SSRF 対策として
// 既定ではプライベート/ループバック/リンクローカル IP への接続と http スキームを拒否する
// （client.SafeHTTPClient() の dialer が接続時に判定）。AllowedHosts が設定されている場合は
// ホストの許可リストをリクエスト前とリダイレクト各ホップで検証する。レスポンスボディは
// MaxSize でサイズ上限を課す。
func fetchFileFromURL(ctx context.Context, rawURL string) (io.ReadCloser, string, string, error) {
	cfg := getFileFetchConfig()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", "", fmt.Errorf("invalid file URL: %w", err)
	}
	if err := checkFileFetchURL(cfg, parsed); err != nil {
		return nil, "", "", err
	}

	// SafeHTTPClient / HTTPClient は呼び出しごとに新しい *http.Client を生成して返すため、
	// この CheckRedirect の設定がリクエスト間で共有・競合することはない。
	httpClient := client.SafeHTTPClient()
	if cfg.AllowLocal {
		httpClient = client.HTTPClient()
	}
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= fileFetchMaxRedirects {
			return fmt.Errorf("stopped after %d redirects", fileFetchMaxRedirects)
		}
		return checkFileFetchURL(cfg, req.URL)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", "", err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", "", err
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close() //nolint: errcheck
		return nil, "", "", fmt.Errorf("failed to download file from URL: %d %s", resp.StatusCode, resp.Status)
	}
	if resp.ContentLength > 0 && resp.ContentLength > cfg.MaxSize {
		resp.Body.Close() //nolint: errcheck
		return nil, "", "", fmt.Errorf("file size %d bytes exceeds the maximum allowed size of %d bytes", resp.ContentLength, cfg.MaxSize)
	}

	filename := ""
	if base := path.Base(parsed.Path); base != "/" && base != "." {
		filename = base
	}

	return newLimitedReadCloser(resp.Body, cfg.MaxSize), filename, resp.Header.Get("Content-Type"), nil
}

// isFileURL は値が http(s) から始まる URL 文字列かどうかを返す。
// URL 形式かどうかの判定のみを行い、許可するかどうかの判定（checkFileFetchURL）とは分離する。
func isFileURL(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return s, true
	}
	return "", false
}

// writeMultipartFile はファイルパートを書き込む。値は base64 文字列、URL 文字列、
// または {filename, contentType, ...} のオブジェクト。
//
// オブジェクトの場合、ファイル内容の取得元は明示キー url / base64 / text / content の
// 優先順位（この順）で決定し、複数指定された場合は最初に一致したものだけを使い残りは無視する。
//   - url: そのURLから取得する（fetchFileFromURL の SSRF 検証を経由）。http(s) で始まらない値はエラー。
//   - base64: 文字列を必ず base64 としてデコードする。失敗したら raw フォールバックせずエラーにする。
//   - text: 文字列をそのまま bytes として使用する。
//   - content: 既存互換のヒューリスティック判定（isFileURL → decodeFileContent）。
//
// オブジェクトでない場合（生の文字列値）は content 相当として同じヒューリスティックを適用する。
func writeMultipartFile(ctx context.Context, writer *multipart.Writer, name string, value any) error { //nolint: gocyclo
	cfg := getFileFetchConfig()
	const defaultFilename = "file"
	filename := defaultFilename
	filenameExplicit := false
	contentType := ""
	contentTypeExplicit := false
	fallbackContent := value

	var (
		explicitURL    string
		hasURL         bool
		explicitBase64 string
		hasBase64      bool
		explicitText   string
		hasText        bool
	)

	if m, ok := value.(map[string]any); ok { //nolint: nestif
		if fn, ok := m["filename"].(string); ok && fn != "" {
			filename = fn
			filenameExplicit = true
		}
		if ct, ok := m["contentType"].(string); ok && ct != "" {
			contentType = ct
			contentTypeExplicit = true
		}
		if v, ok := m["url"].(string); ok {
			explicitURL, hasURL = v, true
		}
		if v, ok := m["base64"].(string); ok {
			explicitBase64, hasBase64 = v, true
		}
		if v, ok := m["text"].(string); ok {
			explicitText, hasText = v, true
		}
		fallbackContent = m["content"]
	}

	var body io.ReadCloser
	switch {
	case hasURL:
		if !strings.HasPrefix(explicitURL, "http://") && !strings.HasPrefix(explicitURL, "https://") {
			return fmt.Errorf("%q: url must start with http:// or https://", name)
		}
		b, urlFilename, urlContentType, err := fetchFileFromURL(ctx, explicitURL)
		if err != nil {
			return err
		}
		body = b
		if !filenameExplicit && urlFilename != "" {
			filename = urlFilename
		}
		if !contentTypeExplicit && urlContentType != "" {
			contentType = urlContentType
		}
	case hasBase64:
		decoded, err := base64.StdEncoding.DecodeString(explicitBase64)
		if err != nil {
			return fmt.Errorf("%q: invalid base64 content: %w", name, err)
		}
		if int64(len(decoded)) > cfg.MaxSize {
			return fmt.Errorf("%q: file size %d bytes exceeds the maximum allowed size of %d bytes", name, len(decoded), cfg.MaxSize)
		}
		body = io.NopCloser(bytes.NewBuffer(decoded))
	case hasText:
		if int64(len(explicitText)) > cfg.MaxSize {
			return fmt.Errorf("%q: file size %d bytes exceeds the maximum allowed size of %d bytes", name, len(explicitText), cfg.MaxSize)
		}
		body = io.NopCloser(bytes.NewBuffer([]byte(explicitText)))
	default:
		if rawURL, ok := isFileURL(fallbackContent); ok { //nolint: nestif
			b, urlFilename, urlContentType, err := fetchFileFromURL(ctx, rawURL)
			if err != nil {
				return err
			}
			body = b
			defer body.Close() //nolint: errcheck
			if !filenameExplicit && urlFilename != "" {
				filename = urlFilename
			}
			if !contentTypeExplicit && urlContentType != "" {
				contentType = urlContentType
			}
		} else {
			d, err := decodeFileContent(fallbackContent, cfg.MaxSize)
			if err != nil {
				return fmt.Errorf("%q: %w", name, err)
			}
			body = io.NopCloser(bytes.NewBuffer(d))
			mtype := mimetype.Detect(d)
			contentType = mtype.String()
			extensions := mtype.Extension()
			if defaultFilename == filename {
				filename = fmt.Sprintf("%s%s", filename, extensions)
			}
		}
	}

	var part io.Writer
	var err error
	if contentType == "" {
		part, err = writer.CreateFormFile(name, filename)
		if err != nil {
			return err
		}
	} else {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, quoteEscaper.Replace(name), quoteEscaper.Replace(filename)))
		h.Set("Content-Type", contentType)
		part, err = writer.CreatePart(h)
		if err != nil {
			return err
		}
	}

	if body != nil {
		defer body.Close() //nolint: errcheck
		_, err = io.Copy(part, body)
		return err
	}
	return err
}

// writeMultipartValue はフォーム値 1 件を multipart ボディに書き込む。
func writeMultipartValue(ctx context.Context, writer *multipart.Writer, name string, value any, param formParameter) error {
	if param.isFile {
		if items, ok := value.([]any); ok {
			for _, item := range items {
				if err := writeMultipartFile(ctx, writer, name, item); err != nil {
					return err
				}
			}
			return nil
		}
		return writeMultipartFile(ctx, writer, name, value)
	}

	switch value.(type) {
	case map[string]any, []any:
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"`, quoteEscaper.Replace(name)))
		h.Set("Content-Type", "application/json")
		part, err := writer.CreatePart(h)
		if err != nil {
			return err
		}
		_, err = part.Write(data)
		return err
	default:
		return writer.WriteField(name, fmt.Sprintf("%v", value))
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
				original_name := extractParameter.paramNameMap[param_name]
				safe_value, err := sanitize_path_parameter_value(param_value, original_name)
				if err != nil {
					return "", fmt.Errorf("invalid path parameter: %w", err)
				}
				_url = strings.ReplaceAll(_url, "{"+original_name+"}", safe_value)
				_url = strings.ReplaceAll(_url, "{{"+original_name+"}}", safe_value)
			}
		}

		params := map[string]any{}
		for _, param_name := range extractParameter.queryParams {
			param_value := input[param_name]
			if param_value != nil && param_value != "" {
				original_name := extractParameter.paramNameMap[param_name]
				params[original_name] = param_value
			}
		}

		client := client.HTTPClient()

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
				for param_name, param := range extractParameter.formParams {
					v := input[param_name]
					if v == nil {
						continue
					}
					if s, ok := v.(string); ok && s == "" {
						continue
					}
					field_name := param.originalName
					if err := writeMultipartValue(ctx, writer, field_name, v, param); err != nil {
						return "", fmt.Errorf("error writing multipart field %s: %w", field_name, err)
					}
				}
				writer.Close() //nolint: errcheck
				bodyBytes = buf.Bytes()
				bodyContentType = writer.FormDataContentType()
			} else {
				formValues := url.Values{}
				for param_name, param := range extractParameter.formParams {
					v := input[param_name]
					if v == nil {
						continue
					}
					if s, ok := v.(string); ok && s == "" {
						continue
					}
					field_name := param.originalName
					switch v.(type) {
					case map[string]any, []any:
						data, err := json.Marshal(v)
						if err != nil {
							return "", fmt.Errorf("error marshaling form field %s: %w", field_name, err)
						}
						formValues.Set(field_name, string(data))
					default:
						formValues.Set(field_name, fmt.Sprintf("%v", v))
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
