// Package azure は Azure OpenAI（Microsoft Foundry）の Responses API でファイルを
// 書き起こす Extractor。v1 API（<endpoint>/openai/v1/responses）を HTTP で直接呼ぶ。
package azure

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/nonchan7720/manifold/guardrails/internal/extract"
)

// maxResponseSize は Responses API の応答の最大バイト数。
const maxResponseSize = 32 << 20

// imageTypes は input_image で送れる画像。
var imageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

const pdfType = "application/pdf"

// Extractor は Azure OpenAI の Responses API でファイルを書き起こす。
type Extractor struct {
	url        string
	apiKey     string
	deployment string
	maxTokens  int
	httpClient *http.Client
}

var _ extract.Extractor = (*Extractor)(nil)

// New は endpoint（https://<resource>.openai.azure.com）の deployment を使う
// Extractor を返す。apiKey は api-key ヘッダーで送る。
func New(
	endpoint, apiKey, deployment string, maxTokens int, httpClient *http.Client,
) *Extractor {
	return &Extractor{
		url:        strings.TrimRight(endpoint, "/") + "/openai/v1/responses",
		apiKey:     apiKey,
		deployment: deployment,
		maxTokens:  maxTokens,
		httpClient: httpClient,
	}
}

func (e *Extractor) Supports(mimeType string) bool {
	return imageTypes[mimeType] || mimeType == pdfType
}

type request struct {
	Model           string  `json:"model"`
	Instructions    string  `json:"instructions"`
	Input           []input `json:"input"`
	MaxOutputTokens int     `json:"max_output_tokens"`
	// Store を false にして、ファイルの中身をサービス側に保存させない。
	Store bool `json:"store"`
}

type input struct {
	Role    string `json:"role"`
	Content []part `json:"content"`
}

type part struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Filename string `json:"filename,omitempty"`
	FileData string `json:"file_data,omitempty"`
}

type response struct {
	Status            string `json:"status"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Output []struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func (e *Extractor) Extract(ctx context.Context, f extract.SourceFile) (string, error) {
	dataURL := "data:" + f.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(f.Data)
	var filePart part
	switch {
	case imageTypes[f.MIMEType]:
		// 小さい文字も読めるよう、高精細で渡す。
		filePart = part{Type: "input_image", ImageURL: dataURL, Detail: "high"}
	case f.MIMEType == pdfType:
		// filename はモデルに見えるので、URI やファイル名は使わず固定の名前にする。
		filePart = part{Type: "input_file", Filename: "document.pdf", FileData: dataURL}
	default:
		return "", fmt.Errorf("azure: unsupported file type %q", f.MIMEType)
	}
	body, err := json.Marshal(request{
		Model:        e.deployment,
		Instructions: extract.SystemPrompt,
		Input: []input{{
			Role:    "user",
			Content: []part{filePart, {Type: "input_text", Text: extract.UserPrompt}},
		}},
		MaxOutputTokens: e.maxTokens,
	})
	if err != nil {
		return "", fmt.Errorf("azure: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("azure: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("api-key", e.apiKey)

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("azure: call: %w", err)
	}
	defer resp.Body.Close() //nolint: errcheck
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return "", fmt.Errorf("azure: read response: %w", err)
	}
	// 応答の本文はファイルの内容を含みうるので、エラー文には出さない。
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("azure: returned status %d", resp.StatusCode)
	}
	if len(raw) > maxResponseSize {
		return "", fmt.Errorf("azure: response exceeds %d bytes", maxResponseSize)
	}
	var out response
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("azure: decode response: %w", err)
	}

	switch out.Status {
	case "completed":
	case "incomplete":
		if out.IncompleteDetails != nil && out.IncompleteDetails.Reason == "max_output_tokens" {
			return "", extract.ErrTruncated
		}
		// content_filter などは、書き起こしが得られていない。
		return "", fmt.Errorf("azure: response is incomplete")
	default:
		return "", fmt.Errorf("azure: response status is %q", out.Status)
	}
	var b strings.Builder
	for _, item := range out.Output {
		for _, c := range item.Content {
			switch c.Type {
			case "output_text":
				b.WriteString(c.Text)
			case "refusal":
				// status が completed でも、モデルが書き起こしを拒否することがある。
				// 書き起こしが得られていないので、テキスト無しとしては扱わない。
				return "", fmt.Errorf("azure: the model refused to transcribe the file")
			}
		}
	}
	return extract.Transcript(b.String())
}
