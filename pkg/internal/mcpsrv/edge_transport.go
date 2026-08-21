package mcpsrv

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// EdgeFrameSender sends a single envelope frame over the edge WebSocket
// connection that owns a binding.
type EdgeFrameSender interface {
	SendEdgeFrame(ctx context.Context, frame EdgeEnvelope) error
}

// EdgeTransport is an mcp.Transport that carries one (Origin, AppSession)
// binding's JSON-RPC traffic over a shared edge WebSocket connection,
// multiplexed via "mcp" envelope frames. Incoming is fed raw MCP payloads by
// the WebSocket connection's frame-dispatch loop; it must be dedicated to
// this binding (not shared across bindings).
type EdgeTransport struct {
	Origin     string
	AppSession string
	Sender     EdgeFrameSender
	Incoming   <-chan json.RawMessage
}

// Connect implements [mcp.Transport].
func (t *EdgeTransport) Connect(context.Context) (mcp.Connection, error) {
	return &edgeConnection{transport: t, closed: make(chan struct{})}, nil
}

type edgeConnection struct {
	transport *EdgeTransport
	closeOnce sync.Once
	closed    chan struct{}
}

func (c *edgeConnection) SessionID() string { return c.transport.AppSession }

func (c *edgeConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closed:
		return nil, io.EOF
	case raw, ok := <-c.transport.Incoming:
		if !ok {
			return nil, io.EOF
		}
		return jsonrpc.DecodeMessage(raw)
	}
}

func (c *edgeConnection) Write(ctx context.Context, msg jsonrpc.Message) error {
	select {
	case <-c.closed:
		return mcp.ErrConnectionClosed
	default:
	}
	raw, err := jsonrpc.EncodeMessage(msg)
	if err != nil {
		return err
	}
	return c.transport.Sender.SendEdgeFrame(ctx, EdgeEnvelope{
		Type:       EdgeFrameMCP,
		Origin:     c.transport.Origin,
		AppSession: c.transport.AppSession,
		Payload:    raw,
	})
}

func (c *edgeConnection) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}
