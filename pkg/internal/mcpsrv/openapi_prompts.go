package mcpsrv

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/template"

	"github.com/goccy/go-yaml"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/client"
)

// OpenAPI モードのサーバーの prompts（mcpServers.<name>.prompts）。
//
// OpenAPI の spec には prompts を表す仕組みが無いため、spec とは別の
// prompts 定義ドキュメント（YAML / JSON）を取得元から読み、prompts/list・
// prompts/get に答える。取得元は2種類。
//
//   - url: 要求のたびにバックエンドから取得する（urlPromptSource）。
//   - file: 起動時に読み込み、変更を監視して読み直す（filePromptSource）。
//
// ドキュメントの形式:
//
//	prompts:
//	  - name: summarize_pet
//	    title: Summarize a pet
//	    description: Summarize a pet's profile
//	    arguments:
//	      - name: petId
//	        description: ID of the pet
//	        required: true
//	    messages:
//	      - role: user
//	        content: "Summarize the pet {{.petId}}."
//
// content は text/template で、引数を名前で参照する（"-" を含む名前は
// {{index . "pet-id"}}）。渡されなかった任意引数は空文字列になる。

// maxPromptDocumentSize は prompts 定義ドキュメントの最大バイト数。
const maxPromptDocumentSize = 1 << 20

// promptDocument は prompts 定義ドキュメント。
type promptDocument struct {
	Prompts []promptDefinition `yaml:"prompts"`
}

type promptDefinition struct {
	Name        string                  `yaml:"name"`
	Title       string                  `yaml:"title"`
	Description string                  `yaml:"description"`
	Arguments   []promptArgumentDef     `yaml:"arguments"`
	Messages    []promptMessageTemplate `yaml:"messages"`
}

type promptArgumentDef struct {
	Name        string `yaml:"name"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
}

type promptMessageTemplate struct {
	Role    string `yaml:"role"`
	Content string `yaml:"content"`
}

// compiledPrompt は検証済みの prompt と、テンプレートを解釈済みのメッセージ。
type compiledPrompt struct {
	prompt   *mcp.Prompt
	messages []compiledMessage
}

type compiledMessage struct {
	role     mcp.Role
	template *template.Template
}

// promptSet は prompts 定義ドキュメント1つ分の prompts。
type promptSet struct {
	list   []*mcp.Prompt
	byName map[string]*compiledPrompt
}

// parsePromptDocument は raw を prompts 定義ドキュメントとして解釈・検証する。
// どれか1つでも不正な prompt があればドキュメント全体をエラーにする。
func parsePromptDocument(raw []byte) (*promptSet, error) {
	var doc promptDocument
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode prompts document: %w", err)
	}
	set := &promptSet{
		list:   make([]*mcp.Prompt, 0, len(doc.Prompts)),
		byName: make(map[string]*compiledPrompt, len(doc.Prompts)),
	}
	for i, def := range doc.Prompts {
		compiled, err := compilePrompt(def)
		if err != nil {
			return nil, fmt.Errorf("prompts[%d]: %w", i, err)
		}
		if _, dup := set.byName[def.Name]; dup {
			return nil, fmt.Errorf("prompts[%d]: duplicate prompt name %q", i, def.Name)
		}
		set.byName[def.Name] = compiled
		set.list = append(set.list, compiled.prompt)
	}
	return set, nil
}

func compilePrompt(def promptDefinition) (*compiledPrompt, error) {
	if def.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if len(def.Messages) == 0 {
		return nil, fmt.Errorf("prompt %q: messages is required", def.Name)
	}
	prompt := &mcp.Prompt{
		Name:        def.Name,
		Title:       def.Title,
		Description: def.Description,
	}
	seen := make(map[string]bool, len(def.Arguments))
	for _, arg := range def.Arguments {
		if arg.Name == "" {
			return nil, fmt.Errorf("prompt %q: argument name is required", def.Name)
		}
		if seen[arg.Name] {
			return nil, fmt.Errorf("prompt %q: duplicate argument %q", def.Name, arg.Name)
		}
		seen[arg.Name] = true
		prompt.Arguments = append(prompt.Arguments, &mcp.PromptArgument{
			Name:        arg.Name,
			Title:       arg.Title,
			Description: arg.Description,
			Required:    arg.Required,
		})
	}
	compiled := &compiledPrompt{prompt: prompt}
	for j, msg := range def.Messages {
		role := mcp.Role(msg.Role)
		if role != "user" && role != "assistant" {
			return nil, fmt.Errorf(
				"prompt %q: messages[%d]: role must be user or assistant", def.Name, j,
			)
		}
		tmpl, err := template.New(fmt.Sprintf("%s/%d", def.Name, j)).
			Option("missingkey=zero").
			Parse(msg.Content)
		if err != nil {
			return nil, fmt.Errorf("prompt %q: messages[%d]: %w", def.Name, j, err)
		}
		compiled.messages = append(compiled.messages, compiledMessage{role: role, template: tmpl})
	}
	return compiled, nil
}

// render は args でメッセージのテンプレートを展開する。必須引数が無ければ
// invalid params を返す。
func (p *compiledPrompt) render(args map[string]string) (*mcp.GetPromptResult, error) {
	data := make(map[string]string, len(p.prompt.Arguments))
	var missing []string
	for _, arg := range p.prompt.Arguments {
		v, ok := args[arg.Name]
		if arg.Required && (!ok || v == "") {
			missing = append(missing, arg.Name)
		}
		data[arg.Name] = v
	}
	if len(missing) > 0 {
		return nil, &jsonrpc.Error{
			Code: jsonrpc.CodeInvalidParams,
			Message: fmt.Sprintf("prompt %q: missing required arguments: %s",
				p.prompt.Name, strings.Join(missing, ", ")),
		}
	}
	res := &mcp.GetPromptResult{
		Description: p.prompt.Description,
		Messages:    make([]*mcp.PromptMessage, 0, len(p.messages)),
	}
	for _, msg := range p.messages {
		var buf bytes.Buffer
		if err := msg.template.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("prompt %q: render: %w", p.prompt.Name, err)
		}
		res.Messages = append(res.Messages, &mcp.PromptMessage{
			Role:    msg.role,
			Content: &mcp.TextContent{Text: buf.String()},
		})
	}
	return res, nil
}

// promptSource は OpenAPI モードのサーバーの prompts の取得元。
type promptSource interface {
	prompts(ctx context.Context) (*promptSet, error)
}

// urlPromptSource は要求のたびに prompts 定義ドキュメントを url から取得する。
// 呼び出し元の ctx で取得するので、ツール呼び出しと同じく呼び出し元の
// トークン（oauth2）が転送される。
type urlPromptSource struct {
	url    string
	client *http.Client
}

func newURLPromptSource(server *config.Server) *urlPromptSource {
	return &urlPromptSource{
		url: server.Prompts.URL,
		client: &http.Client{
			Transport: httpClientRoundTripper(
				server.AuthValue,
				server.OAuth2,
				server.TokenExchange,
				client.NewCloudAuth(server.GoogleServiceAccount, server.AWSIAM),
				server.ExtraHeaders,
			),
		},
	}
}

func (s *urlPromptSource) prompts(ctx context.Context) (_ *promptSet, rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/urlPromptSource/prompts")
	defer func() { trace.EndSpan(ctx, rErr) }()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch prompts: %w", err)
	}
	defer resp.Body.Close() //nolint: errcheck
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("fetch prompts: HTTP %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxPromptDocumentSize+1))
	if err != nil {
		return nil, fmt.Errorf("fetch prompts: %w", err)
	}
	if len(raw) > maxPromptDocumentSize {
		return nil, fmt.Errorf("fetch prompts: document exceeds %d bytes", maxPromptDocumentSize)
	}
	return parsePromptDocument(raw)
}

// newOpenAPIPromptsMiddleware は OpenAPI モードのサーバー向けに、prompts/list・
// prompts/get を src の prompts 定義から答えるミドルウェアを返す。
// authz は MCP バックエンドの中継（newBackendPromptsMiddleware）と同じく、
// 外側の NewAuthzMiddleware がツールと同じ判定で掛ける。
func newOpenAPIPromptsMiddleware(src promptSource) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case methodPromptsList:
				set, err := src.prompts(ctx)
				if err != nil {
					return nil, err
				}
				res := &mcp.ListPromptsResult{Prompts: set.list}
				normalizeCacheable(&res.Cacheable)
				return res, nil
			case methodPromptsGet:
				params, _ := req.GetParams().(*mcp.GetPromptParams)
				if params == nil {
					return nil, unknownPromptError("")
				}
				set, err := src.prompts(ctx)
				if err != nil {
					return nil, err
				}
				p, ok := set.byName[params.Name]
				if !ok {
					return nil, unknownPromptError(params.Name)
				}
				res, err := p.render(params.Arguments)
				if err != nil {
					return nil, err
				}
				return completeRelayedResult(req, res)
			default:
				return next(ctx, method, req)
			}
		}
	}
}
