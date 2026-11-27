package jmespath

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// 評価中の JSON 値は any で持つ。
//
//	nil      null
//	bool     真偽値
//	string   文字列
//	number   数値
//	[]any    配列
//	*object  オブジェクト

// number は JSON の数値。
type number struct {
	f float64
	// raw は元の JSON での表記。計算で作った数値では空。結果へそのまま書き出すので、
	// float64 で表せない整数（2^53 を超える ID など）も桁を失わない。
	raw string
}

// object は JSON のオブジェクト。キーの順序を保つ。
type object struct {
	keys []string
	vals []any
	// index はキーから位置への対応。キーが多いオブジェクトでだけ作る。
	index map[string]int
}

// objectIndexThreshold を超えるキーを持つオブジェクトでは index を作る。
const objectIndexThreshold = 8

func (o *object) find(key string) int {
	if o.index != nil {
		if i, ok := o.index[key]; ok {
			return i
		}
		return -1
	}
	for i, k := range o.keys {
		if k == key {
			return i
		}
	}
	return -1
}

func (o *object) get(key string) (any, bool) {
	if i := o.find(key); i >= 0 {
		return o.vals[i], true
	}
	return nil, false
}

// set は key の値を v にする。既にあるキーなら値だけを置き換える（位置は変えない）。
func (o *object) set(key string, v any) {
	if i := o.find(key); i >= 0 {
		o.vals[i] = v
		return
	}
	o.keys = append(o.keys, key)
	o.vals = append(o.vals, v)
	switch {
	case o.index != nil:
		o.index[key] = len(o.keys) - 1
	case len(o.keys) > objectIndexThreshold:
		o.index = make(map[string]int, len(o.keys)*2)
		for i, k := range o.keys {
			o.index[k] = i
		}
	}
}

// JSON の型名（Describe が使う）。
const (
	typeNull    = "null"
	typeBoolean = "boolean"
	typeString  = "string"
	typeNumber  = "number"
	typeArray   = "array"
	typeObject  = "object"
)

func typeOf(v any) string {
	switch v.(type) {
	case nil:
		return typeNull
	case bool:
		return typeBoolean
	case string:
		return typeString
	case number:
		return typeNumber
	case []any:
		return typeArray
	case *object:
		return typeObject
	}
	return "unknown"
}

// isFalse は JMESPath の偽（false・null・空文字列・空配列・空オブジェクト）かを返す。
func isFalse(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case bool:
		return !v
	case string:
		return v == ""
	case []any:
		return len(v) == 0
	case *object:
		return len(v.keys) == 0
	}
	return false
}

// Document は式を適用する JSON ドキュメント。
type Document struct {
	root any
	// nodes は値の個数、size は元の JSON の長さ。評価の作業量と結果の大きさの上限を
	// 決める基準にする。
	nodes int
	size  int
}

// Decode は JSON を読み込む。重複キーは後のものを採る。
func Decode(doc []byte) (*Document, error) {
	// 不正な UTF-8 を含む JSON も、絞り込まなければそのまま返っていたものなので受け付ける。
	dec := jsontext.NewDecoder(
		bytes.NewReader(doc),
		jsontext.AllowDuplicateNames(true),
		jsontext.AllowInvalidUTF8(true),
	)
	d := &Document{size: len(doc)}
	root, err := d.decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected data after the JSON value")
	}
	d.root = root
	return d, nil
}

func (d *Document) decodeValue(dec *jsontext.Decoder) (any, error) {
	tok, err := dec.ReadToken()
	if err != nil {
		return nil, err
	}
	d.nodes++
	switch tok.Kind() {
	case jsontext.KindNull:
		return nil, nil //nolint:nilnil // JSON の null
	case jsontext.KindTrue:
		return true, nil
	case jsontext.KindFalse:
		return false, nil
	case jsontext.KindString:
		return tok.String(), nil
	case jsontext.KindNumber:
		// 範囲外（1e999 など）は ±Inf になるが、表記は raw に残る。
		f, _ := tok.Float()
		return number{f: f, raw: tok.String()}, nil
	case jsontext.KindBeginObject:
		return d.decodeObject(dec)
	case jsontext.KindBeginArray:
		return d.decodeArray(dec)
	case jsontext.KindInvalid, jsontext.KindEndObject, jsontext.KindEndArray:
	}
	return nil, fmt.Errorf("unexpected JSON token %q", tok.Kind())
}

func (d *Document) decodeObject(dec *jsontext.Decoder) (any, error) {
	obj := &object{}
	for dec.PeekKind() != jsontext.KindEndObject {
		name, err := dec.ReadToken()
		if err != nil {
			return nil, err
		}
		key := name.String()
		v, err := d.decodeValue(dec)
		if err != nil {
			return nil, err
		}
		obj.set(key, v)
	}
	_, err := dec.ReadToken()
	return obj, err
}

func (d *Document) decodeArray(dec *jsontext.Decoder) (any, error) {
	arr := []any{}
	for dec.PeekKind() != jsontext.KindEndArray {
		v, err := d.decodeValue(dec)
		if err != nil {
			return nil, err
		}
		arr = append(arr, v)
	}
	_, err := dec.ReadToken()
	return arr, err
}

// Describe は式を書き直す手がかりとして、ドキュメントの最上位の形を短い英文で返す。
func (d *Document) Describe() string {
	switch v := d.root.(type) {
	case *object:
		return "an object with " + describeKeys(v)
	case []any:
		desc := fmt.Sprintf("an array of %d elements", len(v))
		if len(v) > 0 {
			if first, ok := v[0].(*object); ok {
				desc += "; its first element is an object with " + describeKeys(first)
			} else {
				desc += "; its first element is of type " + typeOf(v[0])
			}
		}
		return desc
	}
	return "a value of type " + typeOf(d.root)
}

const (
	// maxDescribedKeys は Describe が挙げるキーの数の上限。
	maxDescribedKeys = 40
	// maxDescribedKeyBytes は Describe が挙げるキー 1 つの長さの上限。これより長いキーは
	// 挙げずに数だけ伝える（手がかりのほうが結果より大きくならないようにする）。
	maxDescribedKeyBytes = 64
)

func describeKeys(o *object) string {
	if len(o.keys) == 0 {
		return "no keys"
	}
	shown := make([]string, 0, min(len(o.keys), maxDescribedKeys))
	for _, key := range o.keys {
		if len(shown) == maxDescribedKeys {
			break
		}
		if len(key) <= maxDescribedKeyBytes {
			shown = append(shown, displayKey(key))
		}
	}
	if len(shown) == 0 {
		return fmt.Sprintf("%d keys", len(o.keys))
	}
	desc := "keys: " + strings.Join(shown, ", ")
	if more := len(o.keys) - len(shown); more > 0 {
		desc += fmt.Sprintf(", ... (%d more)", more)
	}
	return desc
}

// displayKey はキーを、式の中に書く形で返す。識別子として書けないキー（"x-rate" や
// "a, b"）は引用符で囲むので、並べても 1 つのキーがどこまでかが分かる。
func displayKey(key string) string {
	if isIdentifier(key) {
		return key
	}
	quoted, _ := jsontext.AppendQuote(nil, key)
	return string(quoted)
}

// errResultTooLarge は結果の JSON が上限を超えたことを表す。
var errResultTooLarge = errors.New("the result of the expression is too large")

// appendJSON は v を空白なしの JSON にして buf へ足す。buf が limit バイトを超えたら
// errResultTooLarge を返す。
func appendJSON(buf []byte, v any, limit int) ([]byte, error) {
	var err error
	switch v := v.(type) {
	case nil:
		buf = append(buf, "null"...)
	case bool:
		buf = strconv.AppendBool(buf, v)
	case string:
		buf, err = appendString(buf, v, limit)
	case number:
		buf = appendNumber(buf, v)
	case []any:
		buf, err = appendArray(buf, v, limit)
	case *object:
		buf, err = appendObject(buf, v, limit)
	default:
		return buf, fmt.Errorf("unexpected value of type %T", v)
	}
	if err != nil {
		return buf, err
	}
	if len(buf) > limit {
		return buf, errResultTooLarge
	}
	return buf, nil
}

func appendArray(buf []byte, arr []any, limit int) ([]byte, error) {
	buf = append(buf, '[')
	for i, elem := range arr {
		if i > 0 {
			buf = append(buf, ',')
		}
		var err error
		if buf, err = appendJSON(buf, elem, limit); err != nil {
			return buf, err
		}
	}
	return append(buf, ']'), nil
}

func appendObject(buf []byte, obj *object, limit int) ([]byte, error) {
	buf = append(buf, '{')
	for i, key := range obj.keys {
		if i > 0 {
			buf = append(buf, ',')
		}
		var err error
		if buf, err = appendString(buf, key, limit); err != nil {
			return buf, err
		}
		buf = append(buf, ':')
		if buf, err = appendJSON(buf, obj.vals[i], limit); err != nil {
			return buf, err
		}
	}
	return append(buf, '}'), nil
}

// appendString は s を JSON の文字列にして buf へ足す。引用すると少なくとも len(s)+2
// バイトになるので、それだけで limit を超える文字列は引用する（バッファを伸ばす）前に断る。
func appendString(buf []byte, s string, limit int) ([]byte, error) {
	if len(buf)+len(s)+2 > limit {
		return buf, errResultTooLarge
	}
	// 不正な UTF-8 は U+FFFD に置き換わり、その旨のエラーが返る。置き換えた結果を使う。
	out, _ := jsontext.AppendQuote(buf, s)
	return out, nil
}

func appendNumber(buf []byte, n number) []byte {
	if n.raw != "" {
		return append(buf, n.raw...)
	}
	if math.IsNaN(n.f) || math.IsInf(n.f, 0) {
		// JSON では表せない。
		return append(buf, "null"...)
	}
	return jsontext.AppendFloat(buf, n.f, 64)
}
