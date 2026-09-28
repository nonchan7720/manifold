package mcpsrv

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	a2alegacy "github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2acompat/a2av0"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/services/authz"
	"github.com/stretchr/testify/require"
)

// stubA2AAgent is an httptest-served A2A agent: it publishes an Agent Card
// (v1.0 or v0.3 format) whose endpoint is /rpc on the same server, records
// every message/send it receives and answers with a configurable event.
type stubA2AAgent struct {
	srv    *httptest.Server
	legacy bool // serve a v0.3 card and speak the v0.3 JSON-RPC wire format

	mu       sync.Mutex
	result   a2a.Event
	requests []*a2a.SendMessageRequest
	headers  []http.Header
}

const (
	stubSkillTranslate = "translate"
	stubSkillSummarize = "summarize"
)

func newStubA2AAgent(t *testing.T, legacy bool) *stubA2AAgent {
	t.Helper()
	// The shared internal transport only dials loopback when TEST is set
	// (see newToolCatalogBackendServer in server_manager_test.go).
	t.Setenv("TEST", "true")

	s := &stubA2AAgent{legacy: legacy}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/agent-card.json", s.serveCard)
	mux.HandleFunc("POST /rpc", s.serveRPC)
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *stubA2AAgent) card() *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:               "Translator",
		Description:        "Translates and summarizes documents.",
		Version:            "1.2.3",
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		SupportedInterfaces: []*a2a.AgentInterface{{
			URL:             s.srv.URL + "/rpc",
			ProtocolBinding: a2a.TransportProtocolJSONRPC,
			ProtocolVersion: a2a.Version,
		}},
		Skills: []a2a.AgentSkill{
			{
				ID:          stubSkillTranslate,
				Name:        "Translate",
				Description: "Translate text between languages.",
				Tags:        []string{"language"},
				Examples:    []string{"Translate this to Japanese"},
			},
			{
				ID:          stubSkillSummarize,
				Name:        "Summarize",
				Description: "Summarize a document.",
			},
		},
	}
}

func (s *stubA2AAgent) serveCard(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	card := s.card()
	if s.legacy {
		legacyCard := a2av0.FromV1AgentCard(card)
		legacyCard.ProtocolVersion = string(a2av0.Version)
		_ = json.NewEncoder(w).Encode(legacyCard)
		return
	}
	_ = json.NewEncoder(w).Encode(card)
}

type stubRPCRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	ID     json.RawMessage `json:"id"`
}

func (s *stubA2AAgent) serveRPC(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req stubRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	sendReq, err := s.decodeSendRequest(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.requests = append(s.requests, sendReq)
	s.headers = append(s.headers, r.Header.Clone())
	result := s.result
	s.mu.Unlock()

	var encoded any
	if s.legacy {
		legacyEvent, err := a2av0.FromV1Event(result)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		encoded = legacyEvent
	} else {
		encoded = a2a.StreamResponse{Event: result}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result":  encoded,
	})
}

// decodeSendRequest decodes the wire-format params (v1 SendMessage or v0.3
// message/send) into a v1 request so tests assert one shape.
func (s *stubA2AAgent) decodeSendRequest(req stubRPCRequest) (*a2a.SendMessageRequest, error) {
	if s.legacy {
		if req.Method != "message/send" {
			return nil, fmt.Errorf("unexpected method %s", req.Method)
		}
		var params a2alegacy.MessageSendParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, err
		}
		return a2av0.ToV1SendMessageRequest(&params)
	}
	if req.Method != "SendMessage" {
		return nil, fmt.Errorf("unexpected method %s", req.Method)
	}
	sendReq := &a2a.SendMessageRequest{}
	if err := json.Unmarshal(req.Params, sendReq); err != nil {
		return nil, err
	}
	return sendReq, nil
}

func (s *stubA2AAgent) setResult(ev a2a.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.result = ev
}

func (s *stubA2AAgent) lastRequest(t *testing.T) *a2a.SendMessageRequest {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.NotEmpty(t, s.requests)
	return s.requests[len(s.requests)-1]
}

func (s *stubA2AAgent) lastHeader(t *testing.T) http.Header {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.NotEmpty(t, s.headers)
	return s.headers[len(s.headers)-1]
}

func stubAgentServer(s *stubA2AAgent) *config.Server {
	return (&config.Agent{
		Name:        "translator",
		Description: "Use this agent for translation work. Always pass the caller's session id.",
		URL:         s.srv.URL,
	}).Server()
}

func agentMessage(text string) *a2a.Message {
	return &a2a.Message{
		ID:        "msg-1",
		ContextID: "sess-1",
		Role:      a2a.MessageRoleAgent,
		Parts:     []*a2a.Part{a2a.NewTextPart(text)},
	}
}

func resultMeta(t *testing.T, res *mcp.CallToolResult) a2aResultMeta {
	t.Helper()
	raw, err := json.Marshal(res.Meta[a2aMetaKey])
	require.NoError(t, err)
	var meta a2aResultMeta
	require.NoError(t, json.Unmarshal(raw, &meta))
	return meta
}

func callArgs(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return raw
}

// --- tools/list ---

func TestA2ABackendClient_ListTools_SkillsBecomeTools(t *testing.T) {
	stub := newStubA2AAgent(t, false)
	cfg := stubAgentServer(stub)
	c := NewA2ABackendClient("translator", cfg, nil)
	t.Cleanup(c.Close)

	res, err := c.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, []string{stubSkillTranslate, stubSkillSummarize}, toolNames(res.Tools))

	translate := res.Tools[0]
	require.Equal(t, "Translate", translate.Title)
	// The operator's instruction comes first, then the card's skill text.
	require.Contains(t, translate.Description, cfg.Description)
	require.Less(t,
		indexOf(translate.Description, cfg.Description),
		indexOf(translate.Description, "Translate text between languages."))
	require.Contains(t, translate.Description, "Examples:\n- Translate this to Japanese")
	require.Contains(t, translate.Description, "Tags: language")

	schema, ok := translate.InputSchema.(map[string]any)
	require.True(t, ok)
	require.Equal(t, []string{"sessionId"}, schema["required"])
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	for _, key := range []string{"sessionId", "taskId", "message", "data", "files"} {
		require.Contains(t, props, key)
	}
	files, ok := props["files"].(map[string]any)
	require.True(t, ok)
	items, ok := files["items"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, items, "oneOf")
	meta, ok := items["_meta"].(map[string]any)
	require.True(t, ok)
	manifold, ok := meta["manifold"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, manifold["file"])
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestA2ABackendClient_ListToolInfos_UsesSkillDescriptions(t *testing.T) {
	stub := newStubA2AAgent(t, false)
	c := NewA2ABackendClient("translator", stubAgentServer(stub), nil)
	t.Cleanup(c.Close)

	infos, err := c.ListToolInfos(t.Context())
	require.NoError(t, err)
	require.Equal(t, []ToolInfo{
		{Name: stubSkillTranslate, Description: "Translate text between languages."},
		{Name: stubSkillSummarize, Description: "Summarize a document."},
	}, infos)
}

func TestA2ABackendClient_ListTools_V03Card(t *testing.T) {
	stub := newStubA2AAgent(t, true)
	c := NewA2ABackendClient("translator", stubAgentServer(stub), nil)
	t.Cleanup(c.Close)

	res, err := c.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, []string{stubSkillTranslate, stubSkillSummarize}, toolNames(res.Tools))

	card, err := c.EnsureCard(t.Context())
	require.NoError(t, err)
	require.Len(t, card.SupportedInterfaces, 1)
	require.Equal(t, a2av0.Version, card.SupportedInterfaces[0].ProtocolVersion)
	require.Equal(t, stub.srv.URL+"/rpc", card.SupportedInterfaces[0].URL)
}

func TestA2ABackendClient_EnsureCard_FetchFailureIsRetried(t *testing.T) {
	t.Setenv("TEST", "true")
	fail := true
	var mu sync.Mutex
	stub := &stubA2AAgent{}
	mux := http.NewServeMux()
	cardHandler := func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		failing := fail
		mu.Unlock()
		if failing {
			http.Error(w, "boom", http.StatusServiceUnavailable)
			return
		}
		stub.serveCard(w, r)
	}
	mux.HandleFunc("GET /.well-known/agent-card.json", cardHandler)
	stub.srv = httptest.NewServer(mux)
	t.Cleanup(stub.srv.Close)

	c := NewA2ABackendClient("translator", stubAgentServer(stub), nil)
	t.Cleanup(c.Close)

	_, err := c.EnsureCard(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "resolve agent card")

	mu.Lock()
	fail = false
	mu.Unlock()
	card, err := c.EnsureCard(t.Context())
	require.NoError(t, err)
	require.Equal(t, "Translator", card.Name)
}

// --- tools/call: request shape ---

func TestA2ABackendClient_CallTool_SendsPartsSessionAndSkill(t *testing.T) {
	stub := newStubA2AAgent(t, false)
	stub.setResult(agentMessage("こんにちは"))
	cfg := stubAgentServer(stub)
	cfg.ExtraHeaders = map[string]string{"X-Tenant": "acme"}
	c := NewA2ABackendClient("translator", cfg, nil)
	t.Cleanup(c.Close)

	res, err := c.CallTool(t.Context(), stubSkillTranslate, callArgs(t, map[string]any{
		"sessionId": "sess-1",
		"taskId":    "task-9",
		"message":   "hello",
		"data":      map[string]any{"target": "ja"},
		"files": []any{
			base64.StdEncoding.EncodeToString([]byte("plain text file")),
			map[string]any{
				"text":        "second",
				"filename":    "notes.txt",
				"contentType": "text/plain",
			},
		},
	}))
	require.NoError(t, err)
	require.False(t, res.IsError)

	req := stub.lastRequest(t)
	msg := req.Message
	require.Equal(t, a2a.MessageRoleUser, msg.Role)
	require.Equal(t, "sess-1", msg.ContextID, "sessionId is forwarded as contextId")
	require.Equal(t, a2a.TaskID("task-9"), msg.TaskID)
	require.NotEmpty(t, msg.ID)
	require.Equal(t, stubSkillTranslate, msg.Metadata[a2aSkillMetadataKey])

	require.Len(t, msg.Parts, 4)
	require.Equal(t, "hello", msg.Parts[0].Text(), "message text is sent untouched")
	data, ok := msg.Parts[1].Data().(map[string]any)
	require.True(t, ok)
	require.Equal(t, "ja", data["target"])
	require.Equal(t, []byte("plain text file"), msg.Parts[2].Raw())
	require.Equal(t, "text/plain; charset=utf-8", msg.Parts[2].MediaType)
	require.Equal(t, []byte("second"), msg.Parts[3].Raw())
	require.Equal(t, "notes.txt", msg.Parts[3].Filename)
	require.Equal(t, "text/plain", msg.Parts[3].MediaType)

	require.Equal(t, "acme", stub.lastHeader(t).Get("X-Tenant"))

	require.Equal(t, []mcp.Content{&mcp.TextContent{Text: "こんにちは"}}, res.Content)
	meta := resultMeta(t, res)
	require.Equal(t, "sess-1", meta.ContextID)
	require.Equal(t, "msg-1", meta.MessageID)
	require.Equal(t, "1.0", meta.ProtocolVersion)
	require.Empty(t, meta.State)
}

func TestA2ABackendClient_CallTool_V03Wire(t *testing.T) {
	stub := newStubA2AAgent(t, true)
	stub.setResult(&a2a.Task{
		ID:        "task-1",
		ContextID: "sess-1",
		Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: agentMessage("done")},
	})
	c := NewA2ABackendClient("translator", stubAgentServer(stub), nil)
	t.Cleanup(c.Close)

	res, err := c.CallTool(t.Context(), stubSkillSummarize, callArgs(t, map[string]any{
		"sessionId": "sess-1",
		"message":   "summarize this",
	}))
	require.NoError(t, err)
	require.False(t, res.IsError)

	msg := stub.lastRequest(t).Message
	require.Equal(t, "sess-1", msg.ContextID)
	require.Equal(t, "summarize this", msg.Parts[0].Text())
	require.Equal(t, stubSkillSummarize, msg.Metadata[a2aSkillMetadataKey])

	require.Equal(t, []mcp.Content{&mcp.TextContent{Text: "done"}}, res.Content)
	meta := resultMeta(t, res)
	require.Equal(t, "0.3", meta.ProtocolVersion)
	require.Equal(t, "task-1", meta.TaskID)
	require.Equal(t, "completed", meta.State)
}

// --- tools/call: argument errors are isError results, not protocol errors ---

func TestA2ABackendClient_CallTool_ArgumentErrors(t *testing.T) {
	stub := newStubA2AAgent(t, false)
	stub.setResult(agentMessage("unused"))
	c := NewA2ABackendClient("translator", stubAgentServer(stub), nil)
	t.Cleanup(c.Close)

	cases := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{
			name: "unknown skill",
			tool: "nope",
			args: map[string]any{"sessionId": "s", "message": "m"},
			want: `unknown skill "nope"`,
		},
		{
			name: "missing sessionId",
			tool: stubSkillTranslate,
			args: map[string]any{"message": "m"},
			want: "sessionId is required",
		},
		{
			name: "blank sessionId",
			tool: stubSkillTranslate,
			args: map[string]any{"sessionId": "  ", "message": "m"},
			want: "sessionId is required",
		},
		{
			name: "no parts",
			tool: stubSkillTranslate,
			args: map[string]any{"sessionId": "s"},
			want: "at least one of message, data, files is required",
		},
		{
			name: "bad file",
			tool: stubSkillTranslate,
			args: map[string]any{
				"sessionId": "s",
				"files":     []any{map[string]any{"base64": "***not base64***"}},
			},
			want: `"files[0]": invalid base64 content`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := c.CallTool(t.Context(), tc.tool, callArgs(t, tc.args))
			require.NoError(t, err)
			require.True(t, res.IsError)
			require.Contains(t, res.GetError().Error(), tc.want)
		})
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	require.Empty(t, stub.requests, "nothing reaches the agent on an argument error")
}

func TestA2ABackendClient_CallTool_AgentErrorIsErrorResult(t *testing.T) {
	t.Setenv("TEST", "true")
	stub := &stubA2AAgent{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/agent-card.json", stub.serveCard)
	mux.HandleFunc("POST /rpc", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream down", http.StatusBadGateway)
	})
	stub.srv = httptest.NewServer(mux)
	t.Cleanup(stub.srv.Close)
	c := NewA2ABackendClient("translator", stubAgentServer(stub), nil)
	t.Cleanup(c.Close)

	res, err := c.CallTool(t.Context(), stubSkillTranslate, callArgs(t, map[string]any{
		"sessionId": "s", "message": "m",
	}))
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.GetError().Error(), "send message")
}

// --- tools/call: task results and part mapping ---

func TestA2ABackendClient_CallTool_TaskPartsBecomeContent(t *testing.T) {
	stub := newStubA2AAgent(t, false)
	stub.setResult(&a2a.Task{
		ID:        "task-1",
		ContextID: "sess-1",
		Status: a2a.TaskStatus{
			State:   a2a.TaskStateInputRequired,
			Message: agentMessage("Which language?"),
		},
		Artifacts: []*a2a.Artifact{
			{
				ID:          "art-1",
				Name:        "translation",
				Description: "the translated text",
				Parts: []*a2a.Part{
					a2a.NewTextPart("Bonjour"),
					a2a.NewDataPart(map[string]any{"confidence": 0.9}),
				},
			},
			{
				ID: "art-2",
				Parts: []*a2a.Part{
					a2a.NewFileURLPart("https://files.example.com/out.pdf", "application/pdf"),
					{
						Content:   a2a.Raw([]byte("\x89PNG\r\n\x1a\n....")),
						MediaType: "image/png",
						Filename:  "chart.png",
					},
				},
			},
		},
	})
	c := NewA2ABackendClient("translator", stubAgentServer(stub), storage.NewNoopUploader())
	t.Cleanup(c.Close)

	res, err := c.CallTool(t.Context(), stubSkillTranslate, callArgs(t, map[string]any{
		"sessionId": "sess-1", "message": "hello",
	}))
	require.NoError(t, err)
	require.False(t, res.IsError, "input-required is a normal state, not an error")

	require.Len(t, res.Content, 5)
	require.Equal(t, &mcp.TextContent{Text: "Which language?"}, res.Content[0])
	require.Equal(t, &mcp.TextContent{Text: "Bonjour"}, res.Content[1])
	require.Equal(t, &mcp.TextContent{Text: `{"confidence":0.9}`}, res.Content[2])
	link, ok := res.Content[3].(*mcp.ResourceLink)
	require.True(t, ok)
	require.Equal(t, "https://files.example.com/out.pdf", link.URI)
	require.Equal(t, "application/pdf", link.MIMEType)
	img, ok := res.Content[4].(*mcp.ImageContent)
	require.True(t, ok, "file bytes go through generateContent: inline image without storage")
	require.Equal(t, "image/png", img.MIMEType)
	require.Equal(t, []byte("\x89PNG\r\n\x1a\n...."), img.Data)

	structured, ok := res.StructuredContent.(map[string]any)
	require.True(t, ok, "a single data part is the structuredContent")
	require.Equal(t, 0.9, structured["confidence"])

	meta := resultMeta(t, res)
	require.Equal(t, "task-1", meta.TaskID)
	require.Equal(t, "sess-1", meta.ContextID)
	require.Equal(t, "input-required", meta.State)
	require.Equal(t, "msg-1", meta.MessageID)
	require.Equal(t, []a2aArtifactMeta{
		{ArtifactID: "art-1", Name: "translation", Description: "the translated text"},
		{ArtifactID: "art-2"},
	}, meta.Artifacts)
}

func TestA2ABackendClient_CallTool_FileBytesUploadedWhenStorageEnabled(t *testing.T) {
	stub := newStubA2AAgent(t, false)
	stub.setResult(&a2a.Task{
		ID: "task-1", ContextID: "sess-1",
		Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
		Artifacts: []*a2a.Artifact{{
			ID: "art-1",
			Parts: []*a2a.Part{
				{Content: a2a.Raw([]byte("pdf-bytes")), MediaType: "application/pdf"},
			},
		}},
	})
	media := &fakeMediaService{
		enabled: true,
		doFunc: func(_ context.Context, data []byte, contentType string) (string, string, error) {
			require.Equal(t, []byte("pdf-bytes"), data)
			require.Equal(t, "application/pdf", contentType)
			return "id-1", "https://media.example.com/id-1", nil
		},
	}
	c := NewA2ABackendClient("translator", stubAgentServer(stub), media)
	t.Cleanup(c.Close)

	res, err := c.CallTool(t.Context(), stubSkillTranslate, callArgs(t, map[string]any{
		"sessionId": "sess-1", "message": "hello",
	}))
	require.NoError(t, err)
	require.Len(t, res.Content, 1)
	link, ok := res.Content[0].(*mcp.ResourceLink)
	require.True(t, ok)
	require.Equal(t, "https://media.example.com/id-1", link.URI)
	require.Equal(t, "id-1", link.Name)
}

func TestA2ABackendClient_CallTool_FailedTaskIsErrorResult(t *testing.T) {
	stub := newStubA2AAgent(t, false)
	for _, state := range []a2a.TaskState{a2a.TaskStateFailed, a2a.TaskStateRejected} {
		t.Run(string(state), func(t *testing.T) {
			stub.setResult(&a2a.Task{
				ID: "task-1", ContextID: "sess-1",
				Status: a2a.TaskStatus{State: state, Message: agentMessage("cannot do that")},
			})
			c := NewA2ABackendClient("translator", stubAgentServer(stub), nil)
			t.Cleanup(c.Close)

			res, err := c.CallTool(t.Context(), stubSkillTranslate, callArgs(t, map[string]any{
				"sessionId": "sess-1", "message": "hello",
			}))
			require.NoError(t, err)
			require.True(t, res.IsError)
			require.Equal(t, []mcp.Content{&mcp.TextContent{Text: "cannot do that"}}, res.Content)
			require.Equal(t, a2aStateString(state), resultMeta(t, res).State)
		})
	}
}

func TestA2ABackendClient_CallTool_MultipleDataPartsAreAList(t *testing.T) {
	stub := newStubA2AAgent(t, false)
	stub.setResult(&a2a.Message{
		ID: "m", ContextID: "sess-1", Role: a2a.MessageRoleAgent,
		Parts: []*a2a.Part{
			a2a.NewDataPart(map[string]any{"a": 1.0}),
			a2a.NewDataPart(map[string]any{"b": 2.0}),
		},
	})
	c := NewA2ABackendClient("translator", stubAgentServer(stub), nil)
	t.Cleanup(c.Close)

	res, err := c.CallTool(t.Context(), stubSkillTranslate, callArgs(t, map[string]any{
		"sessionId": "sess-1", "data": map[string]any{"x": 1},
	}))
	require.NoError(t, err)
	list, ok := res.StructuredContent.([]any)
	require.True(t, ok)
	require.Len(t, list, 2)
}

func TestA2ABackendClient_Close_RefusesFurtherUse(t *testing.T) {
	stub := newStubA2AAgent(t, false)
	c := NewA2ABackendClient("translator", stubAgentServer(stub), nil)
	_, err := c.EnsureCard(t.Context())
	require.NoError(t, err)
	c.Close()

	_, err = c.ListTools(t.Context(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "client closed")
}

func TestA2AStateString(t *testing.T) {
	require.Equal(t, "completed", a2aStateString(a2a.TaskStateCompleted))
	require.Equal(t, "input-required", a2aStateString(a2a.TaskStateInputRequired))
	require.Equal(t, "auth-required", a2aStateString(a2a.TaskStateAuthRequired))
	require.Equal(t, "", a2aStateString(a2a.TaskStateUnspecified))
}

// --- MCPServer wiring: /mcp/{agent} through the passthrough + authz ---

func TestMCPServer_Init_A2AAgent(t *testing.T) {
	stub := newStubA2AAgent(t, false)
	stub.setResult(agentMessage("ok"))
	servers := config.Servers{"translator": stubAgentServer(stub)}
	s := NewMCPServer(servers, nil)
	require.NoError(t, s.Init(t.Context()))
	t.Cleanup(s.Close)

	_, ok := s.A2AClient("translator")
	require.True(t, ok)
	_, ok = s.BackendClient("translator")
	require.False(t, ok, "an agent is not an MCP backend client")

	infos, err := s.ToolCatalog(t.Context(), "translator")
	require.NoError(t, err)
	require.Equal(t, []string{stubSkillTranslate, stubSkillSummarize}, func() []string {
		names := make([]string, len(infos))
		for i, info := range infos {
			names[i] = info.Name
		}
		return names
	}())

	srv, err := s.Server("translator")
	require.NoError(t, err)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	_, err = srv.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "0.0.1"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	listed, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, []string{stubSkillTranslate, stubSkillSummarize}, toolNames(listed.Tools))

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      stubSkillTranslate,
		Arguments: map[string]any{"sessionId": "sess-1", "message": "hi"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Equal(t, "sess-1", stub.lastRequest(t).Message.ContextID)
	meta, ok := res.Meta[a2aMetaKey].(map[string]any)
	require.True(t, ok, "_meta.a2a survives the wire")
	require.Equal(t, "sess-1", meta["contextId"])
}

func TestMCPServer_A2AAgent_AuthzPerSkill(t *testing.T) {
	stub := newStubA2AAgent(t, false)
	stub.setResult(agentMessage("ok"))
	servers := config.Servers{"translator": stubAgentServer(stub)}

	d := &fakeDecider{
		allowResult:        false,
		allowedToolsResult: []authz.ToolRef{{Server: "translator", Name: stubSkillSummarize}},
	}
	s := NewMCPServer(servers, nil, WithServerMiddleware(func(name string) []mcp.Middleware {
		return []mcp.Middleware{NewAuthzMiddleware(name, d, testAuthzHeaders(), nil)}
	}))
	require.NoError(t, s.Init(t.Context()))
	t.Cleanup(s.Close)
	srv, err := s.Server("translator")
	require.NoError(t, err)

	httpSrv := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	))
	t.Cleanup(httpSrv.Close)
	headers := http.Header{}
	headers.Set("x-user-id", "alice")
	headers.Set("x-user-groups", "readers")
	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "0.0.1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   httpSrv.URL,
		HTTPClient: &http.Client{Transport: &headerRoundTripper{headers: headers}},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	// tools/list is narrowed to the skills the policy allows.
	listed, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, []string{stubSkillSummarize}, toolNames(listed.Tools))
	require.Equal(t, 1, d.allowedToolsCallCount())

	// tools/call on a denied skill never reaches the agent; the decider saw
	// server=<agent name> and tool=<skill id>.
	_, err = session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      stubSkillTranslate,
		Arguments: map[string]any{"sessionId": "sess-1", "message": "hi"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool not allowed by policy")
	require.Equal(t, 1, d.allowCallCount())
	d.mu.Lock()
	require.Equal(t, authz.ToolRef{Server: "translator", Name: stubSkillTranslate},
		d.allowCalls[len(d.allowCalls)-1].t)
	d.mu.Unlock()
	stub.mu.Lock()
	require.Empty(t, stub.requests)
	stub.mu.Unlock()
}
