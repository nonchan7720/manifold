package mcpsrv

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
)

// RegisterOpenAPI fetches and loads specPath, then builds an MCPToolRegistry
// from it.
func RegisterOpenAPI(
	ctx context.Context,
	specPath string,
	baseUrl string,
	headers map[string]string,
	opts ...RegisterOpenAPIOption,
) (_ *MCPToolRegistry, rErr error) {
	opt := newRegisterOpenAPIOption(opts)

	// headers は nil を渡す: 各ツール関数がリクエストごとに付加するため、
	// トランスポート層でも付加すると二重適用になってしまう。
	rt := httpClientRoundTripper(opt.auth, opt.oauth2, opt.tokenExchange, opt.googleSA, nil)
	c := &http.Client{
		Timeout:   10 * time.Second,
		Transport: rt,
	}
	ctx = trace.StartSpan(ctx, "mcpsrv/RegisterOpenAPI")
	defer func() { trace.EndSpan(ctx, rErr) }()

	if opt.generatedToolsFile != "" {
		return registerFromGeneratedToolsFile(
			ctx, c, opt.generatedToolsFile, baseUrl, headers, opts...,
		)
	}

	source, err := oastomcptool.LoadSpecSource(ctx, specPath)
	if err != nil {
		return nil, err
	}
	return BuildCatalog(ctx, c, source, baseUrl, headers, opts...)
}

// registerFromGeneratedToolsFile builds and verifies a catalog from a
// generated tools file (tools.file) instead of fetching a spec. The catalog
// is rebuilt with the same options (tools.exclude included) the file was
// generated with, so a changed exclude list shows up as a stale file.
func registerFromGeneratedToolsFile(
	ctx context.Context,
	c *http.Client,
	path, baseUrl string,
	headers map[string]string,
	opts ...RegisterOpenAPIOption,
) (*MCPToolRegistry, error) {
	source, catalog, err := oastomcptool.LoadGeneratedSpecSource(ctx, path)
	if err != nil {
		return nil, fmt.Errorf(
			`load generated tools file %q: %w (run "manifold openapi generate")`, path, err,
		)
	}
	registry, err := BuildCatalog(ctx, c, source, baseUrl, headers, opts...)
	if err != nil {
		return nil, err
	}
	if err := VerifyGeneratedTools(registry, catalog); err != nil {
		return nil, fmt.Errorf(`%w (run "manifold openapi generate")`, err)
	}
	return registry, nil
}

// BuildCatalog builds an MCPToolRegistry from an already-loaded spec. Of the
// options, only the catalog-shaping ones (WithToolsConfig / WithExcludeTools)
// apply here; transport and tools.file options are RegisterOpenAPI's.
func BuildCatalog(
	ctx context.Context,
	client *http.Client,
	source *oastomcptool.SpecSource,
	baseUrl string,
	headers map[string]string,
	opts ...RegisterOpenAPIOption,
) (_ *MCPToolRegistry, rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/BuildCatalog")
	defer func() { trace.EndSpan(ctx, rErr) }()

	opt := newRegisterOpenAPIOption(opts)
	exclude := newOperationExcluder(ctx, opt.excludeTools)

	register := NewMCPToolRegistry()
	register.setSpecHash(source.Hash)

	switch source.Format {
	case oastomcptool.SpecFormatSwagger2:
		if err := swagger(
			ctx, client, register, source.Swagger, source.SpecPath, baseUrl, headers, exclude,
		); err != nil {
			return nil, err
		}
	case oastomcptool.SpecFormatOpenAPI3:
		register.setOpenAPISpec(source.OpenAPI)
		if err := openapi(
			ctx, client, register, source.OpenAPI, source.SpecPath, baseUrl, headers, exclude,
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
	exclude operationExcluder,
) (rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/swagger")
	defer func() { trace.EndSpan(ctx, rErr) }()

	if baseUrl == "" {
		baseUrl = oastomcptool.GetBaseUrlFromSwagger(ctx, spec, specPath)
	}
	register.setBaseURL(baseUrl)
	for path, pathItem := range spec.Paths {
		for method, operation := range pathItem.Operations() {
			operationId := operationIDOrDerived(operation.OperationID, method, path)
			baseToolName := strings.ToLower(strings.ReplaceAll(operationId, " ", "_"))
			if exclude(operationId, baseToolName, method, path) {
				continue
			}

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

			annotations := parseAnnotationsExtensionOrWarn(ctx, operation.Extensions, method, path)

			register.RegisterTool(
				baseToolName,
				description,
				inputSchema,
				ToolFunc(toolFunc),
				WithRegisterToolOperation(method, path),
				WithRegisterToolAnnotationsExtension(annotations),
				WithRegisterToolDocs(operation.Summary, operation.Description),
				WithRegisterToolBinaryFields(
					oastomcptool.ResponseBinaryFieldsSwagger(operation, spec),
				),
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
	exclude operationExcluder,
) (rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/openapi")
	defer func() { trace.EndSpan(ctx, rErr) }()

	if baseUrl == "" {
		baseUrl = oastomcptool.GetBaseUrlFromOpenAPI3(ctx, spec, specPath)
	}
	register.setBaseURL(baseUrl)
	for path, pathItem := range spec.Paths.Map() {
		for method, operation := range pathItem.Operations() {
			operationId := operationIDOrDerived(operation.OperationID, method, path)
			baseToolName := strings.ToLower(strings.ReplaceAll(operationId, " ", "_"))
			if exclude(operationId, baseToolName, method, path) {
				continue
			}

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
			annotations := parseAnnotationsExtensionOrWarn(ctx, operation.Extensions, method, path)
			opts := make([]RegisterToolOptions, 0, 10)
			opts = append(opts,
				WithRegisterToolOperation(method, path),
				WithRegisterToolAnnotationsExtension(annotations),
				WithRegisterToolDocs(operation.Summary, operation.Description),
			)
			if !isBinaryResponse {
				opts = append(opts, WithRegisterToolBinaryFields(
					oastomcptool.ResponseBinaryFields(operation),
				))
			}
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

// operationIDOrDerived returns the operation's operationId, or the
// "method_path" id the tool name is derived from when the spec omits it.
func operationIDOrDerived(operationID, method, path string) string {
	if operationID != "" {
		return operationID
	}
	return fmt.Sprintf(
		"%s_%s",
		strings.ToLower(method),
		strings.ReplaceAll(path, "/", "_"),
	)
}

// operationExcluder reports whether the operation is kept out of the
// catalog. It is given both the operationId (or the derived id for
// operations without one) and the tool name derived from it, so a
// tools.exclude pattern can be written either way ("deletePet" or
// "deletepet").
type operationExcluder func(operationID, toolName, method, path string) bool

// newOperationExcluder builds an operationExcluder from tools.exclude
// patterns (path.Match, case-sensitive). Excluded operations are logged at
// debug level so a missing tool can be traced back to the config.
func newOperationExcluder(ctx context.Context, patterns []string) operationExcluder {
	if len(patterns) == 0 {
		return func(string, string, string, string) bool { return false }
	}
	cfg := &config.ToolsConfig{Exclude: patterns}
	return func(operationID, toolName, method, path string) bool {
		if !cfg.Excluded(operationID) && !cfg.Excluded(toolName) {
			return false
		}
		slog.DebugContext(ctx, "operation excluded from catalog by tools.exclude",
			slog.String("operationId", operationID),
			slog.String("tool", toolName),
			slog.String("operation", fmt.Sprintf("%s %s", strings.ToUpper(method), path)),
		)
		return true
	}
}

// parseAnnotationsExtensionOrWarn parses the operation's x-mcp.annotations.
// A malformed extension is ignored with a warning, so the tool keeps its
// method-derived annotations instead of failing the whole catalog.
func parseAnnotationsExtensionOrWarn(
	ctx context.Context,
	extensions map[string]any,
	method, path string,
) AnnotationsExtension {
	ext, err := ParseAnnotationsExtension(extensions)
	if err != nil {
		slog.WarnContext(ctx, "invalid x-mcp.annotations; ignored",
			slog.String("operation", fmt.Sprintf("%s %s", strings.ToUpper(method), path)),
			slog.String("error", err.Error()),
		)
		return AnnotationsExtension{}
	}
	return ext
}
