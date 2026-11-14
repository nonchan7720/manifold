package oastomcptool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/internal/api"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	oasyaml "github.com/oasdiff/yaml"
)

// LoadSwaggerSpec fetches specPath and parses it as a Swagger 2.x spec (JSON
// or YAML).
func LoadSwaggerSpec(ctx context.Context, specPath string) (_ *openapi2.T, rErr error) {
	ctx = trace.StartSpan(ctx, "oastomcptool/LoadSwaggerSpec")
	defer func() { trace.EndSpan(ctx, rErr) }()

	data, err := FetchSpecBytes(ctx, specPath)
	if err != nil {
		return nil, err
	}
	return ParseSwaggerSpec(ctx, data)
}

// ParseSwaggerSpec parses already-fetched raw as a Swagger 2.x spec (JSON or YAML).
func ParseSwaggerSpec(ctx context.Context, raw []byte) (_ *openapi2.T, rErr error) {
	ctx = trace.StartSpan(ctx, "oastomcptool/ParseSwaggerSpec")
	defer func() { trace.EndSpan(ctx, rErr) }()

	// Some real-world specs use "required": bool on property schemas, invalid for openapi2.Schema.
	data, err := normalizeSwaggerJSON(raw)
	if err != nil {
		return nil, err
	}
	var spec openapi2.T
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}
	return &spec, nil
}

// normalizeSwaggerJSON removes boolean "required" fields from schema
// objects, accepting either JSON or YAML input.
func normalizeSwaggerJSON(data []byte) ([]byte, error) {
	var raw any
	jsonErr := json.Unmarshal(data, &raw)
	if jsonErr != nil || !isJSONObject(raw) {
		if converted, yamlErr := oasyaml.YAMLToJSON(data); yamlErr == nil {
			var yamlRaw any
			if err := json.Unmarshal(converted, &yamlRaw); err == nil && isJSONObject(yamlRaw) {
				raw, jsonErr = yamlRaw, nil
			}
		}
	}
	if jsonErr != nil {
		return nil, jsonErr
	}
	removeBoolRequiredFromSchemas(raw)
	return json.Marshal(raw)
}

func isJSONObject(v any) bool {
	_, ok := v.(map[string]any)
	return ok
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
func GetBaseUrlFromSwagger(ctx context.Context, spec *openapi2.T, specPath string) string {
	if spec.Host != "" {
		scheme := "https"
		if len(spec.Schemes) > 0 {
			scheme = spec.Schemes[0]
		}
		return fmt.Sprintf("%s://%s%s", scheme, spec.Host, spec.BasePath)
	}
	return deriveBaseUrlFromSpecPath(ctx, specPath)
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

// jsonPointerUnescaper は JSON Pointer の参照トークンのエスケープを復号する。
var jsonPointerUnescaper = strings.NewReplacer("~1", "/", "~0", "~")

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
	// JSON Pointer のエスケープ（~1 → /、~0 → ~）を復号する（1 パスで置換するため ~01 は ~1 になる）
	name = jsonPointerUnescaper.Replace(name)
	if resolved, ok := spec.Definitions[name]; ok {
		return resolved.Value
	}
	return nil
}

// swaggerSchemaToV3 は Swagger 2 の body スキーマ（$ref は spec.Definitions で解決）を
// openapi3.Schema へ変換する。OpenAPI 3 側の formParameter 構築（newFormParameter）・スキーマ生成
// （buildFormPropertySchema）・実行時解決（normalizeJSONBody）を再利用するための橋渡しで、
// それらが参照する項目（type/format/description/enum/properties/items/allOf/required）のみ写す。
// memo は循環参照（自己参照 definitions）による無限再帰の防止と、同一スキーマの共有を兼ねる。
func swaggerSchemaToV3(
	ref *openapi2.SchemaRef,
	spec *openapi2.T,
	memo map[*openapi2.Schema]*openapi3.Schema,
) *openapi3.Schema {
	var src *openapi2.Schema
	if spec != nil {
		src = resolveSwaggerSchemaRef(ref, spec)
	} else if ref != nil && ref.Ref == "" {
		src = ref.Value
	}
	if src == nil {
		return nil
	}
	if dst, ok := memo[src]; ok {
		return dst
	}
	dst := &openapi3.Schema{
		Type:        src.Type,
		Format:      src.Format,
		Description: src.Description,
		Enum:        src.Enum,
		Required:    src.Required,
		Extensions:  src.Extensions,
	}
	memo[src] = dst
	for _, a := range src.AllOf {
		if c := swaggerSchemaToV3(a, spec, memo); c != nil {
			dst.AllOf = append(dst.AllOf, &openapi3.SchemaRef{Value: c})
		}
	}
	if src.Items != nil {
		if c := swaggerSchemaToV3(src.Items, spec, memo); c != nil {
			dst.Items = &openapi3.SchemaRef{Value: c}
		}
	}
	if len(src.Properties) > 0 {
		dst.Properties = openapi3.Schemas{}
		for name, p := range src.Properties {
			if c := swaggerSchemaToV3(p, spec, memo); c != nil {
				dst.Properties[name] = &openapi3.SchemaRef{Value: c}
			}
		}
	}
	return dst
}

// swaggerBodyV3Schema は in: body パラメータのスキーマを openapi3.Schema へ変換して返す。
// スキーマが無い（解決できない）場合は nil を返す。
func swaggerBodyV3Schema(p *openapi2.Parameter, spec *openapi2.T) *openapi3.Schema {
	if p == nil || p.Schema == nil {
		return nil
	}
	return swaggerSchemaToV3(p.Schema, spec, map[*openapi2.Schema]*openapi3.Schema{})
}

// swaggerFormDataV3Schema は in: formData パラメータ（type / format / enum / items をパラメータ
// 自身が持つ）を openapi3.Schema へ変換する。OpenAPI 3 のフォームプロパティと同じ
// buildFormPropertySchema / newFormParameter に渡すための橋渡し。type: file は OpenAPI 3 の
// ファイル入力（type: string, format: binary）として表す。
func swaggerFormDataV3Schema(p *openapi2.Parameter, spec *openapi2.T) *openapi3.Schema {
	schema := &openapi3.Schema{
		Type:        p.Type,
		Format:      p.Format,
		Description: p.Description,
		Enum:        p.Enum,
		Extensions:  p.Extensions,
	}
	if p.Type != nil && p.Type.Is("file") {
		schema.Type = &openapi3.Types{"string"}
		schema.Format = "binary"
	}
	if p.Items != nil {
		items := swaggerSchemaToV3(p.Items, spec, map[*openapi2.Schema]*openapi3.Schema{})
		if items != nil {
			schema.Items = &openapi3.SchemaRef{Value: items}
		}
	}
	return schema
}

// swaggerCollectionValues は、in: formData の配列値を collectionFormat に従って、同じフィールド名で
// 送る値の列へ変換する。"multi" は要素ごとに 1 フィールド（name=a&name=b）、それ以外は区切り
// 文字で連結した 1 つの値（既定の csv なら name=a,b）。配列でない値はそのまま 1 件で返す。
func swaggerCollectionValues(value any, collectionFormat string) []any {
	items, ok := value.([]any)
	if !ok {
		return []any{value}
	}
	if collectionFormat == "multi" {
		return items
	}
	sep := ","
	switch collectionFormat {
	case "ssv":
		sep = " "
	case "tsv":
		sep = "\t"
	case "pipes":
		sep = "|"
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if isEmptyFormValue(item) {
			continue
		}
		parts = append(parts, fmt.Sprintf("%v", item))
	}
	return []any{strings.Join(parts, sep)}
}

// ResponseIsBinarySwagger は ResponseIsBinary の Swagger 2.x 版。2xx レスポンスのスキーマが
// ボディ全体として binary（type: file、format: binary、またはその配列）かどうかを返す。
func ResponseIsBinarySwagger(operation *openapi2.Operation, spec *openapi2.T) bool {
	if operation == nil || operation.Responses == nil {
		return false
	}
	for status, resp := range operation.Responses.Map() {
		code, err := strconv.Atoi(status)
		if err != nil || code < 200 || code > 299 || resp == nil || resp.Schema == nil {
			continue
		}
		schema := swaggerSchemaToV3(resp.Schema, spec, map[*openapi2.Schema]*openapi3.Schema{})
		if schema == nil {
			continue
		}
		if (schema.Type != nil && schema.Type.Is("file")) || schemaIsBinary(schema) {
			return true
		}
	}
	return false
}

// mergeSwaggerParams merges path-level and operation-level parameters.
// Operation-level parameters win when the same (in, name) combination appears in both.
func mergeSwaggerParams(
	operation *openapi2.Operation,
	pathItemParams []*openapi2.Parameter,
	spec *openapi2.T,
) []*openapi2.Parameter {
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

// extractParametersSwagger extracts parameter names from a Swagger 2.x operation.
// Path-level parameters are merged with operation-level (operation wins on conflict).
//
// 名前は OpenAPI 3.x（extractParameters）と同じく sanitizeParamName を通した MCP 入力スキーマ上の
// 名前で持ち、上流へ送るときの元の名前は paramNameMap / formParameter.originalName から引く。
func extractParametersSwagger(
	operation *openapi2.Operation,
	pathItemParams []*openapi2.Parameter,
	spec *openapi2.T,
) extractParameter {
	var (
		pathParams   = []string{}
		queryParams  = []string{}
		bodyParams   = []string{}
		formParams   = formParameters{}
		isMultipart  = false
		paramNameMap = map[string]string{}
		// sanitize 後の formData 名 -> collectionFormat（未指定は既定の csv）
		collectionFormats = map[string]string{}

		bodyFormParam formParameter
	)

	merged := mergeSwaggerParams(operation, pathItemParams, spec)
	for _, p := range merged {
		name := sanitizeParamName(p.Name)
		switch p.In {
		case "path":
			pathParams = append(pathParams, name)
			paramNameMap[name] = p.Name
		case "query":
			queryParams = append(queryParams, name)
			paramNameMap[name] = p.Name
		case "body":
			bodyParams = append(bodyParams, name)
			// 実行時に JSON ボディを解決（binary 値の読み込み・サニタイズ済みプロパティ名の
			// 復元）するための formParameter ツリー。OpenAPI 3 の bodyFormParam と同じ形。
			if conv := swaggerBodyV3Schema(p, spec); conv != nil {
				bodyFormParam = newFormParameter(conv)
			}
		case "formData":
			param := newFormParameter(swaggerFormDataV3Schema(p, spec))
			param.originalName = p.Name
			formParams[name] = param
			if p.CollectionFormat != "" {
				collectionFormats[name] = p.CollectionFormat
			}
			// type: file requires multipart/form-data
			if param.isFile {
				isMultipart = true
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

		bodyFormParam:     bodyFormParam,
		collectionFormats: collectionFormats,
	}
}

// describeSchemaFieldsSwagger recursively builds a human-readable field summary from a
// Swagger 2.x schema. $refs are resolved via spec.Definitions.
func describeSchemaFieldsSwagger(schema *openapi2.Schema, spec *openapi2.T) string {
	return describeSchemaFieldsSwaggerVisited(schema, spec, map[*openapi2.Schema]bool{})
}

// describeSchemaFieldsSwaggerVisited は describeSchemaFieldsSwagger の実体。visited は現在の
// 再帰パス上にあるスキーマポインタの集合で、自己参照・相互参照する definitions（$ref の解決で
// 同じポインタが返る）による無限再帰を防ぐ。再訪を検知した場合は空文字列を返す。
func describeSchemaFieldsSwaggerVisited(
	schema *openapi2.Schema,
	spec *openapi2.T,
	visited map[*openapi2.Schema]bool,
) string {
	if schema == nil || visited[schema] {
		return ""
	}
	visited[schema] = true
	defer delete(visited, schema)

	return describeSchemaFields(swaggerFieldsView{schema: schema, spec: spec, visited: visited})
}

// swaggerFieldsView は Swagger 2.x のスキーマを schemaFieldsView として見せるアダプタ。
// 子スキーマの $ref は spec.Definitions で解決する。
type swaggerFieldsView struct {
	schema  *openapi2.Schema
	spec    *openapi2.T
	visited map[*openapi2.Schema]bool
}

func (v swaggerFieldsView) propertyNames() []string {
	return slices.Collect(maps.Keys(v.schema.Properties))
}

func (v swaggerFieldsView) required() []string { return v.schema.Required }

func (v swaggerFieldsView) property(name string) (schemaFieldsView, bool) {
	return v.child(v.schema.Properties[name])
}

func (v swaggerFieldsView) items() (schemaFieldsView, bool) { return v.child(v.schema.Items) }

func (v swaggerFieldsView) child(ref *openapi2.SchemaRef) (schemaFieldsView, bool) {
	child := resolveSwaggerSchemaRef(ref, v.spec)
	if child == nil {
		return nil, false
	}
	return swaggerFieldsView{schema: child, spec: v.spec, visited: v.visited}, true
}

func (v swaggerFieldsView) schemaType() *openapi3.Types { return v.schema.Type }

func (v swaggerFieldsView) description() string { return v.schema.Description }

func (v swaggerFieldsView) describeNested() string {
	return describeSchemaFieldsSwaggerVisited(v.schema, v.spec, v.visited)
}

// buildBodyDescriptionSwagger constructs a detailed description for a Swagger 2.x body parameter.
func buildBodyDescriptionSwagger(
	base_desc string,
	schema *openapi2.Schema,
	spec *openapi2.T,
) string {
	if base_desc == "" {
		base_desc = "Request body"
	}
	fields := describeSchemaFieldsSwagger(schema, spec)
	if fields == "" {
		return base_desc + ". Pass a JSON object."
	}
	return fmt.Sprintf("%s. JSON object with fields: {%s}", base_desc, fields)
}

// BuildInputSchemaSwagger builds MCP input schema from a Swagger 2.x operation.
func BuildInputSchemaSwagger(
	operation *openapi2.Operation,
	pathItemParams []*openapi2.Parameter,
	spec *openapi2.T,
) map[string]any {
	properties := map[string]any{}
	required := []string{}

	merged := mergeSwaggerParams(operation, pathItemParams, spec)
	for _, p := range merged {
		// MCP の入力スキーマのプロパティ名に "[" "]" は使えないので、OpenAPI 3.x と同じく
		// sanitizeParamName を通す（"ids[]" -> "ids_"）。元の名前への復元は実行時に行う。
		name := sanitizeParamName(p.Name)
		if in := p.In; in == "body" { //nolint: nestif
			// Body parameter: "schema" holds the full object definition.
			var bodySchema *openapi2.Schema
			if p.Schema != nil {
				bodySchema = resolveSwaggerSchemaRef(p.Schema, spec)
			}
			bodyProps := map[string]any{}
			desc := p.Description
			if bodySchema != nil {
				// プロパティは OpenAPI 3 の JSON ボディと同じ buildFormPropertySchema で組み立てる
				// （ネストした object / array の items / enum / format: binary / $ref / allOf）。
				bodyRequired := []string{}
				if conv := swaggerBodyV3Schema(p, spec); conv != nil {
					if len(conv.AllOf) > 0 {
						conv = mergeAllOf(conv)
					}
					for propName, propRef := range conv.Properties {
						if propRef == nil || propRef.Value == nil {
							continue
						}
						bodyProps[sanitizeParamName(propName)] = buildFormPropertySchema(
							propRef.Value,
						)
						if slices.Contains(conv.Required, propName) {
							bodyRequired = append(bodyRequired, sanitizeParamName(propName))
						}
					}
					// map 走査順は不定なので、出力を安定させるためソートする。
					slices.Sort(bodyRequired)
				}
				properties[name] = map[string]any{
					"type":        "object",
					"description": buildBodyDescriptionSwagger(desc, bodySchema, spec),
					"properties":  bodyProps,
					"required":    bodyRequired,
				}
			} else {
				properties[name] = map[string]any{
					"type":        "object",
					"description": desc,
					"properties":  bodyProps,
				}
			}
		} else if p.In == "formData" {
			// formData は OpenAPI 3 のフォームプロパティと同じ buildFormPropertySchema で組み立てる
			// （type: file は binaryFieldMCPSchema、配列は items、enum も反映される）。
			properties[name] = buildFormPropertySchema(swaggerFormDataV3Schema(p, spec))
		} else {
			// Non-body: "type" is directly on the parameter.
			paramType := schemaTypeStr(p.Type)
			if paramType == "" {
				paramType = "string"
			}
			properties[name] = map[string]any{
				"type":        paramType,
				"description": p.Description,
			}
		}
		if p.Required {
			required = append(required, name)
		}
	}

	return map[string]any{
		"type":       "object",
		"properties": properties,
		"required":   required,
	}
}

// CreateToolFunctionSwagger creates a tool function for a Swagger 2.x operation.
func CreateToolFunctionSwagger( //nolint: gocyclo
	client *http.Client,
	path string,
	method string,
	operation *openapi2.Operation,
	pathItemParams []*openapi2.Parameter,
	spec *openapi2.T,
	base_url string,
	headers map[string]string,
	isBinaryResponse bool,
) ToolFunc {
	if headers == nil {
		headers = map[string]string{}
	}

	extractParameter := extractParametersSwagger(operation, pathItemParams, spec)
	original_method := strings.ToLower(method)

	tool_function := func(ctx context.Context, input map[string]any) ([]byte, string, error) {
		effective_headers := map[string]string{}
		maps.Copy(effective_headers, headers)
		override_auth := contexts.FromRequestAuthHeader(ctx)
		if override_auth != "" {
			effective_headers["Authorization"] = "Bearer " + override_auth
		}

		_url := base_url + path

		for _, param_name := range extractParameter.pathParams {
			param_value := input[param_name]
			if param_value != nil && param_value != "" {
				original_name := extractParameter.paramNameMap[param_name]
				safe_value, err := sanitize_path_parameter_value(param_value, original_name)
				if err != nil {
					return nil, "", fmt.Errorf("invalid path parameter: %w", err)
				}
				_url = strings.ReplaceAll(_url, "{"+original_name+"}", safe_value)
				_url = strings.ReplaceAll(_url, "{{"+original_name+"}}", safe_value)
			}
		}

		params := map[string]any{}
		for _, param_name := range extractParameter.queryParams {
			param_value := input[param_name]
			if param_value != nil && param_value != "" {
				params[extractParameter.paramNameMap[param_name]] = param_value
			}
		}

		parsedURL, err := url.Parse(_url)
		if err != nil {
			return nil, "", fmt.Errorf("error parsing URL: %w", err)
		}
		q := parsedURL.Query()
		for k, v := range params {
			q.Set(k, fmt.Sprintf("%v", v))
		}
		parsedURL.RawQuery = q.Encode()
		finalURL := parsedURL.String()

		var bodyReader io.Reader
		var bodyContentType string

		if len(extractParameter.formParams) > 0 { //nolint: nestif
			if extractParameter.isMultipart {
				formParams := extractParameter.formParams
				pr, ct := newMultipartStreamBody(func(mw *multipart.Writer) error {
					for param_name, param := range formParams {
						v := input[param_name]
						if v == nil {
							continue
						}
						if s, ok := v.(string); ok && s == "" {
							continue
						}
						field_name := param.originalName
						values := []any{v}
						if !param.isFile {
							values = swaggerCollectionValues(
								v, extractParameter.collectionFormats[param_name],
							)
						}
						for _, fv := range values {
							if err := writeMultipartValue(
								ctx, mw, field_name, fv, param,
							); err != nil {
								return fmt.Errorf(
									"error writing multipart field %s: %w",
									field_name,
									err,
								)
							}
						}
					}
					return nil
				})
				// See newMultipartStreamBody's doc comment: Close is always safe here,
				// whether the goroutine already finished or is still writing.
				defer pr.Close() //nolint: errcheck
				bodyReader = pr
				bodyContentType = ct
			} else {
				formValues := url.Values{}
				for param_name, param := range extractParameter.formParams {
					v := input[param_name]
					if isEmptyFormValue(v) {
						continue
					}
					for _, fv := range swaggerCollectionValues(
						v, extractParameter.collectionFormats[param_name],
					) {
						if isEmptyFormValue(fv) {
							continue
						}
						formValues.Add(param.originalName, fmt.Sprintf("%v", fv))
					}
				}
				if len(formValues) > 0 || methodAlwaysSendsBody(original_method) {
					bodyReader = strings.NewReader(formValues.Encode())
					bodyContentType = "application/x-www-form-urlencoded"
				}
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
				if len(extractParameter.bodyFormParam.parameters) > 0 {
					resolved, err := normalizeJSONBody(
						ctx,
						"body",
						json_body,
						extractParameter.bodyFormParam,
					)
					if err != nil {
						return nil, "", fmt.Errorf("error normalizing json body: %w", err)
					}
					if rb, ok := resolved.(map[string]any); ok {
						json_body = rb
					}
				}
				bodyBytes, err := json.Marshal(json_body)
				if err != nil {
					return nil, "", fmt.Errorf("error marshaling request body: %w", err)
				}
				bodyReader = bytes.NewReader(bodyBytes)
				bodyContentType = "application/json"
			}
		}

		if !isSupportedMethod(original_method) {
			return nil, "", fmt.Errorf("unsupported HTTP method: %s", original_method)
		}
		response, err := api.DoRequestWithBody(
			ctx,
			client,
			finalURL,
			original_method,
			bodyReader,
			bodyContentType,
			effective_headers,
		)
		if err != nil {
			return nil, "", fmt.Errorf("error making request: %w", err)
		}
		defer response.Body.Close() //nolint: errcheck

		respBody, err := io.ReadAll(response.Body)
		if err != nil {
			return nil, "", fmt.Errorf("error reading response: %w", err)
		}
		// 400 以上でも Go のエラーにはせず、OpenAPI 3.x（CreateToolFunction）と同じく上流の
		// 応答ボディと Content-Type をそのまま返す。
		if response.StatusCode >= 400 {
			if len(respBody) == 0 {
				respBody = []byte(http.StatusText(response.StatusCode))
			}
		}
		contentType := response.Header.Get("Content-Type")
		// バイナリ応答でも、エラーやテキスト系の応答は生のまま返す
		if shouldBase64EncodeResponse(isBinaryResponse, response.StatusCode, contentType) {
			return []byte(base64.URLEncoding.EncodeToString(respBody)), contentType, nil
		}
		return respBody, contentType, nil
	}

	return tool_function
}
