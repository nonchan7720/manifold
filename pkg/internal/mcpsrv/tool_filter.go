package mcpsrv

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
)

// toolFilter applies mcpServers.<name>.tools.include / exclude / overrides:
// which of the backend's tools are exposed, and under which name and
// description.
type toolFilter struct {
	cfg *config.ToolsConfig
	// overrides is keyed by the original tool name.
	overrides map[string]config.ToolOverride
	// renamedFrom maps an exposed (renamed) name back to the original name.
	renamedFrom map[string]string
	// builtin names the tools the gateway itself registers on the server
	// (a reverse server's create_pairing_code). They aren't the backend's
	// tools, so include / exclude / overrides never apply to them: they are
	// always exposed, under their own name, and only under it — an override
	// can neither alias a builtin tool (authz checks exposed names, so an
	// alias would let a policy that denies the builtin name through) nor
	// rename a backend tool onto a builtin name (that tool is hidden instead).
	builtin map[string]bool
	// builtinPrefixes are the builtin entries ending in config.AgentToolSeparator:
	// a service's <agent>__<skill> tools, whose skill names come from the
	// agent card, so they're matched by the <agent>__ prefix instead.
	builtinPrefixes []string
}

// newToolFilter returns the filter for cfg, or nil when cfg sets no include,
// exclude or overrides. builtin names tools that bypass the filter (see
// toolFilter.builtin).
func newToolFilter(cfg *config.ToolsConfig, builtin ...string) *toolFilter {
	if !cfg.HasFilter() {
		return nil
	}
	f := &toolFilter{
		cfg:         cfg,
		overrides:   cfg.ResolvedOverrides(),
		renamedFrom: map[string]string{},
		builtin:     make(map[string]bool, len(builtin)),
	}
	for _, name := range builtin {
		if strings.HasSuffix(name, config.AgentToolSeparator) {
			f.builtinPrefixes = append(f.builtinPrefixes, name)
			continue
		}
		f.builtin[name] = true
	}
	for original, override := range f.overrides {
		if f.isBuiltin(original) {
			// 組み込みツールへの override は無視する（別名を作らせない）。
			delete(f.overrides, original)
			continue
		}
		if override.Name != "" && !f.isBuiltin(override.Name) {
			f.renamedFrom[override.Name] = original
		}
	}
	return f
}

// isBuiltin reports whether name is a gateway-registered tool that bypasses
// the filter.
func (f *toolFilter) isBuiltin(name string) bool {
	if f.builtin[name] {
		return true
	}
	for _, prefix := range f.builtinPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// exposedName returns the name the original tool is exposed under, and
// whether it is exposed at all.
func (f *toolFilter) exposedName(original string) (string, bool) {
	if f.isBuiltin(original) {
		return original, true
	}
	if !f.cfg.Allowed(original) {
		return "", false
	}
	if override, ok := f.overrides[original]; ok && override.Name != "" {
		if f.isBuiltin(override.Name) {
			// 組み込みツールの名前へはリネームできない。組み込み側がその名前を
			// 持ち続け、こちらは隠す。
			return "", false
		}
		return override.Name, true
	}
	if _, shadowed := f.renamedFrom[original]; shadowed {
		// 別のツールがこの名前へリネームされている。リネームした側を優先する。
		return "", false
	}
	return original, true
}

// originalName resolves a tools/call name to the backend's tool name, and
// reports whether that name is exposed.
func (f *toolFilter) originalName(exposed string) (string, bool) {
	if f.isBuiltin(exposed) {
		return exposed, true
	}
	original := exposed
	if from, ok := f.renamedFrom[exposed]; ok {
		original = from
	} else if override, ok := f.overrides[exposed]; ok && override.Name != "" {
		// リネームしたツールは新しい名前でしか呼べない。
		return "", false
	}
	if !f.cfg.Allowed(original) {
		return "", false
	}
	return original, true
}

// apply returns the exposed subset of tools, renamed and re-described per
// the overrides. Tools are copied before being changed: the SDK's tools/list
// returns the server's registered *mcp.Tool values themselves.
func (f *toolFilter) apply(tools []*mcp.Tool) []*mcp.Tool {
	out := make([]*mcp.Tool, 0, len(tools))
	for _, tool := range tools {
		name, ok := f.exposedName(tool.Name)
		if !ok {
			continue
		}
		override := f.overrides[tool.Name]
		if name == tool.Name && override.Description == "" {
			out = append(out, tool)
			continue
		}
		copied := *tool
		copied.Name = name
		if override.Description != "" {
			copied.Description = override.Description
		}
		out = append(out, &copied)
	}
	return out
}

// applyInfos is apply for the /mcp/list tool catalog.
func (f *toolFilter) applyInfos(infos []ToolInfo) []ToolInfo {
	if f == nil {
		return infos
	}
	out := make([]ToolInfo, 0, len(infos))
	for _, info := range infos {
		name, ok := f.exposedName(info.Name)
		if !ok {
			continue
		}
		if override := f.overrides[info.Name]; override.Description != "" {
			info.Description = override.Description
		}
		info.Name = name
		out = append(out, info)
	}
	return out
}

// unknownToolError matches the SDK's own error for a tools/call naming a tool
// the server doesn't have, so a filtered-out tool is indistinguishable from
// a missing one.
func unknownToolError(name string) error {
	return &jsonrpc.Error{
		Code:    jsonrpc.CodeInvalidParams,
		Message: fmt.Sprintf("unknown tool %q", name),
	}
}

// newToolFilterMiddleware returns the middleware applying cfg's include /
// exclude / overrides, or nil when cfg sets none of them. It sits right
// outside the backend (passthrough, service agents, or the SDK's own tool
// handlers), so every outer layer — cache, authz and audit — only ever sees
// the exposed names. builtin names the gateway's own tools on the server,
// which the filter leaves alone (see toolFilter.builtin).
func newToolFilterMiddleware(cfg *config.ToolsConfig, builtin ...string) mcp.Middleware {
	f := newToolFilter(cfg, builtin...)
	if f == nil {
		return nil
	}
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case authzMethodToolsList:
				res, err := next(ctx, method, req)
				if err != nil {
					return nil, err
				}
				if result, ok := res.(*mcp.ListToolsResult); ok {
					copied := *result
					copied.Tools = f.apply(result.Tools)
					return &copied, nil
				}
				return res, nil
			case authzMethodToolsCall:
				params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
				if !ok {
					return next(ctx, method, req)
				}
				original, ok := f.originalName(params.Name)
				if !ok {
					return nil, unknownToolError(params.Name)
				}
				if original == params.Name {
					return next(ctx, method, req)
				}
				// 外側のミドルウェア（監査ログ等）は公開名を見続けるため、
				// params を書き換えずに複製したリクエストを内側へ渡す。
				return next(ctx, method, withToolName(req, original))
			default:
				return next(ctx, method, req)
			}
		}
	}
}

// withToolName returns a copy of the tools/call request req calling name
// instead, leaving req itself untouched. req must carry
// *mcp.CallToolParamsRaw params.
func withToolName(req mcp.Request, name string) mcp.Request {
	params, _ := req.GetParams().(*mcp.CallToolParamsRaw)
	copiedParams := *params
	copiedParams.Name = name
	if typed, ok := req.(*mcp.CallToolRequest); ok {
		copied := *typed
		copied.Params = &copiedParams
		return &copied
	}
	return &mcp.CallToolRequest{Params: &copiedParams, Extra: req.GetExtra()}
}
