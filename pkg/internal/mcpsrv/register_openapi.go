package mcpsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
)

func RegisterOpenAPI(ctx context.Context, specPath string, baseUrl string) (_ *MCPToolRegistry, rErr error) {
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
		swagger(ctx, register, specPath, baseUrl)
	} else {
		openapi(ctx, register, specPath, baseUrl)
	}
	return register, nil
}

func swagger(ctx context.Context, register *MCPToolRegistry, specPath string, baseUrl string) (rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/swagger")
	defer func() { trace.EndSpan(ctx, rErr) }()

	spec, err := oastomcptool.LoadSwaggerSpec(ctx, specPath)
	if err != nil {
		return err
	}
	if baseUrl == "" {
		baseUrl = oastomcptool.GetBaseUrlFromSwagger(spec, specPath)
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
			toolFunc := oastomcptool.CreateToolFunctionSwagger(path, strings.ToLower(method), operation, pathItem.Parameters, spec, baseUrl, nil)

			register.RegisterTool(baseToolName, description, inputSchema, ToolFunc(toolFunc))
		}
	}
	return nil
}

func openapi(ctx context.Context, register *MCPToolRegistry, specPath string, baseUrl string) (rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/openapi")
	defer func() { trace.EndSpan(ctx, rErr) }()

	spec, err := oastomcptool.LoadOpenAPI3Spec(specPath)
	if err != nil {
		return err
	}
	if baseUrl == "" {
		baseUrl = oastomcptool.GetBaseUrlFromOpenAPI3(spec, specPath)
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
			toolFunc := oastomcptool.CreateToolFunction(path, strings.ToLower(method), operation, baseUrl, nil)

			register.RegisterTool(baseToolName, description, inputSchema, ToolFunc(toolFunc))
		}
	}
	return nil
}
