package httphandler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/nonchan7720/manifold/pkg/internal/mcpsrv"
	edgeservices "github.com/nonchan7720/manifold/pkg/services/edge"
)

const (
	edgeHeartbeatSec     = 20
	edgeHeartbeatTimeout = 2 * edgeHeartbeatSec * time.Second
	edgeAuthTimeout      = 5 * time.Second
	// closeStatusUnauthorized is a private-use WebSocket close code (RFC 6455
	// §7.4.2, 4000-4999 range) signalling a failed/absent edge token auth.
	closeStatusUnauthorized websocket.StatusCode = 4401
)

// EdgeWSHandler serves GET /edge/ws: the browser extension's outbound
// WebSocket connection for the WebMCP reverse-connection gateway (see
// docs/design/webmcp-reverse-gateway.ja.md).
type EdgeWSHandler struct {
	edgeCfg config.EdgeConfig
	pairing *edgeservices.PairingService
	gateway *mcpsrv.ReverseGateway
}

// NewEdgeWSHandler creates an EdgeWSHandler.
func NewEdgeWSHandler(
	edgeCfg config.EdgeConfig,
	pairing *edgeservices.PairingService,
	gateway *mcpsrv.ReverseGateway,
) *EdgeWSHandler {
	return &EdgeWSHandler{edgeCfg: edgeCfg.WithDefaults(), pairing: pairing, gateway: gateway}
}

// ServeHTTP implements http.Handler for GET /edge/ws.
func (h *EdgeWSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := trace.StartSpan(r.Context(), "httphandler/EdgeWSHandler/ServeHTTP")
	var err error
	defer func() { trace.EndSpan(ctx, err) }()

	if h.edgeCfg.Auth == config.EdgeAuthForwardAuth {
		http.Error(
			w,
			"edge.auth=forwardAuth is not implemented yet",
			http.StatusNotImplemented,
		)
		return
	}

	// 拡張(chrome-extension://)からの接続は同一オリジンにならないため、Origin 検証は
	// first-message auth（edge token）に委ねる。
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow() //nolint: errcheck

	identityKey, ok := h.authenticate(ctx, conn)
	if !ok {
		return
	}

	err = wsjson.Write(ctx, conn, mcpsrv.EdgeEnvelope{
		Type:         mcpsrv.EdgeFrameReady,
		HeartbeatSec: edgeHeartbeatSec,
		Origins:      h.gateway.Origins(),
	})
	if err != nil {
		return
	}

	session := newEdgeConnSession(uuid.NewString(), conn, identityKey, h.gateway)
	session.run(ctx)
}

// authenticate reads the first frame (must arrive within edgeAuthTimeout) and
// validates it as an auth frame carrying a valid edge token.
func (h *EdgeWSHandler) authenticate(
	ctx context.Context,
	conn *websocket.Conn,
) (domainedge.IdentityKey, bool) {
	authCtx, cancel := context.WithTimeout(ctx, edgeAuthTimeout)
	defer cancel()

	var frame mcpsrv.EdgeEnvelope
	if err := wsjson.Read(authCtx, conn, &frame); err != nil || frame.Type != mcpsrv.EdgeFrameAuth {
		_ = conn.Close(closeStatusUnauthorized, "auth required")
		return "", false
	}

	identityKeys, err := h.pairing.Authenticate(ctx, frame.Token)
	if err != nil || len(identityKeys) == 0 {
		_ = conn.Close(closeStatusUnauthorized, "invalid edge token")
		return "", false
	}
	return identityKeys[0], true
}

// edgeConnSession demultiplexes one physical edge WebSocket connection's
// envelope frames across its live (origin, appSession) bindings.
type edgeConnSession struct {
	connID      string
	conn        *websocket.Conn
	identityKey domainedge.IdentityKey
	gateway     *mcpsrv.ReverseGateway
	sender      mcpsrv.EdgeFrameSender

	mu       sync.Mutex
	channels map[string]chan json.RawMessage
}

func newEdgeConnSession(
	connID string,
	conn *websocket.Conn,
	identityKey domainedge.IdentityKey,
	gateway *mcpsrv.ReverseGateway,
) *edgeConnSession {
	return &edgeConnSession{
		connID:      connID,
		conn:        conn,
		identityKey: identityKey,
		gateway:     gateway,
		sender:      &wsFrameSender{conn: conn},
		channels:    map[string]chan json.RawMessage{},
	}
}

func bindingChanKey(origin, appSession string) string {
	return origin + "|" + appSession
}

// run reads frames until the connection closes or goes quiet for longer than
// edgeHeartbeatTimeout, then drops every binding this connection owned.
func (s *edgeConnSession) run(ctx context.Context) {
	for {
		readCtx, cancel := context.WithTimeout(ctx, edgeHeartbeatTimeout)
		var frame mcpsrv.EdgeEnvelope
		err := wsjson.Read(readCtx, s.conn, &frame)
		cancel()
		if err != nil {
			break
		}
		s.dispatch(ctx, frame)
	}
	s.gateway.DropConnection(context.WithoutCancel(ctx), s.connID)
}

func (s *edgeConnSession) dispatch(ctx context.Context, frame mcpsrv.EdgeEnvelope) {
	switch frame.Type {
	case mcpsrv.EdgeFrameAppUp:
		s.handleAppUp(ctx, frame)
	case mcpsrv.EdgeFrameAppDown:
		s.handleAppDown(ctx, frame)
	case mcpsrv.EdgeFrameMCP:
		s.routeMCPFrame(frame)
	case mcpsrv.EdgeFramePing:
		_ = wsjson.Write(ctx, s.conn, mcpsrv.EdgeEnvelope{Type: mcpsrv.EdgeFramePong})
	case mcpsrv.EdgeFramePong:
		// heartbeat の応答。特に処理は無い。
	case mcpsrv.EdgeFrameAuth, mcpsrv.EdgeFrameReady, mcpsrv.EdgeFrameError:
		// これらは M → 拡張方向専用のフレーム。拡張から送られてきたら不正入力として扱う。
		fallthrough
	default:
		_ = wsjson.Write(ctx, s.conn, mcpsrv.EdgeEnvelope{
			Type:    mcpsrv.EdgeFrameError,
			Message: "unknown frame type: " + string(frame.Type),
		})
	}
}

func (s *edgeConnSession) handleAppUp(ctx context.Context, frame mcpsrv.EdgeEnvelope) {
	if !s.gateway.IsKnownOrigin(frame.Origin) {
		_ = wsjson.Write(ctx, s.conn, mcpsrv.EdgeEnvelope{
			Type:    mcpsrv.EdgeFrameError,
			Message: "origin not allowed: " + frame.Origin,
		})
		return
	}

	key := bindingChanKey(frame.Origin, frame.AppSession)
	incoming := make(chan json.RawMessage, 32)
	s.mu.Lock()
	s.channels[key] = incoming
	s.mu.Unlock()

	binding := domainedge.Binding{
		IdentityKey: s.identityKey,
		Origin:      frame.Origin,
		AppSession:  frame.AppSession,
		ConnID:      s.connID,
	}
	// HandleAppUp が initialize/tools-list のために往復通信を行うため、読み取りループを
	// 塞がないよう非同期に実行する。
	go func() {
		if err := s.gateway.HandleAppUp(ctx, binding, s.sender, incoming); err != nil {
			slog.ErrorContext(ctx, "edge app.up failed",
				slog.String("origin", frame.Origin), slog.Any("error", err))
			_ = wsjson.Write(ctx, s.conn, mcpsrv.EdgeEnvelope{
				Type:    mcpsrv.EdgeFrameError,
				Message: err.Error(),
			})
			s.mu.Lock()
			delete(s.channels, key)
			s.mu.Unlock()
		}
	}()
}

func (s *edgeConnSession) handleAppDown(ctx context.Context, frame mcpsrv.EdgeEnvelope) {
	key := bindingChanKey(frame.Origin, frame.AppSession)
	s.mu.Lock()
	if ch, ok := s.channels[key]; ok {
		delete(s.channels, key)
		close(ch)
	}
	s.mu.Unlock()

	s.gateway.HandleAppDown(ctx, s.identityKey, frame.Origin, frame.AppSession)
}

func (s *edgeConnSession) routeMCPFrame(frame mcpsrv.EdgeEnvelope) {
	key := bindingChanKey(frame.Origin, frame.AppSession)
	s.mu.Lock()
	ch, ok := s.channels[key]
	s.mu.Unlock()
	if !ok {
		return // 対応する app.up の無い遅延/不正フレームは無視する
	}
	ch <- frame.Payload
}

// wsFrameSender adapts a *websocket.Conn to mcpsrv.EdgeFrameSender.
type wsFrameSender struct {
	conn *websocket.Conn
}

func (s *wsFrameSender) SendEdgeFrame(ctx context.Context, frame mcpsrv.EdgeEnvelope) error {
	return wsjson.Write(ctx, s.conn, frame)
}
