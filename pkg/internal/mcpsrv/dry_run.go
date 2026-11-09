package mcpsrv

import (
	"context"
	"errors"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
)

// InvalidTool is a tool the gateway would skip because the MCP SDK does not
// accept its definition.
type InvalidTool struct {
	Name string
	Err  error
}

// DryRunReport is what DryRunCatalog found for a spec.
type DryRunReport struct {
	// Tools are the tools the gateway would serve, sorted by name.
	Tools []ToolDefinition
	// Invalid are the tools the gateway would skip, sorted by name.
	Invalid []InvalidTool
	// BaseURL is the base URL derived from the spec ("" if none).
	BaseURL string
	// BaseURLErr is set when BaseURL is not an absolute http(s) URL; the
	// gateway then refuses the catalog unless mcpServers.<name>.baseURL is
	// configured.
	BaseURLErr error
}

// errDryRunNotCallable is returned by the handler registered during a dry
// run; the tools are never called.
var errDryRunNotCallable = errors.New("dry-run: tools are not callable")

// DryRunCatalog builds the catalog the gateway would build from source when
// no baseURL or tools options are configured, and registers every tool on a
// throwaway MCP server the same way the gateway does (addToolSafely), without
// sending any request to the API.
func DryRunCatalog(
	ctx context.Context, source *oastomcptool.SpecSource,
) (_ *DryRunReport, rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/DryRunCatalog")
	defer func() { trace.EndSpan(ctx, rErr) }()

	registry, err := BuildCatalog(ctx, &http.Client{}, source, "", nil)
	if err != nil {
		return nil, err
	}

	return dryRunRegistry(registry), nil
}

// dryRunRegistry registers every tool in registry on a throwaway MCP server
// the way attachTools does, and reports which ones would be served.
func dryRunRegistry(registry *MCPToolRegistry) *DryRunReport {
	srv := mcp.NewServer(&mcp.Implementation{Name: "manifold-dry-run"}, nil)
	handler := func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, errDryRunNotCallable
	}
	report := &DryRunReport{BaseURL: registry.BaseURL()}
	invalid := make(map[string]struct{})
	for _, tool := range registry.ListTools() {
		if err := addToolSafely(srv, &tool.tool, handler); err != nil {
			report.Invalid = append(report.Invalid, InvalidTool{Name: tool.tool.Name, Err: err})
			invalid[tool.tool.Name] = struct{}{}
		}
	}
	for _, d := range registry.Definitions() {
		if _, ok := invalid[d.Name]; !ok {
			report.Tools = append(report.Tools, d)
		}
	}
	report.BaseURLErr = checkCatalogBaseURL(registry)
	return report
}
