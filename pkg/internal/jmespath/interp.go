// Package jmespath は JMESPath の式で、JSON から値を選び、条件で絞り込む。
//
// 対応するのは、値を選ぶための構文（フィールド、射影、平坦化、複数選択、パイプ、
// リテラル）と、Features で有効にする 2 つの構文（条件での絞り込み、添字とスライス）。
// 関数（集計・変換・並べ替えなど、値を計算するもの）には対応しない。
//
// 式は呼び出し元が書く外部入力として扱う。どんな式でも panic せず、評価の作業量と
// 結果の大きさには上限がある。結果のキーの順序と数値の表記は、元の JSON のままになる。
package jmespath

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// 評価の上限。式は呼び出し元が書く外部入力で、短い式でも結果を指数的に膨らませられる
// （"[@,@][]" を | でつなぐと 1 段ごとに倍になる）。そのため評価の 1 手ごとに作業量を
// 数え、ドキュメントの大きさに比例した上限で打ち切る。
const (
	// MaxExpressionBytes は式の長さの上限。
	MaxExpressionBytes = 4096
	// stepsPerNode と baseSteps は作業量の上限を決める。上限は
	// ドキュメントの値の個数 × stepsPerNode + baseSteps。
	stepsPerNode = 50
	baseSteps    = 200_000
	// bytesPerStep は、文字列の中身を読む作業を作業量に数えるときの 1 手あたりのバイト数。
	bytesPerStep = 64
	// resultSlack は、結果の JSON が元のドキュメントより大きくなってよい余裕。キー名を
	// 付け直すだけの小さな結果を通すためのもので、結果は「元の長さ + resultSlack」まで。
	resultSlack = 64 << 10
)

// ErrTooExpensive は式の評価が作業量の上限を超えたことを表す。
var ErrTooExpensive = errors.New("the expression is too expensive to evaluate on this result")

// Query は解釈済みの式。複数のゴルーチンから同時に使える。
type Query struct {
	root *node
}

// Features は、式で使える構文のうち選んで有効にするもの。値を選ぶ構文（フィールド、
// [*]、[]、{名前: 式}、[式, 式]、|）は常に使える。
type Features struct {
	// Conditions は条件で絞る構文。[?条件] と、条件を書くための比較（== != < <= > >=）・
	// 論理（&& || !）の演算子。
	Conditions bool
	// Indexing は位置と件数で絞る構文。添字（[0]、[-1]）とスライス（[:5]）。
	Indexing bool
}

// Compile は式を解釈する。式の誤り（構文エラー、有効にしていない構文、対応していない
// 関数の呼び出し）はすべてここで報告するので、評価の前に分かる。
func Compile(expression string, features Features) (q *Query, rErr error) {
	defer recoverAsError(&rErr)
	if len(expression) > MaxExpressionBytes {
		return nil, fmt.Errorf(
			"expression is too long (%d bytes, max %d)", len(expression), MaxExpressionBytes,
		)
	}
	if strings.TrimSpace(expression) == "" {
		return nil, errors.New("expression is empty")
	}
	root, err := parse(expression, features)
	if err != nil {
		return nil, err
	}
	return &Query{root: root}, nil
}

// Search は式を d に適用し、結果を空白なしの JSON で返す。
func (q *Query) Search(d *Document) (out []byte, rErr error) {
	defer recoverAsError(&rErr)
	it := &interp{limit: d.nodes*stepsPerNode + baseSteps}
	v, err := it.eval(q.root, d.root)
	if err != nil {
		return nil, err
	}
	return appendJSON(nil, v, d.size+resultSlack)
}

// recoverAsError は panic をエラーに変える。式もドキュメントも外部入力で、評価は HTTP
// ハンドラの recover が届かないゴルーチンで動きうるため、実装の誤りでプロセスを落とさない。
func recoverAsError(rErr *error) {
	if r := recover(); r != nil {
		*rErr = fmt.Errorf("internal error while handling the expression: %v", r)
	}
}

type interp struct {
	steps int
	limit int
}

// charge は作業量を n だけ数え、上限を超えたら ErrTooExpensive を返す。
func (it *interp) charge(n int) error {
	it.steps += n
	if it.steps > it.limit {
		return ErrTooExpensive
	}
	return nil
}

// eval は節 n を値 v に対して評価する。
func (it *interp) eval(n *node, v any) (any, error) {
	if err := it.charge(1); err != nil {
		return nil, err
	}
	switch n.kind {
	case nIdentity:
		return v, nil
	case nLiteral:
		return n.value, nil
	case nField:
		return field(v, n.name), nil
	case nSubexpression, nPipe, nIndexExpression:
		left, err := it.eval(n.children[0], v)
		if err != nil {
			return nil, err
		}
		return it.eval(n.children[1], left)
	case nIndex:
		return index(v, n.index), nil
	case nSlice:
		return it.slice(v, n.slice)
	case nProjection, nValueProjection, nFilterProjection:
		return it.projection(n, v)
	case nFlatten:
		return it.flatten(n, v)
	case nMultiSelectList, nMultiSelectHash:
		return it.multiSelect(n, v)
	case nOr, nAnd, nNot:
		return it.logical(n, v)
	case nComparator:
		return it.comparator(n, v)
	}
	return nil, fmt.Errorf("unknown syntax node %d", n.kind)
}

func field(v any, name string) any {
	obj, ok := v.(*object)
	if !ok {
		return nil
	}
	val, _ := obj.get(name)
	return val
}

// logical は ||・&&・! を評価する。|| と && は真偽値ではなく、決め手になった側の値を返す。
func (it *interp) logical(n *node, v any) (any, error) {
	left, err := it.eval(n.children[0], v)
	if err != nil {
		return nil, err
	}
	switch {
	case n.kind == nNot:
		return isFalse(left), nil
	case isFalse(left) == (n.kind == nAnd):
		return left, nil
	}
	return it.eval(n.children[1], v)
}

func index(v any, i int) any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	if i < 0 {
		i += len(arr)
	}
	if i < 0 || i >= len(arr) {
		return nil
	}
	return arr[i]
}

// slice は Python と同じ規則で v[start:stop:step] を返す。配列でなければ null。
func (it *interp) slice(v any, parts [3]*int) (any, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, nil //nolint:nilnil // JSON の null
	}
	length := len(arr)
	step := 1
	if parts[2] != nil {
		step = *parts[2]
	}
	if step == 0 {
		return nil, errors.New("slice step cannot be 0")
	}
	// 配列より大きい刻みは、どれも「最初の 1 要素だけ」になる。桁あふれを避けるため丸める。
	step = max(min(step, length+1), -length-1)

	bound := func(part *int, ifOmitted int) int {
		if part == nil {
			return ifOmitted
		}
		i := *part
		switch {
		case i < 0:
			i += length
			if i < 0 {
				if step < 0 {
					return -1
				}
				return 0
			}
		case i >= length:
			if step < 0 {
				return length - 1
			}
			return length
		}
		return i
	}
	var start, stop int
	if step > 0 {
		start, stop = bound(parts[0], 0), bound(parts[1], length)
	} else {
		start, stop = bound(parts[0], length-1), bound(parts[1], -1)
	}

	out := []any{}
	for i := start; (step > 0 && i < stop) || (step < 0 && i > stop); i += step {
		out = append(out, arr[i])
	}
	return out, it.charge(len(out))
}

// projection は左辺の配列（nValueProjection ではオブジェクトの値）の各要素に右辺を
// 適用し、null でない結果を集める。nFilterProjection では条件が真の要素だけを対象にする。
func (it *interp) projection(n *node, v any) (any, error) {
	base, err := it.eval(n.children[0], v)
	if err != nil {
		return nil, err
	}
	var elems []any
	if n.kind == nValueProjection {
		obj, ok := base.(*object)
		if !ok {
			return nil, nil //nolint:nilnil // JSON の null
		}
		elems = obj.vals
	} else {
		arr, ok := base.([]any)
		if !ok {
			return nil, nil //nolint:nilnil // JSON の null
		}
		elems = arr
	}
	out := []any{}
	for _, elem := range elems {
		if n.kind == nFilterProjection {
			matched, err := it.eval(n.children[2], elem)
			if err != nil {
				return nil, err
			}
			if isFalse(matched) {
				continue
			}
		}
		result, err := it.eval(n.children[1], elem)
		if err != nil {
			return nil, err
		}
		if result != nil {
			out = append(out, result)
		}
	}
	return out, it.charge(len(out))
}

// flatten は配列の中の配列を 1 段だけ展開する。
func (it *interp) flatten(n *node, v any) (any, error) {
	base, err := it.eval(n.children[0], v)
	if err != nil {
		return nil, err
	}
	arr, ok := base.([]any)
	if !ok {
		return nil, nil //nolint:nilnil // JSON の null
	}
	out := []any{}
	for _, elem := range arr {
		inner, ok := elem.([]any)
		if !ok {
			inner = []any{elem}
		}
		// 展開で要素が倍々に増える式があるので、足す前に数える。
		if err := it.charge(len(inner)); err != nil {
			return nil, err
		}
		out = append(out, inner...)
	}
	return out, nil
}

// multiSelect は [式, ...] と {名前: 式, ...} を評価する。null に対しては null。
func (it *interp) multiSelect(n *node, v any) (any, error) {
	if v == nil {
		return nil, nil //nolint:nilnil // JSON の null
	}
	if err := it.charge(len(n.children)); err != nil {
		return nil, err
	}
	results := make([]any, len(n.children))
	for i, child := range n.children {
		result, err := it.eval(child, v)
		if err != nil {
			return nil, err
		}
		results[i] = result
	}
	if n.kind == nMultiSelectList {
		return results, nil
	}
	obj := &object{}
	for i, key := range n.keys {
		obj.set(key, results[i])
	}
	return obj, nil
}

// comparator は比較を評価する。大小比較は数値どうし・文字列どうしでだけ成り立ち、
// それ以外は null になる。文字列の大小比較は仕様には無いが、AWS CLI が使う Python 版が
// 受け付けるので（日付の文字列での絞り込みによく使われる）合わせる。
func (it *interp) comparator(n *node, v any) (any, error) {
	left, err := it.eval(n.children[0], v)
	if err != nil {
		return nil, err
	}
	right, err := it.eval(n.children[1], v)
	if err != nil {
		return nil, err
	}
	if n.op == tEQ || n.op == tNE {
		eq, err := it.equal(left, right)
		if err != nil {
			return nil, err
		}
		return eq == (n.op == tEQ), nil
	}
	cmp, ok, err := it.order(left, right)
	if err != nil || !ok {
		return nil, err
	}
	return (n.op == tLT && cmp < 0) || (n.op == tLTE && cmp <= 0) ||
		(n.op == tGT && cmp > 0) || (n.op == tGTE && cmp >= 0), nil
}

// order は数値どうし・文字列どうしの大小を返す。それ以外の組み合わせでは ok が false
// （比較の結果は null になる）。
func (it *interp) order(a, b any) (cmp int, ok bool, err error) {
	switch a := a.(type) {
	case number:
		if b, isNumber := b.(number); isNumber {
			cmp, err = it.compareNumbers(a, b)
			return cmp, true, err
		}
	case string:
		if b, isString := b.(string); isString {
			// 中身を読み比べる長さも作業量に数える。
			return strings.Compare(a, b), true, it.charge(min(len(a), len(b)) / bytesPerStep)
		}
	}
	return 0, false, nil
}

// equal は 2 つの値が等しいかを返す。配列とオブジェクトは中身を比べる（オブジェクトの
// キーの順序は問わない）。
func (it *interp) equal(a, b any) (bool, error) {
	if err := it.charge(1); err != nil {
		return false, err
	}
	switch a := a.(type) {
	case nil:
		return b == nil, nil
	case bool:
		b, ok := b.(bool)
		return ok && a == b, nil
	case string:
		b, ok := b.(string)
		if !ok || len(a) != len(b) {
			return false, nil
		}
		// 長さが同じなら中身を読み比べる。その長さも作業量に数える。
		return a == b, it.charge(len(a) / bytesPerStep)
	case number:
		b, ok := b.(number)
		if !ok {
			return false, nil
		}
		cmp, err := it.compareNumbers(a, b)
		return cmp == 0, err
	case []any:
		b, ok := b.([]any)
		return it.equalArrays(a, b, ok)
	case *object:
		b, ok := b.(*object)
		return it.equalObjects(a, b, ok)
	}
	return false, nil
}

func (it *interp) equalArrays(a, b []any, bothArrays bool) (bool, error) {
	if !bothArrays || len(a) != len(b) {
		return false, nil
	}
	for i := range a {
		if eq, err := it.equal(a[i], b[i]); err != nil || !eq {
			return false, err
		}
	}
	return true, nil
}

func (it *interp) equalObjects(a, b *object, bothObjects bool) (bool, error) {
	if !bothObjects || len(a.keys) != len(b.keys) {
		return false, nil
	}
	for i, key := range a.keys {
		// キーを探すにはその中身を読む（少ないキーなら読み比べ、多ければハッシュを計算する）
		// ので、キーの長さも作業量に数える。
		if err := it.charge(len(key) / bytesPerStep); err != nil {
			return false, err
		}
		other, found := b.get(key)
		if !found {
			return false, nil
		}
		if eq, err := it.equal(a.vals[i], other); err != nil || !eq {
			return false, err
		}
	}
	return true, nil
}

// Equal は 2 つの JSON が同じ値を表すかを返す。オブジェクトのキーの順序と、数値の表記
// （1 と 1.0）の違いは問わない。どちらかが JSON として読めなければ false。
func Equal(a, b []byte) bool {
	if bytes.Equal(a, b) {
		return true
	}
	x, err := Decode(a)
	if err != nil {
		return false
	}
	y, err := Decode(b)
	if err != nil {
		return false
	}
	// 比較は値 1 組につき 1 手と、読み比べた文字列の長さ分しか数えない。
	it := &interp{limit: x.nodes + y.nodes + (x.size+y.size)/bytesPerStep}
	eq, err := it.equal(x.root, y.root)
	return err == nil && eq
}
