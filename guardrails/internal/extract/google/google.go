// Package google は Google の Gemini（Gemini API / Vertex AI）でファイルを書き起こす
// Extractor。
package google

import (
	"context"
	"fmt"

	"github.com/nonchan7720/manifold/guardrails/internal/extract"
	"google.golang.org/genai"
)

// Generator は GenerateContent を呼ぶクライアント（genai.Client の Models）。
type Generator interface {
	GenerateContent(
		ctx context.Context,
		model string,
		contents []*genai.Content,
		config *genai.GenerateContentConfig,
	) (*genai.GenerateContentResponse, error)
}

// supported は書き起こしの対象にする種類。Gemini は音声・動画も受け取れるが、
// 文字の書き起こし（OCR）の対象は画像と PDF に絞る。
var supported = map[string]bool{
	"application/pdf": true,
	"image/png":       true,
	"image/jpeg":      true,
	"image/webp":      true,
	"image/heic":      true,
	"image/heif":      true,
}

// Extractor は Gemini でファイルを書き起こす。
type Extractor struct {
	client    Generator
	model     string
	maxTokens int32
}

var _ extract.Extractor = (*Extractor)(nil)

// New は model を使う Extractor を返す。
func New(client Generator, model string, maxTokens int32) *Extractor {
	return &Extractor{client: client, model: model, maxTokens: maxTokens}
}

func (e *Extractor) Supports(mimeType string) bool { return supported[mimeType] }

func (e *Extractor) Extract(ctx context.Context, f extract.SourceFile) (string, error) {
	if !supported[f.MIMEType] {
		return "", fmt.Errorf("google: unsupported file type %q", f.MIMEType)
	}
	resp, err := e.client.GenerateContent(ctx, e.model,
		[]*genai.Content{genai.NewContentFromParts([]*genai.Part{
			genai.NewPartFromBytes(f.Data, f.MIMEType),
			genai.NewPartFromText(extract.UserPrompt),
		}, genai.RoleUser)},
		&genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{genai.NewPartFromText(extract.SystemPrompt)},
			},
			MaxOutputTokens: e.maxTokens,
			Temperature:     genai.Ptr[float32](0),
		},
	)
	if err != nil {
		return "", fmt.Errorf("google: generate content: %w", err)
	}
	// 候補が無いのは、入力が安全性フィルタ等で拒否されたとき。
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0] == nil {
		return "", fmt.Errorf("google: generate content returned no candidate")
	}
	// STOP 以外では、書き起こしが最後まで得られていない。
	switch reason := resp.Candidates[0].FinishReason; {
	case reason == genai.FinishReasonMaxTokens:
		return "", extract.ErrTruncated
	case reason != genai.FinishReasonStop:
		// SAFETY・RECITATION など。
		return "", fmt.Errorf("google: generate content finished with %q", reason)
	}
	return extract.CleanOutput(resp.Text()), nil
}
