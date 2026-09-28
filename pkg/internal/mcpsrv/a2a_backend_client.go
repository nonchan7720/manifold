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

// a2aMetaKey is the _meta key under which every A2A tool result reports the
// response context (a2aResultMeta).
const a2aMetaKey = "a2a"

// a2aSkillMetadataKey is the message metadata key naming the skill a call
// targets. A2A itself has no per-request skill selector: skills only exist
// in the Agent Card, so the gateway passes the chosen skill's id here rather
// than injecting anything into the message text.
const a2aSkillMetadataKey = "skillId"

// a2aResultMeta is the _meta.a2a payload of a tool result.
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

// a2aCallArgs are the tools/call arguments every skill tool accepts (see
// a2aSkillInputSchema).
type a2aCallArgs struct {
	SessionID string `json:"sessionId"`
	TaskID    string `json:"taskId"`
	Message   string `json:"message"`
	Data      any    `json:"data"`
	Files     []any  `json:"files"`
}

// A2ABackendClient exposes one A2A agent (an agents.<name> entry, carried as
// a config.Server with transport a2a) as an MCP server: each Agent Card skill
// becomes a tool whose call is a message/send with the caller's sessionId as
// the A2A contextId.
//
// The Agent Card is fetched from cfg.URL (+ agentCardPath) and cached for
// the client's lifetime; the message endpoint is whatever the card declares,
// never cfg.URL. Like the http MCP backend, every message is sent with the
// caller's ctx so the oauth2/tokenExchange round trippers see the caller's
// own credentials; the card itself is fetched with only headers/authValue,
// since a public card cannot depend on a per-caller token.
type A2ABackendClient struct {
	name         string
	cfg          *config.Server
	mediaService storage.MediaService

	cardHTTPClient    *http.Client
	messageHTTPClient *http.Client

	mu     sync.Mutex
	card   *a2a.AgentCard
	client *a2aclient.Client
	closed bool
}

// NewA2ABackendClient builds the client for cfg; nothing is fetched until
// EnsureCard, ListTools or CallTool.
func NewA2ABackendClient(
	name string, cfg *config.Server, mediaService storage.MediaService,
) *A2ABackendClient {
	if mediaService == nil {
		mediaService = storage.NewNoopUploader()
	}
	return &A2ABackendClient{
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
}

// parseAgentCard decodes either Agent Card format: a v1.0 card lists
// supportedInterfaces, a v0.3 card has url/preferredTransport instead and is
// converted by the SDK's compatibility parser.
func parseAgentCard(body []byte) (*a2a.AgentCard, error) {
	var probe struct {
		SupportedInterfaces json.RawMessage `json:"supportedInterfaces"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, err
	}
	if len(probe.SupportedInterfaces) > 0 && string(probe.SupportedInterfaces) != "null" {
		return agentcard.DefaultCardParser(body)
	}
	return a2av0.NewAgentCardParser()(body)
}

// EnsureCard fetches and caches the Agent Card (and the SDK client bound to
// its endpoint) on first use; later calls return the cached card. A failed
// fetch is not cached, so the next request retries.
func (c *A2ABackendClient) EnsureCard(ctx context.Context) (_ *a2a.AgentCard, rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/A2ABackendClient/EnsureCard")
	defer func() { trace.EndSpan(ctx, rErr) }()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, fmt.Errorf("agent %s: client closed", c.name)
	}
	if c.card != nil {
		return c.card, nil
	}

	resolver := &agentcard.Resolver{Client: c.cardHTTPClient, CardParser: parseAgentCard}
	var opts []agentcard.ResolveOption
	if c.cfg.AgentCardPath != "" {
		opts = append(opts, agentcard.WithPath(c.cfg.AgentCardPath))
	}
	card, err := resolver.Resolve(ctx, c.cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("agent %s: resolve agent card: %w", c.name, err)
	}
	if len(card.SupportedInterfaces) == 0 {
		return nil, fmt.Errorf("agent %s: agent card declares no endpoint", c.name)
	}

	client, err := a2aclient.NewFromCard(ctx, card,
		a2aclient.WithDefaultsDisabled(),
		a2aclient.WithJSONRPCTransport(c.messageHTTPClient),
		a2av0.WithJSONRPCTransport(a2av0.JSONRPCTransportConfig{Client: c.messageHTTPClient}),
	)
	if err != nil {
		return nil, fmt.Errorf("agent %s: connect: %w", c.name, err)
	}
	c.card = card
	c.client = client
	return card, nil
}

// Close forgets the cached card/client and refuses further use.
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

// a2aSkillInputSchema is the input schema shared by every skill tool. files
// takes the same value shape as an OpenAPI `format: binary` field
// (oastomcptool.FileInputSchema).
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

// a2aSkillDescription builds a skill tool's description: the operator's
// instruction (agents.<name>.description) first, then the skill as the Agent
// Card describes it.
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

// skillTools maps the card's skills to MCP tools, in card order.
func (c *A2ABackendClient) skillTools(card *a2a.AgentCard) []*mcp.Tool {
	tools := make([]*mcp.Tool, 0, len(card.Skills))
	for _, skill := range card.Skills {
		tools = append(tools, &mcp.Tool{
			Name:        skill.ID,
			Title:       skill.Name,
			Description: a2aSkillDescription(c.cfg.Description, skill),
			InputSchema: a2aSkillInputSchema(),
		})
	}
	return tools
}

// ListTools answers tools/list with one tool per Agent Card skill.
func (c *A2ABackendClient) ListTools(
	ctx context.Context, _ *mcp.ListToolsParams,
) (_ *mcp.ListToolsResult, rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/A2ABackendClient/ListTools")
	defer func() { trace.EndSpan(ctx, rErr) }()

	card, err := c.EnsureCard(ctx)
	if err != nil {
		return nil, err
	}
	return &mcp.ListToolsResult{Tools: c.skillTools(card)}, nil
}

// ListToolInfos returns the (skill id, description) catalog for /mcp/list.
func (c *A2ABackendClient) ListToolInfos(ctx context.Context) ([]ToolInfo, error) {
	card, err := c.EnsureCard(ctx)
	if err != nil {
		return nil, err
	}
	infos := make([]ToolInfo, 0, len(card.Skills))
	for _, skill := range card.Skills {
		infos = append(infos, ToolInfo{Name: skill.ID, Description: skill.Description})
	}
	return infos, nil
}

func a2aErrorResult(err error) *mcp.CallToolResult {
	res := &mcp.CallToolResult{}
	res.SetError(err)
	return res
}

// CallTool sends one message/send for the skill named name. Argument and
// agent errors are reported as an isError result so the calling model sees
// them; only an unusable client (closed, no card) is a protocol error.
func (c *A2ABackendClient) CallTool(
	ctx context.Context, name string, args json.RawMessage,
) (_ *mcp.CallToolResult, rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/A2ABackendClient/CallTool",
		attribute.String("tool-name", name))
	defer func() { trace.EndSpan(ctx, rErr) }()

	card, err := c.EnsureCard(ctx)
	if err != nil {
		return nil, err
	}
	skill, ok := findSkill(card, name)
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

func findSkill(card *a2a.AgentCard, id string) (a2a.AgentSkill, bool) {
	for _, skill := range card.Skills {
		if skill.ID == id {
			return skill, true
		}
	}
	return a2a.AgentSkill{}, false
}

// buildMessage validates args and assembles the A2A message: message, data
// and files parts in that order, the sessionId as contextId, and the skill
// id in metadata.
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

// a2aStateString shortens a TaskState ("TASK_STATE_INPUT_REQUIRED") to the
// spec's lower-case form ("input-required") for _meta.a2a.state.
func a2aStateString(state a2a.TaskState) string {
	s := strings.TrimPrefix(string(state), "TASK_STATE_")
	return strings.ReplaceAll(strings.ToLower(s), "_", "-")
}

// toCallToolResult converts a message/send result. A Message yields its
// parts; a Task yields its status message parts followed by every artifact's
// parts, and is an error result when the task failed or was rejected.
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

// partsToContent maps A2A parts to MCP content: text and data parts become
// text (data as JSON), a file URL becomes a resource link as-is, and file
// bytes go through generateContent exactly like an OpenAPI binary response
// (resource link when storage is enabled, inline otherwise). The data part
// values are also returned as structuredContent: one value, or a list when
// there are several.
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
