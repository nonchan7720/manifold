package jmespath

import (
	"cmp"
	"strconv"
	"strings"
)

const (
	// maxExactNumberBytes は、元の表記の桁で比べる数値の表記の長さの上限。これより長い
	// 表記の数値は float64 の値だけで比べる（表記の長さに比例する作業を、比較のたびに
	// 行わないため）。
	maxExactNumberBytes = 512
	// maxDecimalExponent は、桁で比べる数値の指数の絶対値の上限。
	maxDecimalExponent = 1 << 30
)

// compareNumbers は 2 つの数値の大小を返す。float64 では同じ値になる数値（2^53 を超える
// 整数、float64 の精度を超える小数など）は、元の表記があれば丸める前の値で比べる。
// 等値比較と大小比較の両方がこれを使うので、表記（1 と 1.0）を変えても結果は変わらない。
func compareNumbers(a, b number) int {
	switch {
	case a.f < b.f:
		return -1
	case a.f > b.f:
		return 1
	}
	// float64 への丸めは大小を逆転させないので、ここまでの判定は丸める前の値でも正しい。
	// 計算で作った数値（raw が空）は float64 の値がそのまま値。
	if a.raw == b.raw || a.raw == "" || b.raw == "" {
		return 0
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
	exp    int
}

// parseDecimal は JSON の数値の表記を decimal にする。表記が長すぎる、または指数が
// 大きすぎる場合は ok が false。
func parseDecimal(s string) (d decimal, ok bool) {
	if s == "" || len(s) > maxExactNumberBytes {
		return decimal{}, false
	}
	sign := 1
	if s[0] == '-' {
		sign, s = -1, s[1:]
	}
	exp := 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		e, err := strconv.Atoi(s[i+1:])
		if err != nil || e > maxDecimalExponent || e < -maxDecimalExponent {
			return decimal{}, false
		}
		exp, s = e, s[:i]
	}
	intPart, fracPart, _ := strings.Cut(s, ".")
	digits := intPart + fracPart
	significant := strings.TrimLeft(digits, "0")
	if significant == "" {
		return decimal{}, true
	}
	return decimal{
		sign:   sign,
		digits: strings.TrimRight(significant, "0"),
		// 小数点を有効数字の先頭の直前まで動かした分を指数に足す。
		exp: exp + len(intPart) - (len(digits) - len(significant)),
	}, true
}

// compareDecimals は JSON の数値の表記 x と y の大小を、丸めずに返す。桁で比べられない
// 表記（parseDecimal 参照）では 0 を返す。呼び出し元は float64 の値が等しいことを
// 確かめているので、そのときは float64 での比較の結果になる。
func compareDecimals(x, y string) int {
	a, okA := parseDecimal(x)
	b, okB := parseDecimal(y)
	if !okA || !okB {
		return 0
	}
	if a.sign != b.sign || a.sign == 0 {
		return cmp.Compare(a.sign, b.sign)
	}
	magnitude := cmp.Compare(a.exp, b.exp)
	if magnitude == 0 {
		// どちらも 0.<digits> の形なので、桁の並びの辞書順が大小になる。
		magnitude = strings.Compare(a.digits, b.digits)
	}
	return magnitude * a.sign
}
