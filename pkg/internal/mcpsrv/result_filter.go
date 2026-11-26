package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/jmespath"
)

// 結果の絞り込み（mcpServers.<name>.tools.resultFilter）。
//
// ツールの結果は、エージェントが必要とする量よりずっと大きいことが多い。有効にすると
// ゲートウェイは tools/list で各ツールの input schema に引数（既定 "_jmespath"）を足し、
// 呼び出し元が tools/call でそこへ JMESPath の式を渡すと、
//
//  1. その引数を取り除いてバックエンドを呼び、
//  2. 結果に含まれる JSON（structuredContent と、JSON のテキスト）に式を適用し、
//  3. 式の結果だけを返す。
//
// 式は呼び出し元が書く外部入力なので、評価は作業量に上限のある自前の評価器
// （pkg/internal/jmespath）で行う。

const (
	// maxResultFilterBytes は絞り込みの対象にする JSON 1 つの長さの上限。評価のために
	// JSON を木に展開するので、極端に大きな結果は展開せずに断る。
	maxResultFilterBytes = 32 << 20
	// maxShadowWarnings は、同名の引数を持つツールとして覚えておく数の上限。ツール名は
	// バックエンドが決めるので、際限なく覚えない。
	maxShadowWarnings = 256
)

// resultFilterDescription は足した引数の説明。全ツールの tools/list に載るので短く保つ。
const resultFilterDescription = "Optional JMESPath expression. When set, the tool returns only " +
	"the expression's result instead of the full JSON " +
	`(e.g. "items[?state=='open'].{id: id, title: title}").`

type resultFilter struct {
	server   string
	cfg      *config.ResultFilterConfig
	argument string
	// builtin はゲートウェイ自身が登録するツール（reverse サーバーの
	// create_pairing_code）。バックエンドのツールではないので対象にしない。
	builtin map[string]bool

	// shadowed は、足す引数と同じ名前の引数を既に持つツール。警告を 1 度だけ出すために
	// 覚える。
	mu       sync.Mutex
	shadowed map[string]struct{}
}

// newResultFilterMiddleware は cfg の結果の絞り込みを行うミドルウェアを返す。cfg が
// 無効なら nil。ツールの絞り込み（newToolFilterMiddleware）の内側に置くので、ツール名は
// バックエンドの元の名前で見える。サービスにぶら下げたエージェントのツール
// （newServiceAgentsMiddleware）は更に外側で処理されるため、対象にならない。
func newResultFilterMiddleware(
	server string, cfg *config.ResultFilterConfig, builtin ...string,
) mcp.Middleware {
	if cfg == nil || !cfg.Enabled {
		return nil
	}
	f := &resultFilter{
		server:   server,
		cfg:      cfg,
		argument: cfg.ArgumentName(),
		builtin:  make(map[string]bool, len(builtin)),
		shadowed: map[string]struct{}{},
	}
	for _, name := range builtin {
		f.builtin[name] = true
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
					copied.Tools = f.advertise(ctx, result.Tools)
					return &copied, nil
				}
				return res, nil
			case authzMethodToolsCall:
				return f.call(ctx, next, req)
			}
			return next(ctx, method, req)
		}
	}
}

func (f *resultFilter) applies(tool string) bool {
	return !f.builtin[tool] && f.cfg.Applies(tool)
}

// advertise は対象のツールの input schema に式の引数を足した一覧を返す。tools の要素は
// SDK が保持する登録済みのツールそのものなので、書き換えずに複製する。
func (f *resultFilter) advertise(ctx context.Context, tools []*mcp.Tool) []*mcp.Tool {
	out := make([]*mcp.Tool, len(tools))
	for i, tool := range tools {
		out[i] = f.advertiseTool(ctx, tool)
	}
	return out
}

func (f *resultFilter) advertiseTool(ctx context.Context, tool *mcp.Tool) *mcp.Tool {
	// MCP Apps の UI を持つツールの結果は UI が描画に使うので、絞り込みを勧めない。
	if tool == nil || !f.applies(tool.Name) || hasMCPAppsUI(tool) {
		return tool
	}
	schema, shadows, ok := f.withArgument(tool.InputSchema)
	if !ok {
		return tool
	}
	if shadows {
		f.warnShadowed(ctx, tool.Name)
	}
	copied := *tool
	copied.InputSchema = schema
	// 式を渡すと結果の形は式が決めるので、outputSchema はもう約束できない。残すと、
	// structuredContent を outputSchema で検証するクライアント（TypeScript / Python の
	// SDK）が絞り込んだ結果を拒否する。
	copied.OutputSchema = nil
	return &copied
}

// withArgument は schema（ツールの input schema）に式の引数を足した複製を返す。
// 最上位が "type": "object" のスキーマとして読めなければ ok が false。shadows は、
// 同じ名前のプロパティが既にあって置き換えたことを表す。
func (f *resultFilter) withArgument(schema any) (out map[string]any, shadows, ok bool) {
	if m, isMap := schema.(map[string]any); isMap {
		out = maps.Clone(m)
	} else if err := remarshalJSON(schema, &out); err != nil {
		return nil, false, false
	}
	if typ, _ := out["type"].(string); typ != "object" {
		return nil, false, false
	}
	properties, isMap := out["properties"].(map[string]any)
	if !isMap && out["properties"] != nil {
		return nil, false, false
	}
	properties = maps.Clone(properties)
	if properties == nil {
		properties = map[string]any{}
	}
	_, shadows = properties[f.argument]
	properties[f.argument] = map[string]any{
		"type":        "string",
		"description": resultFilterDescription,
	}
	out["properties"] = properties
	if shadows {
		// 置き換えた引数が必須だと、呼び出し元は式を渡さずにはツールを呼べなくなる。
		out["required"] = withoutRequired(out["required"], f.argument)
	}
	return out, shadows, true
}

// withoutRequired は JSON Schema の required から name を除いたものを返す。
func withoutRequired(required any, name string) any {
	switch names := required.(type) {
	case []string:
		return slices.DeleteFunc(slices.Clone(names), func(n string) bool { return n == name })
	case []any:
		return slices.DeleteFunc(slices.Clone(names), func(n any) bool { return n == name })
	}
	return required
}

func (f *resultFilter) warnShadowed(ctx context.Context, tool string) {
	f.mu.Lock()
	_, known := f.shadowed[tool]
	if !known && len(f.shadowed) < maxShadowWarnings {
		f.shadowed[tool] = struct{}{}
	} else {
		known = true
	}
	f.mu.Unlock()
	if known {
		return
	}
	slog.WarnContext(ctx, "tool already has an argument named like the result filter argument; "+
		"the result filter takes it over (set tools.resultFilter.argument to another name, "+
		"or exclude the tool)",
		slog.String("server", f.server), slog.String("tool", tool),
		slog.String("argument", f.argument))
}

// call は tools/call を処理する。式の引数が無ければそのまま転送する。
func (f *resultFilter) call(
	ctx context.Context, next mcp.MethodHandler, req mcp.Request,
) (mcp.Result, error) {
	params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
	if !ok || !f.applies(params.Name) {
		return next(ctx, authzMethodToolsCall, req)
	}
	expression, rest, present, err := f.takeExpression(params.Arguments)
	if !present {
		return next(ctx, authzMethodToolsCall, req)
	}
	if err != nil {
		return f.errorResult(req, err.Error()+". The tool was not called.")
	}
	// バックエンドは式の引数を知らないので、転送する引数からは取り除く。
	req = withToolArguments(req, rest)
	if strings.TrimSpace(expression) == "" {
		return next(ctx, authzMethodToolsCall, req)
	}
	// 式の誤りは、バックエンドを呼ぶ（副作用が起きる）前に返す。
	query, err := jmespath.Compile(expression)
	if err != nil {
		return f.errorResult(req, fmt.Sprintf(
			"invalid %s: %s. The tool was not called.", f.argument, err.Error()))
	}

	res, err := next(ctx, authzMethodToolsCall, req)
	if err != nil {
		return res, err
	}
	result, ok := res.(*mcp.CallToolResult)
	if !ok || result == nil || result.IsError || result.NeedsInput() {
		// エラーの内容や、追加の入力の要求は絞り込まない。
		return res, nil
	}
	return f.apply(ctx, req, params.Name, query, result)
}

// takeExpression は tools/call の引数から式の引数を取り出す。present は引数に式の
// 引数があったか、rest はそれを除いた引数。式が文字列でも null でもなければ err を返す。
func (f *resultFilter) takeExpression(
	args json.RawMessage,
) (expression string, rest json.RawMessage, present bool, err error) {
	// ほとんどの呼び出しは式を渡さない。大きな引数を毎回デコードしないよう先に探す。
	if !bytes.Contains(args, []byte(`"`+f.argument+`"`)) {
		return "", nil, false, nil
	}
	// オブジェクトでない引数は、今までどおりバックエンドに判断させる（fields は nil のまま）。
	fields := argumentFields(args)
	raw, present := fields[f.argument]
	if !present {
		return "", nil, false, nil
	}
	delete(fields, f.argument)
	if rest, err = json.Marshal(fields); err != nil {
		return "", nil, true, fmt.Errorf("invalid arguments: %w", err)
	}
	if string(bytes.TrimSpace(raw)) == "null" {
		return "", rest, true, nil
	}
	if json.Unmarshal(raw, &expression) != nil {
		return "", rest, true, fmt.Errorf("%s must be a string (a JMESPath expression)", f.argument)
	}
	return expression, rest, true, nil
}

// argumentFields は tools/call の引数を最上位のフィールドに分ける。引数が JSON の
// オブジェクトでなければ nil を返す。
func argumentFields(args json.RawMessage) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(args, &fields) != nil {
		return nil
	}
	return fields
}

// resultFilterRun は 1 回の tools/call での絞り込みの経過。
type resultFilterRun struct {
	query *jmespath.Query
	// documents は式を適用した JSON の数、before / after はその前後の長さの合計。
	documents     int
	before, after int
	// nullOf は式の結果が null になった最初の JSON。
	nullOf *jmespath.Document
	// failedOn は評価に失敗した JSON（読めていれば）。
	failedOn *jmespath.Document
}

// filter は doc に式を適用した結果を返す。doc が JSON として読めなければ ok が false。
func (r *resultFilterRun) filter(doc []byte) (out []byte, ok bool, err error) {
	if len(doc) > maxResultFilterBytes {
		return nil, false, fmt.Errorf(
			"the result is too large to filter (%d bytes, the limit is %d)",
			len(doc), maxResultFilterBytes)
	}
	document, err := jmespath.Decode(doc)
	if err != nil {
		return nil, false, nil //nolint:nilerr // JSON でないテキストは絞り込まずに返す
	}
	if out, err = r.query.Search(document); err != nil {
		r.failedOn = document
		return nil, false, err
	}
	r.documents++
	r.before += len(doc)
	r.after += len(out)
	if r.nullOf == nil && string(out) == "null" {
		r.nullOf = document
	}
	return out, true, nil
}

// apply は result に含まれる JSON へ式を適用した結果を返す。JSON でないテキストや画像、
// リソースはそのまま残す。
func (f *resultFilter) apply(
	ctx context.Context,
	req mcp.Request,
	tool string,
	query *jmespath.Query,
	result *mcp.CallToolResult,
) (mcp.Result, error) {
	// 構造体のコピーは result の非公開の resultType を保つ。
	copied := *result
	copied.Content = slices.Clone(result.Content)
	run := &resultFilterRun{query: query}

	textFiltered, err := run.filterTexts(result.Content, copied.Content)
	if err == nil && result.StructuredContent != nil {
		err = run.filterStructured(&copied, textFiltered)
	}
	if err != nil {
		return f.evaluationError(req, run, err)
	}

	switch {
	case run.documents == 0:
		copied.Content = append(copied.Content, &mcp.TextContent{
			Text: f.argument + " was not applied: the result is not JSON.",
		})
	case run.nullOf != nil:
		// 式が null を返すのは、たいてい最上位のキーの見当が外れているとき。形を添えて、
		// 全体を取り直さずに式を直せるようにする。
		copied.Content = append(copied.Content, &mcp.TextContent{
			Text: fmt.Sprintf("%s returned null. The result is %s.",
				f.argument, run.nullOf.Describe()),
		})
	}
	slog.DebugContext(ctx, "tool result filtered",
		slog.String("server", f.server), slog.String("tool", tool),
		slog.Int("documents", run.documents),
		slog.Int("bytes_before", run.before), slog.Int("bytes_after", run.after))
	return &copied, nil
}

// filterTexts は content のうち JSON のテキストに式を適用し、結果を out の同じ位置へ
// 入れる。1 つでも適用したら filtered が true。
func (r *resultFilterRun) filterTexts(content, out []mcp.Content) (filtered bool, _ error) {
	for i, c := range content {
		text, ok := c.(*mcp.TextContent)
		if !ok || !startsLikeJSONContainer(text.Text) {
			continue
		}
		result, ok, err := r.filter([]byte(text.Text))
		if err != nil {
			return filtered, err
		}
		if !ok {
			continue
		}
		rewritten := *text
		rewritten.Text = string(result)
		out[i] = &rewritten
		filtered = true
	}
	return filtered, nil
}

// filterStructured は result の structuredContent に式を適用する。式の結果を載せた
// テキストが無ければ（textFiltered が false）、テキストとしても足す。
func (r *resultFilterRun) filterStructured(result *mcp.CallToolResult, textFiltered bool) error {
	raw, err := structuredContentJSON(result.StructuredContent)
	if err != nil {
		return fmt.Errorf("structuredContent is not JSON: %w", err)
	}
	out, ok, err := r.filter(raw)
	if err != nil || !ok {
		return err
	}
	// structuredContent は、古いプロトコルのクライアントが受け付けるオブジェクトの
	// ときだけ返す。配列やスカラーになった結果はテキストで返る。
	result.StructuredContent = nil
	if len(out) > 0 && out[0] == '{' {
		result.StructuredContent = json.RawMessage(out)
	}
	if !textFiltered {
		result.Content = append(result.Content, &mcp.TextContent{Text: string(out)})
	}
	return nil
}

// structuredContentJSON は structuredContent を JSON にする。
func structuredContentJSON(structured any) ([]byte, error) {
	if raw, ok := structured.(json.RawMessage); ok {
		return raw, nil
	}
	return json.Marshal(structured)
}

// startsLikeJSONContainer は text が JSON のオブジェクトか配列で始まるかを返す。数値や
// 真偽値だけのテキストは、JSON ではなく普通の文章として扱う。
func startsLikeJSONContainer(text string) bool {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

// evaluationError は、バックエンドの呼び出しは成功したが式を評価できなかったことを
// ツールのエラーとして返す。結果そのものは返さない（絞り込みたかった大きさのまま返すと、
// 呼び出し元のコンテキストを埋めてしまう）。
func (f *resultFilter) evaluationError(
	req mcp.Request, run *resultFilterRun, err error,
) (mcp.Result, error) {
	msg := fmt.Sprintf("%s could not be evaluated: %s. The tool call itself completed, "+
		"so do not repeat it for its side effects.", f.argument, err.Error())
	if run.failedOn != nil {
		msg += " The result is " + run.failedOn.Describe() + "."
	}
	return f.errorResult(req, msg)
}

// errorResult は msg を内容とするツールのエラー（isError）を返す。呼び出し元の LLM が
// 読んで式を直せるよう、JSON-RPC のエラーにはしない。
func (f *resultFilter) errorResult(req mcp.Request, msg string) (mcp.Result, error) {
	result := &mcp.CallToolResult{}
	result.SetError(errors.New(msg))
	// SDK の組み込みハンドラを通らない結果なので、resultType を補う。
	return completeRelayedResult(req, result)
}
