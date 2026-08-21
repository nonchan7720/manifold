package mcpsrv

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type recordingFrameSender struct {
	frames chan EdgeEnvelope
}

func newRecordingFrameSender() *recordingFrameSender {
	return &recordingFrameSender{frames: make(chan EdgeEnvelope, 8)}
}

func (s *recordingFrameSender) SendEdgeFrame(_ context.Context, frame EdgeEnvelope) error {
	s.frames <- frame
	return nil
}

func TestEdgeConnection_Write_SendsMCPFrame(t *testing.T) {
	sender := newRecordingFrameSender()
	transport := &EdgeTransport{
		Origin:     "https://app1.example.com",
		AppSession: "session-1",
		Sender:     sender,
		Incoming:   make(chan json.RawMessage),
	}
	conn, err := transport.Connect(t.Context())
	require.NoError(t, err)

	req := &jsonrpc.Request{Method: "ping"}
	require.NoError(t, conn.Write(t.Context(), req))

	frame := <-sender.frames
	require.Equal(t, EdgeFrameMCP, frame.Type)
	require.Equal(t, "https://app1.example.com", frame.Origin)
	require.Equal(t, "session-1", frame.AppSession)
	require.Contains(t, string(frame.Payload), `"ping"`)
}

func TestEdgeConnection_Read_DecodesIncomingPayload(t *testing.T) {
	incoming := make(chan json.RawMessage, 1)
	transport := &EdgeTransport{
		Origin:     "https://app1.example.com",
		AppSession: "session-1",
		Sender:     newRecordingFrameSender(),
		Incoming:   incoming,
	}
	conn, err := transport.Connect(t.Context())
	require.NoError(t, err)

	raw, err := jsonrpc.EncodeMessage(&jsonrpc.Request{Method: "ping"})
	require.NoError(t, err)
	incoming <- raw

	msg, err := conn.Read(t.Context())
	require.NoError(t, err)
	req, ok := msg.(*jsonrpc.Request)
	require.True(t, ok)
	require.Equal(t, "ping", req.Method)
}

func TestEdgeConnection_Read_ReturnsEOFAfterClose(t *testing.T) {
	transport := &EdgeTransport{
		Origin:     "https://app1.example.com",
		AppSession: "session-1",
		Sender:     newRecordingFrameSender(),
		Incoming:   make(chan json.RawMessage),
	}
	conn, err := transport.Connect(t.Context())
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	_, err = conn.Read(t.Context())
	require.ErrorIs(t, err, io.EOF)
}

func TestEdgeConnection_Write_FailsAfterClose(t *testing.T) {
	transport := &EdgeTransport{
		Origin:     "https://app1.example.com",
		AppSession: "session-1",
		Sender:     newRecordingFrameSender(),
		Incoming:   make(chan json.RawMessage),
	}
	conn, err := transport.Connect(t.Context())
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	err = conn.Write(t.Context(), &jsonrpc.Request{Method: "ping"})
	require.ErrorIs(t, err, mcp.ErrConnectionClosed)
}

func TestEdgeConnection_SessionID_ReturnsAppSession(t *testing.T) {
	transport := &EdgeTransport{
		Origin:     "https://app1.example.com",
		AppSession: "session-1",
		Sender:     newRecordingFrameSender(),
		Incoming:   make(chan json.RawMessage),
	}
	conn, err := transport.Connect(t.Context())
	require.NoError(t, err)
	require.Equal(t, "session-1", conn.SessionID())
}

// --- ラウンドトリップ: 2 つの EdgeTransport をブリッジし、実際の mcp.Client / mcp.Server で
// initialize + tools/list + tools/call まで成立することを確認する。

type bridgedSender struct {
	peerIncoming chan json.RawMessage
}

func (s *bridgedSender) SendEdgeFrame(_ context.Context, frame EdgeEnvelope) error {
	s.peerIncoming <- frame.Payload
	return nil
}

func TestEdgeTransport_RoundTrip_ClientCallsServerTool(t *testing.T) {
	toClient := make(chan json.RawMessage, 16)
	toServer := make(chan json.RawMessage, 16)

	clientTransport := &EdgeTransport{
		Origin:     "https://app1.example.com",
		AppSession: "session-1",
		Sender:     &bridgedSender{peerIncoming: toServer},
		Incoming:   toClient,
	}
	serverTransport := &EdgeTransport{
		Origin:     "https://app1.example.com",
		AppSession: "session-1",
		Sender:     &bridgedSender{peerIncoming: toClient},
		Incoming:   toServer,
	}

	srv := mcp.NewServer(&mcp.Implementation{Name: "page", Version: "0.0.1"}, nil)
	srv.AddTool(
		&mcp.Tool{
			Name:        "echo",
			Description: "echo",
			InputSchema: map[string]any{"type": "object"},
		},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
		},
	)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, err := srv.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)

	client := mcp.NewClient(&mcp.Implementation{Name: "manifold", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 1)
	require.Equal(t, "echo", tools.Tools[0].Name)

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "echo"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "pong", text.Text)
}
