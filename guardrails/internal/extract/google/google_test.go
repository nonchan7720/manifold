package google

import (
	"context"
	"errors"
	"testing"

	"github.com/nonchan7720/manifold/guardrails/internal/extract"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

// fakeGenerator は GenerateContent の入力を記録し、決めた結果を返す。
type fakeGenerator struct {
	model    string
	contents []*genai.Content
	config   *genai.GenerateContentConfig
	resp     *genai.GenerateContentResponse
	err      error
}

func (f *fakeGenerator) GenerateContent(
	_ context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig,
) (*genai.GenerateContentResponse, error) {
	f.model, f.contents, f.config = model, contents, config
	return f.resp, f.err
}

func candidate(reason genai.FinishReason, text string) *genai.GenerateContentResponse {
	return &genai.GenerateContentResponse{Candidates: []*genai.Candidate{{
		FinishReason: reason,
		Content:      &genai.Content{Parts: []*genai.Part{{Text: text}}},
	}}}
}

func TestGoogleExtractor_Extract(t *testing.T) {
	f := &fakeGenerator{resp: candidate(genai.FinishReasonStop, " hello\n")}
	e := New(f, "gemini-model", 2048)
	require.True(t, e.Supports("application/pdf"))
	require.False(t, e.Supports("application/msword"))

	got, err := e.Extract(
		t.Context(),
		extract.SourceFile{MIMEType: "application/pdf", Data: []byte("%PDF")},
	)
	require.NoError(t, err)
	require.Equal(t, "hello", got)

	require.Equal(t, "gemini-model", f.model)
	require.EqualValues(t, 2048, f.config.MaxOutputTokens)
	require.Equal(t, extract.SystemPrompt, f.config.SystemInstruction.Parts[0].Text)
	parts := f.contents[0].Parts
	require.Len(t, parts, 2)
	require.Equal(t, "application/pdf", parts[0].InlineData.MIMEType)
	require.Equal(t, []byte("%PDF"), parts[0].InlineData.Data)
	require.Equal(t, extract.UserPrompt, parts[1].Text)
}

func TestGoogleExtractor_Failures(t *testing.T) {
	file := extract.SourceFile{MIMEType: "image/png", Data: []byte("img")}
	t.Run("truncated", func(t *testing.T) {
		f := &fakeGenerator{resp: candidate(genai.FinishReasonMaxTokens, "partial")}
		_, err := New(f, "m", 10).Extract(t.Context(), file)
		require.ErrorIs(t, err, extract.ErrTruncated)
	})
	t.Run("safety", func(t *testing.T) {
		f := &fakeGenerator{resp: candidate(genai.FinishReasonSafety, "")}
		_, err := New(f, "m", 10).Extract(t.Context(), file)
		require.ErrorContains(t, err, "SAFETY")
	})
	t.Run("no candidate", func(t *testing.T) {
		f := &fakeGenerator{resp: &genai.GenerateContentResponse{}}
		_, err := New(f, "m", 10).Extract(t.Context(), file)
		require.ErrorContains(t, err, "no candidate")
	})
	t.Run("api error", func(t *testing.T) {
		f := &fakeGenerator{err: errors.New("quota")}
		_, err := New(f, "m", 10).Extract(t.Context(), file)
		require.ErrorContains(t, err, "quota")
	})
	t.Run("no text", func(t *testing.T) {
		f := &fakeGenerator{resp: candidate(genai.FinishReasonStop, extract.NoTextMarker)}
		got, err := New(f, "m", 10).Extract(t.Context(), file)
		require.NoError(t, err)
		require.Empty(t, got)
	})
}
