package fileguard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// maxDownstreamResponseSize は下流のガードレールの応答の最大バイト数。
const maxDownstreamResponseSize = 32 << 20

// Downstream は下流のテキスト用ガードレール（Generic Guardrail API）のクライアント。
type Downstream struct {
	url        string
	headers    map[string]string
	httpClient *http.Client
}

// NewDownstream は url（<base>/beta/litellm_basic_guardrail_api）を呼ぶ Downstream を
// 返す。headers は認証用のトークン等。
func NewDownstream(url string, headers map[string]string, httpClient *http.Client) *Downstream {
	return &Downstream{url: url, headers: headers, httpClient: httpClient}
}

// Apply は body を下流へ送り、判定を返す。texts は body に入れたテキストの数で、
// 下流が不正な応答（未知の action、書き換えたテキストの数の不一致）を返した場合は
// エラーにする。
func (d *Downstream) Apply(
	ctx context.Context, body map[string]json.RawMessage, texts int,
) (apiResponse, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return apiResponse{}, fmt.Errorf("downstream: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(raw))
	if err != nil {
		return apiResponse{}, fmt.Errorf("downstream: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range d.headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := d.httpClient.Do(httpReq)
	if err != nil {
		return apiResponse{}, fmt.Errorf("downstream: call: %w", err)
	}
	defer resp.Body.Close() //nolint: errcheck
	if resp.StatusCode != http.StatusOK {
		return apiResponse{}, fmt.Errorf("downstream: returned status %d", resp.StatusCode)
	}
	rawResp, err := io.ReadAll(io.LimitReader(resp.Body, maxDownstreamResponseSize+1))
	if err != nil {
		return apiResponse{}, fmt.Errorf("downstream: read response: %w", err)
	}
	if len(rawResp) > maxDownstreamResponseSize {
		return apiResponse{}, fmt.Errorf(
			"downstream: response exceeds %d bytes", maxDownstreamResponseSize,
		)
	}
	var out apiResponse
	if err := json.Unmarshal(rawResp, &out); err != nil {
		return apiResponse{}, fmt.Errorf("downstream: decode response: %w", err)
	}
	switch out.Action {
	case actionNone:
		return apiResponse{Action: actionNone}, nil
	case actionIntervened:
		if len(out.Texts) != texts {
			return apiResponse{}, fmt.Errorf(
				"downstream: GUARDRAIL_INTERVENED returned %d texts for %d inputs",
				len(out.Texts), texts,
			)
		}
		return apiResponse{Action: actionIntervened, Texts: out.Texts}, nil
	case actionBlocked:
		return apiResponse{Action: actionBlocked, BlockedReason: out.BlockedReason}, nil
	default:
		return apiResponse{}, fmt.Errorf("downstream: unknown action %q", out.Action)
	}
}
