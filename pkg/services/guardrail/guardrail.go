// Package guardrail は LiteLLM の Generic Guardrail API
// （https://docs.litellm.ai/docs/adding_provider/generic_guardrail_api）の
// クライアント。ガードレールの API には標準化された仕様が無いため、LiteLLM 向けに
// 作られたガードレールのサービスをそのまま使えるこの形に固定する。
package guardrail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/client"
)

// Action はガードレールの判定。
type Action string

const (
	// ActionNone は変更なし。
	ActionNone Action = "NONE"
	// ActionIntervened はガードレールがテキストを書き換えた（マスキング等）。
	ActionIntervened Action = "GUARDRAIL_INTERVENED"
	// ActionBlocked はテキストを通さない。
	ActionBlocked Action = "BLOCKED"
)

// InputType は検査するテキストがリクエスト側かレスポンス側か。
type InputType string

const (
	InputTypeRequest  InputType = "request"
	InputTypeResponse InputType = "response"
)

// maxResponseSize はガードレールの応答の最大バイト数。
const maxResponseSize = 16 << 20

// Request はガードレールに渡すテキストと文脈。
type Request struct {
	// Texts は検査するテキスト。結果の Texts はこれと 1 対 1 で対応する。
	Texts     []string
	InputType InputType
	// TraceID は呼び出しを関連付ける ID（litellm_trace_id）。
	TraceID string
	// Params はガードレールへ渡す任意の文脈
	// （additional_provider_specific_params）。
	Params map[string]any
}

// Result はガードレールの判定結果。
type Result struct {
	Action Action
	// Texts は Action が ActionIntervened のときの書き換え後のテキスト。
	// Request.Texts と同じ数・順序であることを Apply が保証する。
	Texts []string
	// BlockedReason は Action が ActionBlocked のときの理由。
	BlockedReason string
}

// Guardrail はテキストを検査・マスキングするガードレール。
type Guardrail interface {
	Apply(ctx context.Context, req Request) (Result, error)
}

type apiRequest struct {
	Texts          []string       `json:"texts"`
	InputType      InputType      `json:"input_type"`
	CallID         string         `json:"litellm_call_id"`
	TraceID        string         `json:"litellm_trace_id"`
	RequestData    map[string]any `json:"request_data"`
	ProviderParams map[string]any `json:"additional_provider_specific_params,omitempty"`
}

type apiResponse struct {
	Action        Action   `json:"action"`
	BlockedReason string   `json:"blocked_reason,omitempty"`
	Texts         []string `json:"texts,omitempty"`
}

// HTTPGuardrail は Generic Guardrail API を HTTP で呼ぶ Guardrail。
type HTTPGuardrail struct {
	url        string
	headers    map[string]string
	httpClient *http.Client
}

var _ Guardrail = (*HTTPGuardrail)(nil)

// NewHTTPGuardrail は cfg の URL を呼ぶ Guardrail を返す。httpClient が nil なら
// cfg.Timeout を持つ既定のクライアントを使う。
func NewHTTPGuardrail(cfg config.GuardrailConfig, httpClient *http.Client) *HTTPGuardrail {
	if httpClient == nil {
		httpClient = &http.Client{
			Transport: client.CustomTransport(),
			Timeout:   cfg.TimeoutOrDefault(),
		}
	}
	return &HTTPGuardrail{url: cfg.URL, headers: cfg.Headers, httpClient: httpClient}
}

// Apply は req をガードレールに送り、判定を返す。ガードレールが不正な応答
// （未知の action、Texts の数の不一致等）を返した場合は
// エラーにする。
func (g *HTTPGuardrail) Apply(ctx context.Context, req Request) (_ Result, rErr error) {
	ctx = trace.StartSpan(ctx, "guardrail/HTTPGuardrail/Apply")
	defer func() { trace.EndSpan(ctx, rErr) }()

	traceID := req.TraceID
	if traceID == "" {
		traceID = uuid.NewString()
	}
	body, err := json.Marshal(apiRequest{
		Texts:          req.Texts,
		InputType:      req.InputType,
		CallID:         uuid.NewString(),
		TraceID:        traceID,
		RequestData:    map[string]any{},
		ProviderParams: req.Params,
	})
	if err != nil {
		return Result{}, fmt.Errorf("guardrail: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.url, bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("guardrail: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range g.headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := g.httpClient.Do(httpReq)
	if err != nil {
		return Result{}, fmt.Errorf("guardrail: call: %w", err)
	}
	defer resp.Body.Close() //nolint: errcheck
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("guardrail: returned status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return Result{}, fmt.Errorf("guardrail: read response: %w", err)
	}
	if len(raw) > maxResponseSize {
		return Result{}, fmt.Errorf("guardrail: response exceeds %d bytes", maxResponseSize)
	}
	var out apiResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return Result{}, fmt.Errorf("guardrail: decode response: %w", err)
	}

	switch out.Action {
	case ActionNone:
		return Result{Action: ActionNone}, nil
	case ActionIntervened:
		if len(out.Texts) != len(req.Texts) {
			return Result{}, fmt.Errorf(
				"guardrail: GUARDRAIL_INTERVENED returned %d texts for %d inputs",
				len(out.Texts), len(req.Texts),
			)
		}
		return Result{Action: ActionIntervened, Texts: out.Texts}, nil
	case ActionBlocked:
		return Result{Action: ActionBlocked, BlockedReason: out.BlockedReason}, nil
	default:
		return Result{}, fmt.Errorf("guardrail: unknown action %q", out.Action)
	}
}
