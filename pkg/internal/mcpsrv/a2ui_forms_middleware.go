package mcpsrv

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
)

// A2UI over MCP による、書き込み系ツールの入力フォーム（mcpServers.<name>.a2ui.forms）。
//
// A2UI over MCP では、サーバーがレイアウトのテンプレートを a2ui:// リソース
// （application/a2ui+json）として公開し、ツールの _meta.ui.resourceUri で結び付ける。
// ホストはテンプレートを resources/read で取得してネイティブに描画し、tools/call の
// 結果に埋め込まれた updateDataModel で値を流し込む。
//
// OpenAPI モードのサーバーで、読み取り専用でないツール（POST / PUT / PATCH /
// DELETE など）に対して次を行う。
//
//   - tools/list: A2UI 対応の呼び出し元には、ツールの _meta.ui にフォームの
//     リソースを付け、送信用のツール <tool>__submit を並べる。
//   - tools/call <tool>: A2UI 対応の呼び出し元には API を実行せず、モデルの引数を
//     初期値にした updateDataModel を返す（フォームが表示される）。
//   - tools/call <tool>__submit: フォームの送信を受けたエージェント側が呼ぶ。値を
//     入力スキーマの型に直し、<tool> として内側へ渡して実行する。
//   - resources/list・resources/read: ツールごとのフォームのテンプレートを返す。
//
// A2UI に対応しない呼び出し元には何も変えず、<tool> は従来どおり直接実行される。
//
// フォームの送信ボタンは A2UI の action（イベント）を発火するだけで、UI から MCP は
// 呼ばない。イベント名は <tool>__submit、context はその引数なので、イベントを
// 受けたエージェント側（ホスト）がそのまま tools/call に渡す。実行の主体を
// エージェント側に置くことで、UI に MCP の接続や認証情報を持たせずに済み、
// 送信結果もエージェントに返る。
//
// このミドルウェアは最も外側（metrics・audit・authz より外）に置く。
// <tool>__submit を <tool> に書き換えてから内側へ渡すので、authz・guardrail・
// 監査・メトリクスは実際に実行されるツール名で掛かる。フォームを出す対象は、内側の
// tools/list（tool filter と authz を通ったもの）で呼び出し元に見えるツールに限る。
//
// <tool>__submit は通常のツールで、ユーザーが実際に送信したかどうかをゲートウェイは
// 知らない。人の確認を強制する仕組みではなく、確認の機会を与える仕組みである。
// 強制したいホストは、ユーザーの送信イベントと一致する呼び出しだけを通すよう、
// ホスト側（ツール呼び出しの許可フックなど）で <tool>__submit を絞る。

const (
	// a2uiMetaKey はリクエストの _meta で A2UI のレンダラの能力を申告するキー。
	a2uiMetaKey = "a2ui"
	// a2uiFormURIPrefix はフォームのテンプレートのリソース URI の接頭辞。
	a2uiFormURIPrefix = "a2ui://form/"
	// a2uiSubmitSuffix はフォームの送信用ツールの名前の接尾辞。
	a2uiSubmitSuffix = "__submit"
	// a2uiSurfacePrefix はフォームの surfaceId の接頭辞。
	a2uiSurfacePrefix = "manifold-form-"
)

func a2uiFormURI(tool string) string {
	return a2uiFormURIPrefix + url.PathEscape(tool)
}

// a2uiFormToolFromURI はフォームのリソース URI からツール名を返す。
func a2uiFormToolFromURI(uri string) (string, bool) {
	escaped, ok := strings.CutPrefix(uri, a2uiFormURIPrefix)
	if !ok || escaped == "" || strings.Contains(escaped, "/") {
		return "", false
	}
	tool, err := url.PathUnescape(escaped)
	if err != nil || tool == "" {
		return "", false
	}
	return tool, true
}

// a2uiFormUIMeta はツールとその結果の _meta.ui に入れる、フォームへのリンク。
func a2uiFormUIMeta(tool string) map[string]any {
	return map[string]any{"resourceUri": a2uiFormURI(tool), "mimeType": a2uiMIMEType}
}

// a2uiFormTarget は tool がフォームを付ける対象かを返す。OpenAPI のツールは HTTP
// メソッド（と x-mcp.annotations）から annotations が付くので、それが読み取り専用で
// ないものを対象にする。annotations の無いツール（エージェントのスキル等）と、
// 既に UI を持つツールは対象にしない。
func a2uiFormTarget(tool *mcp.Tool) ([]*a2uiField, bool) {
	if tool == nil || tool.Annotations == nil || tool.Annotations.ReadOnlyHint {
		return nil, false
	}
	if _, hasUI := tool.Meta[mcpAppsMetaKey]; hasUI {
		return nil, false
	}
	return buildA2UIFields(tool.InputSchema)
}

// callerDeclaredA2UI は meta（リクエストの _meta）の a2ui から、呼び出し元が
// このフォームを描画できるかを返す。declared は申告があったかどうか。
// 描画には v1.0 の basic カタログが要る。
func callerDeclaredA2UI(meta mcp.Meta) (supported, declared bool) {
	raw, ok := meta[a2uiMetaKey]
	if !ok {
		return false, false
	}
	decl, _ := raw.(map[string]any)
	caps, _ := decl["rendererCapabilities"].(map[string]any)
	v1, _ := caps[a2uiProtocolVersion].(map[string]any)
	ids, _ := v1["supportedCatalogIds"].([]any)
	return slices.Contains(ids, any(a2uiBasicCatalogID)), true
}

// a2uiRequestMeta は tools/list・tools/call のリクエストの _meta を返す。
func a2uiRequestMeta(req mcp.Request) mcp.Meta {
	if req == nil {
		return nil
	}
	switch params := req.GetParams().(type) {
	case *mcp.CallToolParamsRaw:
		if params != nil {
			return params.Meta
		}
	case *mcp.ListToolsParams:
		if params != nil {
			return params.Meta
		}
	}
	return nil
}

// a2uiFormsMiddleware は newA2UIFormsMiddleware の実体。
type a2uiFormsMiddleware struct {
	server        string
	assumeSupport bool
	// inline はフォーム全体をツール結果のテキストに載せるか（a2ui.inline）。
	inline bool
}

// newA2UIFormsMiddleware は書き込み系ツールの A2UI フォームを配信するミドルウェアを
// 返す。cfg.A2UI.Forms が有効な OpenAPI モードのサーバーに、他のミドルウェアより
// 外側へ追加する。
func newA2UIFormsMiddleware(name string, cfg *config.Server) mcp.Middleware {
	m := &a2uiFormsMiddleware{
		server:        name,
		assumeSupport: cfg != nil && cfg.A2UI != nil && cfg.A2UI.AssumeSupport,
		inline:        cfg != nil && cfg.A2UI != nil && cfg.A2UI.Inline,
	}
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case authzMethodToolsList:
				return m.listTools(ctx, next, req)
			case authzMethodToolsCall:
				return m.callTool(ctx, next, req)
			case authzMethodResourcesList:
				return m.listResources(ctx, next, req)
			case authzMethodResourcesRead:
				return m.readResource(ctx, next, req)
			default:
				return next(ctx, method, req)
			}
		}
	}
}

// supports は req の呼び出し元を A2UI 対応とみなすかを返す。リクエストの申告を
// 優先し、申告が無ければ a2ui.assumeSupport に従う。
func (m *a2uiFormsMiddleware) supports(req mcp.Request) bool {
	if supported, declared := callerDeclaredA2UI(a2uiRequestMeta(req)); declared {
		return supported
	}
	return m.assumeSupport
}

func (m *a2uiFormsMiddleware) listTools(
	ctx context.Context, next mcp.MethodHandler, req mcp.Request,
) (mcp.Result, error) {
	res, err := next(ctx, authzMethodToolsList, req)
	if err != nil || !m.supports(req) {
		return res, err
	}
	list, ok := res.(*mcp.ListToolsResult)
	if !ok || list == nil {
		return res, nil
	}
	names := make(map[string]struct{}, len(list.Tools))
	for _, tool := range list.Tools {
		if tool != nil {
			names[tool.Name] = struct{}{}
		}
	}
	// 内側（SDK やキャッシュ）が保持している結果を書き換えないよう、複製して足す。
	tools := make([]*mcp.Tool, 0, len(list.Tools))
	for _, tool := range list.Tools {
		if _, target := a2uiFormTarget(tool); !target {
			tools = append(tools, tool)
			continue
		}
		submitName := tool.Name + a2uiSubmitSuffix
		if _, collides := names[submitName]; collides {
			slog.WarnContext(ctx,
				"a2ui form skipped: the submit tool name collides with an existing tool",
				slog.String("server", m.server), slog.String("tool", tool.Name))
			tools = append(tools, tool)
			continue
		}
		withForm := *tool
		withForm.Meta = maps.Clone(tool.Meta)
		if withForm.Meta == nil {
			withForm.Meta = mcp.Meta{}
		}
		withForm.Meta[mcpAppsMetaKey] = a2uiFormUIMeta(tool.Name)

		submit := *tool
		submit.Name = submitName
		submit.Title = ""
		submit.Description = fmt.Sprintf(
			"Submits the %s form. Call this only after the user has submitted the form "+
				"(the form's %s action), with the action's context as the arguments; "+
				"it executes %s.", tool.Name, submitName, tool.Name)
		submit.Meta = nil
		tools = append(tools, &withForm, &submit)
	}
	copied := *list
	copied.Tools = tools
	return &copied, nil
}

// lookupTools は内側の tools/list（呼び出し元に見えるツール）から names の各ツールを
// 探して返す。見つからなかった名前はキーに入らない。
func (m *a2uiFormsMiddleware) lookupTools(
	ctx context.Context, next mcp.MethodHandler, req mcp.Request, names ...string,
) (map[string]*mcp.Tool, error) {
	found := make(map[string]*mcp.Tool, len(names))
	err := walkServiceTools(ctx, next, req, "", func(tool *mcp.Tool) bool {
		if tool != nil && slices.Contains(names, tool.Name) {
			found[tool.Name] = tool
		}
		return len(found) == len(names)
	})
	return found, err
}

func (m *a2uiFormsMiddleware) callTool(
	ctx context.Context, next mcp.MethodHandler, req mcp.Request,
) (mcp.Result, error) {
	callReq, ok := req.(*mcp.CallToolRequest)
	if !ok || callReq == nil || callReq.Params == nil {
		// 想定外の params は内側に任せてエラーにさせる。
		return next(ctx, authzMethodToolsCall, req)
	}
	name := callReq.Params.Name
	base, isSubmit := strings.CutSuffix(name, a2uiSubmitSuffix)
	if !isSubmit && !m.supports(req) {
		return next(ctx, authzMethodToolsCall, req)
	}

	lookup := []string{name}
	if isSubmit && base != "" {
		lookup = append(lookup, base)
	}
	tools, err := m.lookupTools(ctx, next, req, lookup...)
	if err != nil {
		// 一覧を取れないときはフォームを諦め、内側の通常の処理に任せる。
		slog.WarnContext(ctx, "a2ui form skipped: tools/list failed",
			slog.String("server", m.server), slog.String("tool", name), slog.Any("error", err))
		return next(ctx, authzMethodToolsCall, req)
	}

	if tool, exists := tools[name]; exists {
		// 実在するツール。名前が __submit で終わっていても、そのツール自身が優先。
		fields, target := a2uiFormTarget(tool)
		if !target || !m.supports(req) {
			return next(ctx, authzMethodToolsCall, req)
		}
		return m.formResult(ctx, req, tool, fields, callReq.Params.Arguments)
	}
	if !isSubmit {
		return next(ctx, authzMethodToolsCall, req)
	}
	fields, target := a2uiFormTarget(tools[base])
	if !target {
		return next(ctx, authzMethodToolsCall, req)
	}
	return m.submit(ctx, next, callReq, base, fields)
}

// decodeA2UIArguments は tools/call の arguments を map に直す。数値は桁を落とさない
// よう json.Number のまま持つ。空なら空の map。
func decodeA2UIArguments(raw json.RawMessage) (map[string]any, error) {
	args := map[string]any{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return args, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&args); err != nil {
		return nil, err
	}
	if args == nil {
		args = map[string]any{}
	}
	return args, nil
}

// formResult は <tool> の呼び出しに対して、API を実行せずフォームを返す。
func (m *a2uiFormsMiddleware) formResult(
	ctx context.Context,
	req mcp.Request,
	tool *mcp.Tool,
	fields []*a2uiField,
	arguments json.RawMessage,
) (mcp.Result, error) {
	args, err := decodeA2UIArguments(arguments)
	if err != nil {
		// 引数を読めなくても、空のフォームは出せる。
		args = map[string]any{}
	}
	if m.inline {
		return m.inlineFormResult(ctx, req, tool, fields, args)
	}
	data, err := json.Marshal([]any{map[string]any{
		"version": a2uiProtocolVersion,
		"updateDataModel": map[string]any{
			"surfaceId": a2uiSurfacePrefix + tool.Name,
			"path":      "/",
			"value":     a2uiInitialData(fields, args),
		},
	}})
	if err != nil {
		return nil, fmt.Errorf("marshal a2ui form data: %w", err)
	}
	slog.InfoContext(ctx, "a2ui form returned instead of calling the tool",
		slog.String("server", m.server), slog.String("tool", tool.Name))
	res := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf(
				"A form for %s was shown to the user, prefilled with the given arguments. "+
					"The request has NOT been sent yet: it is sent when the user submits "+
					"the form. Do not call %s again unless the user asks.",
				tool.Name, tool.Name)},
			&mcp.EmbeddedResource{
				Resource: &mcp.ResourceContents{
					URI:      a2uiFormURI(tool.Name) + "/data",
					MIMEType: a2uiMIMEType,
					Text:     string(data),
				},
				// 描画用の JSON はモデルのコンテキストに入れない。
				Annotations: &mcp.Annotations{Audience: []mcp.Role{"user"}},
			},
		},
		Meta: mcp.Meta{mcpAppsMetaKey: a2uiFormUIMeta(tool.Name)},
	}
	return completeRelayedResult(req, res)
}

// inlineFormResult は a2ui.inline のときの formResult。フォーム全体を A2UI Express の
// <a2ui> ブロックにして、エージェントがそのまま出力できるようテキストで返す。
// エージェント側の A2UI Agent SDK がそれを A2UI メッセージにコンパイルする。
//
// surfaceId は呼び出しごとに変える。A2UI は既存の surfaceId での createSurface を
// エラーにするので、同じツールのフォームを 2 回出せるようにするため。
func (m *a2uiFormsMiddleware) inlineFormResult(
	ctx context.Context,
	req mcp.Request,
	tool *mcp.Tool,
	fields []*a2uiField,
	args map[string]any,
) (mcp.Result, error) {
	submitTool := tool.Name + a2uiSubmitSuffix
	block, err := buildA2UIExpressForm(
		m.formSpec(tool, fields, a2uiSurfacePrefix+tool.Name+"-"+rand.Text()[:8]),
		a2uiInitialData(fields, args),
	)
	if err != nil {
		return nil, fmt.Errorf("build a2ui express form: %w", err)
	}
	slog.InfoContext(ctx, "a2ui form returned instead of calling the tool",
		slog.String("server", m.server), slog.String("tool", tool.Name))
	res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(
		"The %s request has NOT been sent yet. Show the user the form by including "+
			"the <a2ui> block below in your reply exactly as it is, without changing "+
			"it. When the user submits the form you receive an action named %s: call "+
			"the %s tool with that action's context as the arguments, unchanged. "+
			"Do not call %s before that.\n\n%s",
		tool.Name, submitTool, submitTool, submitTool, block)}}}
	return completeRelayedResult(req, res)
}

// formSpec は tool のフォームを組み立てるための入力を返す。
func (m *a2uiFormsMiddleware) formSpec(
	tool *mcp.Tool, fields []*a2uiField, surfaceID string,
) a2uiFormSpec {
	title := tool.Title
	if title == "" {
		title = tool.Name
	}
	return a2uiFormSpec{
		surfaceID:   surfaceID,
		title:       title,
		description: truncateRunes(strings.TrimSpace(tool.Description), maxA2UIFormTextRunes),
		submitTool:  tool.Name + a2uiSubmitSuffix,
		fields:      fields,
	}
}

// submit は <tool>__submit を <tool> の呼び出しに直して内側へ渡す。
func (m *a2uiFormsMiddleware) submit(
	ctx context.Context,
	next mcp.MethodHandler,
	req *mcp.CallToolRequest,
	tool string,
	fields []*a2uiField,
) (mcp.Result, error) {
	params := *req.Params
	params.Name = tool
	// 引数を読めないときは書き換えずに渡し、ツール側のエラーにさせる。
	if args, err := decodeA2UIArguments(req.Params.Arguments); err == nil {
		coerced, err := a2uiCoerceInput(fields, args)
		if err != nil {
			res := &mcp.CallToolResult{}
			res.SetError(err)
			return completeRelayedResult(req, res)
		}
		raw, err := json.Marshal(coerced)
		if err != nil {
			return nil, fmt.Errorf("marshal a2ui form input: %w", err)
		}
		params.Arguments = raw
	}
	return next(ctx, authzMethodToolsCall, &mcp.CallToolRequest{
		Session: req.Session,
		Params:  &params,
		Extra:   req.Extra,
	})
}

func (m *a2uiFormsMiddleware) listResources(
	ctx context.Context, next mcp.MethodHandler, req mcp.Request,
) (mcp.Result, error) {
	res := &mcp.ListResourcesResult{Resources: []*mcp.Resource{}}
	err := walkServiceTools(ctx, next, req, "", func(tool *mcp.Tool) bool {
		if _, target := a2uiFormTarget(tool); target {
			res.Resources = append(res.Resources, &mcp.Resource{
				URI:         a2uiFormURI(tool.Name),
				Name:        tool.Name + " form",
				Description: "A2UI input form for the " + tool.Name + " tool.",
				MIMEType:    a2uiMIMEType,
			})
		}
		return false
	})
	if err != nil {
		return nil, err
	}
	normalizeCacheable(&res.Cacheable)
	return res, nil
}

func (m *a2uiFormsMiddleware) readResource(
	ctx context.Context, next mcp.MethodHandler, req mcp.Request,
) (mcp.Result, error) {
	params, _ := req.GetParams().(*mcp.ReadResourceParams)
	if params == nil {
		return nil, mcp.ResourceNotFoundError("")
	}
	name, ok := a2uiFormToolFromURI(params.URI)
	if !ok {
		return nil, mcp.ResourceNotFoundError(params.URI)
	}
	tools, err := m.lookupTools(ctx, next, req, name)
	if err != nil {
		return nil, err
	}
	tool := tools[name]
	fields, target := a2uiFormTarget(tool)
	if !target {
		return nil, mcp.ResourceNotFoundError(params.URI)
	}
	template, err := json.Marshal(buildA2UIFormTemplate(
		m.formSpec(tool, fields, a2uiSurfacePrefix+tool.Name)))
	if err != nil {
		return nil, fmt.Errorf("marshal a2ui form template: %w", err)
	}
	res := &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
		URI:      params.URI,
		MIMEType: a2uiMIMEType,
		Text:     string(template),
	}}}
	normalizeCacheable(&res.Cacheable)
	return completeRelayedResult(req, res)
}
