package mcpsrv

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildA2UIExpressForm(t *testing.T) {
	fields, ok := buildA2UIFields(a2uiTestSchema())
	require.True(t, ok)
	spec := a2uiFormSpec{
		surfaceID:   "manifold-form-createorder-ABCD1234",
		title:       "createorder",
		description: "Create an order",
		submitTool:  "createorder__submit",
		fields:      fields,
	}
	data := a2uiInitialData(fields, map[string]any{
		"orderId": "o-1",
		"body":    map[string]any{"title": "desk", "priority": "high", "tags": []any{"gift"}},
	})
	block, err := buildA2UIExpressForm(spec, data)
	require.NoError(t, err)

	// 決定的であること。
	again, err := buildA2UIExpressForm(spec, data)
	require.NoError(t, err)
	require.Equal(t, block, again)

	lines := strings.Split(block, "\n")
	require.Equal(t, "<a2ui>", lines[0])
	require.Equal(t, "</a2ui>", lines[len(lines)-1])
	require.Equal(t, `surface("manifold-form-createorder-ABCD1234")`, lines[1])

	for _, want := range []string{
		// 初期値は葉ごとのデータパスへの代入。
		`$/input/orderId = "o-1"`,
		`$/input/body/title = "desk"`,
		`$/input/body/quantity = "1"`,
		`$/input/body/priority = ["high"]`,
		`$/input/body/tags = ["gift"]`,
		`$/input/body/urgent = false`,
		`$/input/body/a_b_c = ""`,
		// コンポーネントはキーワード引数、チェックは値を明示した位置引数。
		`text_1 = Text(text="### createorder")`,
		`text_2 = Text(text="Create an order", variant="caption")`,
		`field_3 = TextField([?required($/input/orderId, "orderId is required")], ` +
			`label="orderId *", value=$/input/orderId)`,
		`field_6 = TextField([?required($/input/body/title, "title is required"), ` +
			`?length($/input/body/title, 1, 40, ` +
			`"title must be at most 40 characters and at least 1"), ` +
			`?regex($/input/body/title, "^[a-z]+$", "title has an invalid format")], ` +
			`label="title *", value=$/input/body/title)`,
		// バックスラッシュを含む正規表現は raw 文字列。
		`field_12 = TextField([?regex($/input/body/price, ` +
			`r"^(-?[0-9]+(\.[0-9]+)?([eE][-+]?[0-9]+)?)?$", "price must be a number")], ` +
			`label="price", value=$/input/body/price, variant="number")`,
		`field_13 = ChoicePicker(label="priority", ` +
			`options=[{label: "low", value: "low"}, {label: "high", value: "high"}], ` +
			`value=$/input/body/priority, variant="mutuallyExclusive")`,
		`field_10 = DateTimeInput(enableDate=true, label="dueDate", value=$/input/body/dueDate)`,
		`field_16 = CheckBox(label="urgent", value=$/input/body/urgent)`,
		`root = Card(child=column_19)`,
	} {
		require.Contains(t, lines, want)
	}

	var submit string
	for _, line := range lines {
		if strings.HasPrefix(line, "submit_") {
			submit = line
		}
	}
	// 送信ボタンはイベントを発火する。未入力の必須項目があれば押せない。
	require.Equal(t, `submit_18 = Button([`+
		`?required($/input/orderId, "orderId is required"), `+
		`?required($/input/body/title, "title is required"), `+
		`?required($/input/body/quantity, "quantity is required")], `+
		`action=Event("createorder__submit", {body: $/input/body, orderId: $/input/orderId}), `+
		`child=text_17, variant="primary")`, submit)

	// 各コンポーネントは 1 行で、変数名は識別子。
	for _, line := range lines[2 : len(lines)-1] {
		require.Regexp(t, `^(\$/[A-Za-z0-9_/]+|[A-Za-z_][A-Za-z0-9_]*) = `, line)
	}
}

func TestBuildA2UIExpressForm_NoFields(t *testing.T) {
	block, err := buildA2UIExpressForm(a2uiFormSpec{
		surfaceID: "s", title: "deleteall", submitTool: "deleteall__submit",
	}, a2uiInitialData(nil, nil))
	require.NoError(t, err)
	// 入力の無いツールは確認ダイアログになる。空のデータモデルは代入しない。
	require.NotContains(t, block, "$/input")
	require.Contains(t, block, `action=Event("deleteall__submit", {})`)
}

func TestExpressString(t *testing.T) {
	for in, want := range map[string]string{
		`plain`:            `"plain"`,
		`say "hi"`:         `"say \"hi\""`,
		"line1\nline2\t!":  `"line1\nline2\t!"`,
		`^\d{3}$`:          `r"^\d{3}$"`,
		`a\b "c"`:          `"a\\b \"c\""`,
		`close </a2ui> me`: `"close < /a2ui> me"`,
		`open <A2UI> me`:   `"open < A2UI> me"`,
		``:                 `""`,
	} {
		require.Equal(t, want, expressString(in), "input %q", in)
	}
}

func TestExpressKey(t *testing.T) {
	for in, want := range map[string]string{
		"body":         "body",
		"_x1":          "_x1",
		"X-Request-Id": `"X-Request-Id"`,
		"1st":          `"1st"`,
		"true":         `"true"`,
		"_":            `"_"`,
		"":             `""`,
	} {
		require.Equal(t, want, expressKey(in), "input %q", in)
	}
}

func TestExpressValue_Unsupported(t *testing.T) {
	_, err := expressValue(struct{}{}, false)
	require.Error(t, err)
	_, err = expressCheck(map[string]any{
		"condition": map[string]any{"@call": "email", "args": map[string]any{}},
	})
	require.Error(t, err)
	_, err = expressComponent("not a component")
	require.Error(t, err)
}
