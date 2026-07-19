package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/internal/client"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
)

func RegisterOpenAPI(ctx context.Context, specPath string, baseUrl string, headers map[string]string, opts ...RegisterOpenAPIOption) (_ *MCPToolRegistry, rErr error) {
	opt := &registerOpenAPIOption{}
	for _, fn := range opts {
		fn(opt)
	}

	rt := client.Transport()
	if v := opt.tokenExchange; v != nil {
		source := &client.InMemoryRegistry{}
		registry := client.NewBaseTokenRegistry(v.URL, source)
		rt = client.NewTokenExchangeRoundTrip(rt, registry)
	}
	c := &http.Client{
		Timeout:   10 * time.Second,
		Transport: rt,
	}
	ctx = trace.StartSpan(ctx, "mcpsrv/RegisterOpenAPI")
	defer func() { trace.EndSpan(ctx, rErr) }()
	register := NewMCPToolRegistry()

	// バージョン判定のため最小限の JSON デコード
	raw, err := oastomcptool.FetchSpecBytes(ctx, specPath)
	if err != nil {
		return nil, err
	}
	var versionProbe struct {
		Swagger string `json:"swagger"`
	}
	_ = json.Unmarshal(raw, &versionProbe)
	isSwagger := versionProbe.Swagger != ""

	if isSwagger {
		if err := swagger(ctx, register, specPath, baseUrl, headers); err != nil {
			return nil, err
		}
	} else {
		if err := openapi(ctx, c, register, specPath, baseUrl, headers); err != nil {
			return nil, err
		}
	}
	return register, nil
}

func swagger(ctx context.Context, register *MCPToolRegistry, specPath string, baseUrl string, headers map[string]string) (rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/swagger")
	defer func() { trace.EndSpan(ctx, rErr) }()

	spec, err := oastomcptool.LoadSwaggerSpec(ctx, specPath)
	if err != nil {
		return err
	}
	if baseUrl == "" {
		baseUrl = oastomcptool.GetBaseUrlFromSwagger(ctx, spec, specPath)
	}
	for path, pathItem := range spec.Paths {
		for method, operation := range pathItem.Operations() {
			var operationId string
			if operation.OperationID != "" {
				operationId = operation.OperationID
			} else {
				operationId = fmt.Sprintf("%s_%s", strings.ToLower(method), strings.ReplaceAll(path, "/", "_"))
			}
			baseToolName := strings.ToLower(strings.ReplaceAll(operationId, " ", "_"))

			description := fmt.Sprintf("%s %s", strings.ToUpper(method), path)
			if operation.Summary != "" {
				description = operation.Summary
			} else if operation.Description != "" {
				description = operation.Description
			}

			inputSchema := oastomcptool.BuildInputSchemaSwagger(operation, pathItem.Parameters, spec)
			toolFunc := oastomcptool.CreateToolFunctionSwagger(path, strings.ToLower(method), operation, pathItem.Parameters, spec, baseUrl, headers)

			register.RegisterTool(baseToolName, description, inputSchema, ToolFunc(toolFunc))
		}
	}
	return nil
}

func openapi(ctx context.Context, client *http.Client, register *MCPToolRegistry, specPath string, baseUrl string, headers map[string]string) (rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/openapi")
	defer func() { trace.EndSpan(ctx, rErr) }()

	spec, err := oastomcptool.LoadOpenAPI3Spec(specPath)
	if err != nil {
		return err
	}
	if baseUrl == "" {
		baseUrl = oastomcptool.GetBaseUrlFromOpenAPI3(ctx, spec, specPath)
	}
	for path, pathItem := range spec.Paths.Map() {
		for method, operation := range pathItem.Operations() {
			var operationId string
			if operation.OperationID != "" {
				operationId = operation.OperationID
			} else {
				operationId = fmt.Sprintf("%s_%s", strings.ToLower(method), strings.ReplaceAll(path, "/", "_"))
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
			if isBinaryResponse {
				opts = append(opts, WithRegisterToolMeta(map[string]any{
					"manifold": map[string]any{
						"binaryResponse": true,
					},
				}))
			}
			register.RegisterTool(baseToolName, description, inputSchema, ToolFunc(toolFunc), opts...)
		}
	}
	return nil
}
