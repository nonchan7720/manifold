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
// 式でできるのは、返すフィールドを選ぶこと。条件で絞る構文（[?...]）と、位置・件数で
// 絞る構文（[0]、[:5]）は、tools.resultFilter.features でツールごとに有効にする。
// 集計・変換・並べ替えなどの関数は使えない。式は呼び出し元が書く外部入力なので、評価は
// 作業量に上限のある自前の評価器（pkg/internal/jmespath）で行う。

const (
	// maxResultFilterBytes は絞り込みの対象にする JSON 1 つの長さの上限。評価のために
	// JSON を木に展開するので、極端に大きな結果は展開せずに断る。
	maxResultFilterBytes = 32 << 20
	// maxShadowWarnings は、同名の引数を持つツールとして覚えておく数の上限。ツール名は
	// バックエンドが決めるので、際限なく覚えない。
	maxShadowWarnings = 256
	// structuredResultKey は、オブジェクトでない式の結果を structuredContent に載せる
	// ときのキー。structuredHintKey は、そこに添える手がかりのキー。
	structuredResultKey = "result"
	structuredHintKey   = "hint"
)

// resultFilterDescription は足した引数の説明を返す。対象の全ツールの tools/list に載る
// ので短く保つ。使える構文と使えない構文を書くのは、呼び出し元が試して失敗する往復を
// 減らすため。
func resultFilterDescription(features jmespath.Features) string {
	example := "items[].{id: id, title: title}"
	supported := []string{"field selection"}
	unsupported := []string{}
	if features.Conditions {
		example = "items[?state=='open'].{id: id, title: title}"
		supported = append(supported, "[?conditions]")
	} else {
		unsupported = append(unsupported, "[?conditions]")
	}
	if features.Indexing {
		supported = append(supported, "[index] and [start:stop] slices")
	} else {
		unsupported = append(unsupported, "[index], slices")
	}
	unsupported = append(unsupported, "functions")
	return fmt.Sprintf("Optional JMESPath expression. When set, the tool returns only the "+
		"expression's result instead of the full JSON, e.g. %q. Supported: %s. Not supported: %s.",
		example, strings.Join(supported, ", "), strings.Join(unsupported, ", "))
}

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

// features は tool（元のツール名）で使える追加の構文を返す。
func (f *resultFilter) features(tool string) jmespath.Features {
	return jmespath.Features{
		Conditions: f.cfg.ConditionsEnabled(tool),
		Indexing:   f.cfg.IndexingEnabled(tool),
	}
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
	if tool == nil || !f.applies(tool.Name) {
		return tool
	}
	schema, shadows, ok := f.withArgument(tool.InputSchema, f.features(tool.Name))
	if !ok {
		return tool
	}
	if shadows {
		f.warnShadowed(ctx, tool.Name)
	}
	if hasMCPAppsUI(tool) {
		// MCP Apps の UI を持つツールの結果は UI が描画に使うので、絞り込みを勧めない
		// （引数を足さない）。ただし tools/call の時点ではツールが UI を持つかが分からない
		// ので（MCP バックエンドの tools/list は毎回バックエンドへ転送していて、手元に
		// 一覧が無い）、式の引数が送られてくれば絞り込む。ツール自身が同じ名前の引数を
		// 持つ場合に上の警告を出すのは、UI を持つツールでもその引数が使えなくなるため。
		return tool
	}
	copied := *tool
	copied.InputSchema = schema
	if tool.OutputSchema != nil {
		copied.OutputSchema = permissiveOutputSchema(tool.OutputSchema)
	}
	return &copied
}

// withArgument は schema（ツールの input schema）に式の引数を足した複製を返す。
// 最上位が "type": "object" のスキーマとして読めなければ ok が false。shadows は、
// 同じ名前のプロパティが既にあって置き換えたことを表す。
func (f *resultFilter) withArgument(
	schema any, features jmespath.Features,
) (out map[string]any, shadows, ok bool) {
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
		"description": resultFilterDescription(features),
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

// permissiveOutputSchema は outputSchema を、元の内容を読めるまま、どんな結果でも検証を
// 通る形にして返す。
//
// outputSchema は「結果にどんなフィールドがあるか」を呼び出し元に伝える唯一の記述なので、
// 消さずに残す。一方で、式を渡した呼び出しの結果は式が形を決めるので、元のスキーマには
// 合わない。TypeScript / Python の SDK は structuredContent を outputSchema で検証し、
// 合わない結果を拒否するため、元のスキーマは anyOf の選択肢の 1 つ（もう 1 つは何でも
// 通す {}）として載せる。
func permissiveOutputSchema(schema any) any {
	var original map[string]any
	if err := remarshalJSON(schema, &original); err != nil || original == nil {
		// true / false などオブジェクトでないスキーマは、そのまま返す。
		return schema
	}
	out := map[string]any{}
	if original["type"] == "object" {
		// 式の結果は、オブジェクトでなければ {"result": ...} に包んで返す（structuredResult）。
		out["type"] = "object"
	}
	for _, key := range []string{"title", "description"} {
		if v, ok := original[key]; ok {
			out[key] = v
		}
	}
	nested := maps.Clone(original)
	if _, hasID := nested["$id"]; !hasID {
		// $id が無ければ、"#/$defs/..." の参照は文書の最上位から解決される。元のスキーマを
		// 1 段下げても参照が切れないよう、定義は最上位へ移す。
		if hasPointerRefOutsideDefinitions(original) {
			// 定義以外の場所を指す参照（"#/properties/..." など）は移せない。元のスキーマは
			// 載せずに、何でも通すスキーマだけを返す。
			return out
		}
		for _, key := range []string{"$schema", "$defs", "definitions"} {
			if v, ok := nested[key]; ok {
				out[key] = v
				delete(nested, key)
			}
		}
	}
	out["anyOf"] = []any{nested, map[string]any{}}
	return out
}

// hasPointerRefOutsideDefinitions は schema のどこかに、定義（$defs / definitions）以外の
// 場所を JSON Pointer で指す $ref があるかを返す。
func hasPointerRefOutsideDefinitions(schema any) bool {
	switch v := schema.(type) {
	case map[string]any:
		if ref, ok := v["$ref"].(string); ok && strings.HasPrefix(ref, "#/") &&
			!strings.HasPrefix(ref, "#/$defs/") && !strings.HasPrefix(ref, "#/definitions/") {
			return true
		}
		for _, child := range v {
			if hasPointerRefOutsideDefinitions(child) {
				return true
			}
		}
	case []any:
		return slices.ContainsFunc(v, hasPointerRefOutsideDefinitions)
	}
	return false
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
	// 式の誤り（構文エラー、このツールで有効にしていない構文、関数）は、バックエンドを
	// 呼ぶ（副作用が起きる）前に返す。
	query, err := jmespath.Compile(expression, f.features(params.Name))
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
	// キーは "\u005fjmespath" のようにエスケープしても書けるので、バックスラッシュを
	// 含む引数は探さずにデコードして確かめる。
	if !bytes.Contains(args, []byte(`"`+f.argument+`"`)) && !bytes.ContainsRune(args, '\\') {
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
	// nulls は式の結果が null になった JSON の数、nullOf はその最初のもの。
	nulls  int
	nullOf *jmespath.Document
	// failedOn は評価に失敗した JSON（読めていれば）。
	failedOn *jmespath.Document
	// texts はテキストに適用した式の結果。structured は structuredContent に適用した
	// 式の結果（structuredContent が無ければ nil）。
	texts      [][]byte
	structured []byte
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
	if string(out) == "null" {
		r.nulls++
		if r.nullOf == nil {
			r.nullOf = document
		}
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

	err := run.filterTexts(result.Content, copied.Content)
	if err == nil && result.StructuredContent != nil {
		err = run.filterStructured(&copied)
	}
	if err != nil {
		return f.evaluationError(req, run, err)
	}

	hint := ""
	switch {
	case run.documents == 0:
		hint = f.argument + " was not applied: the result is not JSON."
	case run.nulls == run.documents:
		// 式が null しか返さないのは、たいてい最上位のキーの見当が外れているとき。形を
		// 添えて、全体を取り直さずに式を直せるようにする。
		hint = fmt.Sprintf("%s returned null. The result is %s.",
			f.argument, run.nullOf.Describe())
	}
	if hint != "" {
		copied.Content = append(copied.Content, &mcp.TextContent{Text: hint})
	}
	if run.structured != nil {
		copied.StructuredContent = structuredResult(run.structured, hint)
	}
	slog.DebugContext(ctx, "tool result filtered",
		slog.String("server", f.server), slog.String("tool", tool),
		slog.Int("documents", run.documents),
		slog.Int("bytes_before", run.before), slog.Int("bytes_after", run.after))
	return &copied, nil
}

// filterTexts は content のうち JSON のテキストに式を適用し、結果を out の同じ位置へ
// 入れる。
func (r *resultFilterRun) filterTexts(content, out []mcp.Content) error {
	for i, c := range content {
		text, ok := c.(*mcp.TextContent)
		if !ok || !startsLikeJSONContainer(text.Text) {
			continue
		}
		result, ok, err := r.filter([]byte(text.Text))
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		rewritten := *text
		rewritten.Text = string(result)
		out[i] = &rewritten
		r.texts = append(r.texts, result)
	}
	return nil
}

// filterStructured は result の structuredContent に式を適用し、結果を r.structured に
// 入れる。同じ値を載せたテキストが無ければ、テキストとしても足す（テキストしか読まない
// 呼び出し元にも届くように。テキストの JSON と structuredContent が別の内容のことも
// ある）。
func (r *resultFilterRun) filterStructured(result *mcp.CallToolResult) error {
	raw, err := structuredContentJSON(result.StructuredContent)
	if err != nil {
		return fmt.Errorf("structuredContent is not JSON: %w", err)
	}
	out, ok, err := r.filter(raw)
	if err != nil || !ok {
		return err
	}
	r.structured = out
	// テキストと structuredContent はキーの順序や数値の表記が違うことがあるので、
	// バイト列ではなく値で比べる。
	inText := slices.ContainsFunc(r.texts, func(text []byte) bool {
		return jmespath.Equal(text, out)
	})
	if !inText {
		result.Content = append(result.Content, &mcp.TextContent{Text: string(out)})
	}
	return nil
}

// structuredResult は式の結果 out を structuredContent に載せる形で返す。
//
// structuredContent はオブジェクトでなければならず（古いプロトコルのクライアントは配列や
// スカラーを受け付けない）、outputSchema を持つツールでは省略もできない（SDK が「結果に
// structuredContent が無い」と拒否する）。そのため、オブジェクトでない結果は
// {"result": ...} に包む。hint があれば、structuredContent しか読まない呼び出し元にも
// 届くよう、包んだ結果に添える（オブジェクトの結果は式が選んだ形のまま返す）。
func structuredResult(out []byte, hint string) json.RawMessage {
	if len(out) > 0 && out[0] == '{' {
		return out
	}
	wrapped := map[string]json.RawMessage{structuredResultKey: out}
	if hint != "" {
		// 文字列の JSON 化は失敗しない。
		wrapped[structuredHintKey], _ = json.Marshal(hint)
	}
	encoded, err := json.Marshal(wrapped)
	if err != nil {
		// out は評価器が作った JSON なので、ここには来ない。
		return out
	}
	return encoded
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
