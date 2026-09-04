package mcpsrv

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
)

// RegisterOpenAPI fetches and loads specPath, then builds an MCPToolRegistry
// from it. It is "spec の入手" (LoadSpecSource) followed by "カタログ構築"
// (BuildCatalog) — the two are split out so a caller that already holds a
// *oastomcptool.SpecSource (e.g. a future CLI) can reuse BuildCatalog
// directly without refetching/reparsing the spec.
func RegisterOpenAPI(
	ctx context.Context,
	specPath string,
	baseUrl string,
	headers map[string]string,
	opts ...RegisterOpenAPIOption,
) (_ *MCPToolRegistry, rErr error) {
	opt := &registerOpenAPIOption{}
	for _, fn := range opts {
		fn(opt)
	}

	// transport.go の httpClientRoundTripper を使い、AuthValue/OAuth2/TokenExchange の
	// いずれが設定されていても正しい認証方式でトランスポートを組み立てる
	// （以前は tokenExchange しか考慮しておらず、AuthValue/OAuth2 が無視されていた）。
	// headers はここでは nil を渡す: openapi()/swagger() が生成する各ツール関数が
	// effective_headers としてリクエストごとに headers を付加するため、トランスポート層でも
	// 同じ headers を NewExtraHeaderRoundTripper で付加すると二重適用になってしまう。
	rt := httpClientRoundTripper(opt.auth, opt.oauth2, opt.tokenExchange, nil)
	c := &http.Client{
		Timeout:   10 * time.Second,
		Transport: rt,
	}
	ctx = trace.StartSpan(ctx, "mcpsrv/RegisterOpenAPI")
	defer func() { trace.EndSpan(ctx, rErr) }()

	if opt.generatedToolsFile != "" {
		return registerFromGeneratedToolsFile(ctx, c, opt.generatedToolsFile, baseUrl, headers)
	}

	source, err := oastomcptool.LoadSpecSource(ctx, specPath)
	if err != nil {
		return nil, err
	}
	return BuildCatalog(ctx, c, source, baseUrl, headers)
}

// registerFromGeneratedToolsFile builds a catalog from a generated tools
// file (tools.file) instead of fetching a spec: it loads path with no
// network access (oastomcptool.LoadGeneratedSpecSource), builds the catalog
// from the internalized spec it carries exactly like the live path does,
// and verifies the result against the file's own "tools" section — a
// mismatch means the file is stale relative to what its spec now produces.
func registerFromGeneratedToolsFile(
	ctx context.Context,
	c *http.Client,
	path, baseUrl string,
	headers map[string]string,
) (*MCPToolRegistry, error) {
	source, catalog, err := oastomcptool.LoadGeneratedSpecSource(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("load generated tools file %q: %w", path, err)
	}
	registry, err := BuildCatalog(ctx, c, source, baseUrl, headers)
	if err != nil {
		return nil, err
	}
	if err := VerifyGeneratedTools(registry, catalog); err != nil {
		return nil, fmt.Errorf(`%w (run "manifold openapi generate")`, err)
	}
	return registry, nil
}

// BuildCatalog builds an MCPToolRegistry from an already-loaded spec. This is
// the "カタログ構築" phase of RegisterOpenAPI (the former body of the
// openapi()/swagger() loops), factored out so it can be driven by a spec
// loaded from anywhere (today: RegisterOpenAPI via LoadSpecSource; later: a
// CLI reading the same spec once to both display and build the catalog).
func BuildCatalog(
	ctx context.Context,
	client *http.Client,
	source *oastomcptool.SpecSource,
	baseUrl string,
	headers map[string]string,
) (_ *MCPToolRegistry, rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/BuildCatalog")
	defer func() { trace.EndSpan(ctx, rErr) }()

	register := NewMCPToolRegistry()
	register.setSpecHash(source.Hash)

	switch source.Format {
	case oastomcptool.SpecFormatSwagger2:
		if err := swagger(
			ctx, client, register, source.Swagger, source.SpecPath, baseUrl, headers,
		); err != nil {
			return nil, err
		}
	case oastomcptool.SpecFormatOpenAPI3:
		if err := openapi(
			ctx, client, register, source.OpenAPI, source.SpecPath, baseUrl, headers,
		); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported spec format: %q", source.Format)
	}
	return register, nil
}

func swagger(
	ctx context.Context,
	client *http.Client,
	register *MCPToolRegistry,
	spec *openapi2.T,
	specPath string,
	baseUrl string,
	headers map[string]string,
) (rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/swagger")
	defer func() { trace.EndSpan(ctx, rErr) }()

	if baseUrl == "" {
		baseUrl = oastomcptool.GetBaseUrlFromSwagger(ctx, spec, specPath)
	}
	for path, pathItem := range spec.Paths {
		for method, operation := range pathItem.Operations() {
			var operationId string
			if operation.OperationID != "" {
				operationId = operation.OperationID
			} else {
				operationId = fmt.Sprintf(
					"%s_%s",
					strings.ToLower(method),
					strings.ReplaceAll(path, "/", "_"),
				)
			}
			baseToolName := strings.ToLower(strings.ReplaceAll(operationId, " ", "_"))

			description := fmt.Sprintf("%s %s", strings.ToUpper(method), path)
			if operation.Summary != "" {
				description = operation.Summary
			} else if operation.Description != "" {
				description = operation.Description
			}

			inputSchema := oastomcptool.BuildInputSchemaSwagger(
				operation,
				pathItem.Parameters,
				spec,
			)
			toolFunc := oastomcptool.CreateToolFunctionSwagger(
				client,
				path,
				strings.ToLower(method),
				operation,
				pathItem.Parameters,
				spec,
				baseUrl,
				headers,
			)

			register.RegisterTool(
				baseToolName,
				description,
				inputSchema,
				ToolFunc(toolFunc),
				WithRegisterToolOperation(method, path),
			)
		}
	}
	return nil
}

func openapi(
	ctx context.Context,
	client *http.Client,
	register *MCPToolRegistry,
	spec *openapi3.T,
	specPath string,
	baseUrl string,
	headers map[string]string,
) (rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/openapi")
	defer func() { trace.EndSpan(ctx, rErr) }()

	if baseUrl == "" {
		baseUrl = oastomcptool.GetBaseUrlFromOpenAPI3(ctx, spec, specPath)
	}
	for path, pathItem := range spec.Paths.Map() {
		for method, operation := range pathItem.Operations() {
			var operationId string
			if operation.OperationID != "" {
				operationId = operation.OperationID
			} else {
				operationId = fmt.Sprintf(
					"%s_%s",
					strings.ToLower(method),
					strings.ReplaceAll(path, "/", "_"),
				)
			}
			baseToolName := strings.ToLower(strings.ReplaceAll(operationId, " ", "_"))

			description := fmt.Sprintf("%s %s", strings.ToUpper(method), path)
			if operation.Summary != "" {
				description = operation.Summary
			} else if operation.Description != "" {
				description = operation.Description
			}

			inputSchema := oastomcptool.BuildInputSchema(operation)
			isBinaryResponse := oastomcptool.ResponseIsBinary(operation)
			toolFunc := oastomcptool.CreateToolFunction(
				client,
				path,
				strings.ToLower(method),
				operation,
				baseUrl,
				headers,
				isBinaryResponse,
			)
			opts := make([]RegisterToolOptions, 0, 10)
			opts = append(opts, WithRegisterToolOperation(method, path))
			if isBinaryResponse {
				opts = append(opts, WithRegisterToolMeta(map[string]any{
					"manifold": map[string]any{
						"binaryResponse": true,
					},
				}))
			}
			register.RegisterTool(
				baseToolName,
				description,
				inputSchema,
				ToolFunc(toolFunc),
				opts...)
		}
	}
	return nil
}
