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

// GuardrailOptions selects what NewGuardrailMiddleware checks.
type GuardrailOptions struct {
	// PreCall checks the tools/call arguments before they reach the backend.
	PreCall bool
	// PostCall checks the tools/call result before it reaches the caller.
	PostCall bool
	// ResourceRead checks the resources/read result before it reaches the
	// caller.
	ResourceRead bool
	// Files also sends the files of a checked result (images, audio, resource
	// blobs) to the guardrail. Without it they pass through unchecked.
	Files bool
	// MaxFileBytes caps the total size of the files of one result. A result
	// over it cannot be checked and is rejected. Zero means no files fit.
	MaxFileBytes int64
}

// NewGuardrailMiddleware runs g for server serverName (of service
// serviceCode) on tools/call and, with opts.ResourceRead, on resources/read:
// with PreCall, the tools/call arguments before they reach the backend; with
// PostCall / ResourceRead, the result before it reaches the caller.
// GUARDRAIL_INTERVENED rewrites the text, BLOCKED rejects the call, and a
// guardrail failure rejects it too (fail closed).
//
// It sits inside authz, so denied calls never reach the guardrail, and
// outside the tool filter, so it sees the names callers use.
func NewGuardrailMiddleware(
	serverName, serviceCode string, g guardrail.Guardrail, opts GuardrailOptions,
) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case authzMethodToolsCall:
				return guardrailToolsCall(ctx, g, serverName, serviceCode, opts, next, req)
			case authzMethodResourcesRead:
				if opts.ResourceRead {
					return guardrailResourcesRead(ctx, g, serverName, serviceCode, opts, next, req)
				}
			}
			return next(ctx, method, req)
		}
	}
}

// guardrailToolsCall checks a tools/call: with opts.PreCall the arguments
// before the backend is called, with opts.PostCall the result after it.
func guardrailToolsCall(
	ctx context.Context,
	g guardrail.Guardrail,
	serverName, serviceCode string,
	opts GuardrailOptions,
	next mcp.MethodHandler,
	req mcp.Request,
) (mcp.Result, error) {
	params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
	if !ok {
		return next(ctx, authzMethodToolsCall, req)
	}
	gp := map[string]any{
		"server": serverName, "service": serviceCode,
		"method": authzMethodToolsCall, "tool": params.Name,
	}
	if opts.PreCall {
		var err error
		if req, err = guardrailPreCall(ctx, g, gp, req, params); err != nil {
			return nil, err
		}
	}
	res, err := next(ctx, authzMethodToolsCall, req)
	if err != nil || !opts.PostCall {
		return res, err
	}
	result, ok := res.(*mcp.CallToolResult)
	if !ok {
		return res, nil
	}
	return guardrailPostCall(ctx, g, gp, opts, result)
}

// guardrailResourcesRead checks the contents of a resources/read result: the
// text of every content and, with opts.Files, the blobs.
func guardrailResourcesRead(
	ctx context.Context,
	g guardrail.Guardrail,
	serverName, serviceCode string,
	opts GuardrailOptions,
	next mcp.MethodHandler,
	req mcp.Request,
) (mcp.Result, error) {
	res, err := next(ctx, authzMethodResourcesRead, req)
	if err != nil {
		return res, err
	}
	result, ok := res.(*mcp.ReadResourceResult)
	if !ok || result == nil {
		return res, nil
	}
	gp := map[string]any{
		"server": serverName, "service": serviceCode, "method": authzMethodResourcesRead,
	}
	if params, ok := req.GetParams().(*mcp.ReadResourceParams); ok && params != nil {
		gp["uri"] = params.URI
	}

	// The struct copy keeps the unexported resultType of result.
	copied := *result
	copied.Contents = append([]*mcp.ResourceContents(nil), result.Contents...)
	var in guardrailResponseInput
	for i, rc := range result.Contents {
		if rc == nil {
			continue
		}
		in.addResource(rc, opts.Files, func(rewritten *mcp.ResourceContents) {
			copied.Contents[i] = rewritten
		})
	}
	changed, err := in.check(ctx, g, gp, opts)
	if err != nil {
		return nil, err
	}
	if !changed {
		return result, nil
	}
	return &copied, nil
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

// guardrailResponseInput collects what a result sends to the guardrail: the
// texts, how to write each one back when the guardrail rewrites it, and the
// files (which the guardrail can only pass or block).
type guardrailResponseInput struct {
	texts   []string
	rewrite []func(text string) error
	files   []guardrail.File
}

// addText adds a text to check. rewrite stores the rewritten text when the
// guardrail rewrites it.
func (in *guardrailResponseInput) addText(text string, rewrite func(text string) error) {
	in.texts = append(in.texts, text)
	in.rewrite = append(in.rewrite, rewrite)
}

// addResource adds the contents of rc: its text, or with files its blob. set
// stores the rewritten copy of rc when the guardrail rewrites the text.
func (in *guardrailResponseInput) addResource(
	rc *mcp.ResourceContents, files bool, set func(rewritten *mcp.ResourceContents),
) {
	if rc.Text != "" {
		in.addText(rc.Text, func(text string) error {
			rewritten := *rc
			rewritten.Text = text
			set(&rewritten)
			return nil
		})
	}
	if files && len(rc.Blob) > 0 {
		in.files = append(in.files, guardrail.File{
			URI: rc.URI, MIMEType: rc.MIMEType, Data: rc.Blob,
		})
	}
}

// check sends in to the guardrail and writes rewritten texts back. It reports
// whether anything was rewritten. A BLOCKED decision, a guardrail failure,
// files over opts.MaxFileBytes and a guardrail that would rewrite a file all
// return the error to answer the caller with.
func (in *guardrailResponseInput) check(
	ctx context.Context, g guardrail.Guardrail, gp map[string]any, opts GuardrailOptions,
) (changed bool, _ error) {
	if len(in.texts) == 0 && len(in.files) == 0 {
		return false, nil
	}
	var fileBytes int64
	for _, f := range in.files {
		fileBytes += int64(len(f.Data))
	}
	if fileBytes > opts.MaxFileBytes {
		// The files cannot be checked, so the result does not go through.
		slog.WarnContext(ctx, "guardrail cannot check a result: files exceed the size limit",
			slog.Any("method", gp["method"]), slog.Any("tool", gp["tool"]),
			slog.Any("server", gp["server"]),
			slog.Int64("bytes", fileBytes), slog.Int64("limit", opts.MaxFileBytes))
		return false, guardrailBlockedError("files exceed the size limit of the guardrail check")
	}

	res, err := g.Apply(ctx, guardrail.Request{
		Texts: in.texts, Files: in.files, InputType: guardrail.InputTypeResponse, Params: gp,
	})
	if err != nil {
		slog.ErrorContext(ctx, "guardrail failed on a result",
			slog.Any("method", gp["method"]), slog.Any("tool", gp["tool"]),
			slog.Any("server", gp["server"]), slog.Any("error", err))
		return false, errGuardrailUnavailable
	}
	switch res.Action {
	case guardrail.ActionBlocked:
		logGuardrailDecision(ctx, gp, guardrail.InputTypeResponse, res)
		return false, guardrailBlockedError(res.BlockedReason)
	case guardrail.ActionIntervened:
		if res.FilesRewritten {
			// The guardrail wants a file changed (an image masked, ...), which
			// cannot be done here. Passing the original on would ignore its
			// decision, so the result does not go through.
			logGuardrailDecision(ctx, gp, guardrail.InputTypeResponse, res)
			return false, guardrailBlockedError(
				"the guardrail would rewrite a file, which cannot be rewritten",
			)
		}
		for i, rewrite := range in.rewrite {
			if err := rewrite(res.Texts[i]); err != nil {
				slog.ErrorContext(ctx, "guardrail rewrote a result into invalid content",
					slog.Any("method", gp["method"]), slog.Any("tool", gp["tool"]),
					slog.Any("server", gp["server"]), slog.Any("error", err))
				return false, errGuardrailUnavailable
			}
		}
		logGuardrailDecision(ctx, gp, guardrail.InputTypeResponse, res)
		return true, nil
	case guardrail.ActionNone:
	}
	return false, nil
}

// guardrailPostCall checks result: the text of every TextContent and embedded
// resource, then StructuredContent (as JSON) when present, and with
// opts.Files the images, audio and embedded resource blobs. Resource links
// are not fetched: the caller reads them with resources/read, which
// opts.ResourceRead checks.
func guardrailPostCall(
	ctx context.Context,
	g guardrail.Guardrail,
	gp map[string]any,
	opts GuardrailOptions,
	result *mcp.CallToolResult,
) (mcp.Result, error) {
	// The struct copy keeps the unexported resultType of result.
	copied := *result
	copied.Content = append([]mcp.Content(nil), result.Content...)
	var in guardrailResponseInput
	for i, c := range result.Content {
		switch v := c.(type) {
		case *mcp.TextContent:
			in.addText(v.Text, func(text string) error {
				rewritten := *v
				rewritten.Text = text
				copied.Content[i] = &rewritten
				return nil
			})
		case *mcp.EmbeddedResource:
			if v.Resource == nil {
				continue
			}
			in.addResource(v.Resource, opts.Files, func(rc *mcp.ResourceContents) {
				rewritten := *v
				rewritten.Resource = rc
				copied.Content[i] = &rewritten
			})
		case *mcp.ImageContent:
			if opts.Files && len(v.Data) > 0 {
				in.files = append(in.files, guardrail.File{MIMEType: v.MIMEType, Data: v.Data})
			}
		case *mcp.AudioContent:
			if opts.Files && len(v.Data) > 0 {
				in.files = append(in.files, guardrail.File{MIMEType: v.MIMEType, Data: v.Data})
			}
		}
	}
	if result.StructuredContent != nil {
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil {
			return nil, fmt.Errorf("encode structuredContent for the guardrail: %w", err)
		}
		in.addText(string(raw), func(text string) error {
			var structured any
			if err := json.Unmarshal([]byte(text), &structured); err != nil {
				return fmt.Errorf("structuredContent is not JSON: %w", err)
			}
			copied.StructuredContent = structured
			return nil
		})
	}
	changed, err := in.check(ctx, g, gp, opts)
	if err != nil {
		return nil, err
	}
	if !changed {
		return result, nil
	}
	return &copied, nil
}

func logGuardrailDecision(
	ctx context.Context, gp map[string]any, input guardrail.InputType, res guardrail.Result,
) {
	// resources/read の URI は秘匿値を含みうるのでログに出さない。
	slog.InfoContext(ctx, "guardrail decision",
		slog.Any("server", gp["server"]), slog.Any("method", gp["method"]),
		slog.Any("tool", gp["tool"]),
		slog.String("input", string(input)), slog.String("action", string(res.Action)),
		slog.String("reason", res.BlockedReason))
}

// withToolArguments returns a copy of the tools/call request req with args
// as its arguments, leaving req itself untouched. req must carry
// *mcp.CallToolParamsRaw params.
func withToolArguments(req mcp.Request, args json.RawMessage) mcp.Request {
	return withCallToolParams(req, func(params *mcp.CallToolParamsRaw) { params.Arguments = args })
}
