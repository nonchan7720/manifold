package jmespath

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// SyntaxError は式を解釈できなかったことを表す。
type SyntaxError struct {
	// Offset はエラーの位置（式の先頭からのバイト数）。
	Offset int
	Msg    string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("%s at offset %d", e.Msg, e.Offset)
}

type tokenKind int

const (
	tEOF tokenKind = iota
	tUnquotedIdentifier
	tQuotedIdentifier
	tNumber
	tLiteral
	tDot
	tStar
	tFlatten // []
	tFilter  // [?
	tLbracket
	tRbracket
	tLparen
	tRparen
	tLbrace
	tRbrace
	tComma
	tColon
	tPipe
	tOr
	tAnd
	tNot
	tEQ
	tNE
	tLT
	tLTE
	tGT
	tGTE
	tCurrent // @
	tExpref  // &
)

type token struct {
	kind tokenKind
	pos  int
	// text は式の中での表記（エラーメッセージ用）。
	text string
	// value は識別子の名前（string）、数値（int）、リテラルの値。
	value any
}

// simpleTokens は 1 文字で決まるトークン。
var simpleTokens = map[byte]tokenKind{
	'.': tDot, '*': tStar, ']': tRbracket, '(': tLparen, ')': tRparen,
	'{': tLbrace, '}': tRbrace, ',': tComma, ':': tColon, '@': tCurrent,
}

// tokenize は式をトークンの列にする。末尾は必ず tEOF。
func tokenize(src string) ([]token, error) {
	var tokens []token
	pos := 0
	for pos < len(src) {
		c := src[pos]
		start := pos
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			pos++
			continue
		case isIdentifierStart(c):
			for pos < len(src) && isIdentifierPart(src[pos]) {
				pos++
			}
			tokens = append(tokens, token{
				kind: tUnquotedIdentifier, pos: start, text: src[start:pos], value: src[start:pos],
			})
			continue
		case c == '-' || isDigit(c):
			tok, next, err := lexNumber(src, pos)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, tok)
			pos = next
			continue
		case c == '"' || c == '\'' || c == '`':
			tok, next, err := lexDelimited(src, pos)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, tok)
			pos = next
			continue
		}
		kind, width, err := lexOperator(src, pos)
		if err != nil {
			return nil, err
		}
		pos += width
		tokens = append(tokens, token{kind: kind, pos: start, text: src[start:pos]})
	}
	return append(tokens, token{kind: tEOF, pos: len(src)}), nil
}

// lexOperator は pos から始まる記号のトークンの種類と長さを返す。
func lexOperator(src string, pos int) (tokenKind, int, error) {
	c := src[pos]
	next := byte(0)
	if pos+1 < len(src) {
		next = src[pos+1]
	}
	if kind, ok := simpleTokens[c]; ok {
		return kind, 1, nil
	}
	switch c {
	case '[':
		switch next {
		case ']':
			return tFlatten, 2, nil
		case '?':
			return tFilter, 2, nil
		}
		return tLbracket, 1, nil
	case '|':
		if next == '|' {
			return tOr, 2, nil
		}
		return tPipe, 1, nil
	case '&':
		if next == '&' {
			return tAnd, 2, nil
		}
		return tExpref, 1, nil
	case '!':
		if next == '=' {
			return tNE, 2, nil
		}
		return tNot, 1, nil
	case '<':
		if next == '=' {
			return tLTE, 2, nil
		}
		return tLT, 1, nil
	case '>':
		if next == '=' {
			return tGTE, 2, nil
		}
		return tGT, 1, nil
	case '=':
		if next == '=' {
			return tEQ, 2, nil
		}
		return 0, 0, &SyntaxError{Offset: pos, Msg: "unexpected '=' (use '==' to compare)"}
	}
	// 識別子に使えない文字（非 ASCII を含む）。1 バイトずつではなく文字として示す。
	r, _ := utf8.DecodeRuneInString(src[pos:])
	return 0, 0, &SyntaxError{
		Offset: pos,
		Msg: fmt.Sprintf(
			`unexpected character %q (quote names that are not plain identifiers, e.g. "my-key")`,
			r,
		),
	}
}

func lexNumber(src string, pos int) (token, int, error) {
	start := pos
	if src[pos] == '-' {
		pos++
	}
	digits := pos
	for pos < len(src) && isDigit(src[pos]) {
		pos++
	}
	if pos == digits {
		return token{}, 0, &SyntaxError{Offset: start, Msg: "unexpected '-'"}
	}
	n, err := strconv.Atoi(src[start:pos])
	if err != nil {
		return token{}, 0, &SyntaxError{Offset: start, Msg: "number is out of range"}
	}
	return token{kind: tNumber, pos: start, text: src[start:pos], value: n}, pos, nil
}

// lexDelimited は引用符で囲まれたトークン（"名前"、'文字列'、`JSON`）を読む。
func lexDelimited(src string, pos int) (token, int, error) {
	start := pos
	delim := src[pos]
	end := -1
	for i := pos + 1; i < len(src); i++ {
		if src[i] == '\\' {
			i++
			continue
		}
		if src[i] == delim {
			end = i
			break
		}
	}
	if end < 0 {
		return token{}, 0, &SyntaxError{
			Offset: start, Msg: fmt.Sprintf("unterminated %c...%c", delim, delim),
		}
	}
	body := src[start+1 : end]
	tok := token{pos: start, text: src[start : end+1]}
	switch delim {
	case '"':
		var name string
		if err := json.Unmarshal([]byte(src[start:end+1]), &name); err != nil {
			return token{}, 0, &SyntaxError{Offset: start, Msg: "invalid quoted identifier"}
		}
		tok.kind, tok.value = tQuotedIdentifier, name
	case '\'':
		// 生の文字列。\' だけがエスケープで、それ以外のバックスラッシュはそのまま残る。
		tok.kind, tok.value = tLiteral, strings.ReplaceAll(body, `\'`, `'`)
	default:
		value, err := parseLiteral(strings.ReplaceAll(body, "\\`", "`"))
		if err != nil {
			return token{}, 0, &SyntaxError{Offset: start, Msg: err.Error()}
		}
		tok.kind, tok.value = tLiteral, value
	}
	return tok, end + 1, nil
}

// parseLiteral は `...` の中身を JSON として読む。JSON として読めないものは、参照実装
// （AWS CLI が使う Python 版）に合わせて文字列とみなす（`foo` は "foo"）。
func parseLiteral(body string) (any, error) {
	doc, err := Decode([]byte(body))
	if err == nil {
		return doc.root, nil
	}
	quoted, qErr := Decode([]byte(`"` + strings.TrimLeft(body, " \t\r\n") + `"`))
	if qErr != nil {
		return nil, errors.New("invalid JSON literal")
	}
	return quoted.root, nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentifierStart(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isIdentifierPart(c byte) bool { return isIdentifierStart(c) || isDigit(c) }
