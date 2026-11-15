package mcpsrv

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func a2uiTestSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"orderId", "body"},
		"properties": map[string]any{
			"orderId": map[string]any{"type": "string", "description": "order id"},
			"body": map[string]any{
				"type":     "object",
				"required": []any{"title", "quantity"},
				"properties": map[string]any{
					"title": map[string]any{
						"type": "string", "minLength": float64(1), "maxLength": float64(40),
						"pattern": "^[a-z]+$",
					},
					"quantity": map[string]any{"type": "integer", "default": float64(1)},
					"price":    map[string]any{"type": "number"},
					"priority": map[string]any{"type": "string", "enum": []any{"low", "high"}},
					"urgent":   map[string]any{"type": "boolean"},
					"dueDate":  map[string]any{"type": "string", "format": "date"},
					"tags": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string", "enum": []any{"gift", "fragile"}},
					},
					"extra":  map[string]any{"type": "object"},
					"a/b~c":  map[string]any{"type": "string"},
					"secret": map[string]any{"type": "string", "format": "password"},
					"any":    map[string]any{},
				},
			},
		},
	}
}

// componentsByLabel は updateComponents のコンポーネントを label で引けるようにする。
func a2uiTemplateComponents(t *testing.T, template []any) (all []map[string]any) {
	t.Helper()
	require.Len(t, template, 2)
	update := template[1].(map[string]any)["updateComponents"].(map[string]any)
	for _, c := range update["components"].([]any) {
		all = append(all, c.(map[string]any))
	}
	return all
}

func a2uiComponentByLabel(t *testing.T, comps []map[string]any, label string) map[string]any {
	t.Helper()
	for _, c := range comps {
		if c["label"] == label {
			return c
		}
	}
	require.Failf(t, "component not found", "label %q", label)
	return nil
}

func TestBuildA2UIFormTemplate(t *testing.T) {
	fields, ok := buildA2UIFields(a2uiTestSchema())
	require.True(t, ok)
	template := buildA2UIFormTemplate(a2uiFormSpec{
		surfaceID:  "manifold-form-createorder",
		title:      "createorder",
		submitTool: "createorder__submit",
		fields:     fields,
	})

	// JSON に直せて、決定的であること。
	first, err := json.Marshal(template)
	require.NoError(t, err)
	second, err := json.Marshal(buildA2UIFormTemplate(a2uiFormSpec{
		surfaceID:  "manifold-form-createorder",
		title:      "createorder",
		submitTool: "createorder__submit",
		fields:     fields,
	}))
	require.NoError(t, err)
	require.JSONEq(t, string(first), string(second))

	create := template[0].(map[string]any)
	require.Equal(t, "1.0", create["version"])
	require.Equal(t, map[string]any{
		"surfaceId": "manifold-form-createorder",
		"catalogId": a2uiBasicCatalogID,
	}, create["createSurface"])

	comps := a2uiTemplateComponents(t, template)
	ids := map[string]bool{}
	for _, c := range comps {
		id := c["id"].(string)
		require.False(t, ids[id], "duplicate component id %q", id)
		ids[id] = true
		// コンポーネント ID は UAX #31 の識別子（英数字とアンダースコア）。
		require.Regexp(t, `^[A-Za-z_][A-Za-z0-9_]*$`, id)
	}
	require.True(t, ids["root"])
	// 参照している子がすべて定義されていること。
	for _, c := range comps {
		if child, ok := c["child"].(string); ok {
			require.True(t, ids[child], "undefined child %q", child)
		}
		if children, ok := c["children"].([]any); ok {
			for _, child := range children {
				require.True(t, ids[child.(string)], "undefined child %v", child)
			}
		}
	}

	title := a2uiComponentByLabel(t, comps, "title *")
	require.Equal(t, "TextField", title["component"])
	require.Equal(t, map[string]any{"@path": "/input/body/title"}, title["value"])
	checks := title["@checks"].([]any)
	require.Len(t, checks, 3) // required, length, regex
	require.Equal(t, "required",
		checks[0].(map[string]any)["condition"].(map[string]any)["@call"])
	length := checks[1].(map[string]any)["condition"].(map[string]any)
	require.Equal(t, "length", length["@call"])
	require.Equal(t, 40, length["args"].(map[string]any)["max"])
	require.Equal(t, 1, length["args"].(map[string]any)["min"])

	quantity := a2uiComponentByLabel(t, comps, "quantity *")
	require.Equal(t, "number", quantity["variant"])

	priority := a2uiComponentByLabel(t, comps, "priority")
	require.Equal(t, "ChoicePicker", priority["component"])
	require.Equal(t, "mutuallyExclusive", priority["variant"])
	require.Len(t, priority["options"], 2)
	require.Nil(t, priority["@checks"])

	tags := a2uiComponentByLabel(t, comps, "tags")
	require.Equal(t, "multipleSelection", tags["variant"])

	require.Equal(t, "CheckBox", a2uiComponentByLabel(t, comps, "urgent")["component"])
	due := a2uiComponentByLabel(t, comps, "dueDate")
	require.Equal(t, "DateTimeInput", due["component"])
	require.Equal(t, true, due["enableDate"])
	require.Nil(t, due["enableTime"])
	require.Equal(t, "longText", a2uiComponentByLabel(t, comps, "extra (JSON)")["variant"])
	require.Equal(t, "obscured", a2uiComponentByLabel(t, comps, "secret")["variant"])
	// データパスに書けない文字を含むプロパティ名は、別名のキーに束縛する。
	require.Equal(t, map[string]any{"@path": "/input/body/a_b_c"},
		a2uiComponentByLabel(t, comps, "a/b~c")["value"])

	var submit map[string]any
	for _, c := range comps {
		if c["component"] == "Button" {
			submit = c
		}
	}
	require.NotNil(t, submit)
	// 送信ボタンは UI から MCP を呼ばず、イベントを発火するだけ。イベント名が
	// 送信用ツールの名前、context がその引数（トップレベルのプロパティごとに
	// データモデルの部分木を束縛する）。
	require.Equal(t, map[string]any{
		"event": map[string]any{
			"name": "createorder__submit",
			"context": map[string]any{
				"orderId": map[string]any{"@path": "/input/orderId"},
				"body":    map[string]any{"@path": "/input/body"},
			},
		},
	}, submit["action"])
	// 必須項目（orderId, title, quantity）が未入力のままでは送信できない。
	require.Len(t, submit["@checks"], 3)
}

func TestBuildA2UIFields_Rejects(t *testing.T) {
	for name, schema := range map[string]any{
		"nil":        nil,
		"not object": map[string]any{"type": "string"},
		"not schema": "oops",
	} {
		t.Run(name, func(t *testing.T) {
			_, ok := buildA2UIFields(schema)
			require.False(t, ok)
		})
	}

	t.Run("too many fields", func(t *testing.T) {
		props := map[string]any{}
		for i := range maxA2UIFormFields + 1 {
			props["p"+strings.Repeat("x", i)] = map[string]any{"type": "string"}
		}
		_, ok := buildA2UIFields(map[string]any{"type": "object", "properties": props})
		require.False(t, ok)
	})

	t.Run("no properties is a confirmation form", func(t *testing.T) {
		fields, ok := buildA2UIFields(map[string]any{"type": "object"})
		require.True(t, ok)
		require.Empty(t, fields)
	})

	t.Run("malformed properties do not panic", func(t *testing.T) {
		fields, ok := buildA2UIFields(map[string]any{
			"type":     "object",
			"required": []any{1, "missing", "a", "a"},
			"properties": map[string]any{
				"a": "not a schema",
				"b": map[string]any{"type": []any{"string", "null"}, "enum": []any{1, 2}},
				"c": map[string]any{"type": "array", "items": "x"},
			},
		})
		require.True(t, ok)
		require.Len(t, fields, 3)
		require.NotPanics(t, func() {
			buildA2UIFormTemplate(a2uiFormSpec{fields: fields})
			a2uiInitialData(fields, map[string]any{"a": []any{1}, "b": 3, "c": "x"})
		})
	})
}

func TestA2UIRegexSupported(t *testing.T) {
	for pattern, want := range map[string]bool{
		`^[a-z]+$`:               true,
		`^\d{3}-\d{4}$`:          true,
		`\\1`:                    true,
		``:                       false,
		`(a)\1`:                  false,
		`(?<=a)b`:                false,
		`(?<name>a)`:             false,
		`\p{L}+`:                 false,
		`abc\`:                   false,
		strings.Repeat("a", 257): false,
	} {
		require.Equal(t, want, a2uiRegexSupported(pattern), "pattern %q", pattern)
	}
}

func TestA2UIInitialData(t *testing.T) {
	fields, ok := buildA2UIFields(a2uiTestSchema())
	require.True(t, ok)
	data := a2uiInitialData(fields, map[string]any{
		"orderId": "o-1",
		"body": map[string]any{
			"title":    "desk",
			"price":    json.Number("12.5"),
			"priority": "high",
			"urgent":   true,
			"tags":     []any{"gift"},
			"extra":    map[string]any{"k": "v"},
		},
	})
	require.Len(t, data, 1)
	input := data["input"].(map[string]any)
	require.Equal(t, "o-1", input["orderId"])
	require.Equal(t, map[string]any{
		"title":    "desk",
		"quantity": "1", // スキーマの default
		"price":    "12.5",
		"priority": []any{"high"},
		"urgent":   true,
		"dueDate":  "",
		"tags":     []any{"gift"},
		"extra":    "{\n  \"k\": \"v\"\n}",
		"a_b_c":    "",
		"secret":   "",
		"any":      "",
	}, input["body"])

	// 引数が無くても、束縛先はすべて埋まる。
	empty := a2uiInitialData(fields, nil)["input"].(map[string]any)
	require.Equal(t, "", empty["orderId"])
	body := empty["body"].(map[string]any)
	require.Equal(t, []any{}, body["priority"])
	require.Equal(t, false, body["urgent"])
}

func TestA2UICoerceInput(t *testing.T) {
	fields, ok := buildA2UIFields(a2uiTestSchema())
	require.True(t, ok)

	t.Run("form values", func(t *testing.T) {
		out, err := a2uiCoerceInput(fields, map[string]any{
			"orderId": "o-1",
			"body": map[string]any{
				"title":    "desk",
				"quantity": " 3 ",
				"price":    "12.5",
				"priority": []any{"high"},
				"urgent":   false,
				"dueDate":  "",
				"tags":     []any{},
				"extra":    `{"k": [1, 2]}`,
				"a/b~c":    "",
				"secret":   "",
				"any":      "plain text",
			},
			"unknown": "kept",
		})
		require.NoError(t, err)
		raw, err := json.Marshal(out)
		require.NoError(t, err)
		require.JSONEq(t, `{
			"orderId": "o-1",
			"body": {
				"title": "desk", "quantity": 3, "price": 12.5, "priority": "high",
				"urgent": false, "extra": {"k": [1, 2]}, "any": "plain text"
			},
			"unknown": "kept"
		}`, string(raw))
	})

	t.Run("already typed values pass through", func(t *testing.T) {
		in := map[string]any{
			"orderId": "o-1",
			"body": map[string]any{
				"title": "desk", "quantity": json.Number("3"), "priority": "low",
				"tags": []any{"gift"}, "extra": map[string]any{"k": "v"},
			},
		}
		out, err := a2uiCoerceInput(fields, in)
		require.NoError(t, err)
		require.Equal(t, in, out)
	})

	t.Run("invalid values", func(t *testing.T) {
		_, err := a2uiCoerceInput(fields, map[string]any{
			"body": map[string]any{"quantity": "1.5"},
		})
		require.EqualError(t, err, "body.quantity: must be an integer")
		_, err = a2uiCoerceInput(fields, map[string]any{
			"body": map[string]any{"price": "abc"},
		})
		require.EqualError(t, err, "body.price: must be a number")
		_, err = a2uiCoerceInput(fields, map[string]any{
			"body": map[string]any{"extra": "{not json"},
		})
		require.EqualError(t, err, "body.extra: must be valid JSON")
	})

	t.Run("required empty values are kept for the API to reject", func(t *testing.T) {
		out, err := a2uiCoerceInput(fields, map[string]any{
			"orderId": "",
			"body":    map[string]any{"title": ""},
		})
		require.NoError(t, err)
		require.Equal(t, map[string]any{
			"orderId": "",
			"body":    map[string]any{"title": ""},
		}, out)
	})
}

func TestA2UIDataKey(t *testing.T) {
	fields, ok := buildA2UIFields(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"X-Request-Id": map[string]any{"type": "string"},
			"X_Request_Id": map[string]any{"type": "string"},
			"X.Request.Id": map[string]any{"type": "string"},
			"plain":        map[string]any{"type": "string"},
			"123":          map[string]any{"type": "integer"},
			"":             map[string]any{"type": "string"},
		},
	})
	require.True(t, ok)
	keys := map[string]string{}
	seen := map[string]bool{}
	for _, f := range fields {
		keys[f.name] = f.key
		require.False(t, seen[f.key], "duplicate key %q", f.key)
		seen[f.key] = true
		require.Regexp(t, `^[A-Za-z0-9_]+$`, f.key)
	}
	// そのまま使える名前は変えず、別名はそれと重ならないようにする。
	require.Equal(t, "plain", keys["plain"])
	require.Equal(t, "X_Request_Id", keys["X_Request_Id"])
	require.Equal(t, "X_Request_Id_2", keys["X-Request-Id"])
	require.Equal(t, "X_Request_Id_3", keys["X.Request.Id"])
	// 数字だけの名前は、データパスで配列の添字と読まれないようにする。
	require.Equal(t, "_123", keys["123"])
	require.Equal(t, "_", keys[""])

	// フォームは別名のキーで送ってくる。送信時にプロパティ名へ戻す。
	out, err := a2uiCoerceInput(fields, map[string]any{
		"X_Request_Id_2": "req-1",
		"X_Request_Id":   "real",
		"_123":           "7",
		"plain":          "p",
	})
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"X-Request-Id": "req-1",
		"X_Request_Id": "real",
		"123":          int64(7),
		"plain":        "p",
	}, out)

	// プロパティ名で届いた値はそのまま通す。
	out, err = a2uiCoerceInput(fields, map[string]any{"X-Request-Id": "req-2"})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"X-Request-Id": "req-2"}, out)
}

func TestBuildA2UIFields_GroupsLast(t *testing.T) {
	fields, ok := buildA2UIFields(map[string]any{
		"type":     "object",
		"required": []any{"body"},
		"properties": map[string]any{
			"body": map[string]any{
				"type":     "object",
				"required": []any{"name"},
				"properties": map[string]any{
					"category": map[string]any{
						"type":       "object",
						"properties": map[string]any{"id": map[string]any{"type": "integer"}},
					},
					"id":     map[string]any{"type": "integer"},
					"name":   map[string]any{"type": "string"},
					"status": map[string]any{"type": "string"},
				},
			},
			"apiKey": map[string]any{"type": "string"},
		},
	})
	require.True(t, ok)
	names := func(fields []*a2uiField) (out []string) {
		for _, f := range fields {
			out = append(out, f.name)
		}
		return out
	}
	// 入れ子のオブジェクトは、必須であっても同じ階層の他の項目の後ろ。
	require.Equal(t, []string{"apiKey", "body"}, names(fields))
	require.Equal(t, []string{"name", "id", "status", "category"}, names(fields[1].children))

	// 必須のグループの見出しは、太字の外に必須の印を置く。
	comps := buildA2UIFormComponents(
		a2uiFormSpec{title: "t", submitTool: "t__submit", fields: fields},
	)
	var texts []string
	for _, c := range comps {
		if text, ok := c.(map[string]any)["text"].(string); ok {
			texts = append(texts, text)
		}
	}
	require.Contains(t, texts, "**body** *")
	require.Contains(t, texts, "**category**")
}

func a2uiListSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []any{"photoUrls"},
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
			"photoUrls": map[string]any{
				"type": "array", "items": map[string]any{"type": "string"},
			},
			"scores": map[string]any{
				"type": "array", "items": map[string]any{"type": "integer"},
			},
			"tags": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":     "object",
					"required": []any{"name"},
					"properties": map[string]any{
						"id":     map[string]any{"type": "integer"},
						"name":   map[string]any{"type": "string"},
						"hidden": map[string]any{"type": "boolean"},
					},
				},
			},
			// 入力欄にできない配列は JSON のテキスト欄のまま。
			"matrix": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		},
	}
}

// 配列は JSON のテキスト欄ではなく、要素ごとの入力欄として並べる。
func TestA2UIForm_Lists(t *testing.T) {
	fields, ok := buildA2UIFields(a2uiListSchema())
	require.True(t, ok)
	byName := map[string]*a2uiField{}
	var order []string
	for _, f := range fields {
		byName[f.name] = f
		order = append(order, f.name)
	}
	// オブジェクトの配列は見出しを持つので、他の項目の後ろ。
	require.Equal(t, []string{"photoUrls", "matrix", "name", "scores", "tags"}, order)
	require.Equal(t, a2uiFieldList, byName["photoUrls"].kind)
	require.Equal(t, a2uiFieldText, byName["photoUrls"].item.kind)
	require.Equal(t, a2uiFieldInteger, byName["scores"].item.kind)
	require.Equal(t, a2uiFieldGroup, byName["tags"].item.kind)
	require.Equal(t, a2uiFieldJSON, byName["matrix"].kind)
	// 要素の中の項目は必須にしない（空の行で送信を止めない）。
	for _, child := range byName["tags"].item.children {
		require.False(t, child.required, child.name)
	}

	args := map[string]any{
		"photoUrls": []any{"https://example.com/1.png", "https://example.com/2.png"},
		"tags":      []any{map[string]any{"id": json.Number("7"), "name": "cute"}},
	}
	a2uiSizeLists(fields, args)
	// 既にある要素 + 空の行 2 つ。値の無い配列は既定の 3 行。
	require.Equal(t, 4, byName["photoUrls"].slots)
	require.Equal(t, 3, byName["scores"].slots)
	require.Equal(t, 3, byName["tags"].slots)

	input := a2uiInitialData(fields, args)["input"].(map[string]any)
	require.Equal(t,
		[]any{"https://example.com/1.png", "https://example.com/2.png", "", ""}, input["photoUrls"])
	require.Equal(t, []any{"", "", ""}, input["scores"])
	require.Equal(t, []any{
		map[string]any{"id": "7", "name": "cute", "hidden": false},
		map[string]any{"id": "", "name": "", "hidden": false},
		map[string]any{"id": "", "name": "", "hidden": false},
	}, input["tags"])

	comps := buildA2UIFormComponents(
		a2uiFormSpec{title: "t", submitTool: "t__submit", fields: fields})
	var cs []map[string]any
	for _, c := range comps {
		cs = append(cs, c.(map[string]any))
	}
	// 行ごとに配列の添字のパスへ束縛する。必須の配列は最初の行だけ必須。
	first := a2uiComponentByLabel(t, cs, "photoUrls 1 *")
	require.Equal(t, map[string]any{"@path": "/input/photoUrls/0"}, first["value"])
	require.Len(t, first["@checks"], 1)
	last := a2uiComponentByLabel(t, cs, "photoUrls 4")
	require.Equal(t, map[string]any{"@path": "/input/photoUrls/3"}, last["value"])
	require.Nil(t, last["@checks"])
	require.Equal(t, "number", a2uiComponentByLabel(t, cs, "scores 2")["variant"])
	var tagNamePaths []any
	for _, c := range cs {
		if c["label"] == "name" && c["component"] == "TextField" {
			tagNamePaths = append(tagNamePaths, c["value"].(map[string]any)["@path"])
		}
	}
	require.Equal(t,
		[]any{"/input/name", "/input/tags/0/name", "/input/tags/1/name", "/input/tags/2/name"},
		tagNamePaths)

	// Express でも同じ形で書ける。
	block, err := buildA2UIExpressForm(
		a2uiFormSpec{surfaceID: "s", title: "t", submitTool: "t__submit", fields: fields},
		a2uiInitialData(fields, args))
	require.NoError(t, err)
	require.Contains(t, block,
		`$/input/tags = [{hidden: false, id: "7", name: "cute"}, `+
			`{hidden: false, id: "", name: ""}, {hidden: false, id: "", name: ""}]`)
	require.Contains(t, block, `value=$/input/tags/1/name`)

	// 送信時は空のままの行を取り除き、要素を型に直す。
	out, err := a2uiCoerceInput(fields, map[string]any{
		"name":      "Rex",
		"photoUrls": []any{"https://example.com/1.png", "", "https://example.com/3.png", ""},
		"scores":    []any{"3", "", "5"},
		"tags": []any{
			map[string]any{"id": "7", "name": "cute", "hidden": false},
			map[string]any{"id": "", "name": "", "hidden": false},
			map[string]any{"id": "", "name": "", "hidden": true},
		},
		"matrix": "",
	})
	require.NoError(t, err)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"name": "Rex",
		"photoUrls": ["https://example.com/1.png", "https://example.com/3.png"],
		"scores": [3, 5],
		"tags": [{"id": 7, "name": "cute", "hidden": false}, {"hidden": true}]
	}`, string(raw))

	// 任意の配列は、すべて空なら送らない。必須の配列は空でも送る。
	out, err = a2uiCoerceInput(fields, map[string]any{
		"photoUrls": []any{"", ""},
		"scores":    []any{"", ""},
	})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"photoUrls": []any{}}, out)

	_, err = a2uiCoerceInput(fields, map[string]any{"scores": []any{"1", "x"}})
	require.EqualError(t, err, "scores[1]: must be an integer")
}
