// Package guardrail は LiteLLM の Generic Guardrail API
// （https://docs.litellm.ai/docs/adding_provider/generic_guardrail_api）の
// クライアント。ガードレールの API には標準化された仕様が無いため、LiteLLM 向けに
// 作られたガードレールのサービスをそのまま使えるこの形に固定する。
package guardrail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

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

// File は検査するファイル（画像・音声・リソースの blob）。
type File struct {
	// URI はファイルの出どころ（リソースの URI）。無ければ空。
	URI string
	// MIMEType は送り元が申告した種類。無ければ空。
	MIMEType string
	Data     []byte
}

// Request はガードレールに渡すテキストと文脈。
type Request struct {
	// Texts は検査するテキスト。結果の Texts はこれと 1 対 1 で対応する。
	Texts []string
	// Files は検査するファイル。ガードレールはファイルを書き換えられないので、
	// 判定は通すか拒否するかだけになる。
	Files     []File
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
	// FilesRewritten は、Action が ActionIntervened のときに、ガードレールが
	// ファイル（画像）の書き換えを返したかどうか。ファイルは書き換えられないので、
	// 呼び出し側は元のファイルを通さずに拒否する。
	FilesRewritten bool
}

// Guardrail はテキストを検査・マスキングするガードレール。
type Guardrail interface {
	Apply(ctx context.Context, req Request) (Result, error)
}

// apiFile は apiRequest.Files の1件。Data は JSON では base64 になる。フィールドの
// 並びと型は File と同じにしておく（splitFiles が型変換で詰め替える）。
type apiFile struct {
	URI      string `json:"uri,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
	Data     []byte `json:"data"`
}

// apiRequest は Generic Guardrail API のリクエスト。Files は LiteLLM の仕様に無い
// Manifold の拡張で、画像以外のファイルを運ぶ（画像は仕様にある Images で送る）。
type apiRequest struct {
	Texts []string `json:"texts"`
	// Images は画像（base64）。
	Images         []string       `json:"images,omitempty"`
	Files          []apiFile      `json:"files,omitempty"`
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
	// Images は書き換え後の画像。Generic Guardrail API は画像の書き換えも返せるが、
	// Manifold はファイルを書き換えないので、書き換えの有無だけを見る。
	Images []string `json:"images,omitempty"`
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
	images, files := splitFiles(req.Files)
	// texts は仕様上必須なので、ファイルだけの検査でも null ではなく [] を送る。
	texts := req.Texts
	if texts == nil {
		texts = []string{}
	}
	body, err := json.Marshal(apiRequest{
		Texts:          texts,
		Images:         images,
		Files:          files,
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
		// 送った画像と同じものを返しただけなら、書き換えではない。images を返したのに
		// 送ったものと違えば、空（すべて取り除いた）でも書き換えとして扱う。
		filesRewritten := out.Images != nil && !slices.Equal(out.Images, images)
		return Result{
			Action: ActionIntervened, Texts: out.Texts, FilesRewritten: filesRewritten,
		}, nil
	case ActionBlocked:
		return Result{Action: ActionBlocked, BlockedReason: out.BlockedReason}, nil
	default:
		return Result{}, fmt.Errorf("guardrail: unknown action %q", out.Action)
	}
}

// splitFiles は files を Generic Guardrail API の images（画像の base64）と、
// それ以外を運ぶ files に分ける。
func splitFiles(files []File) (images []string, rest []apiFile) {
	for _, f := range files {
		if isImage(f) {
			images = append(images, base64.StdEncoding.EncodeToString(f.Data))
			continue
		}
		rest = append(rest, apiFile(f))
	}
	return images, rest
}

// isImage は f が画像かを返す。申告された種類（image/*）に加えて、中身からも判定する。
// 種類は検査対象（バックエンド）の自己申告なので、申告だけで決めると、画像を
// application/octet-stream などと申告するだけで、images しか読まないガードレールの
// 検査を外れてしまう。
func isImage(f File) bool {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(f.MIMEType)), "image/") {
		return true
	}
	return strings.HasPrefix(http.DetectContentType(f.Data), "image/")
}
