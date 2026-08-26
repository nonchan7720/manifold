package mcpsrv

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
)

// openAPIServerState は OpenAPI モードのサーバーについて、現在 srv に登録されている
// ツールとその元になった spec を記録する。
type openAPIServerState struct {
	srv       *mcp.Server
	cfg       *config.Server
	toolNames []string
	specHash  string
}

// refreshServer は spec を取り直し、内容が変わっていればツール定義を入れ替える。
// 入れ替えを行った場合のみ true を返す。
func (s *MCPServer) refreshServer(ctx context.Context, name string) (bool, error) {
	s.mu.Lock()
	state, ok := s.openAPIStates[name]
	s.mu.Unlock()
	if !ok {
		return false, fmt.Errorf("not found openapi mcp server: %s", name)
	}

	register, err := RegisterOpenAPI(
		ctx,
		state.cfg.Spec,
		state.cfg.BaseURL,
		state.cfg.ExtraHeaders,
		registerOpenAPIOptions(state.cfg)...)
	if err != nil {
		return false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if register.SpecHash() == state.specHash {
		return false, nil
	}
	toolNames := attachTools(state.srv, register, s.mediaUploader)
	removed := make([]string, 0, len(state.toolNames))
	for _, toolName := range state.toolNames {
		if !slices.Contains(toolNames, toolName) {
			removed = append(removed, toolName)
		}
	}
	if len(removed) > 0 {
		state.srv.RemoveTools(removed...)
	}
	state.toolNames = toolNames
	state.specHash = register.SpecHash()
	return true, nil
}

// StartSpecRefresh は OpenAPI モードの各サーバーについて、解決された間隔ごとに
// spec を取り直す goroutine を起動する。Close で全て停止する。
func (s *MCPServer) StartSpecRefresh(ctx context.Context, global time.Duration) {
	ctx, cancel := context.WithCancel(ctx)

	s.mu.Lock()
	s.refreshCancel = cancel
	targets := make(map[string]time.Duration, len(s.openAPIStates))
	for name, state := range s.openAPIStates {
		if interval := state.cfg.EffectiveSpecRefreshInterval(global); interval > 0 {
			targets[name] = interval
		}
	}
	s.mu.Unlock()

	for name, interval := range targets {
		s.refreshWG.Add(1)
		go func() {
			defer s.refreshWG.Done()
			s.refreshLoop(ctx, name, interval)
		}()
	}
}

func (s *MCPServer) refreshLoop(ctx context.Context, name string, interval time.Duration) {
	slog.InfoContext(ctx, "start spec refresh",
		slog.String("server", name), slog.Duration("interval", interval))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 取得・パースに失敗しても既存のツール定義は残したまま次回に持ち越す。
			changed, err := s.refreshServer(ctx, name)
			switch {
			case err != nil:
				slog.WarnContext(ctx, "spec refresh failed",
					slog.String("server", name), slog.Any("error", err))
			case changed:
				slog.InfoContext(ctx, "spec refreshed", slog.String("server", name))
			}
		}
	}
}

func (s *MCPServer) stopSpecRefresh() {
	s.mu.Lock()
	cancel := s.refreshCancel
	s.refreshCancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.refreshWG.Wait()
}
