package jmespath

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// function は組み込み関数。
type function struct {
	name string
	// minArgs / maxArgs は引数の個数の範囲。maxArgs が負なら上限なし。
	minArgs int
	maxArgs int
	call    func(c *callContext) (any, error)
}

// callContext は 1 回の関数呼び出し。
type callContext struct {
	it   *interp
	fn   *function
	args []any
}

func (f *function) checkArity(n int) error {
	switch {
	case f.maxArgs >= 0 && f.minArgs == f.maxArgs && n != f.minArgs:
		return fmt.Errorf("%s() takes %d argument(s), got %d", f.name, f.minArgs, n)
	case n < f.minArgs:
		return fmt.Errorf("%s() takes at least %d argument(s), got %d", f.name, f.minArgs, n)
	case f.maxArgs >= 0 && n > f.maxArgs:
		return fmt.Errorf("%s() takes at most %d argument(s), got %d", f.name, f.maxArgs, n)
	}
	return nil
}

// functions は JMESPath の標準の組み込み関数（名前から引く）。
var functions = func() map[string]*function {
	list := []*function{
		{name: "abs", minArgs: 1, maxArgs: 1, call: fnAbs},
		{name: "avg", minArgs: 1, maxArgs: 1, call: fnAvg},
		{name: "ceil", minArgs: 1, maxArgs: 1, call: fnCeil},
		{name: "contains", minArgs: 2, maxArgs: 2, call: fnContains},
		{name: "ends_with", minArgs: 2, maxArgs: 2, call: fnEndsWith},
		{name: "floor", minArgs: 1, maxArgs: 1, call: fnFloor},
		{name: "join", minArgs: 2, maxArgs: 2, call: fnJoin},
		{name: "keys", minArgs: 1, maxArgs: 1, call: fnKeys},
		{name: "length", minArgs: 1, maxArgs: 1, call: fnLength},
		{name: "map", minArgs: 2, maxArgs: 2, call: fnMap},
		{name: "max", minArgs: 1, maxArgs: 1, call: fnMax},
		{name: "max_by", minArgs: 2, maxArgs: 2, call: fnMaxBy},
		{name: "merge", minArgs: 1, maxArgs: -1, call: fnMerge},
		{name: "min", minArgs: 1, maxArgs: 1, call: fnMin},
		{name: "min_by", minArgs: 2, maxArgs: 2, call: fnMinBy},
		{name: "not_null", minArgs: 1, maxArgs: -1, call: fnNotNull},
		{name: "reverse", minArgs: 1, maxArgs: 1, call: fnReverse},
		{name: "sort", minArgs: 1, maxArgs: 1, call: fnSort},
		{name: "sort_by", minArgs: 2, maxArgs: 2, call: fnSortBy},
		{name: "starts_with", minArgs: 2, maxArgs: 2, call: fnStartsWith},
		{name: "sum", minArgs: 1, maxArgs: 1, call: fnSum},
		{name: "to_array", minArgs: 1, maxArgs: 1, call: fnToArray},
		{name: "to_number", minArgs: 1, maxArgs: 1, call: fnToNumber},
		{name: "to_string", minArgs: 1, maxArgs: 1, call: fnToString},
		{name: "type", minArgs: 1, maxArgs: 1, call: fnType},
		{name: "values", minArgs: 1, maxArgs: 1, call: fnValues},
	}
	byName := make(map[string]*function, len(list))
	for _, f := range list {
		byName[f.name] = f
	}
	return byName
}()

// call は関数の節を評価する。引数は現在の値に対して評価してから渡す。
func (it *interp) call(n *node, v any) (any, error) {
	args := make([]any, len(n.children))
	for i, child := range n.children {
		arg, err := it.eval(child, v)
		if err != nil {
			return nil, err
		}
		args[i] = arg
	}
	return n.fn.call(&callContext{it: it, fn: n.fn, args: args})
}

// typeError は i 番目（0 始まり）の引数の型が合わないことを表すエラーを返す。
func (c *callContext) typeError(i int, want string) error {
	return fmt.Errorf("%s(): argument %d must be %s, got %s",
		c.fn.name, i+1, want, describeValue(c.args[i]))
}

// describeValue はエラーメッセージ用に値の型を返す。配列は要素の型も添える。
func describeValue(v any) string {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return typeOf(v)
	}
	elem := typeOf(arr[0])
	for _, e := range arr[1:] {
		if typeOf(e) != elem {
			return "an array of mixed types"
		}
	}
	return "an array of " + elem + "s"
}

func (c *callContext) number(i int) (number, error) {
	n, ok := c.args[i].(number)
	if !ok {
		return number{}, c.typeError(i, "a number")
	}
	return n, nil
}

func (c *callContext) str(i int) (string, error) {
	s, ok := c.args[i].(string)
	if !ok {
		return "", c.typeError(i, "a string")
	}
	return s, nil
}

func (c *callContext) array(i int) ([]any, error) {
	arr, ok := c.args[i].([]any)
	if !ok {
		return nil, c.typeError(i, "an array")
	}
	return arr, nil
}

func (c *callContext) object(i int) (*object, error) {
	obj, ok := c.args[i].(*object)
	if !ok {
		return nil, c.typeError(i, "an object")
	}
	return obj, nil
}

func (c *callContext) expref(i int) (*expref, error) {
	ref, ok := c.args[i].(*expref)
	if !ok {
		return nil, c.typeError(i, "an expression reference (&expr)")
	}
	return ref, nil
}

// numbers は i 番目の引数を数値の配列として返す。
func (c *callContext) numbers(i int) ([]number, error) {
	arr, err := c.array(i)
	if err != nil {
		return nil, err
	}
	out := make([]number, len(arr))
	for j, elem := range arr {
		n, ok := elem.(number)
		if !ok {
			return nil, c.typeError(i, "an array of numbers")
		}
		out[j] = n
	}
	return out, c.it.charge(len(arr))
}

// orderable は i 番目の引数を、数値だけ・または文字列だけの配列として返す。
func (c *callContext) orderable(i int) ([]any, error) {
	arr, err := c.array(i)
	if err != nil {
		return nil, err
	}
	if err := checkComparable(arr); err != nil {
		return nil, c.typeError(i, "an array of numbers or an array of strings")
	}
	return arr, c.it.charge(len(arr))
}

// checkComparable は keys が数値だけ、または文字列だけかを検査する。
func checkComparable(keys []any) error {
	if len(keys) == 0 {
		return nil
	}
	want := typeOf(keys[0])
	if want != typeNumber && want != typeString {
		return fmt.Errorf("got %s", want)
	}
	for _, key := range keys[1:] {
		if typeOf(key) != want {
			return fmt.Errorf("got %s and %s", want, typeOf(key))
		}
	}
	return nil
}

// compareValues は checkComparable を通った 2 つの値の大小を返す。
func compareValues(a, b any) int {
	cmp, _ := order(a, b)
	return cmp
}

// sortCost は n 要素の並べ替えの作業量。
func sortCost(n int) int {
	return n * bits.Len(uint(n))
}

func fnAbs(c *callContext) (any, error) {
	n, err := c.number(0)
	if err != nil {
		return nil, err
	}
	if n.f >= 0 {
		return n, nil
	}
	return number{f: -n.f, raw: strings.TrimPrefix(n.raw, "-")}, nil
}

func fnAvg(c *callContext) (any, error) {
	nums, err := c.numbers(0)
	if err != nil || len(nums) == 0 {
		return nil, err
	}
	sum := 0.0
	for _, n := range nums {
		sum += n.f
	}
	return number{f: sum / float64(len(nums))}, nil
}

func fnCeil(c *callContext) (any, error) {
	n, err := c.number(0)
	if err != nil {
		return nil, err
	}
	return roundTo(n, math.Ceil), nil
}

func fnFloor(c *callContext) (any, error) {
	n, err := c.number(0)
	if err != nil {
		return nil, err
	}
	return roundTo(n, math.Floor), nil
}

// roundTo は n を round で丸める。もともと整数なら元の表記を保つ。
func roundTo(n number, round func(float64) float64) number {
	if isIntegerLiteral(n.raw) {
		return n
	}
	return number{f: round(n.f)}
}

func fnContains(c *callContext) (any, error) {
	switch subject := c.args[0].(type) {
	case string:
		search, ok := c.args[1].(string)
		if !ok {
			return false, nil
		}
		return strings.Contains(subject, search), c.it.charge(len(subject) / 64)
	case []any:
		for _, elem := range subject {
			eq, err := c.it.equal(elem, c.args[1])
			if err != nil {
				return nil, err
			}
			if eq {
				return true, nil
			}
		}
		return false, nil
	}
	return nil, c.typeError(0, "a string or an array")
}

func fnStartsWith(c *callContext) (any, error) {
	s, prefix, err := c.twoStrings()
	if err != nil {
		return nil, err
	}
	return strings.HasPrefix(s, prefix), nil
}

func fnEndsWith(c *callContext) (any, error) {
	s, suffix, err := c.twoStrings()
	if err != nil {
		return nil, err
	}
	return strings.HasSuffix(s, suffix), nil
}

func (c *callContext) twoStrings() (string, string, error) {
	a, err := c.str(0)
	if err != nil {
		return "", "", err
	}
	b, err := c.str(1)
	return a, b, err
}

func fnJoin(c *callContext) (any, error) {
	glue, err := c.str(0)
	if err != nil {
		return nil, err
	}
	arr, err := c.array(1)
	if err != nil {
		return nil, err
	}
	parts := make([]string, len(arr))
	size := 0
	for i, elem := range arr {
		s, ok := elem.(string)
		if !ok {
			return nil, c.typeError(1, "an array of strings")
		}
		parts[i] = s
		size += len(s) + len(glue)
	}
	// 結果の文字列の長さだけ数える（つなぐたびに倍になる式を止める）。
	if err := c.it.charge(size + len(arr)); err != nil {
		return nil, err
	}
	return strings.Join(parts, glue), nil
}

func fnKeys(c *callContext) (any, error) {
	obj, err := c.object(0)
	if err != nil {
		return nil, err
	}
	out := make([]any, len(obj.keys))
	for i, key := range obj.keys {
		out[i] = key
	}
	return out, c.it.charge(len(out))
}

func fnValues(c *callContext) (any, error) {
	obj, err := c.object(0)
	if err != nil {
		return nil, err
	}
	return slices.Clone(obj.vals), c.it.charge(len(obj.vals))
}

func fnLength(c *callContext) (any, error) {
	switch v := c.args[0].(type) {
	case string:
		return number{f: float64(utf8.RuneCountInString(v))}, c.it.charge(len(v) / 64)
	case []any:
		return number{f: float64(len(v))}, nil
	case *object:
		return number{f: float64(len(v.keys))}, nil
	}
	return nil, c.typeError(0, "a string, an array or an object")
}

func fnMap(c *callContext) (any, error) {
	ref, err := c.expref(0)
	if err != nil {
		return nil, err
	}
	arr, err := c.array(1)
	if err != nil {
		return nil, err
	}
	out, err := c.it.evalEach(ref, arr)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// evalEach は ref の式を arr の各要素に適用した結果を返す（null も残す）。
func (it *interp) evalEach(ref *expref, arr []any) ([]any, error) {
	if err := it.charge(len(arr)); err != nil {
		return nil, err
	}
	out := make([]any, len(arr))
	for i, elem := range arr {
		result, err := it.eval(ref.node, elem)
		if err != nil {
			return nil, err
		}
		out[i] = result
	}
	return out, nil
}

func fnMax(c *callContext) (any, error) { return c.extreme(1) }
func fnMin(c *callContext) (any, error) { return c.extreme(-1) }

// extreme は配列の最大（sign が正）または最小（負）の要素を返す。空の配列では null。
func (c *callContext) extreme(sign int) (any, error) {
	arr, err := c.orderable(0)
	if err != nil || len(arr) == 0 {
		return nil, err
	}
	best := arr[0]
	for _, elem := range arr[1:] {
		if compareValues(elem, best)*sign > 0 {
			best = elem
		}
	}
	return best, nil
}

func fnMaxBy(c *callContext) (any, error) { return c.extremeBy(1) }
func fnMinBy(c *callContext) (any, error) { return c.extremeBy(-1) }

// extremeBy は式の値が最大（sign が正）または最小（負）の要素を返す。空の配列では null。
func (c *callContext) extremeBy(sign int) (any, error) {
	arr, keys, err := c.sortKeys()
	if err != nil || len(arr) == 0 {
		return nil, err
	}
	best := 0
	for i := 1; i < len(arr); i++ {
		if compareValues(keys[i], keys[best])*sign > 0 {
			best = i
		}
	}
	return arr[best], nil
}

// sortKeys は「配列, &式」の引数から、配列と、各要素に式を適用した値を返す。値は数値
// だけ、または文字列だけでなければならない。
func (c *callContext) sortKeys() (arr, keys []any, err error) {
	if arr, err = c.array(0); err != nil {
		return nil, nil, err
	}
	ref, err := c.expref(1)
	if err != nil {
		return nil, nil, err
	}
	if keys, err = c.it.evalEach(ref, arr); err != nil {
		return nil, nil, err
	}
	if err := checkComparable(keys); err != nil {
		return nil, nil, fmt.Errorf(
			"%s(): the expression must return a number for every element, or a string for "+
				"every element (%s)", c.fn.name, err.Error())
	}
	return arr, keys, nil
}

func fnMerge(c *callContext) (any, error) {
	out := &object{}
	for i := range c.args {
		obj, err := c.object(i)
		if err != nil {
			return nil, err
		}
		if err := c.it.charge(len(obj.keys)); err != nil {
			return nil, err
		}
		for j, key := range obj.keys {
			out.set(key, obj.vals[j])
		}
	}
	return out, nil
}

func fnNotNull(c *callContext) (any, error) {
	for _, arg := range c.args {
		if arg != nil {
			return arg, nil
		}
	}
	return nil, nil //nolint:nilnil // JSON の null
}

func fnReverse(c *callContext) (any, error) {
	switch v := c.args[0].(type) {
	case string:
		runes := []rune(v)
		slices.Reverse(runes)
		return string(runes), c.it.charge(len(v))
	case []any:
		out := slices.Clone(v)
		slices.Reverse(out)
		return out, c.it.charge(len(out))
	}
	return nil, c.typeError(0, "a string or an array")
}

func fnSort(c *callContext) (any, error) {
	arr, err := c.orderable(0)
	if err != nil {
		return nil, err
	}
	if err := c.it.charge(sortCost(len(arr))); err != nil {
		return nil, err
	}
	out := slices.Clone(arr)
	slices.SortStableFunc(out, compareValues)
	return out, nil
}

func fnSortBy(c *callContext) (any, error) {
	arr, keys, err := c.sortKeys()
	if err != nil {
		return nil, err
	}
	if err := c.it.charge(sortCost(len(arr))); err != nil {
		return nil, err
	}
	perm := make([]int, len(arr))
	for i := range perm {
		perm[i] = i
	}
	slices.SortStableFunc(perm, func(a, b int) int { return compareValues(keys[a], keys[b]) })
	out := make([]any, len(arr))
	for i, from := range perm {
		out[i] = arr[from]
	}
	return out, nil
}

func fnSum(c *callContext) (any, error) {
	nums, err := c.numbers(0)
	if err != nil {
		return nil, err
	}
	sum := 0.0
	for _, n := range nums {
		sum += n.f
	}
	return number{f: sum}, nil
}

func fnToArray(c *callContext) (any, error) {
	if arr, ok := c.args[0].([]any); ok {
		return arr, nil
	}
	return []any{c.args[0]}, nil
}

func fnToNumber(c *callContext) (any, error) {
	switch v := c.args[0].(type) {
	case number:
		return v, nil
	case string:
		if n, ok := parseNumber(v); ok {
			return n, nil
		}
	}
	return nil, nil //nolint:nilnil // JSON の null
}

// parseNumber は文字列を数値として読む。10 進の整数・小数・指数表記だけを受け付ける。
func parseNumber(s string) (number, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsFunc(s, func(r rune) bool {
		return (r < '0' || r > '9') && !strings.ContainsRune("+-.eE", r)
	}) {
		return number{}, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return number{}, false
	}
	n := number{f: f}
	// JSON の数値としてそのまま書ける表記なら、桁を失わないよう保つ。
	if doc, err := Decode([]byte(s)); err == nil {
		if parsed, ok := doc.root.(number); ok {
			n.raw = parsed.raw
		}
	}
	return n, true
}

func fnToString(c *callContext) (any, error) {
	if s, ok := c.args[0].(string); ok {
		return s, nil
	}
	// 残りの作業量を超える長さの文字列は作らない。
	encoded, err := appendJSON(nil, c.args[0], c.it.limit-c.it.steps)
	if err != nil {
		if errors.Is(err, errResultTooLarge) {
			return nil, ErrTooExpensive
		}
		return nil, err
	}
	return string(encoded), c.it.charge(len(encoded))
}

func fnType(c *callContext) (any, error) {
	if _, ok := c.args[0].(*expref); ok {
		return nil, c.typeError(0, "a value")
	}
	return typeOf(c.args[0]), nil
}
