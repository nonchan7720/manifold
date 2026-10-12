package mcpsrv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/nonchan7720/manifold/pkg/config"
)

// auditRedactor は監査ログに書く引数とエラーメッセージから秘密情報を伏せる
// （audit.redact）。外部を呼ばずプロセス内で処理する。
type auditRedactor struct {
	// keys は正規化したキー名。キー名を正規化した結果がいずれかで終わる値を伏せる。
	keys     []string
	patterns []*regexp.Regexp
}

// newAuditRedactor は cfg（と config.DefaultAuditRedactKeys）から auditRedactor を
// 作る。正規表現は設定の検証で確かめてあるが、ここでもコンパイルに失敗すれば
// エラーを返す。
func newAuditRedactor(cfg config.AuditRedactConfig) (*auditRedactor, error) {
	r := &auditRedactor{}
	for _, k := range append(append([]string{}, config.DefaultAuditRedactKeys...), cfg.Keys...) {
		if n := normalizeRedactKey(k); n != "" {
			r.keys = append(r.keys, n)
		}
	}
	for _, p := range cfg.Patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("audit.redact.patterns: %q: %w", p, err)
		}
		r.patterns = append(r.patterns, re)
	}
	return r, nil
}

// normalizeRedactKey は大文字小文字と "-"・"_"・"." を無視してキー名を比べる
// ための正規化を行う。
func normalizeRedactKey(k string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '-', '_', '.':
			return -1
		}
		return r
	}, strings.ToLower(k))
}

// sensitiveKey は値を伏せるキー名かを返す。
func (r *auditRedactor) sensitiveKey(k string) bool {
	n := normalizeRedactKey(k)
	for _, key := range r.keys {
		if strings.HasSuffix(n, key) {
			return true
		}
	}
	return false
}

// redactString は s のうち patterns に一致する部分を伏せる。
func (r *auditRedactor) redactString(s string) string {
	for _, re := range r.patterns {
		s = re.ReplaceAllLiteralString(s, config.AuditRedactReplacement)
	}
	return s
}

// redactArguments は引数の JSON のうち、伏せるキー名の値と、patterns に一致する
// 文字列を伏せた JSON を返す。JSON として読めない場合は文字列として伏せて返す。
func (r *auditRedactor) redactArguments(raw json.RawMessage) any {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return r.redactString(string(raw))
	}
	return r.redact(v)
}

func (r *auditRedactor) redact(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if r.sensitiveKey(k) {
				t[k] = config.AuditRedactReplacement
				continue
			}
			t[k] = r.redact(child)
		}
		return t
	case []any:
		for i, child := range t {
			t[i] = r.redact(child)
		}
		return t
	case string:
		return r.redactString(t)
	default:
		return v
	}
}
