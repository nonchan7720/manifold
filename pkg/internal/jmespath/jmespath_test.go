package jmespath

import (
	"encoding/json"
	"encoding/json/jsontext"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func search(t *testing.T, expression, doc string) string {
	t.Helper()
	d, err := Decode([]byte(doc))
	require.NoError(t, err)
	q, err := Compile(expression)
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
			q, err := Compile(c.Expression)
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

		// 文字列に対する contains は、探す値が文字列でなければ false。
		{"contains with a non-string search", "contains(s, `1`)", `false`},

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
		{"a function after a dot", "foo.keys(@)", `["a","b"]`},
		{"flatten stops at one level", "list[]", `[{"v":1},{"v":2},{"v":3}]`},
		{"a negative step", "nums[4:1:-1]", `[4,3,2]`},
		{"a step larger than the array", "nums[::100]", `[0]`},
		{"a huge negative step", "nums[::-9223372036854775808]", `[5]`},
		{"an out of range slice", "nums[10:20]", `[]`},
		{"quoted identifiers", `obj."k"`, `"v"`},
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

	require.Equal(t, `["zeta","alpha","ids"]`, search(t, "keys(@)", doc))
	require.Equal(
		t,
		`{"id":12345678901234567890,"price":1.10,"exp":1E2,"tiny":0.1000000000000000055511151231257827,"neg":-0}`,
		search(t, "alpha", doc),
	)
	// 複数選択は式に書いた順。
	require.Equal(t, `{"p":1.10,"i":12345678901234567890}`,
		search(t, "alpha.{p: price, i: id}", doc))
	require.Equal(t, `"12345678901234567890"`, search(t, "to_string(alpha.id)", doc))
	require.Equal(t, `9007199254740993`, search(t, "max(ids)", doc))

	// float64 では同じ値になる整数も、桁が違えば等しくない。
	require.Equal(t, `[9007199254740993]`, search(t, "ids[?@ == `9007199254740993`]", doc))
	require.Equal(t, `[9007199254740992]`, search(t, "ids[?@ == `9007199254740992`]", doc))
	require.Equal(t, `true`, search(t, "alpha.id == `12345678901234567890`", doc))
	require.Equal(t, `false`, search(t, "alpha.id == `12345678901234567891`", doc))
	// 表記が違っても値が同じなら等しい。
	require.Equal(t, `true`, search(t, "alpha.exp == `100` && alpha.price == `1.1`", doc))
	require.Equal(t, `true`, search(t, "alpha.neg == `0`", doc))

	// 計算で作った数値は最短の表記になる。
	require.Equal(t, `[3,2.5,0.1,3,-1]`,
		search(t, "[length(@), avg(@), `0.1`, to_number('3'), floor(`-0.5`)]", `[1, 2, 4.5]`))
	require.Equal(t, `[12345678901234567890,1.50,null]`,
		search(t, "[to_number(a), to_number(b), to_number(c)]",
			`{"a": "12345678901234567890", "b": " 1.50 ", "c": "0x10"}`))
}

// 重複キーは後の値を採り、不正な UTF-8 は置換文字になる（JSON として読めるものは通す）。
func TestSearch_LenientInput(t *testing.T) {
	require.Equal(t, `{"a":3,"b":2}`, search(t, "@", `{"a":1,"b":2,"a":3}`))
	require.Equal(t, "\"\uFFFD\"", search(t, "s", "{\"s\":\"\xff\"}"))
	// エスケープされた文字は、エスケープなしで返る。
	require.Equal(t, `"<&>"`, search(t, "s", `{"s":"\u003c&\u003e"}`))
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
		{"single equals", "items[?a = `1`]", "unexpected '=' (use '==' to compare) at offset 9"},
		{"unterminated raw string", "items[?a == 'x]", "unterminated '...'"},
		{"unterminated literal", "`1", "unterminated `...`"},
		{"unterminated quoted identifier", `"abc`, `unterminated "..."`},
		{"non-ASCII bare identifier", "items[].名前", `unexpected character '名'`},
		{"hyphen in a bare identifier", "meta.x-rate", "unexpected '-'"},
		{"two expressions", "a b", `unexpected "b" at offset 2`},
		{"stray closing bracket", "a]", `unexpected "]"`},
		{"slice with four parts", "a[1:2:3:4]", "a slice has at most three parts"},
		{"quoted function name", `"length"(@)`, "a quoted identifier cannot be a function name"},
		{"calling a non-function", "a.b[0](c)", "only a function name can be called"},
		{"unknown function", "items[?foo(@)]", "unknown function foo() at offset 7"},
		{"too few arguments", "length()", "length() takes 1 argument(s), got 0"},
		{"too many arguments", "length(a, b)", "length() takes 1 argument(s), got 2"},
		{"variadic needs one argument", "merge()", "merge() takes at least 1 argument(s), got 0"},
		{"trailing comma in arguments", "not_null(a,)", `unexpected ")"`},
		{"number out of range", "a[99999999999999999999]", "number is out of range"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Compile(tt.expression)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestCompile_SyntaxErrorCarriesTheOffset(t *testing.T) {
	_, err := Compile("items[?a = `1`]")
	var syntaxErr *SyntaxError
	require.ErrorAs(t, err, &syntaxErr)
	require.Equal(t, 9, syntaxErr.Offset)
}

func TestSearch_Errors(t *testing.T) {
	const doc = `{"nums": [1, 2], "mixed": [1, "a"], "s": "x", "objs": [{"k": 1}, {"k": "a"}, {"k": null}]}`
	tests := []struct {
		name       string
		expression string
		wantErr    string
	}{
		{"wrong argument type", "length(nums[0])",
			"length(): argument 1 must be a string, an array or an object, got number"},
		{"null argument", "keys(missing)", "keys(): argument 1 must be an object, got null"},
		{"mixed array", "sort(mixed)",
			"sort(): argument 1 must be an array of numbers or an array of strings, " +
				"got an array of mixed types"},
		{"array of the wrong type", "sum(objs)",
			"sum(): argument 1 must be an array of numbers, got an array of objects"},
		{"join needs strings", "join(',', nums)",
			"join(): argument 2 must be an array of strings, got an array of numbers"},
		{"expression reference expected", "sort_by(nums, s)",
			"sort_by(): argument 2 must be an expression reference (&expr), got string"},
		{"sort_by with mixed keys", "sort_by(objs, &k)",
			"sort_by(): the expression must return a number for every element, or a string " +
				"for every element (got number and string)"},
		{"max_by with a null key", "max_by(objs[2:], &k)", "(got null)"},
		{"merge needs objects", "merge(nums)", "merge(): argument 1 must be an object"},
		{"slice step of zero", "nums[::0]", "slice step cannot be 0"},
		{"an expression reference is not a value", "&nums", "returns an expression, not a value"},
	}
	d, err := Decode([]byte(doc))
	require.NoError(t, err)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := Compile(tt.expression)
			require.NoError(t, err)
			_, err = q.Search(d)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
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
		doubling + "|to_string(@)",
		doubling + "|contains([@], @)",
		doubling + "|sort_by(@, &to_string(@))",
		"@" + strings.Repeat("|[@,@]", 30) + strings.Repeat("[]", 30),
		"[0]" + strings.Repeat("|join('',[@,@])", 60),
		"[0]" + strings.Repeat("|to_string([@,@])", 40),
		"@" + strings.Repeat("|merge({a:@},{b:@})", 60),
	} {
		q, err := Compile(expression)
		require.NoError(t, err)
		start := time.Now()
		_, err = q.Search(d)
		require.Error(t, err, expression)
		require.True(
			t,
			err == ErrTooExpensive || err == errResultTooLarge, //nolint:errorlint // 番兵エラーそのもの
			"unexpected error %v for %s",
			err,
			expression,
		)
		require.Less(t, time.Since(start), 5*time.Second, expression)
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
		"length(sort_by(items[?state == 'open' && contains(labels[].name, 'bug')], &id)"+
			"[].{id: id, labels: labels[].name, n: length(body)})",
		doc.String())
	require.Equal(t, "20000", got)
}

// 結果が元のドキュメントより resultSlack を超えて大きくなる式は止める。
func TestSearch_ResultSizeLimit(t *testing.T) {
	big := `"` + strings.Repeat("x", 40<<10) + `"`
	d, err := Decode([]byte(big))
	require.NoError(t, err)

	q, err := Compile("[@, @]")
	require.NoError(t, err)
	out, err := q.Search(d)
	require.NoError(t, err)
	require.Len(t, out, 2*len(big)+3)

	q, err = Compile("[@, @, @, @]")
	require.NoError(t, err)
	_, err = q.Search(d)
	require.ErrorIs(t, err, errResultTooLarge)
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Decode([]byte(tt.doc))
			require.NoError(t, err)
			require.Equal(t, tt.want, d.Describe())
		})
	}

	d, err := Decode([]byte(manyKeys))
	require.NoError(t, err)
	require.Contains(t, d.Describe(), ", ... (3 more)")
}

// Query は複数のゴルーチンから同時に使える。
func TestQuery_ConcurrentSearch(t *testing.T) {
	q, err := Compile("items[?id > `1`].{id: id, n: length(name)}")
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
		require.Equal(t, `[{"id":2,"n":2}]`, <-done)
	}
}

// どんな式でも panic せず、結果は JSON になる。
func FuzzSearch(f *testing.F) {
	for _, seed := range []string{
		"items[].id", "items[?id > `1`].{id: id, n: name}", "length(items)", "keys(@)",
		"items[:1]", "sort_by(items, &name)[].name", "m.*.v", "items[*].tags[]",
		"join(', ', items[].name)", "map(&id, items)", "to_string(@)", "items[::-1]",
		"items[::0]", "a.b.c || `1`", "!items", "merge(m.k1, m.k2)", "@|[@,@][]|[@,@][]",
		"`{\"a\":[1,2]}`.a[::-1]", "'\\''", "名前", "merge(A0)", "max_by(items, &owner)",
	} {
		f.Add(seed)
	}
	const doc = `{"items":[{"id":1,"name":"a","tags":["x","y"],"owner":{"login":"alice"},"n":1.5},` +
		`{"id":2,"name":"b","tags":[],"owner":null,"n":-3}],"total":2,"s":"str",` +
		`"m":{"k1":{"v":1},"k2":{"v":2}},"e":[],"z":null,"b":true}`
	d, err := Decode([]byte(doc))
	require.NoError(f, err)
	f.Fuzz(func(t *testing.T, expression string) {
		q, err := Compile(expression)
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
