package toolsearch

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "ascii_words",
			in:   "Hello, World! foo_bar 123",
			want: []string{"hello", "world", "foo_bar", "123"},
		},
		{
			name: "cjk_bigram",
			in:   "注文検索",
			want: []string{"注文", "文検", "検索"},
		},
		{
			name: "cjk_single_rune",
			in:   "猫",
			want: []string{"猫"},
		},
		{
			name: "ascii_cjk_mixed",
			in:   "pet 注文検索 search",
			want: []string{"pet", "注文", "文検", "検索", "search"},
		},
		{
			name: "ascii_cjk_adjacent_no_space",
			in:   "pet注文",
			want: []string{"pet", "注文"},
		},
		{
			name: "hiragana_bigram",
			in:   "たべもの",
			want: []string{"たべ", "べも", "もの"},
		},
		{
			name: "katakana_bigram",
			in:   "カタカナ",
			want: []string{"カタ", "タカ", "カナ"},
		},
		{
			name: "hangul_bigram",
			in:   "안녕하세요",
			want: []string{"안녕", "녕하", "하세", "세요"},
		},
		{
			name: "empty",
			in:   "",
			want: nil,
		},
		{
			name: "only_separators",
			in:   "   ,,, ---",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Tokenize(tt.in)
			require.Equal(t, tt.want, got)
		})
	}
}
