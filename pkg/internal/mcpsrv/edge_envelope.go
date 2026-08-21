package mcpsrv

import "encoding/json"

// EdgeFrameType is the "type" discriminator of an edge WebSocket envelope
// frame (see the "フレーム定義" table in docs/design/webmcp-reverse-gateway.ja.md).
type EdgeFrameType string

const (
	EdgeFrameAuth    EdgeFrameType = "auth"
	EdgeFrameReady   EdgeFrameType = "ready"
	EdgeFrameAppUp   EdgeFrameType = "app.up"
	EdgeFrameAppDown EdgeFrameType = "app.down"
	EdgeFrameMCP     EdgeFrameType = "mcp"
	EdgeFramePing    EdgeFrameType = "ping"
	EdgeFramePong    EdgeFrameType = "pong"
	EdgeFrameError   EdgeFrameType = "error"
)

// EdgeEnvelope is the single frame shape multiplexed over the edge
// WebSocket connection. Fields are populated according to Type; unused
// fields are omitted on the wire.
type EdgeEnvelope struct {
	V            int             `json:"v,omitempty"`
	Type         EdgeFrameType   `json:"type"`
	Token        string          `json:"token,omitempty"`
	HeartbeatSec int             `json:"heartbeatSec,omitempty"`
	Origins      []string        `json:"origins,omitempty"`
	Origin       string          `json:"origin,omitempty"`
	AppSession   string          `json:"appSession,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	Message      string          `json:"message,omitempty"`
}
