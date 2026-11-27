package jmespath

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
)

// maxDecimalExponent は、桁で比べられる数値の指数の絶対値の上限。これを超える指数は
// 桁数に足しても int64 に収まらないので、比べずにエラーにする。
const maxDecimalExponent = 1 << 62

// compareNumbers は 2 つの数値の大小を返す。float64 では同じ値になる数値（2^53 を超える
// 整数、float64 の精度や範囲を超える数値など）は、元の表記を丸めずに比べる。等値比較と
// 大小比較の両方がこれを使うので、表記（1 と 1.0）を変えても結果は変わらない。
func (it *interp) compareNumbers(a, b number) (int, error) {
	switch {
	case a.f < b.f:
		return -1, nil
	case a.f > b.f:
		return 1, nil
	}
	// float64 への丸めは大小を逆転させないので、ここまでの判定は丸める前の値でも正しい。
	if a.raw == "" || b.raw == "" {
		return 0, nil
	}
	// 元の表記を読み比べる。表記はいくらでも長くできるので、その長さも作業量に数える。
	if err := it.charge((len(a.raw) + len(b.raw)) / bytesPerStep); err != nil {
		return 0, err
	}
	if a.raw == b.raw {
		return 0, nil
	}
	return compareDecimals(a.raw, b.raw)
}

// decimal は JSON の数値の表記を符号・有効数字・指数に分けたもの。
// 値は sign × 0.<digits> × 10^exp。
type decimal struct {
	// sign は負なら -1、0 なら 0、正なら 1。
	sign int
	// digits は先頭と末尾の 0 を除いた有効数字。0 では空。
	digits string
	exp    int64
}

// parseDecimal は JSON の数値の表記を decimal にする。指数が大きすぎて扱えない表記は
// エラーにする。
func parseDecimal(s string) (decimal, error) {
	original := s
	sign := 1
	if strings.HasPrefix(s, "-") {
		sign, s = -1, s[1:]
	}
	var exp int64
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		e, err := strconv.ParseInt(s[i+1:], 10, 64)
		if err != nil || e > maxDecimalExponent || e < -maxDecimalExponent {
			return decimal{}, fmt.Errorf(
				"the exponent of the number %s is too large to compare", abbreviate(original))
		}
		exp, s = e, s[:i]
	}
	intPart, fracPart, _ := strings.Cut(s, ".")
	digits := intPart + fracPart
	significant := strings.TrimLeft(digits, "0")
	if significant == "" {
		return decimal{}, nil
	}
	return decimal{
		sign:   sign,
		digits: strings.TrimRight(significant, "0"),
		// 小数点を有効数字の先頭の直前まで動かした分を指数に足す。
		exp: exp + int64(len(intPart)) - int64(len(digits)-len(significant)),
	}, nil
}

// compareDecimals は JSON の数値の表記 x と y の大小を、丸めずに返す。
func compareDecimals(x, y string) (int, error) {
	a, err := parseDecimal(x)
	if err != nil {
		return 0, err
	}
	b, err := parseDecimal(y)
	if err != nil {
		return 0, err
	}
	if a.sign != b.sign || a.sign == 0 {
		return cmp.Compare(a.sign, b.sign), nil
	}
	magnitude := cmp.Compare(a.exp, b.exp)
	if magnitude == 0 {
		// どちらも 0.<digits> の形なので、桁の並びの辞書順が大小になる。
		magnitude = strings.Compare(a.digits, b.digits)
	}
	return magnitude * a.sign, nil
}

// abbreviate は長い表記を、エラーメッセージに載せられる長さに縮める。
func abbreviate(s string) string {
	const limit = 32
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}
