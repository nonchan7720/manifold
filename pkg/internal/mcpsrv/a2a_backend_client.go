package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/a2aproject/a2a-go/v2/a2acompat/a2av0"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
	"go.opentelemetry.io/otel/attribute"
)

// a2aMetaKey は A2A のツール結果がレスポンスのコンテキスト（a2aResultMeta）を
// 報告する _meta のキー。
const a2aMetaKey = "a2a"

// a2aSkillMetadataKey は呼び出し対象のスキルを示すメッセージ metadata のキー。
// A2A にはリクエスト単位でスキルを指定する仕組みがなく、スキルは Agent Card に
// しか存在しない。そのためメッセージ本文には何も足さず、選ばれたスキル ID を
// ここに入れて渡す。
const a2aSkillMetadataKey = "skillId"

// a2aResultMeta はツール結果の _meta.a2a の内容。
type a2aResultMeta struct {
	ProtocolVersion string            `json:"protocolVersion,omitempty"`
	ContextID       string            `json:"contextId,omitempty"`
	TaskID          string            `json:"taskId,omitempty"`
	State           string            `json:"state,omitempty"`
	MessageID       string            `json:"messageId,omitempty"`
	Artifacts       []a2aArtifactMeta `json:"artifacts,omitempty"`
}

type a2aArtifactMeta struct {
	ArtifactID  string `json:"artifactId"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// a2aCallArgs は全スキルツール共通の tools/call 引数（a2aSkillInputSchema 参照）。
type a2aCallArgs struct {
	SessionID string `json:"sessionId"`
	TaskID    string `json:"taskId"`
	Message   string `json:"message"`
	Data      any    `json:"data"`
	Files     []any  `json:"files"`
}

// A2ABackendClient は A2A エージェント 1 つ（agents.<name> エントリ、または
// mcpServers.<name>.agents.<agent> エントリ。どちらも transport a2a の
// config.Server として渡される）を MCP サーバーとして公開する。Agent Card の
// 各スキル（cfg.Skills を指定した場合はそのスキルだけ）がツールになり、その
// 呼び出しは呼び出し元の sessionId を A2A の contextId に載せた message/send になる。
// mcpServers.<name>.agents 配下のエージェントではツール名に <agent>__ の接頭辞が付く
// （withToolPrefix 参照）。
//
// Agent Card は cfg.URL（+ agentCardPath）から取得してクライアントの生存期間中
// キャッシュする。メッセージの送信先は Card に書かれたエンドポイントであり、
// cfg.URL ではない。http MCP バックエンドと同様、メッセージは呼び出し元の ctx で
// 送るため、oauth2/tokenExchange の RoundTripper は呼び出し元本人の認証情報を
// 使う。Card の取得には headers/authValue だけを付ける。公開 Card の取得が
// 呼び出し元ごとのトークンに依存することはないため。
type A2ABackendClient struct {
	name         string
	cfg          *config.Server
	mediaService storage.MediaService
	// toolPrefix はツール名（tools/list・tools/call）に付ける接頭辞。トップレベルの
	// agents では空、mcpServers.<name>.agents 配下では "<agent>__"。
	toolPrefix string

	cardHTTPClient    *http.Client
	messageHTTPClient *http.Client

	mu     sync.Mutex
	card   *a2a.AgentCard
	client *a2aclient.Client
	closed bool
}

// a2aClientOption は NewA2ABackendClient の任意設定。
type a2aClientOption func(*A2ABackendClient)

// withToolPrefix はスキルのツール名に prefix を付ける（<prefix><skill ID>）。
// tools/call では同じ接頭辞を外してスキルを引く。
func withToolPrefix(prefix string) a2aClientOption {
	return func(c *A2ABackendClient) { c.toolPrefix = prefix }
}

// NewA2ABackendClient は cfg 用のクライアントを組み立てる。EnsureCard・ListTools・
// CallTool のいずれかが呼ばれるまで何も取得しない。
func NewA2ABackendClient(
	name string, cfg *config.Server, mediaService storage.MediaService, opts ...a2aClientOption,
) *A2ABackendClient {
	if mediaService == nil {
		mediaService = storage.NewNoopUploader()
	}
	c := &A2ABackendClient{
		name:         name,
		cfg:          cfg,
		mediaService: mediaService,
		cardHTTPClient: &http.Client{
			Transport: httpClientRoundTripper(cfg.AuthValue, nil, nil, cfg.ExtraHeaders),
		},
		messageHTTPClient: &http.Client{
			Transport: httpClientRoundTripper(
				cfg.AuthValue, cfg.OAuth2, cfg.TokenExchange, cfg.ExtraHeaders,
			),
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// parseAgentCard は両形式の Agent Card を復号する。v1.0 の Card は
// supportedInterfaces を持ち、v0.3 の Card は代わりに url/preferredTransport を
// 持つため、後者は SDK の互換パーサーで変換する。
//
// v0.3 では preferredTransport と protocolVersion は省略可能（既定はそれぞれ
// JSONRPC と 0.3）だが、SDK の互換パーサーは preferredTransport が無い Card の
// url をエンドポイントとして扱わない（additionalInterfaces だけが残る）。
// そのままだと主エンドポイントを失うため、preferredTransport が無い Card では
// url を JSONRPC のエンドポイントとして先頭に補う。
func parseAgentCard(body []byte) (*a2a.AgentCard, error) {
	var probe struct {
		SupportedInterfaces json.RawMessage `json:"supportedInterfaces"`
		URL                 string          `json:"url"`
		PreferredTransport  string          `json:"preferredTransport"`
		ProtocolVersion     string          `json:"protocolVersion"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, err
	}
	if len(probe.SupportedInterfaces) > 0 && string(probe.SupportedInterfaces) != "null" {
		return agentcard.DefaultCardParser(body)
	}
	card, err := a2av0.NewAgentCardParser()(body)
	if err != nil {
		return nil, err
	}
	if probe.PreferredTransport == "" && probe.URL != "" &&
		!hasJSONRPCInterface(card, probe.URL) {
		version := a2a.ProtocolVersion(probe.ProtocolVersion)
		if version == "" {
			version = a2av0.Version
		}
		primary := &a2a.AgentInterface{
			URL:             probe.URL,
			ProtocolBinding: a2a.TransportProtocolJSONRPC,
			ProtocolVersion: version,
		}
		card.SupportedInterfaces = append(
			[]*a2a.AgentInterface{primary}, card.SupportedInterfaces...,
		)
	}
	return card, nil
}

// hasJSONRPCInterface は card が url を JSONRPC のエンドポイントとして既に
// 持っているかを返す。URL が同じでもバインディングが異なるインターフェース
// （例: HTTP+JSON）は別物として扱う。resolveCard は JSONRPC トランスポートしか
// 登録しないため、それを重複とみなすと接続できるインターフェースが無くなる。
func hasJSONRPCInterface(card *a2a.AgentCard, url string) bool {
	for _, iface := range card.SupportedInterfaces {
		if iface != nil && iface.URL == url &&
			iface.ProtocolBinding == a2a.TransportProtocolJSONRPC {
			return true
		}
	}
	return false
}

// EnsureCard は初回利用時に Agent Card（とそのエンドポイントに紐づく SDK
// クライアント）を取得してキャッシュし、以降はキャッシュを返す。取得失敗は
// キャッシュしないため、次のリクエストで再試行される。
//
// 取得はロックを保持せずに行い、cfg の timeout（CallTimeoutOrDefault）で
// 打ち切る。応答しない Agent Card サーバーが起動処理や他の呼び出し・Close を
// 巻き込んで止めないようにするため。同時に取得が走った場合は先に完了した
// 結果を採用し、後から完了した分は破棄する。
func (c *A2ABackendClient) EnsureCard(ctx context.Context) (_ *a2a.AgentCard, rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/A2ABackendClient/EnsureCard")
	defer func() { trace.EndSpan(ctx, rErr) }()

	c.mu.Lock()
	closed, card := c.closed, c.card
	c.mu.Unlock()
	if closed {
		return nil, fmt.Errorf("agent %s: client closed", c.name)
	}
	if card != nil {
		return card, nil
	}

	card, client, err := c.resolveCard(ctx)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	switch {
	case c.closed:
		c.mu.Unlock()
		_ = client.Destroy()
		return nil, fmt.Errorf("agent %s: client closed", c.name)
	case c.card != nil:
		// 別の呼び出しが先に取得を終えていた。そちらを採用する。
		card = c.card
		c.mu.Unlock()
		_ = client.Destroy()
		return card, nil
	default:
		c.card = card
		c.client = client
		c.mu.Unlock()
		// 採用した取得結果に対してだけ、Card が持たない skills を警告する
		// （取得のたびではなく、Card をキャッシュしたとき 1 回）。
		c.warnMissingSkills(ctx, card)
		return card, nil
	}
}

// warnMissingSkills は skills に設定されているが Agent Card に無いスキル ID を
// 警告ログに出す。設定ミスの可能性はあるが、Card は後から更新され得るため
// 起動や呼び出しは失敗させない（該当 ID は公開されないだけ）。
func (c *A2ABackendClient) warnMissingSkills(ctx context.Context, card *a2a.AgentCard) {
	for _, id := range c.cfg.Skills {
		if _, ok := findSkill(card.Skills, id); !ok {
			slog.WarnContext(ctx, "a2a skill in skills is not in the agent card; skipped",
				slog.String("agent", c.name), slog.String("skill", id))
		}
	}
}

// exposedSkills はツールとして公開するスキルを返す。skills が空なら Card の全
// スキルを Card の順序で、指定があれば設定された順序で指定 ID のスキルだけを返す。
// Card に無い ID は読み飛ばす。
func (c *A2ABackendClient) exposedSkills(card *a2a.AgentCard) []a2a.AgentSkill {
	if len(c.cfg.Skills) == 0 {
		return card.Skills
	}
	skills := make([]a2a.AgentSkill, 0, len(c.cfg.Skills))
	for _, id := range c.cfg.Skills {
		if skill, ok := findSkill(card.Skills, id); ok {
			skills = append(skills, skill)
		}
	}
	return skills
}

// resolveCard は Agent Card を取得し、そのエンドポイントへ接続する SDK
// クライアントを組み立てる。ロックを保持せずに呼ぶこと。
func (c *A2ABackendClient) resolveCard(
	ctx context.Context,
) (*a2a.AgentCard, *a2aclient.Client, error) {
	resolveCtx, cancel := context.WithTimeout(ctx, c.cfg.CallTimeoutOrDefault())
	defer cancel()

	resolver := &agentcard.Resolver{Client: c.cardHTTPClient, CardParser: parseAgentCard}
	var opts []agentcard.ResolveOption
	if c.cfg.AgentCardPath != "" {
		opts = append(opts, agentcard.WithPath(c.cfg.AgentCardPath))
	}
	card, err := resolver.Resolve(resolveCtx, c.cfg.URL, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("agent %s: resolve agent card: %w", c.name, err)
	}
	if len(card.SupportedInterfaces) == 0 {
		return nil, nil, fmt.Errorf("agent %s: agent card declares no endpoint", c.name)
	}

	client, err := a2aclient.NewFromCard(resolveCtx, card,
		a2aclient.WithDefaultsDisabled(),
		a2aclient.WithJSONRPCTransport(c.messageHTTPClient),
		a2av0.WithJSONRPCTransport(a2av0.JSONRPCTransportConfig{Client: c.messageHTTPClient}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("agent %s: connect: %w", c.name, err)
	}
	return card, client, nil
}

// Close はキャッシュした Card/クライアントを破棄し、以降の利用を拒否する。
func (c *A2ABackendClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.client != nil {
		_ = c.client.Destroy()
		c.client = nil
	}
	c.card = nil
}

// a2aSkillInputSchema は全スキルツール共通の input schema。files は OpenAPI の
// `format: binary` フィールドと同じ値の形（oastomcptool.FileInputSchema）を取る。
func a2aSkillInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"sessionId": map[string]any{
				"type": "string",
				"description": "Session ID of the calling agent. Forwarded as the A2A contextId, " +
					"so every message sent with the same sessionId shares one conversation with the agent.",
			},
			"taskId": map[string]any{
				"type": "string",
				"description": "Task ID from a previous result's _meta.a2a.taskId. " +
					"Set it to continue that task (e.g. to answer an input-required state).",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "Text message for the agent.",
			},
			"data": map[string]any{
				"type":        "object",
				"description": "Structured JSON data sent to the agent as a data part.",
			},
			"files": map[string]any{
				"type":        "array",
				"description": "Files sent to the agent as file parts.",
				"items":       oastomcptool.FileInputSchema("File to send to the agent."),
			},
		},
		"required": []string{"sessionId"},
	}
}

// a2aSkillDescription はスキルツールの description を組み立てる。運用者の指示文
// （mcpServers / agents の description）を先頭に、続けて Agent Card のスキル情報を並べる。
func a2aSkillDescription(instruction string, skill a2a.AgentSkill) string {
	var b strings.Builder
	if instruction != "" {
		b.WriteString(strings.TrimSpace(instruction))
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Skill: %s (%s)", skill.Name, skill.ID)
	if skill.Description != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(skill.Description))
	}
	if len(skill.Tags) > 0 {
		b.WriteString("\nTags: ")
		b.WriteString(strings.Join(skill.Tags, ", "))
	}
	if len(skill.Examples) > 0 {
		b.WriteString("\nExamples:")
		for _, example := range skill.Examples {
			b.WriteString("\n- ")
			b.WriteString(example)
		}
	}
	return b.String()
}

// skillTools は公開対象のスキル（exposedSkills 参照）を MCP ツールへ変換する。
func (c *A2ABackendClient) skillTools(card *a2a.AgentCard) []*mcp.Tool {
	skills := c.exposedSkills(card)
	tools := make([]*mcp.Tool, 0, len(skills))
	for _, skill := range skills {
		tools = append(tools, &mcp.Tool{
			Name:        c.toolPrefix + skill.ID,
			Title:       skill.Name,
			Description: a2aSkillDescription(c.cfg.Description, skill),
			InputSchema: a2aSkillInputSchema(),
		})
	}
	return tools
}

// ListTools は tools/list に対して公開対象のスキル 1 つにつきツール 1 つを返す。
func (c *A2ABackendClient) ListTools(
	ctx context.Context, _ *mcp.ListToolsParams,
) (_ *mcp.ListToolsResult, rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/A2ABackendClient/ListTools")
	defer func() { trace.EndSpan(ctx, rErr) }()

	card, err := c.EnsureCard(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "a2a tools/list failed: agent card unavailable",
			slog.String("agent", c.name), slog.Any("error", err))
		return nil, err
	}
	return &mcp.ListToolsResult{Tools: c.skillTools(card)}, nil
}

// ListToolInfos は /mcp/list 用の（スキル ID, description）一覧を返す。
func (c *A2ABackendClient) ListToolInfos(ctx context.Context) ([]ToolInfo, error) {
	card, err := c.EnsureCard(ctx)
	if err != nil {
		return nil, err
	}
	skills := c.exposedSkills(card)
	infos := make([]ToolInfo, 0, len(skills))
	for _, skill := range skills {
		infos = append(
			infos,
			ToolInfo{Name: c.toolPrefix + skill.ID, Description: skill.Description},
		)
	}
	return infos, nil
}

func a2aErrorResult(err error) *mcp.CallToolResult {
	res := &mcp.CallToolResult{}
	res.SetError(err)
	return res
}

// CallTool は name で指定されたスキルに対して message/send を 1 回送る。引数の
// 誤りやエージェント側のエラーは、呼び出し元のモデルに見えるよう isError の
// 結果として返す。プロトコルエラーにするのはクライアントが使えない場合
// （Close 済み、Card 未取得）だけ。
func (c *A2ABackendClient) CallTool(
	ctx context.Context, name string, args json.RawMessage,
) (_ *mcp.CallToolResult, rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/A2ABackendClient/CallTool",
		attribute.String("tool-name", name))
	defer func() { trace.EndSpan(ctx, rErr) }()

	// toolPrefix があるときは接頭辞を外してスキル ID にする。接頭辞が合わない
	// 名前は、Card に無いスキルと同じ扱いにする。
	skillID, ok := strings.CutPrefix(name, c.toolPrefix)
	if !ok {
		return a2aErrorResult(fmt.Errorf("unknown skill %q", name)), nil
	}

	card, err := c.EnsureCard(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "a2a tools/call failed: agent card unavailable",
			slog.String("agent", c.name), slog.String("skill", name), slog.Any("error", err))
		return nil, err
	}
	skill, ok := findSkill(c.exposedSkills(card), skillID)
	if !ok {
		return a2aErrorResult(fmt.Errorf("unknown skill %q", name)), nil
	}

	msg, err := c.buildMessage(ctx, skill, args)
	if err != nil {
		return a2aErrorResult(err), nil
	}
	slog.InfoContext(ctx, "call a2a skill",
		slog.String("agent", c.name), slog.String("skill", skill.ID),
		slog.String("context_id", msg.ContextID))

	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	if client == nil {
		return nil, fmt.Errorf("agent %s: client closed", c.name)
	}

	callCtx, cancel := context.WithTimeout(ctx, c.cfg.CallTimeoutOrDefault())
	defer cancel()
	result, err := client.SendMessage(callCtx, &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		return a2aErrorResult(fmt.Errorf("agent %s: send message: %w", c.name, err)), nil
	}
	return c.toCallToolResult(ctx, result)
}

// findSkill は skills から id に一致するスキルを探す。呼び出しでは公開対象
// （exposedSkills）だけを渡し、公開していないスキルを呼べないようにする。
func findSkill(skills []a2a.AgentSkill, id string) (a2a.AgentSkill, bool) {
	for _, skill := range skills {
		if skill.ID == id {
			return skill, true
		}
	}
	return a2a.AgentSkill{}, false
}

// buildMessage は引数を検証して A2A メッセージを組み立てる。パートは message・
// data・files の順、sessionId は contextId に、スキル ID は metadata に入れる。
func (c *A2ABackendClient) buildMessage(
	ctx context.Context, skill a2a.AgentSkill, raw json.RawMessage,
) (*a2a.Message, error) {
	var args a2aCallArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if strings.TrimSpace(args.SessionID) == "" {
		return nil, errors.New("sessionId is required")
	}
	if args.Message == "" && args.Data == nil && len(args.Files) == 0 {
		return nil, errors.New("at least one of message, data, files is required")
	}

	parts := make([]*a2a.Part, 0, 2+len(args.Files))
	if args.Message != "" {
		parts = append(parts, a2a.NewTextPart(args.Message))
	}
	if args.Data != nil {
		parts = append(parts, a2a.NewDataPart(args.Data))
	}
	for i, value := range args.Files {
		file, err := oastomcptool.ResolveFileInput(ctx, fmt.Sprintf("files[%d]", i), value)
		if err != nil {
			return nil, err
		}
		parts = append(parts, &a2a.Part{
			Content:   a2a.Raw(file.Data),
			Filename:  file.Filename,
			MediaType: file.ContentType,
		})
	}

	return &a2a.Message{
		ID:        a2a.NewMessageID(),
		Role:      a2a.MessageRoleUser,
		ContextID: args.SessionID,
		TaskID:    a2a.TaskID(args.TaskID),
		Parts:     parts,
		Metadata:  map[string]any{a2aSkillMetadataKey: skill.ID},
	}, nil
}

// a2aStateString は TaskState（"TASK_STATE_INPUT_REQUIRED"）を _meta.a2a.state 用に
// 仕様の小文字形式（"input-required"）へ短縮する。
func a2aStateString(state a2a.TaskState) string {
	s := strings.TrimPrefix(string(state), "TASK_STATE_")
	return strings.ReplaceAll(strings.ToLower(s), "_", "-")
}

// toCallToolResult は message/send の結果を変換する。Message はそのパートを、
// Task は status のメッセージのパートに続けて各 artifact のパートを返し、タスクが
// failed / rejected ならエラー結果にする。
func (c *A2ABackendClient) toCallToolResult(
	ctx context.Context, result a2a.SendMessageResult,
) (*mcp.CallToolResult, error) {
	res := &mcp.CallToolResult{}
	meta := a2aResultMeta{}
	var parts []*a2a.Part

	switch v := result.(type) {
	case *a2a.Message:
		meta.ContextID = v.ContextID
		meta.TaskID = string(v.TaskID)
		meta.MessageID = v.ID
		parts = v.Parts
	case *a2a.Task:
		meta.ContextID = v.ContextID
		meta.TaskID = string(v.ID)
		meta.State = a2aStateString(v.Status.State)
		if v.Status.Message != nil {
			meta.MessageID = v.Status.Message.ID
			parts = append(parts, v.Status.Message.Parts...)
		}
		for _, artifact := range v.Artifacts {
			meta.Artifacts = append(meta.Artifacts, a2aArtifactMeta{
				ArtifactID:  string(artifact.ID),
				Name:        artifact.Name,
				Description: artifact.Description,
			})
			parts = append(parts, artifact.Parts...)
		}
		if v.Status.State == a2a.TaskStateFailed || v.Status.State == a2a.TaskStateRejected {
			res.IsError = true
		}
	default:
		return nil, fmt.Errorf("agent %s: unexpected message/send result %T", c.name, result)
	}
	c.mu.Lock()
	if c.card != nil && len(c.card.SupportedInterfaces) > 0 {
		meta.ProtocolVersion = string(c.card.SupportedInterfaces[0].ProtocolVersion)
	}
	c.mu.Unlock()

	content, structured, err := c.partsToContent(ctx, parts)
	if err != nil {
		return nil, err
	}
	res.Content = content
	res.StructuredContent = structured
	res.Meta = mcp.Meta{a2aMetaKey: meta}
	return res, nil
}

// partsToContent は A2A のパートを MCP の content へ変換する。text・data パートは
// テキスト（data は JSON 文字列）、ファイル URL はそのままリソースリンク、ファイル
// のバイト列は OpenAPI のバイナリレスポンスと同じく generateContent を通す
// （storage 有効ならリソースリンク、無効ならインライン）。data パートの値は
// structuredContent としても返す（1 つならその値、複数ならリスト）。
func (c *A2ABackendClient) partsToContent(
	ctx context.Context, parts []*a2a.Part,
) ([]mcp.Content, any, error) {
	content := make([]mcp.Content, 0, len(parts))
	var data []any
	for _, part := range parts {
		if part == nil {
			continue
		}
		switch v := part.Content.(type) {
		case a2a.Text:
			content = append(content, &mcp.TextContent{Text: string(v)})
		case a2a.Data:
			encoded, err := json.Marshal(v.Value)
			if err != nil {
				return nil, nil, fmt.Errorf("agent %s: encode data part: %w", c.name, err)
			}
			content = append(content, &mcp.TextContent{Text: string(encoded)})
			data = append(data, v.Value)
		case a2a.URL:
			name := part.Filename
			if name == "" {
				name = string(v)
			}
			content = append(content, &mcp.ResourceLink{
				URI:      string(v),
				Name:     name,
				MIMEType: part.MediaType,
			})
		case a2a.Raw:
			generated, err := generateContent(ctx, part.MediaType, []byte(v), c.mediaService)
			if err != nil {
				return nil, nil, fmt.Errorf("agent %s: file part: %w", c.name, err)
			}
			content = append(content, generated...)
		}
	}
	switch len(data) {
	case 0:
		return content, nil, nil
	case 1:
		return content, data[0], nil
	default:
		return content, data, nil
	}
}
