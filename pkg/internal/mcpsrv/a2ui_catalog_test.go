package mcpsrv

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

const (
	a2uiCustomCatalogPath = "fixtures/a2ui_custom_catalog.json"
	a2uiCustomCatalogID   = "https://example.com/catalogs/acme.json"
)

// a2uiCustomComponents は fixtures/a2ui_custom_catalog.json（部品名もプロパティ名も basic
// カタログと違うカタログ）への対応。キーは設定の読み込みで小文字になりうる。
func a2uiCustomComponents() map[string]config.A2UIComponentConfig {
	return map[string]config.A2UIComponentConfig{
		"text": {
			Component:  "Label",
			Properties: map[string]string{"text": "content", "variant": "tone"},
		},
		"column": {Component: "Stack", Properties: map[string]string{"children": "items"}},
		"button": {
			Component: "Action",
			Properties: map[string]string{
				"label":   "caption",
				"action":  "onPress",
				"variant": "kind",
			},
		},
		"textfield": {
			Component:  "Input",
			Properties: map[string]string{"label": "title", "value": "text", "variant": "mode"},
		},
		"checkbox": {
			Component:  "Toggle",
			Properties: map[string]string{"label": "title", "value": "checked"},
		},
		"choicepicker": {Component: "Select", Properties: map[string]string{"label": "title"}},
	}
}

func a2uiCustomVocabulary(t *testing.T) *a2uiVocabulary {
	t.Helper()
	vocab, err := loadA2UIVocabulary(&config.A2UIConfig{
		Catalog: a2uiCustomCatalogPath, Components: a2uiCustomComponents(),
	})
	require.NoError(t, err)
	require.NotNil(t, vocab)
	return vocab
}

func TestA2UIVocabulary_DefaultIsBasicCatalog(t *testing.T) {
	vocab, err := loadA2UIVocabulary(&config.A2UIConfig{Forms: true})
	require.NoError(t, err)
	require.Nil(t, vocab)
	require.Equal(t, a2uiBasicCatalogID, vocab.id())
	require.True(t, vocab.has("DateTimeInput"))
	require.True(t, vocab.isRef("Column", "children"))
	require.False(t, vocab.isRef("Text", "text"))
	components := []any{map[string]any{"id": "root", "component": "Card", "child": "c"}}
	require.Equal(t, components, vocab.translate(components))
}

// エージェントのカタログが basic カタログと違う部品名・プロパティ名を使う場合、
// フォームはそのカタログの語彙で書かれる。
func TestA2UIVocabulary_CustomCatalog(t *testing.T) {
	vocab := a2uiCustomVocabulary(t)
	require.Equal(t, a2uiCustomCatalogID, vocab.id())
	// カタログに無い部品は使わない。
	require.False(t, vocab.has("Card"))
	require.False(t, vocab.has("DateTimeInput"))
	require.True(t, vocab.has("ChoicePicker"))

	fields, ok := buildA2UIFields(map[string]any{
		"type":     "object",
		"required": []any{"title"},
		"properties": map[string]any{
			"title": map[string]any{
				"type":      "string",
				"maxLength": float64(40),
				"pattern":   "^[a-z]+$",
			},
			"quantity": map[string]any{"type": "integer"},
			"notes":    map[string]any{"type": "string", "format": "password"},
			"priority": map[string]any{"type": "string", "enum": []any{"low", "high"}},
			"urgent":   map[string]any{"type": "boolean"},
			"dueDate":  map[string]any{"type": "string", "format": "date"},
			"labels": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string", "enum": []any{"gift", "fragile"}},
			},
		},
	})
	require.True(t, ok)
	vocab.adapt(fields)
	kinds := map[string]a2uiFieldKind{}
	for _, f := range fields {
		kinds[f.name] = f.kind
	}
	// 日時の部品が無いので日付はテキスト欄、複数選択を表せないので行ごとのテキスト欄。
	require.Equal(t, a2uiFieldText, kinds["dueDate"])
	require.Equal(t, a2uiFieldList, kinds["labels"])
	require.Equal(t, a2uiFieldChoice, kinds["priority"])
	require.Equal(t, a2uiFieldBool, kinds["urgent"])

	spec := a2uiFormSpec{
		surfaceID: "s", title: "createorder", description: "Create an order",
		submitTool: "createorder__submit", fields: fields, vocab: vocab,
	}
	template := buildA2UIFormTemplate(spec)
	require.Equal(t, a2uiCustomCatalogID,
		template[0].(map[string]any)["createSurface"].(map[string]any)["catalogId"])

	byID := map[string]map[string]any{}
	used := map[string]bool{}
	for _, raw := range buildA2UIFormComponents(spec) {
		c := raw.(map[string]any)
		byID[c["id"].(string)] = c
		used[c["component"].(string)] = true
	}
	// basic カタログの部品名は 1 つも残らない。
	require.Equal(t, map[string]bool{
		"Label": true, "Stack": true, "Action": true, "Input": true, "Toggle": true, "Select": true,
	}, used)

	// Card が無いので、縦に並べる部品が root になる。
	root := byID["root"]
	require.Equal(t, "Stack", root["component"])
	for _, child := range root["items"].([]any) {
		require.Contains(t, byID, child.(string), "undefined child")
	}

	find := func(component, prop string, value any) map[string]any {
		t.Helper()
		for _, c := range byID {
			if c["component"] == component && c[prop] == value {
				return c
			}
		}
		require.Failf(t, "component not found", "%s with %s=%v", component, prop, value)
		return nil
	}
	title := find("Input", "title", "title *")
	require.Equal(t, map[string]any{"@path": "/input/title"}, title["text"])
	require.NotContains(t, title, "label")
	require.NotContains(t, title, "value")
	// カタログに無い関数（length）のチェックは付けない。required と regex は付ける。
	var calls []any
	for _, check := range title["@checks"].([]any) {
		calls = append(calls, check.(map[string]any)["condition"].(map[string]any)["@call"])
	}
	require.Equal(t, []any{"required", "regex"}, calls)

	// enum にある値だけ書く: number はある、obscured は無い。
	require.Equal(t, "number", find("Input", "title", "quantity")["mode"])
	require.NotContains(t, find("Input", "title", "notes"), "mode")
	// 説明文の variant（caption）はカタログの enum に無いので付けない。
	require.NotContains(t, find("Label", "content", "Create an order"), "tone")

	require.Equal(t, map[string]any{"@path": "/input/urgent"},
		find("Toggle", "title", "urgent")["checked"])
	// 選択の部品は variant を持たないので書かない。
	priority := find("Select", "title", "priority")
	require.NotContains(t, priority, "variant")
	require.Len(t, priority["options"], 2)

	// ボタンのラベルは子の部品ではなく、文字列のプロパティ。
	submit := find("Action", "caption", "Submit")
	require.NotContains(t, submit, "child")
	require.Equal(t, "primary", submit["kind"])
	require.Equal(t, "createorder__submit",
		submit["onPress"].(map[string]any)["event"].(map[string]any)["name"])
	for _, c := range byID {
		require.NotEqual(t, "Submit", c["content"], "the label component is absorbed")
	}

	// Express でも同じ語彙で書く。子を指すプロパティ（items）は変数名の参照。
	block, err := buildA2UIExpressForm(spec, a2uiInitialData(fields, nil))
	require.NoError(t, err)
	lines := strings.Split(block, "\n")
	require.Contains(t, lines, `text_1 = Label(content="### createorder")`)
	require.Contains(t, lines[len(lines)-2], "root = Stack(items=[text_1, text_2, field_3, ")
	require.NotContains(t, block, "TextField(")
	require.NotContains(t, block, "?length(")
	require.Contains(t, block, `caption="Submit"`)
	require.Contains(t, block, `onPress=Event("createorder__submit", {`)

	// 真偽値・選択をテキストで入力させた場合も、送信時に型へ戻す。
	out, err := a2uiCoerceInput(fields, map[string]any{
		"title": "desk", "dueDate": "2026-03-20", "labels": []any{"gift", "", ""},
	})
	require.NoError(t, err)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	require.JSONEq(t, `{"title": "desk", "dueDate": "2026-03-20", "labels": ["gift"]}`, string(raw))
}

// チェックボックスも選択の部品も無いカタログでは、真偽値と選択をテキスト欄にする。
func TestA2UIVocabulary_MinimalCatalog(t *testing.T) {
	components := a2uiCustomComponents()
	delete(components, "checkbox")
	delete(components, "choicepicker")
	vocab, err := loadA2UIVocabulary(&config.A2UIConfig{
		Catalog: a2uiCustomCatalogPath, Components: components,
	})
	require.NoError(t, err)
	require.False(t, vocab.has("CheckBox"))
	require.False(t, vocab.has("ChoicePicker"))

	fields, ok := buildA2UIFields(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"urgent":   map[string]any{"type": "boolean"},
			"priority": map[string]any{"type": "string", "enum": []any{"low", "high"}},
		},
	})
	require.True(t, ok)
	vocab.adapt(fields)
	for _, f := range fields {
		require.Equal(t, a2uiFieldText, f.kind, f.name)
	}
	input := a2uiInitialData(fields, map[string]any{"urgent": true})["input"].(map[string]any)
	require.Equal(t, map[string]any{"urgent": "true", "priority": ""}, input)
	out, err := a2uiCoerceInput(fields, map[string]any{"urgent": "false", "priority": "high"})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"urgent": false, "priority": "high"}, out)

	// 選択の部品だけある場合、真偽値は true / false の選択にする。
	vocab = a2uiCustomVocabulary(t)
	delete(vocab.components, "CheckBox")
	fields, _ = buildA2UIFields(map[string]any{
		"type":       "object",
		"properties": map[string]any{"urgent": map[string]any{"type": "boolean"}},
	})
	vocab.adapt(fields)
	require.Equal(t, a2uiFieldChoice, fields[0].kind)
	require.Equal(t, []string{"true", "false"}, fields[0].options)
	require.Equal(t, []any{"true"}, fields[0].initialValue(true))
	out, err = a2uiCoerceInput(fields, map[string]any{"urgent": []any{"true"}})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"urgent": true}, out)
}

func TestLoadA2UIVocabulary_Errors(t *testing.T) {
	write := func(t *testing.T, content string) string {
		t.Helper()
		path := t.TempDir() + "/catalog.json"
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		return path
	}
	for _, tc := range []struct {
		name    string
		cfg     func(t *testing.T) *config.A2UIConfig
		wantErr string
	}{
		{
			name: "missing file",
			cfg: func(*testing.T) *config.A2UIConfig {
				return &config.A2UIConfig{Catalog: "fixtures/nonexistent.json"}
			},
			wantErr: "a2ui.catalog:",
		},
		{
			name: "not json",
			cfg: func(t *testing.T) *config.A2UIConfig {
				return &config.A2UIConfig{Catalog: write(t, "[]")}
			},
			wantErr: "is not a JSON object",
		},
		{
			name: "no catalogId",
			cfg: func(t *testing.T) *config.A2UIConfig {
				return &config.A2UIConfig{Catalog: write(t, `{"components": {"Text": {}}}`)}
			},
			wantErr: "has no catalogId",
		},
		{
			name: "no components",
			cfg: func(t *testing.T) *config.A2UIConfig {
				return &config.A2UIConfig{Catalog: write(t, `{"catalogId": "x"}`)}
			},
			wantErr: "has no components",
		},
		{
			// 部品名もプロパティ名も違うカタログは、対応を指定しないと使えない。
			name: "required role unresolved",
			cfg: func(*testing.T) *config.A2UIConfig {
				return &config.A2UIConfig{Catalog: a2uiCustomCatalogPath}
			},
			wantErr: `has no component "Text" for Text; set a2ui.components.Text.component`,
		},
		{
			name: "named component missing",
			cfg: func(*testing.T) *config.A2UIConfig {
				components := a2uiCustomComponents()
				components["checkbox"] = config.A2UIComponentConfig{Component: "Switch"}
				return &config.A2UIConfig{Catalog: a2uiCustomCatalogPath, Components: components}
			},
			wantErr: `has no component "Switch" for CheckBox`,
		},
		{
			name: "named property missing",
			cfg: func(*testing.T) *config.A2UIConfig {
				components := a2uiCustomComponents()
				components["textfield"] = config.A2UIComponentConfig{
					Component:  "Input",
					Properties: map[string]string{"label": "caption", "value": "text"},
				}
				return &config.A2UIConfig{Catalog: a2uiCustomCatalogPath, Components: components}
			},
			wantErr: `component "Input" has no property "caption" ` +
				`(a2ui.components.TextField.properties.label)`,
		},
		{
			name: "required property unresolved",
			cfg: func(*testing.T) *config.A2UIConfig {
				components := a2uiCustomComponents()
				components["textfield"] = config.A2UIComponentConfig{Component: "Input"}
				return &config.A2UIConfig{Catalog: a2uiCustomCatalogPath, Components: components}
			},
			wantErr: `component "Input" has no property "label" for TextField`,
		},
		{
			name: "button without a label",
			cfg: func(*testing.T) *config.A2UIConfig {
				components := a2uiCustomComponents()
				components["button"] = config.A2UIComponentConfig{
					Component: "Action", Properties: map[string]string{"action": "onPress"},
				}
				return &config.A2UIConfig{Catalog: a2uiCustomCatalogPath, Components: components}
			},
			wantErr: "has neither a child nor a label property for Button",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vocab, err := loadA2UIVocabulary(tc.cfg(t))
			require.Error(t, err)
			require.Nil(t, vocab)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
