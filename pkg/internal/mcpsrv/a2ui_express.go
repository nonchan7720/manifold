package mcpsrv

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// フォームを A2UI Express 記法で書き出す。
//
// Express はモデルが出力するための簡潔な DSL で、<a2ui>...</a2ui> の中に書く。
// エージェント側の A2UI Agent SDK がそれをワイヤ形式（JSON）の A2UI メッセージに
// コンパイルしてレンダラへ渡す。エージェントの出力から A2UI を拾うホストでは、
// ゲートウェイが JSON ではなく Express を返せば、エージェントはそれをそのまま
// 出力するだけで済む。
//
// 書き方は A2UI の Express 仕様（specification/proposals/express/a2ui_express.md）と、
// 公式 SDK のデコンパイラ（JSON → Express）の出力に合わせる。
//
//   - コンポーネントは `id = Component(prop=value, ...)`。位置引数の順序はカタログの
//     定義に依存するので、プロパティは常にキーワード引数で書く。
//   - データ束縛は `$/path`、イベントは `Event("name", {key: $/path})`。
//   - チェックは `?name(value, params..., "message")` を位置引数として先頭に置く。
//     値の束縛を明示するので、コンポーネントの value に依存しない（Button にも書ける）。
//   - 初期値は葉ごとに `$/path = value`。
//
// ここで扱うのは buildA2UIFormComponents が作る形だけで、任意の A2UI メッセージの
// 変換器ではない。

const (
	// a2uiExpressOpenTag と a2uiExpressCloseTag は Express ブロックを囲むタグ。
	a2uiExpressOpenTag  = "<a2ui>"
	a2uiExpressCloseTag = "</a2ui>"
)

// a2uiExpressCheckParams は、チェックに使う basic カタログの関数の引数の並び。
// Express ではチェックの引数を位置で書くので、カタログの定義順が要る。
var a2uiExpressCheckParams = map[string][]string{
	"required": {"value"},
	"regex":    {"value", "pattern"},
	"length":   {"value", "min", "max"},
}

// buildA2UIExpressForm はフォーム全体（surface、初期値、コンポーネント）を Express の
// <a2ui> ブロックとして返す。
func buildA2UIExpressForm(spec a2uiFormSpec, dataModel map[string]any) (string, error) {
	lines := []string{
		a2uiExpressOpenTag,
		"surface(" + expressString(spec.surfaceID) + ")",
	}
	for _, leaf := range expressDataLeaves(dataModel, "") {
		value, err := expressValue(leaf.value, false)
		if err != nil {
			return "", err
		}
		lines = append(lines, "$"+leaf.path+" = "+value)
	}
	for _, c := range buildA2UIFormComponents(spec) {
		line, err := expressComponent(c, spec.vocab)
		if err != nil {
			return "", err
		}
		lines = append(lines, line)
	}
	lines = append(lines, a2uiExpressCloseTag)
	return strings.Join(lines, "\n"), nil
}

type expressDataLeaf struct {
	path  string
	value any
}

// expressDataLeaves はデータモデルを葉ごとのパスに平坦化する（キーの昇順）。空の
// マップは何も代入しない。キーは a2uiDataKey を通っているので、パスにそのまま書ける。
func expressDataLeaves(v any, path string) []expressDataLeaf {
	m, ok := v.(map[string]any)
	if !ok {
		return []expressDataLeaf{{path: path, value: v}}
	}
	var leaves []expressDataLeaf
	for _, key := range slices.Sorted(maps.Keys(m)) {
		leaves = append(leaves, expressDataLeaves(m[key], path+"/"+key)...)
	}
	return leaves
}

// expressComponent は 1 つのコンポーネントを `id = Component(...)` の行にする。
func expressComponent(component any, vocab *a2uiVocabulary) (string, error) {
	c, ok := component.(map[string]any)
	if !ok {
		return "", fmt.Errorf("a2ui express: component is not an object")
	}
	id, _ := c["id"].(string)
	name, _ := c["component"].(string)
	if id == "" || name == "" {
		return "", fmt.Errorf("a2ui express: component without id or name")
	}
	var args []string
	if checks, ok := c["@checks"].([]any); ok && len(checks) > 0 {
		reprs := make([]string, 0, len(checks))
		for _, check := range checks {
			repr, err := expressCheck(check)
			if err != nil {
				return "", err
			}
			reprs = append(reprs, repr)
		}
		args = append(args, "["+strings.Join(reprs, ", ")+"]")
	}
	for _, prop := range slices.Sorted(maps.Keys(c)) {
		if prop == "id" || prop == "component" || prop == "@checks" {
			continue
		}
		// 子の部品を指すプロパティ（basic カタログなら child / children）の値は、他の
		// コンポーネントの ID（変数名）への参照。
		value, err := expressValue(c[prop], vocab.isRef(name, prop))
		if err != nil {
			return "", err
		}
		args = append(args, prop+"="+value)
	}
	return id + " = " + name + "(" + strings.Join(args, ", ") + ")", nil
}

// expressCheck はチェックを `?name(value, params..., "message")` にする。途中の
// 引数が無ければ "_" で埋め、メッセージは引数の並びの後ろに置く。
func expressCheck(check any) (string, error) {
	rule, _ := check.(map[string]any)
	condition, _ := rule["condition"].(map[string]any)
	name, _ := condition["@call"].(string)
	params, known := a2uiExpressCheckParams[name]
	if !known {
		return "", fmt.Errorf("a2ui express: unsupported check %q", name)
	}
	callArgs, _ := condition["args"].(map[string]any)
	args := make([]string, 0, len(params)+1)
	for _, param := range params {
		v, ok := callArgs[param]
		if !ok {
			args = append(args, "_")
			continue
		}
		repr, err := expressValue(v, false)
		if err != nil {
			return "", err
		}
		args = append(args, repr)
	}
	if message, _ := rule["message"].(string); message != "" {
		args = append(args, expressString(message))
	} else {
		for len(args) > 0 && args[len(args)-1] == "_" {
			args = args[:len(args)-1]
		}
	}
	return "?" + name + "(" + strings.Join(args, ", ") + ")", nil
}

// expressValue は値を Express の式にする。ref が true なら、文字列はコンポーネントへの
// 参照（変数名）としてそのまま書く。
func expressValue(v any, ref bool) (string, error) {
	switch val := v.(type) {
	case nil:
		return "null", nil
	case bool:
		return strconv.FormatBool(val), nil
	case string:
		if ref {
			return val, nil
		}
		return expressString(val), nil
	case int:
		return strconv.Itoa(val), nil
	case int64:
		return strconv.FormatInt(val, 10), nil
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64), nil
	case json.Number:
		return val.String(), nil
	case []string:
		items := make([]any, 0, len(val))
		for _, item := range val {
			items = append(items, item)
		}
		return expressValue(items, ref)
	case []any:
		items := make([]string, 0, len(val))
		for _, item := range val {
			repr, err := expressValue(item, ref)
			if err != nil {
				return "", err
			}
			items = append(items, repr)
		}
		return "[" + strings.Join(items, ", ") + "]", nil
	case map[string]any:
		return expressMap(val)
	}
	return "", fmt.Errorf("a2ui express: unsupported value of type %T", v)
}

// expressMap はオブジェクトを式にする。データ束縛とイベントは専用の書き方、
// それ以外はマップリテラル。
func expressMap(m map[string]any) (string, error) {
	if path, ok := m["@path"].(string); ok && len(m) == 1 {
		return "$" + path, nil
	}
	if event, ok := m["event"].(map[string]any); ok && len(m) == 1 {
		name, _ := event["name"].(string)
		args := []string{expressString(name)}
		if context, ok := event["context"].(map[string]any); ok {
			repr, err := expressMapLiteral(context)
			if err != nil {
				return "", err
			}
			args = append(args, repr)
		}
		return "Event(" + strings.Join(args, ", ") + ")", nil
	}
	return expressMapLiteral(m)
}

func expressMapLiteral(m map[string]any) (string, error) {
	items := make([]string, 0, len(m))
	for _, key := range slices.Sorted(maps.Keys(m)) {
		repr, err := expressValue(m[key], false)
		if err != nil {
			return "", err
		}
		items = append(items, expressKey(key)+": "+repr)
	}
	return "{" + strings.Join(items, ", ") + "}", nil
}

// expressKey はマップのキーを書く。識別子として書けるものはそのまま、それ以外
// （"X-Request-Id" など）と予約語は文字列にする。
func expressKey(key string) string {
	if expressIdentifier(key) {
		return key
	}
	return expressString(key)
}

func expressIdentifier(s string) bool {
	switch s {
	case "", "_", "true", "false", "null":
		return false
	}
	for i, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// a2uiSentinelRE は文字列の中に現れる Express のタグ。
var a2uiSentinelRE = regexp.MustCompile(`(?i)<(/?a2ui)`)

// expressString は文字列リテラルを書く。値は spec やツールの引数から来る外部入力
// なので、引用符とバックスラッシュを必ずエスケープし、リテラルの外へ出られない
// ようにする（raw 文字列は使わない）。
//
// タグの検出は文字列リテラルの中かどうかを見ないので、spec 由来の文字列（ツールの
// 説明など）に </a2ui> が含まれるとブロックが途中で閉じてしまう。"<" の後ろに空白を
// 入れてタグとして読まれないようにする。
func expressString(s string) string {
	s = a2uiSentinelRE.ReplaceAllString(s, "< ${1}")
	// バックスラッシュを最初に置き換える（後続のエスケープを二重にしないため）。
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return `"` + s + `"`
}
