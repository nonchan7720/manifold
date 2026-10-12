package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/services/guardrail"
)

// errGuardrailUnavailable is returned when the guardrail could not decide
// (call failed, non-200, invalid answer). Like authz, the guardrail fails
// closed: the call does not proceed unchecked. The reason is logged
// server-side, never echoed back.
var errGuardrailUnavailable = &jsonrpc.Error{
	Code:    jsonrpc.CodeInternalError,
	Message: "guardrail check failed",
}

// guardrailBlockedError reports a call the guardrail blocked. The guardrail's
// blocked_reason is meant for the caller, so it is passed on.
func guardrailBlockedError(reason string) error {
	msg := "blocked by guardrail"
	if reason != "" {
		msg += ": " + reason
	}
	return &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: msg}
}

// NewGuardrailMiddleware runs g on tools/call for server serverName (of
// service serviceCode): with preCall, the arguments before they reach the
// backend; with postCall, the text of the result before it reaches the
// caller. GUARDRAIL_INTERVENED rewrites them, BLOCKED rejects the call, and
// a guardrail failure rejects it too (fail closed).
//
// It sits inside authz, so denied calls never reach the guardrail, and
// outside the tool filter, so it sees the names callers use.
func NewGuardrailMiddleware(
	serverName, serviceCode string, g guardrail.Guardrail, preCall, postCall bool,
) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != authzMethodToolsCall {
				return next(ctx, method, req)
			}
			params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
			if !ok {
				return next(ctx, method, req)
			}
			gp := map[string]any{"server": serverName, "service": serviceCode, "tool": params.Name}
			if preCall {
				var err error
				if req, err = guardrailPreCall(ctx, g, gp, req, params); err != nil {
					return nil, err
				}
			}
			res, err := next(ctx, method, req)
			if err != nil || !postCall {
				return res, err
			}
			result, ok := res.(*mcp.CallToolResult)
			if !ok {
				return res, nil
			}
			return guardrailPostCall(ctx, g, gp, result)
		}
	}
}

// guardrailPreCall checks the arguments, returning req to forward (rewritten
// when the guardrail intervened).
func guardrailPreCall(
	ctx context.Context,
	g guardrail.Guardrail,
	gp map[string]any,
	req mcp.Request,
	params *mcp.CallToolParamsRaw,
) (mcp.Request, error) {
	args := params.Arguments
	if len(bytes.TrimSpace(args)) == 0 {
		args = json.RawMessage("{}")
	}
	res, err := g.Apply(ctx, guardrail.Request{
		Texts: []string{string(args)}, InputType: guardrail.InputTypeRequest, Params: gp,
	})
	if err != nil {
		slog.ErrorContext(ctx, "guardrail failed on tools/call arguments",
			slog.Any("tool", gp["tool"]), slog.Any("server", gp["server"]), slog.Any("error", err))
		return nil, errGuardrailUnavailable
	}
	switch res.Action {
	case guardrail.ActionBlocked:
		logGuardrailDecision(ctx, gp, guardrail.InputTypeRequest, res)
		return nil, guardrailBlockedError(res.BlockedReason)
	case guardrail.ActionIntervened:
		rewritten := json.RawMessage(res.Texts[0])
		var obj map[string]any
		if err := json.Unmarshal(rewritten, &obj); err != nil {
			slog.ErrorContext(
				ctx,
				"guardrail rewrote tools/call arguments into non-object JSON",
				slog.Any(
					"tool",
					gp["tool"],
				),
				slog.Any("server", gp["server"]),
				slog.Any("error", err),
			)
			return nil, errGuardrailUnavailable
		}
		logGuardrailDecision(ctx, gp, guardrail.InputTypeRequest, res)
		return withToolArguments(req, rewritten), nil
	case guardrail.ActionNone:
	}
	return req, nil
}

// guardrailPostCall checks the text of result: every TextContent, then
// StructuredContent (as JSON) when present. Other content (images, resource
// links, ...) passes through unchecked.
func guardrailPostCall(
	ctx context.Context, g guardrail.Guardrail, gp map[string]any, result *mcp.CallToolResult,
) (mcp.Result, error) {
	var (
		texts   []string
		indexes []int
	)
	for i, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			texts = append(texts, tc.Text)
			indexes = append(indexes, i)
		}
	}
	hasStructured := result.StructuredContent != nil
	if hasStructured {
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil {
			return nil, fmt.Errorf("encode structuredContent for the guardrail: %w", err)
		}
		texts = append(texts, string(raw))
	}
	if len(texts) == 0 {
		return result, nil
	}

	res, err := g.Apply(ctx, guardrail.Request{
		Texts: texts, InputType: guardrail.InputTypeResponse, Params: gp,
	})
	if err != nil {
		slog.ErrorContext(ctx, "guardrail failed on tools/call result",
			slog.Any("tool", gp["tool"]), slog.Any("server", gp["server"]), slog.Any("error", err))
		return nil, errGuardrailUnavailable
	}
	switch res.Action {
	case guardrail.ActionBlocked:
		logGuardrailDecision(ctx, gp, guardrail.InputTypeResponse, res)
		return nil, guardrailBlockedError(res.BlockedReason)
	case guardrail.ActionIntervened:
		copied := *result
		copied.Content = append([]mcp.Content(nil), result.Content...)
		for j, i := range indexes {
			tc := *result.Content[i].(*mcp.TextContent)
			tc.Text = res.Texts[j]
			copied.Content[i] = &tc
		}
		if hasStructured {
			var structured any
			if err := json.Unmarshal([]byte(res.Texts[len(texts)-1]), &structured); err != nil {
				slog.ErrorContext(ctx, "guardrail rewrote structuredContent into invalid JSON",
					slog.Any("tool", gp["tool"]), slog.Any("server", gp["server"]),
					slog.Any("error", err))
				return nil, errGuardrailUnavailable
			}
			copied.StructuredContent = structured
		}
		logGuardrailDecision(ctx, gp, guardrail.InputTypeResponse, res)
		return &copied, nil
	case guardrail.ActionNone:
	}
	return result, nil
}

func logGuardrailDecision(
	ctx context.Context, gp map[string]any, input guardrail.InputType, res guardrail.Result,
) {
	slog.InfoContext(ctx, "guardrail decision",
		slog.Any("server", gp["server"]), slog.Any("tool", gp["tool"]),
		slog.String("input", string(input)), slog.String("action", string(res.Action)),
		slog.String("reason", res.BlockedReason))
}

// withToolArguments returns a copy of the tools/call request req with args
// as its arguments, leaving req itself untouched. req must carry
// *mcp.CallToolParamsRaw params.
func withToolArguments(req mcp.Request, args json.RawMessage) mcp.Request {
	params, _ := req.GetParams().(*mcp.CallToolParamsRaw)
	copiedParams := *params
	copiedParams.Arguments = args
	if typed, ok := req.(*mcp.CallToolRequest); ok {
		copied := *typed
		copied.Params = &copiedParams
		return &copied
	}
	return &mcp.CallToolRequest{Params: &copiedParams, Extra: req.GetExtra()}
}
