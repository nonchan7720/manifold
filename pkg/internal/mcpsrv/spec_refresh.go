package mcpsrv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/oasbreaking"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// openAPIServerState は OpenAPI モードのサーバーについて、現在 srv に登録されている
// ツールとその元になった spec を記録する。
type openAPIServerState struct {
	srv       *mcp.Server
	cfg       *config.Server
	toolInfos []ToolInfo
	specHash  string
	// spec は toolInfos の元になった OpenAPI 3.x ドキュメントで、リフレッシュ時の
	// 破壊的変更検出の base になる。起動時の取得に失敗した場合や Swagger 2.x の
	// 場合は nil で、その間は検出しない。
	spec *openapi3.T
	// operations は spec の "METHOD /path" → ツール名。
	operations map[string]string
	// rejectedHash は specRefreshRejectOn で直近に拒否した spec のハッシュ。
	// 採用するまで base は変わらないため、同じ spec を毎回 diff・報告し直さない。
	rejectedHash string
}

// adopt は register から作ったツールを現在のものとして記録する。
func (st *openAPIServerState) adopt(register *MCPToolRegistry, toolInfos []ToolInfo) {
	st.toolInfos = toolInfos
	st.specHash = register.SpecHash()
	st.spec = register.OpenAPISpec()
	st.operations = toolOperations(register)
	st.rejectedHash = ""
}

// toolOperations は register のツールを "METHOD /path" → ツール名 で返す。
func toolOperations(register *MCPToolRegistry) map[string]string {
	defs := register.Definitions()
	ops := make(map[string]string, len(defs))
	for _, d := range defs {
		ops[d.Method+" "+d.Path] = d.Name
	}
	return ops
}

// refreshServer は spec を取り直し、内容が変わっていればツール定義を入れ替える。
// 入れ替えを行った場合のみ true を返す。変更が specRefreshRejectOn 以上の
// 破壊的変更を含む場合は入れ替えず、既存のツールを提供し続ける（false, nil）。
func (s *MCPServer) refreshServer(ctx context.Context, name string) (bool, error) {
	s.mu.Lock()
	state, ok := s.openAPIStates[name]
	var (
		baseSpec     *openapi3.T
		baseOps      map[string]string
		baseHash     string
		rejectedHash string
		rejectOn     oasbreaking.Level
	)
	if ok {
		baseSpec, baseOps, baseHash = state.spec, state.operations, state.specHash
		rejectedHash = state.rejectedHash
		rejectOn = state.cfg.EffectiveSpecRefreshRejectOn(s.refreshRejectOn)
	}
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

	newHash := register.SpecHash()
	if newHash == baseHash {
		return false, nil
	}
	if newHash == rejectedHash {
		slog.DebugContext(
			ctx,
			"refreshed spec is still the rejected revision; keeping current tools",
			slog.String("server", name),
		)
		return false, nil
	}
	// Same rule as at startup (registerOpenAPIServer): a spec that lost its
	// servers entry would leave every tools/call without a base URL, so keep
	// the current tools instead of adopting it. Remember the revision like a
	// rejected breaking change, so the WARN is logged once per revision
	// rather than on every tick until the spec is fixed.
	if err := checkCatalogBaseURL(register); err != nil {
		s.mu.Lock()
		state.rejectedHash = newHash
		s.mu.Unlock()
		s.metrics.rejected.Add(ctx, 1, metric.WithAttributes(
			attribute.String("server", name), attribute.String("reason", rejectReasonBaseURL),
		))
		return false, err
	}
	if s.rejectSpecChanges(ctx, name, baseSpec, baseOps, register, rejectOn) {
		s.mu.Lock()
		state.rejectedHash = newHash
		s.mu.Unlock()
		return false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	toolInfos := attachTools(state.srv, register, s.mediaUploader)
	removed := make([]string, 0, len(state.toolInfos))
	for _, prev := range state.toolInfos {
		if !slices.ContainsFunc(toolInfos, func(ti ToolInfo) bool { return ti.Name == prev.Name }) {
			removed = append(removed, prev.Name)
		}
	}
	if len(removed) > 0 {
		state.srv.RemoveTools(removed...)
	}
	state.adopt(register, toolInfos)
	// The tools just changed underneath mcpServers.<name>.cache: drop the
	// cached tools/list pages and tools/call results so a client re-reading
	// the list after notifications/tools/list_changed sees the new tools
	// rather than the old ones for up to cache.toolsList.
	if s.toolCache != nil {
		s.toolCache.InvalidateServer(name)
	}
	return true, nil
}

// rejectSpecChanges は base（現在提供中の spec）から register の spec への変更を
// oasdiff で分類してログとメトリクスに記録し、最大レベルが rejectOn 以上
// （rejectOn が LevelNone なら常に false）なら true を返す。base が無い
// （起動時の取得失敗・Swagger 2.x）場合や検出自体に失敗した場合は、
// 従来どおり採用させるため false を返す。
func (s *MCPServer) rejectSpecChanges(
	ctx context.Context,
	name string,
	base *openapi3.T,
	baseOps map[string]string,
	register *MCPToolRegistry,
	rejectOn oasbreaking.Level,
) bool {
	revision := register.OpenAPISpec()
	if base == nil || revision == nil {
		return false
	}
	changes, err := oasbreaking.Check(base, revision)
	if err != nil {
		slog.WarnContext(
			ctx,
			"spec refresh breaking-change detection failed; adopting the new spec",
			slog.String("server", name),
			slog.Any("error", err),
		)
		return false
	}

	// 削除された operation は base にしか無い。それ以外は新しい spec の名前を優先する。
	toolByOperation := maps.Clone(baseOps)
	if toolByOperation == nil {
		toolByOperation = map[string]string{}
	}
	maps.Copy(toolByOperation, toolOperations(register))
	oasbreaking.ResolveTools(changes, toolByOperation)

	for _, c := range changes {
		s.metrics.changes.Add(ctx, 1, metric.WithAttributes(
			attribute.String("server", name), attribute.String("level", c.Level.String()),
		))
	}

	counts := oasbreaking.Count(changes)
	summary := []any{
		slog.String("server", name),
		slog.Int("error", counts.Err),
		slog.Int("warning", counts.Warn),
		slog.Int("info", counts.Info),
	}
	maxLevel := oasbreaking.MaxLevel(changes)
	if rejectOn != oasbreaking.LevelNone && maxLevel >= rejectOn {
		s.metrics.rejected.Add(ctx, 1, metric.WithAttributes(
			attribute.String("server", name), attribute.String("level", maxLevel.String()),
			attribute.String("reason", rejectReasonBreaking),
		))
		slog.ErrorContext(ctx, "spec refresh rejected; keeping the previous spec and tools",
			append(summary,
				slog.String("rejectOn", rejectOn.String()),
				slog.Any("changes", changesAtOrAbove(changes, rejectOn)),
			)...)
		return true
	}

	if counts.Breaking() == 0 {
		slog.InfoContext(ctx, "spec refresh detected no breaking changes", summary...)
		return false
	}
	slog.WarnContext(ctx, "spec refresh detected breaking changes", summary...)
	for _, c := range changes {
		if !c.Level.Breaking() {
			continue
		}
		slog.WarnContext(ctx, "breaking change in refreshed spec", changeLogAttrs(name, c)...)
	}
	return false
}

// changeLogAttrs は破壊的変更 1 件分のログ属性を返す。
func changeLogAttrs(server string, c oasbreaking.Change) []any {
	return []any{
		slog.String("server", server),
		slog.String("level", c.Level.String()),
		slog.String("id", c.ID),
		slog.String("operation", c.Operation),
		slog.String("tool", c.Tool),
		slog.String("message", c.Message),
	}
}

// changesAtOrAbove は拒否の理由になった（minLevel 以上の）変更をログ用に返す。
func changesAtOrAbove(
	changes []oasbreaking.Change,
	minLevel oasbreaking.Level,
) []oasbreaking.Change {
	return slices.DeleteFunc(slices.Clone(changes), func(c oasbreaking.Change) bool {
		return c.Level < minLevel
	})
}

// Values of the "reason" attribute on the rejected metric.
const (
	rejectReasonBreaking = "breaking_change" // specRefreshRejectOn
	rejectReasonBaseURL  = "base_url_unresolved"
)

// specRefreshMetrics は spec リフレッシュ時の破壊的変更検出のメトリクス。
type specRefreshMetrics struct {
	changes  metric.Int64Counter
	rejected metric.Int64Counter
}

func newSpecRefreshMetrics(mp metric.MeterProvider) *specRefreshMetrics {
	meter := mp.Meter("github.com/nonchan7720/manifold/pkg/internal/mcpsrv")
	noopMeter := noop.NewMeterProvider().Meter("")
	counter := func(name, unit, desc string) metric.Int64Counter {
		c, err := meter.Int64Counter(name, metric.WithDescription(desc), metric.WithUnit(unit))
		if err != nil {
			slog.Warn(
				"failed to create metric",
				slog.String("metric", name),
				slog.Any("error", err),
			)
			c, _ = noopMeter.Int64Counter(name)
		}
		return c
	}
	return &specRefreshMetrics{
		changes: counter(
			"manifold.openapi.spec_refresh.changes", "{change}",
			"Changes detected between the active and the refreshed OpenAPI spec, by level",
		),
		rejected: counter(
			"manifold.openapi.spec_refresh.rejected",
			"{spec}",
			"Refreshed OpenAPI specs rejected (breaking changes per specRefreshRejectOn, or an unresolvable baseURL), by reason and, for breaking changes, the highest change level",
		),
	}
}

// StartSpecRefresh は OpenAPI モードの各サーバーについて、解決された間隔ごとに
// spec を取り直す goroutine を起動する。既に走っているサイクルがあれば
// 停止してから起動し直す。Close で全て停止する。
func (s *MCPServer) StartSpecRefresh(ctx context.Context, global config.SpecRefreshConfig) {
	s.stopSpecRefresh()

	ctx, cancel := context.WithCancel(ctx)

	s.mu.Lock()
	s.refreshCancel = cancel
	s.refreshRejectOn = global.RejectOn
	targets := make(map[string]time.Duration, len(s.openAPIStates))
	for name, state := range s.openAPIStates {
		// tools.file を持つサーバーは生成物から起動しており、spec を取り直す対象では
		// ない。EffectiveSpecRefreshInterval が既に 0 を返すので自然に外れるが、
		// 「リフレッシュしない」という不変条件をここでも明示しておく。
		if state.cfg.GeneratedToolsFile() != "" {
			continue
		}
		if interval := state.cfg.EffectiveSpecRefreshInterval(global.Interval); interval > 0 {
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
			case errors.Is(err, errBaseURLUnresolved):
				slog.ErrorContext(ctx, "spec refresh rejected; keeping the previous spec and tools",
					slog.String("server", name), slog.String("reason", rejectReasonBaseURL),
					slog.Any("error", err))
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
