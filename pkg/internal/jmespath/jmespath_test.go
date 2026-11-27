package jmespath

import (
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// compileAll は、条件と位置の構文も有効にして式を解釈する。
func compileAll(expression string) (*Query, error) {
	return Compile(expression, Features{Conditions: true, Indexing: true})
}

func search(t *testing.T, expression, doc string) string {
	t.Helper()
	d, err := Decode([]byte(doc))
	require.NoError(t, err)
	q, err := compileAll(expression)
	require.NoError(t, err, expression)
	out, err := q.Search(d)
	require.NoError(t, err, expression)
	return string(out)
}

// testdata/reference_cases.json の期待値は、参照実装（AWS CLI が使う Python 版 jmespath）で
// 同じ式を同じドキュメントに適用した結果。キーの順序と数値の表記まで一致することを確かめる。
func TestSearch_MatchesReferenceImplementation(t *testing.T) {
	raw, err := os.ReadFile("testdata/reference_cases.json")
	require.NoError(t, err)
	var suite struct {
		Given json.RawMessage `json:"given"`
		Cases []struct {
			Expression string         `json:"expression"`
			Result     jsontext.Value `json:"result"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &suite))
	require.NotEmpty(t, suite.Cases)

	doc, err := Decode(suite.Given)
	require.NoError(t, err)
	for _, c := range suite.Cases {
		t.Run(c.Expression, func(t *testing.T) {
			q, err := compileAll(c.Expression)
			require.NoError(t, err)
			got, err := q.Search(doc)
			require.NoError(t, err)

			want := c.Result
			require.NoError(t, want.Compact())
			require.Equal(t, string(want), string(got))
		})
	}
}

// 参照実装と意図して違える点、参照実装がエラーにする点。
func TestSearch_Semantics(t *testing.T) {
	const doc = `{
		"foo": {"a": {"bar": {"baz": 1}, "arr": [{"x": 1}, {"x": 2}]}, "b": {"bar": {"baz": 2}, "arr": [{"x": 3}]}},
		"list": [[{"v": 1}, {"v": 2}], [{"v": 3}]],
		"nums": [0, 1, 2, 3, 4, 5],
		"s": "abc", "n": 5, "nil": null, "obj": {"k": "v"}
	}`
	tests := []struct {
		name       string
		expression string
		want       string
	}{
		// 参照実装では null になる（オブジェクトの射影の後ろが 1 段しか射影されない）が、
		// 仕様どおり各値に残りの式を適用する。
		{"object projection keeps projecting", "foo.*.bar.baz", `[1,2]`},
		{"object projection with an index", "foo.*.arr[0].x", `[1,3]`},
		// 参照実装では添字の直後のスライスが射影にならない。
		{"a slice after an index projects", "list[0][0:1].v", `[1]`},

		// 大小比較は数値どうし・文字列どうしだけ。それ以外は null（参照実装は例外を投げる）。
		{"ordering a string against a number", "s > `1`", `null`},
		{"ordering against null", "nil < `1`", `null`},
		{"ordering objects", "obj < obj", `null`},
		{"ordering in a filter drops the element", "nums[?@ > 'a']", `[]`},
		{"ordering strings", "s >= 'abc' && s < 'abd'", `true`},

		// バッククォートの中が JSON でなければ文字列（参照実装と同じ、非推奨の書き方）。
		{"a literal that is not JSON is a string", "`abc`", `"abc"`},
		{"a raw string keeps backslashes", `'a\\b\n'`, `"a\\\\b\\n"`},
		{"an escaped quote in a raw string", `'it\'s'`, `"it's"`},
		{"an escaped backtick in a literal", "`\"a\\`b\"`", "\"a`b\""},

		{"pipe passes null on", "missing | [0]", `null`},
		{"multiselect on null is null", "missing.[a, b]", `null`},
		{"multiselect hash on null is null", "missing.{a: a}", `null`},
		{"or returns the first truthy value", "missing || nil || n", `5`},
		{"and returns the first falsy value", "n && nil && s", `null`},
		{"not", "!missing", `true`},
		{"parentheses", "(missing || nums[1]) == `1`", `true`},
		// 0 は偽ではない（偽は false・null・空文字列・空配列・空オブジェクト）。
		{"zero is truthy", "nums[0] || nums[1]", `0`},
		{"comparison binds tighter than and", "n == `5` && s == 'abc'", `true`},
		{"flatten stops at one level", "list[]", `[{"v":1},{"v":2},{"v":3}]`},
		{"a negative step", "nums[4:1:-1]", `[4,3,2]`},
		{"a step larger than the array", "nums[::100]", `[0]`},
		{"a huge negative step", "nums[::-9223372036854775808]", `[5]`},
		{"an out of range slice", "nums[10:20]", `[]`},
		{"quoted identifiers", `obj."k"`, `"v"`},
		{
			"deep equality ignores the order of keys",
			"`{\"a\":1,\"b\":[1,2]}` == `{\"b\":[1,2],\"a\":1}`",
			`true`,
		},
		{"deep equality compares every element", "`[1,[2,3]]` == `[1,[2,4]]`", `false`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, search(t, tt.expression, doc))
		})
	}
}

// 結果は元の JSON の表記のまま返る。float64 で表せない整数や小数の桁も失わない。
func TestSearch_PreservesNumbersAndKeyOrder(t *testing.T) {
	const doc = `{"zeta": 1, "alpha": {"id": 12345678901234567890, "price": 1.10, "exp": 1E2,
		"tiny": 0.1000000000000000055511151231257827, "neg": -0},
		"ids": [9007199254740993, 9007199254740992]}`

	require.Equal(
		t,
		`{"id":12345678901234567890,"price":1.10,"exp":1E2,"tiny":0.1000000000000000055511151231257827,"neg":-0}`,
		search(t, "alpha", doc),
	)
	// キーは元の JSON の順、複数選択は式に書いた順。
	require.Equal(t, `[1,{"p":1.10,"i":12345678901234567890}]`,
		search(t, "[zeta, alpha.{p: price, i: id}]", doc))
	require.Equal(t, `[9007199254740993,9007199254740992]`, search(t, "ids", doc))
}

// 数値の比較は丸める前の値で行う。float64 では同じ値になる整数（2^53 を超える ID）も
// 区別し、表記（整数・小数・指数）を変えても結果は変わらない。
func TestSearch_ComparesNumbersExactly(t *testing.T) {
	const doc = `{"id": 9007199254740993, "ids": [9007199254740993, 9007199254740992],
		"big": 12345678901234567890, "price": 1.10, "exp": 1E2, "neg": -0,
		"tiny": 0.1000000000000000055511151231257827, "under": 1e-400, "over": 1e400}`
	tests := []struct {
		expression string
		want       string
	}{
		// 等値
		{"ids[?@ == `9007199254740993`]", `[9007199254740993]`},
		{"ids[?@ == `9007199254740992`]", `[9007199254740992]`},
		{"id == `9007199254740993`", `true`},
		{"id == `9007199254740992`", `false`},
		{"id != `9007199254740992`", `true`},
		{"big == `12345678901234567890`", `true`},
		{"big == `12345678901234567891`", `false`},
		// 右辺を小数や指数で書いても、丸める前の値で比べる。
		{"id == `9007199254740992.0`", `false`},
		{"id == `9007199254740993.0`", `true`},
		{"id == `9.007199254740993e15`", `true`},
		{"id == `9.007199254740992e15`", `false`},
		{"tiny == `0.1`", `false`},
		// 表記が違っても値が同じなら等しい。
		{"exp == `100` && exp == `100.0` && exp == `1e2`", `true`},
		{"price == `1.1` && price == `1.100`", `true`},
		{"neg == `0` && neg == `0.0` && neg == `-0`", `true`},
		// 大小
		{"id > `9007199254740992`", `true`},
		{"id >= `9007199254740993` && id <= `9007199254740993`", `true`},
		{"id < `9007199254740994`", `true`},
		{"id > `9007199254740993`", `false`},
		{"id > `9007199254740992.5`", `true`},
		{"ids[?@ > `9007199254740992`]", `[9007199254740993]`},
		{"ids[?@ < `9007199254740993`]", `[9007199254740992]`},
		{"tiny > `0.1`", `true`},
		{"big > `-12345678901234567891`", `true`},
		{"`-12345678901234567891` < `-12345678901234567890`", `true`},
		// float64 では 0 や無限大になる値も、桁で比べる。
		{"under > `0` && under < `1e-399`", `true`},
		{"over > `1e399` && over == `1e400`", `true`},
	}
	for _, tt := range tests {
		t.Run(tt.expression, func(t *testing.T) {
			require.Equal(t, tt.want, search(t, tt.expression, doc))
		})
	}
}

// float64 では区別できない（どちらも無限大になる）ほど長い数値も、桁で比べる。
func TestSearch_ComparesVeryLongNumbers(t *testing.T) {
	big := "1" + strings.Repeat("0", 600) // 10^600
	smaller := strings.Repeat("9", 600)   // 10^600 - 1
	doc := `{"big": ` + big + `, "smaller": ` + smaller + `, "same": ` + big + `.0}`
	tests := []struct {
		expression string
		want       string
	}{
		{"big == smaller", `false`},
		{"big != smaller", `true`},
		{"big > smaller", `true`},
		{"smaller < big", `true`},
		{"big <= smaller", `false`},
		{"big == same", `true`},
		{"big >= same && big <= same", `true`},
		{"[big, smaller][?@ > `1e599`]", `[` + big + `,` + smaller + `]`},
	}
	for _, tt := range tests {
		t.Run(tt.expression, func(t *testing.T) {
			require.Equal(t, tt.want, search(t, tt.expression, doc))
		})
	}

	// 値は同じで表記だけが長いリテラルでも、結果は変わらない。
	padded := "`9007199254740992." + strings.Repeat("0", 600) + "`"
	for expression, want := range map[string]string{
		"id == " + padded: `false`,
		"id > " + padded:  `true`,
		"id <= " + padded: `false`,
	} {
		require.Equal(t, want, search(t, expression, `{"id": 9007199254740993}`), expression)
	}
}

// 桁で比べる作業は表記の長さに比例するので、作業量に数える（同じ比較を式に何百回も
// 並べられるため）。指数が大きすぎて桁数を数えられない数値は、比べずにエラーにする。
func TestSearch_NumberComparisonLimits(t *testing.T) {
	long := strings.Repeat("7", 4<<20)
	d, err := Decode([]byte(`{"a": ` + long + `1, "b": ` + long + `2}`))
	require.NoError(t, err)

	// 1 回なら通る。
	q, err := compileAll("a < b")
	require.NoError(t, err)
	out, err := q.Search(d)
	require.NoError(t, err)
	require.Equal(t, "true", string(out))

	for _, comparison := range []string{"a < b", "a == b"} {
		q, err := compileAll(
			"[" + strings.TrimSuffix(strings.Repeat(comparison+",", 300), ",") + "]",
		)
		require.NoError(t, err)
		_, err = q.Search(d)
		require.ErrorIs(t, err, ErrTooExpensive, comparison)
	}

	d, err = Decode([]byte(`{"a": 1e99999999999999999999, "b": 2e99999999999999999999}`))
	require.NoError(t, err)
	for _, comparison := range []string{"a < b", "a == b"} {
		q, err := compileAll(comparison)
		require.NoError(t, err)
		_, err = q.Search(d)
		require.ErrorContains(t, err,
			"the exponent of the number 1e99999999999999999999 is too large to compare")
	}
	// 同じ表記どうしなら、指数を読まずに等しいと分かる。
	require.Equal(t, "true", search(t, "a == a && a <= a", `{"a": 1e99999999999999999999}`))
}

func TestCompareDecimals(t *testing.T) {
	tests := []struct {
		x, y string
		want int
	}{
		{"1", "1.0", 0},
		{"100", "1e2", 0},
		{"0.00123", "123e-5", 0},
		{"-0", "0", 0},
		{"0", "0.000", 0},
		{"0e5", "0", 0},
		{"12", "123", -1},
		{"0.12", "0.123", -1},
		{"2", "10", -1},
		{"-2", "-10", 1},
		{"-1", "1", -1},
		{"0", "-1", 1},
		{"0", "1e-999", -1},
		{"1e-999", "1e-1000", 1},
		{"9007199254740993", "9007199254740992", 1},
		{"1" + strings.Repeat("0", 2000), strings.Repeat("9", 2000), 1},
		{"1e4611686018427387904", "1e4611686018427387903", 1},
	}
	for _, tt := range tests {
		t.Run(abbreviate(tt.x)+" vs "+abbreviate(tt.y), func(t *testing.T) {
			got, err := compareDecimals(tt.x, tt.y)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			got, err = compareDecimals(tt.y, tt.x)
			require.NoError(t, err)
			require.Equal(t, -tt.want, got)
		})
	}

	// 指数が大きすぎる表記は比べない。長い表記はエラーメッセージで縮める。
	for _, x := range []string{"1e4611686018427387905", "1e-4611686018427387905", "1e99999999999999999999"} {
		_, err := compareDecimals(x, "1")
		require.ErrorContains(t, err, "the exponent of the number "+x+" is too large to compare")
		_, err = compareDecimals("1", x)
		require.Error(t, err)
	}
	_, err := compareDecimals(strings.Repeat("9", 100)+"e99999999999999999999", "1")
	require.ErrorContains(t, err, "the number "+strings.Repeat("9", 32)+"... is too large")
}

// 重複キーは後の値を採り、不正な UTF-8 は置換文字になる（JSON として読めるものは通す）。
func TestSearch_LenientInput(t *testing.T) {
	require.Equal(t, `{"a":3,"b":2}`, search(t, "@", `{"a":1,"b":2,"a":3}`))
	require.Equal(t, `"`+string(rune(0xFFFD))+`"`, search(t, "s", "{\"s\":\"\xff\"}"))
	// エスケープされた文字は、エスケープなしで返る。
	require.Equal(t, `"<&>"`, search(t, "s", `{"s":"`+`\`+`u003c&`+`\`+`u003e"}`))
}

func TestCompile_Errors(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		wantErr    string
	}{
		{"empty", "", "expression is empty"},
		{"only whitespace", " \n\t", "expression is empty"},
		{"too long", strings.Repeat("a", MaxExpressionBytes+1), "expression is too long"},
		{"nested too deeply", strings.Repeat("(", 300) + "a" + strings.Repeat(")", 300),
			"nested too deeply"},
		{
			"trailing dot",
			"items.",
			"expected a name, '*', '[' or '{' after '.', got end of expression",
		},
		{"current node after a dot", "items[0].@", `after '.', got "@" at offset 9`},
		{"unclosed bracket", "items[0", `unexpected end of expression`},
		{"unclosed filter", "items[?a == `1`", "expected ']', got end of expression"},
		{"unclosed multiselect hash", "{a: b", "expected ',' or '}', got end of expression"},
		{"hash without a key", "{`1`: b}", "expected a key name"},
		// フィールドの一覧の表記（items[]{id, name}）をそのまま式に書いた場合は、書き方を示す。
		{
			"field list notation as an expression",
			"items[]{id, name}",
			`unexpected "{" (to select several fields, write .{id: id, name: name}) at offset 7`,
		},
		{"field list notation for an object", "owner{login}", `unexpected "{" (to select`},
		{
			"hash of bare names",
			"items[].{id, name}",
			`expected ':' after the key "id", got "," (write each field as name: expression, ` +
				`e.g. {id: id, name: name}) at offset 11`,
		},
		{
			"hash ending in a bare name",
			"{id: id, name}",
			`expected ':' after the key "name", got "}"`,
		},
		{"single equals", "items[?a = `1`]", "unexpected '=' (use '==' to compare) at offset 9"},
		{"unterminated raw string", "items[?a == 'x]", "unterminated '...'"},
		{"unterminated literal", "`1", "unterminated `...`"},
		{"unterminated quoted identifier", `"abc`, `unterminated "..."`},
		{"non-ASCII bare identifier", "items[].名前", `unexpected character '名'`},
		{"hyphen in a bare identifier", "meta.x-rate", "unexpected '-'"},
		{"two expressions", "a b", `unexpected "b" at offset 2`},
		{"stray closing bracket", "a]", `unexpected "]"`},
		{"slice with four parts", "a[1:2:3:4]", "a slice has at most three parts"},
		{"slice step of zero", "a[::0]", "slice step cannot be 0 at offset 4"},
		{"number out of range", "a[99999999999999999999]", "number is out of range"},

		// 関数（値を計算するもの）には対応しない。
		{
			"aggregate function",
			"length(items)",
			"functions are not supported (length()): an expression can only select and filter at offset 0",
		},
		{
			"function in a filter",
			"items[?contains(title, 'bug')]",
			"functions are not supported (contains()): an expression can only select and filter at offset 7",
		},
		{"function after a dot", "items.keys(@)", "functions are not supported (keys())"},
		{
			"function after a pipe",
			"items | sort_by(@, &id)",
			"functions are not supported (sort_by())",
		},
		{"unknown function", "nope()", "functions are not supported (nope())"},
		{
			"expression reference",
			"&id",
			"unexpected '&' (expression references are only for functions, which are not supported; " +
				"use '&&' for a logical and) at offset 0",
		},
		{"quoted function name", `"length"(@)`, "unexpected '(' at offset 8"},
		{"calling a non-function", "a.b[0](c)", "unexpected '(' at offset 6"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := compileAll(tt.expression)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// 条件で絞る構文と、位置・件数で絞る構文は、それぞれ有効にしたときだけ使える。値を選ぶ
// 構文は常に使える。
func TestCompile_Features(t *testing.T) {
	const (
		conditionsOff = "filtering by a condition ([?...] with comparison and logical operators) " +
			"is not enabled"
		indexingOff = "indexes and slices ([0], [:5]) are not enabled"
	)
	tests := []struct {
		name       string
		expression string
		// needs は、この式に必要な構文。
		needsConditions bool
		needsIndexing   bool
		// wantErr は、必要な構文を無効にしたときのエラー。
		wantErr string
	}{
		{name: "field", expression: "items"},
		{name: "nested fields", expression: "a.b.c"},
		{name: "list projection", expression: "items[*].id"},
		{name: "flatten", expression: "items[].labels[].name"},
		{name: "object projection", expression: "by_id.*.name"},
		{name: "multiselect hash", expression: "items[].{id: id, author: user.login}"},
		{name: "multiselect list", expression: "[total, items[].id]"},
		{name: "pipe", expression: "items[].id | @"},
		{name: "literal", expression: "{id: id, source: 'api'}"},
		{name: "quoted identifier", expression: `meta."x-rate"`},

		{name: "filter", expression: "items[?open]", needsConditions: true,
			wantErr: `"[?" cannot be used: ` + conditionsOff + " at offset 5"},
		{name: "filter at the start", expression: "[?open]", needsConditions: true,
			wantErr: `"[?" cannot be used: ` + conditionsOff + " at offset 0"},
		{name: "comparison", expression: "a == b", needsConditions: true,
			wantErr: `"==" cannot be used: ` + conditionsOff + " at offset 2"},
		{name: "ordering", expression: "a >= `1`", needsConditions: true,
			wantErr: `">=" cannot be used: ` + conditionsOff},
		{name: "or", expression: "a || b", needsConditions: true,
			wantErr: `"||" cannot be used: ` + conditionsOff},
		{name: "and", expression: "a && b", needsConditions: true,
			wantErr: `"&&" cannot be used: ` + conditionsOff},
		{name: "not", expression: "!a", needsConditions: true,
			wantErr: `"!" cannot be used: ` + conditionsOff + " at offset 0"},

		{name: "index", expression: "items[0]", needsIndexing: true,
			wantErr: indexingOff + " at offset 5"},
		{name: "negative index", expression: "items[-1].id", needsIndexing: true,
			wantErr: indexingOff},
		{name: "index at the start", expression: "[0]", needsIndexing: true,
			wantErr: indexingOff + " at offset 0"},
		{name: "slice", expression: "items[:5]", needsIndexing: true, wantErr: indexingOff},
		{name: "slice with a step", expression: "items[::2].id", needsIndexing: true,
			wantErr: indexingOff},
		{name: "index after a projection", expression: "items[*].tags[0]", needsIndexing: true,
			wantErr: indexingOff + " at offset 13"},

		{name: "both", expression: "items[?open][:5].id", needsConditions: true,
			needsIndexing: true, wantErr: conditionsOff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 必要な構文だけを有効にすれば通る。
			_, err := Compile(tt.expression, Features{
				Conditions: tt.needsConditions, Indexing: tt.needsIndexing,
			})
			require.NoError(t, err)

			_, err = Compile(tt.expression, Features{})
			if !tt.needsConditions && !tt.needsIndexing {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
			var syntaxErr *SyntaxError
			require.ErrorAs(t, err, &syntaxErr)
		})
	}

	// 片方だけ有効な場合、もう片方の構文は使えない。
	_, err := Compile("items[?open][:5].id", Features{Conditions: true})
	require.ErrorContains(t, err, indexingOff+" at offset 12")
	_, err = Compile("items[:5][?open].id", Features{Indexing: true})
	require.ErrorContains(t, err, conditionsOff+" at offset 9")

	// 関数は、どの構文を有効にしても使えない。
	_, err = Compile("length(items)", Features{Conditions: true, Indexing: true})
	require.ErrorContains(t, err, "functions are not supported")
}

func TestCompile_SyntaxErrorCarriesTheOffset(t *testing.T) {
	_, err := compileAll("items[?a = `1`]")
	var syntaxErr *SyntaxError
	require.ErrorAs(t, err, &syntaxErr)
	require.Equal(t, 9, syntaxErr.Offset)
}

// 構文解析が刻み 0 を弾くが、評価側でも止まる（無限ループにしない）。
func TestInterp_SliceStepZero(t *testing.T) {
	zero := 0
	_, err := (&interp{limit: 100}).slice([]any{1}, [3]*int{nil, nil, &zero})
	require.ErrorContains(t, err, "slice step cannot be 0")
}

// 短い式で結果を指数的に膨らませても、ドキュメントの大きさに比例した作業量で止まる。
// 既存の Go 実装では、最初の式（321 バイト）は数十 GiB のメモリを確保しようとする。
func TestSearch_StopsExpensiveExpressions(t *testing.T) {
	d, err := Decode([]byte(`["x"]`))
	require.NoError(t, err)
	doubling := "@" + strings.Repeat("|[@,@]", 60)
	for _, expression := range []string{
		"@" + strings.Repeat("|[@,@][]", 40),
		doubling,
		doubling + "|@ == @",
		doubling + "|[?@ == @]",
		"@" + strings.Repeat("|[@,@]", 30) + strings.Repeat("[]", 30),
		"@" + strings.Repeat("|{a:@,b:@}", 60),
		"@" + strings.Repeat("|{a:@,b:@}.*", 40),
	} {
		q, err := compileAll(expression)
		require.NoError(t, err)
		start := time.Now()
		_, err = q.Search(d)
		require.Error(t, err, expression)
		require.True(t, errors.Is(err, ErrTooExpensive) || errors.Is(err, errResultTooLarge),
			"unexpected error %v for %s", err, expression)
		require.Less(t, time.Since(start), 5*time.Second, expression)
	}
}

// 長い文字列どうしの比較も作業量に数える（同じ比較を式に何百回も並べられるため）。
func TestSearch_ChargesStringComparisons(t *testing.T) {
	long := strings.Repeat("x", 8<<20)
	d, err := Decode([]byte(`{"a":"` + long + `","b":"` + long + `"}`))
	require.NoError(t, err)

	// 1 回なら通る。
	for _, expression := range []string{"a == b", "a <= b"} {
		q, err := compileAll(expression)
		require.NoError(t, err)
		out, err := q.Search(d)
		require.NoError(t, err)
		require.Equal(t, "true", string(out))
	}
	for _, comparison := range []string{"a == b", "a <= b"} {
		q, err := compileAll(
			"[" + strings.TrimSuffix(strings.Repeat(comparison+",", 300), ",") + "]",
		)
		require.NoError(t, err)
		_, err = q.Search(d)
		require.ErrorIs(t, err, ErrTooExpensive, comparison)
	}
}

// 上限はドキュメントの大きさに比例するので、大きなドキュメントへの普通の式は通る。
func TestSearch_LargeDocumentWithinBudget(t *testing.T) {
	var doc strings.Builder
	doc.WriteString(`{"items":[`)
	const n = 20000
	for i := range n {
		if i > 0 {
			doc.WriteString(",")
		}
		doc.WriteString(`{"id":1,"state":"open","labels":[{"name":"bug"},{"name":"p1"}],"body":"`)
		doc.WriteString(strings.Repeat("x", 100))
		doc.WriteString(`"}`)
	}
	doc.WriteString(`]}`)

	got := search(t,
		"items[?state == 'open' && labels[?name == 'bug']].{id: id, labels: labels[].name}"+
			" | [[0], [-1]]",
		doc.String())
	require.Equal(t, `[{"id":1,"labels":["bug","p1"]},{"id":1,"labels":["bug","p1"]}]`, got)
}

// 結果が元のドキュメントより resultSlack を超えて大きくなる式は止める。上限を超えると
// 分かっている文字列は、引用する前に断る。
func TestSearch_ResultSizeLimit(t *testing.T) {
	big := `"` + strings.Repeat("x", 40<<10) + `"`
	d, err := Decode([]byte(big))
	require.NoError(t, err)

	q, err := compileAll("[@, @]")
	require.NoError(t, err)
	out, err := q.Search(d)
	require.NoError(t, err)
	require.Len(t, out, 2*len(big)+3)

	q, err = compileAll("[@, @, @, @]")
	require.NoError(t, err)
	_, err = q.Search(d)
	require.ErrorIs(t, err, errResultTooLarge)
}

func TestAppendJSON_Limit(t *testing.T) {
	obj := &object{}
	obj.set("key", "value")
	const encoded = `{"key":"value"}`

	// 上限ちょうどは通る。
	out, err := appendJSON(nil, obj, len(encoded))
	require.NoError(t, err)
	require.Equal(t, encoded, string(out))

	for limit := range len(encoded) {
		_, err := appendJSON(nil, obj, limit)
		require.ErrorIs(t, err, errResultTooLarge, "limit %d", limit)
	}

	// 上限を超える文字列やキーは、バッファへ書く前に断る。
	long := strings.Repeat("x", 1<<20)
	out, err = appendJSON(make([]byte, 0, 16), long, 100)
	require.ErrorIs(t, err, errResultTooLarge)
	require.Empty(t, out)
	longKey := &object{}
	longKey.set(long, 1)
	out, err = appendJSON(make([]byte, 0, 16), longKey, 100)
	require.ErrorIs(t, err, errResultTooLarge)
	require.Equal(t, "{", string(out))
}

func TestDecode_Errors(t *testing.T) {
	for _, doc := range []string{``, `{"a":1`, `{"a":1,}`, `{"a":1} x`, `{"a":1}{"b":2}`, `{a:1}`, `nope`} {
		t.Run(doc, func(t *testing.T) {
			_, err := Decode([]byte(doc))
			require.Error(t, err)
		})
	}
}

func TestDocument_Describe(t *testing.T) {
	manyKeys := "{"
	for i := range maxDescribedKeys + 3 {
		if i > 0 {
			manyKeys += ","
		}
		manyKeys += `"k` + strings.Repeat("x", i) + `":1`
	}
	manyKeys += "}"
	longKey := strings.Repeat("k", maxDescribedKeyBytes+1)

	tests := []struct {
		name string
		doc  string
		want string
	}{
		{"object", `{"items":[],"total":0}`, "an object with keys: items, total"},
		{"empty object", `{}`, "an object with no keys"},
		{"array of objects", `[{"id":1,"name":"a"},{"id":2}]`,
			"an array of 2 elements; its first element is an object with keys: id, name"},
		{"array of scalars", `["a","b","c"]`,
			"an array of 3 elements; its first element is of type string"},
		{"empty array", `[]`, "an array of 0 elements"},
		{"scalar", `"text"`, "a value of type string"},
		// 識別子として書けないキーは、式に書く形（引用符つき）で示す。"a, b" は 1 つのキー。
		{"keys that need quoting", `{"a, b":1,"x-rate":2,"ok_1":3,"1st":4,"":5,"q\"q":6}`,
			`an object with keys: "a, b", "x-rate", ok_1, "1st", "", "q\"q"`},
		{"more keys than the limit", manyKeys, ", ... (3 more)"},
		// 長すぎるキーは挙げずに数える。
		{
			"a very long key",
			`{"id":1,"` + longKey + `":2}`,
			"an object with keys: id, ... (1 more)",
		},
		{"only very long keys", `{"` + longKey + `":1}`, "an object with 1 keys"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Decode([]byte(tt.doc))
			require.NoError(t, err)
			require.Contains(t, d.Describe(), tt.want)
			require.NotContains(t, d.Describe(), longKey)
		})
	}
}

func TestEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"same text", `{"a":1}`, `{"a":1}`, true},
		{"key order and whitespace", `{"a":1,"b":[1,2]}`, `{ "b": [1, 2], "a": 1 }`, true},
		{"number notation", `[1, 100, 1.10]`, `[1.0, 1e2, 1.1]`, true},
		{"different value", `{"a":1}`, `{"a":2}`, false},
		{"different type", `[1]`, `["1"]`, false},
		{"different length", `[1,2]`, `[1,2,3]`, false},
		{"missing key", `{"a":1,"b":2}`, `{"a":1,"c":2}`, false},
		{"large integers", `[9007199254740993]`, `[9007199254740992]`, false},
		{"scalars", `null`, `null`, true},
		{"null and false", `null`, `false`, false},
		{"not JSON", `{"a":1}`, `nope`, false},
		{"neither is JSON", `nope`, `nope`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, Equal([]byte(tt.a), []byte(tt.b)))
			require.Equal(t, tt.want, Equal([]byte(tt.b), []byte(tt.a)))
		})
	}
}

// Query は複数のゴルーチンから同時に使える。
func TestQuery_ConcurrentSearch(t *testing.T) {
	q, err := compileAll("items[?id > `1`].{id: id, n: name}")
	require.NoError(t, err)
	done := make(chan string, 8)
	for range cap(done) {
		go func() {
			d, err := Decode([]byte(`{"items":[{"id":1,"name":"a"},{"id":2,"name":"bb"}]}`))
			if err != nil {
				done <- err.Error()
				return
			}
			out, err := q.Search(d)
			if err != nil {
				done <- err.Error()
				return
			}
			done <- string(out)
		}()
	}
	for range cap(done) {
		require.Equal(t, `[{"id":2,"n":"bb"}]`, <-done)
	}
}

// どんな式でも panic せず、結果は JSON になる。
func FuzzSearch(f *testing.F) {
	for _, seed := range []string{
		"items[].id", "items[?id > `1`].{id: id, n: name}", "items[0]", "items[:1]",
		"m.*.v", "items[*].tags[]", "items[::-1]", "items[::0]", "a.b.c || `1`", "!items",
		"@|[@,@][]|[@,@][]", "`{\"a\":[1,2]}`.a[::-1]", "'\\''", "名前", "length(items)",
		"items[?n >= `1.5` && name != 'b']", "[items[0].big, total]", "items[?owner].owner.login",
	} {
		f.Add(seed)
	}
	const doc = `{"items":[{"id":1,"name":"a","tags":["x","y"],"owner":{"login":"alice"},"n":1.5,` +
		`"big":12345678901234567890},{"id":2,"name":"b","tags":[],"owner":null,"n":-3}],` +
		`"total":2,"s":"str","m":{"k1":{"v":1},"k2":{"v":2}},"e":[],"z":null,"b":true}`
	d, err := Decode([]byte(doc))
	require.NoError(f, err)
	f.Fuzz(func(t *testing.T, expression string) {
		q, err := compileAll(expression)
		if err != nil {
			require.NotContains(t, err.Error(), "internal error")
			return
		}
		out, err := q.Search(d)
		if err != nil {
			require.NotContains(t, err.Error(), "internal error")
			return
		}
		_, err = Decode(out)
		require.NoError(t, err, "the result is not JSON: %s", out)
	})
}
