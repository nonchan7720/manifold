// Package bedrock は AWS Bedrock の Converse API でファイルを書き起こす Extractor。
package bedrock

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/nonchan7720/manifold/guardrails/internal/extract"
)

// Converser は Converse を呼ぶクライアント（*bedrockruntime.Client）。
type Converser interface {
	Converse(
		ctx context.Context,
		params *bedrockruntime.ConverseInput,
		optFns ...func(*bedrockruntime.Options),
	) (*bedrockruntime.ConverseOutput, error)
}

// imageFormats / documentFormats は Converse が受け取れる種類。csv・html・txt・md も
// 受け取れるが、テキストなので LLM を通さずに検査する（extract.IsText）。
var (
	imageFormats = map[string]types.ImageFormat{
		"image/png":  types.ImageFormatPng,
		"image/jpeg": types.ImageFormatJpeg,
		"image/gif":  types.ImageFormatGif,
		"image/webp": types.ImageFormatWebp,
	}
	documentFormats = map[string]types.DocumentFormat{
		"application/pdf":    types.DocumentFormatPdf,
		"application/msword": types.DocumentFormatDoc,
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": types.DocumentFormatDocx,
		"application/vnd.ms-excel": types.DocumentFormatXls,
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": types.DocumentFormatXlsx,
	}
)

// documentName は DocumentBlock の name。name はモデルが指示として解釈しうる
// （プロンプトインジェクションの入口になる）ので、ファイル名や URI は使わず
// 固定の中立な名前にする。
const documentName = "document"

// Extractor は Converse でファイルを書き起こす。
type Extractor struct {
	client    Converser
	model     string
	maxTokens int32
}

var _ extract.Extractor = (*Extractor)(nil)

// New は model（モデル ID か推論プロファイル）を使う Extractor を返す。
func New(client Converser, model string, maxTokens int32) *Extractor {
	return &Extractor{client: client, model: model, maxTokens: maxTokens}
}

func (e *Extractor) Supports(mimeType string) bool {
	_, image := imageFormats[mimeType]
	_, document := documentFormats[mimeType]
	return image || document
}

func (e *Extractor) Extract(ctx context.Context, f extract.SourceFile) (string, error) {
	var block types.ContentBlock
	if format, ok := imageFormats[f.MIMEType]; ok {
		block = &types.ContentBlockMemberImage{Value: types.ImageBlock{
			Format: format,
			Source: &types.ImageSourceMemberBytes{Value: f.Data},
		}}
	} else if format, ok := documentFormats[f.MIMEType]; ok {
		block = &types.ContentBlockMemberDocument{Value: types.DocumentBlock{
			Format: format,
			Name:   aws.String(documentName),
			Source: &types.DocumentSourceMemberBytes{Value: f.Data},
		}}
	} else {
		return "", fmt.Errorf("bedrock: unsupported file type %q", f.MIMEType)
	}

	out, err := e.client.Converse(ctx, &bedrockruntime.ConverseInput{
		ModelId: aws.String(e.model),
		System: []types.SystemContentBlock{
			&types.SystemContentBlockMemberText{Value: extract.SystemPrompt},
		},
		Messages: []types.Message{{
			Role: types.ConversationRoleUser,
			// 文書には、同じメッセージにテキストのブロックが必要。
			Content: []types.ContentBlock{
				block,
				&types.ContentBlockMemberText{Value: extract.UserPrompt},
			},
		}},
		InferenceConfig: &types.InferenceConfiguration{
			MaxTokens:   aws.Int32(e.maxTokens),
			Temperature: aws.Float32(0),
		},
	})
	if err != nil {
		return "", fmt.Errorf("bedrock: converse: %w", err)
	}

	// end_turn・stop_sequence 以外では、書き起こしが最後まで得られていない。
	switch reason := out.StopReason; {
	case reason == types.StopReasonMaxTokens,
		reason == types.StopReasonModelContextWindowExceeded:
		return "", extract.ErrTruncated
	case reason != types.StopReasonEndTurn && reason != types.StopReasonStopSequence:
		// content_filtered・guardrail_intervened など。
		return "", fmt.Errorf("bedrock: converse stopped with %q", reason)
	}
	message, ok := out.Output.(*types.ConverseOutputMemberMessage)
	if !ok {
		return "", fmt.Errorf("bedrock: converse returned no message")
	}
	var b strings.Builder
	for _, c := range message.Value.Content {
		if text, ok := c.(*types.ContentBlockMemberText); ok {
			b.WriteString(text.Value)
		}
	}
	return extract.CleanOutput(b.String()), nil
}
