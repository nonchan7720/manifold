package mcpsrv

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/nonchan7720/manifold/pkg/config"
)

// エージェントのカタログに合わせたフォームの組み立て。
//
// A2UI のカタログ（catalog.json）は、使える部品とそのプロパティを決める。カタログは
// エージェント（とレンダラ）の持ち物で、basic カタログとは限らない。ゲートウェイが
// basic カタログの部品名を決め打ちで書くと、独自のカタログを持つエージェントは
// それをコンパイルできない。
//
// そこで、フォームはまず basic カタログの語彙（Text, Column, TextField ...）で組み立て、
// 最後にカタログの部品名・プロパティ名へ書き換える（translate）。カタログに無い種類の
// 入力欄は、組み立てる前に別の入力欄へ置き換える（adapt）。
//
//   - a2ui.catalog が無ければ basic カタログそのもの（何も書き換えない）。
//   - 役割（basic カタログの部品名）ごとに、カタログの同名の部品を使う。名前や
//     プロパティが違う部品は a2ui.components で対応を指定する。
//   - Text, Column, Button, TextField は必須。Card, CheckBox, ChoicePicker,
//     DateTimeInput は、カタログに無ければ使わない（テキスト欄などで代える）。
//   - 対応が取れない場合は起動を失敗させる（実行時に壊れたフォームを返さない）。

// maxA2UICatalogBytes は読み込むカタログの大きさの上限。
const maxA2UICatalogBytes = 4 << 20

// a2uiRoleProp は役割のプロパティ。
type a2uiRoleProp struct {
	name string
	// required が true のプロパティがカタログの部品に無ければ、その役割は使えない。
	required bool
	// ref はプロパティの値が他の部品の ID（変数名）かどうか。
	ref bool
}

// a2uiRole はフォームの部品の役割。名前は basic カタログの部品名。
type a2uiRole struct {
	name string
	// required が true の役割がカタログで使えなければ、起動を失敗させる。
	required bool
	props    []a2uiRoleProp
}

// a2uiRoles はフォームが使う役割。Button は child（ラベルの部品）か label（文字列）の
// どちらかが要る（loadA2UIVocabulary で確かめる）。
var a2uiRoles = []a2uiRole{
	{name: "Text", required: true, props: []a2uiRoleProp{
		{name: "text", required: true}, {name: "variant"},
	}},
	{name: "Column", required: true, props: []a2uiRoleProp{
		{name: "children", required: true, ref: true},
	}},
	{name: "Card", props: []a2uiRoleProp{{name: "child", required: true, ref: true}}},
	{name: "Button", required: true, props: []a2uiRoleProp{
		{name: "action", required: true}, {name: "child", ref: true}, {name: "label"},
		{name: "variant"},
	}},
	{name: "TextField", required: true, props: []a2uiRoleProp{
		{name: "label", required: true}, {name: "value", required: true}, {name: "variant"},
	}},
	{name: "CheckBox", props: []a2uiRoleProp{
		{name: "label", required: true}, {name: "value", required: true},
	}},
	{name: "ChoicePicker", props: []a2uiRoleProp{
		{name: "value", required: true}, {name: "options", required: true},
		{name: "label"}, {name: "variant"},
	}},
	{name: "DateTimeInput", props: []a2uiRoleProp{
		{name: "value", required: true}, {name: "label"},
		{name: "enableDate"}, {name: "enableTime"},
	}},
}

// a2uiComponentSpec は役割に割り当てたカタログの部品。
type a2uiComponentSpec struct {
	component string
	// props は役割のプロパティ名 → カタログの部品のプロパティ名。無いものは使えない。
	props map[string]string
	// enums は役割のプロパティ名 → カタログが許す値（enum があるプロパティのみ）。
	enums map[string][]any
	// refs はカタログの部品のプロパティ名のうち、値が他の部品の ID であるもの。
	refs map[string]struct{}
}

// a2uiVocabulary はカタログに合わせた部品の語彙。nil は basic カタログそのもの。
type a2uiVocabulary struct {
	catalogID  string
	components map[string]*a2uiComponentSpec
	// functions はカタログが定義する関数の名前（チェックに使えるもの）。
	functions map[string]struct{}
	// refs はカタログの部品名 → 他の部品の ID を値に持つプロパティ名。
	refs map[string]map[string]struct{}
}

// id は createSurface に書くカタログ ID を返す。
func (v *a2uiVocabulary) id() string {
	if v == nil {
		return a2uiBasicCatalogID
	}
	return v.catalogID
}

// has は役割 role の部品を使えるかを返す。
func (v *a2uiVocabulary) has(role string) bool {
	if v == nil {
		return true
	}
	_, ok := v.components[role]
	return ok
}

// allows は役割 role のプロパティ prop に value を書けるかを返す。
func (v *a2uiVocabulary) allows(role, prop string, value any) bool {
	if v == nil {
		return true
	}
	spec, ok := v.components[role]
	if !ok {
		return false
	}
	if _, mapped := spec.props[prop]; !mapped {
		return false
	}
	enum, limited := spec.enums[prop]
	return !limited || slices.Contains(enum, value)
}

// isRef はカタログの部品 component のプロパティ prop の値が、他の部品の ID かを返す。
func (v *a2uiVocabulary) isRef(component, prop string) bool {
	if v == nil {
		return prop == "child" || prop == "children"
	}
	_, ok := v.refs[component][prop]
	return ok
}

// loadA2UIVocabulary は cfg のカタログを読み、フォームの語彙を返す。カタログの
// 指定が無ければ nil（basic カタログ）。
func loadA2UIVocabulary(cfg *config.A2UIConfig) (*a2uiVocabulary, error) {
	if cfg == nil || cfg.Catalog == "" {
		return nil, nil //nolint:nilnil // nil の語彙は basic カタログを表す
	}
	file, err := os.Open(cfg.Catalog)
	if err != nil {
		return nil, fmt.Errorf("a2ui.catalog: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxA2UICatalogBytes+1))
	if err != nil {
		return nil, fmt.Errorf("a2ui.catalog: %w", err)
	}
	if len(raw) > maxA2UICatalogBytes {
		return nil, fmt.Errorf("a2ui.catalog: %s is larger than %d bytes",
			cfg.Catalog, maxA2UICatalogBytes)
	}
	var catalog map[string]any
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return nil, fmt.Errorf("a2ui.catalog: %s is not a JSON object: %w", cfg.Catalog, err)
	}
	return newA2UIVocabulary(catalog, cfg.Components)
}

// newA2UIVocabulary はカタログの中身と a2ui.components から語彙を組み立てる。
func newA2UIVocabulary(
	catalog map[string]any, overrides map[string]config.A2UIComponentConfig,
) (*a2uiVocabulary, error) {
	catalogID, _ := catalog["catalogId"].(string)
	if catalogID == "" {
		return nil, fmt.Errorf("a2ui.catalog: the catalog has no catalogId")
	}
	components, _ := catalog["components"].(map[string]any)
	if len(components) == 0 {
		return nil, fmt.Errorf("a2ui.catalog: the catalog has no components")
	}
	v := &a2uiVocabulary{
		catalogID:  catalogID,
		components: map[string]*a2uiComponentSpec{},
		functions:  map[string]struct{}{},
		refs:       map[string]map[string]struct{}{},
	}
	if functions, ok := catalog["functions"].(map[string]any); ok {
		for name := range functions {
			v.functions[name] = struct{}{}
		}
	}
	for _, role := range a2uiRoles {
		spec, err := resolveA2UIRole(role, components, a2uiOverrideFor(overrides, role.name))
		if err != nil {
			return nil, err
		}
		if spec == nil {
			continue
		}
		v.components[role.name] = spec
		v.refs[spec.component] = spec.refs
	}
	return v, nil
}

// a2uiOverrideFor は役割 role の指定を返す。設定のキーは読み込み時に小文字へ
// 揃えられることがあるので、大文字小文字を区別せずに探す。
func a2uiOverrideFor(
	overrides map[string]config.A2UIComponentConfig, role string,
) config.A2UIComponentConfig {
	for key, override := range overrides {
		if strings.EqualFold(key, role) {
			return override
		}
	}
	return config.A2UIComponentConfig{}
}

// resolveA2UIRole は役割 role にカタログの部品を割り当てる。任意の役割で、カタログに
// 部品や必須のプロパティが無ければ nil（その役割は使わない）。設定で名指しした部品や
// プロパティがカタログに無い場合と、必須の役割を割り当てられない場合はエラー。
func resolveA2UIRole(
	role a2uiRole, components map[string]any, override config.A2UIComponentConfig,
) (*a2uiComponentSpec, error) {
	name := role.name
	if override.Component != "" {
		name = override.Component
	}
	schema, exists := asSchemaObject(components[name])
	if !exists {
		if override.Component != "" || role.required {
			return nil, fmt.Errorf(
				"a2ui.catalog: the catalog has no component %q for %s; "+
					"set a2ui.components.%s.component", name, role.name, role.name)
		}
		return nil, nil //nolint:nilnil // 任意の役割は、無ければ使わない
	}
	properties := a2uiCatalogProperties(schema)
	spec := &a2uiComponentSpec{
		component: name,
		props:     map[string]string{},
		enums:     map[string][]any{},
		refs:      map[string]struct{}{},
	}
	for _, prop := range role.props {
		actual, named := a2uiPropertyOverride(override.Properties, prop.name)
		if !named {
			actual = prop.name
		}
		propSchema, ok := properties[actual]
		if !ok {
			if named {
				return nil, fmt.Errorf(
					"a2ui.catalog: component %q has no property %q (a2ui.components.%s.properties.%s)",
					name,
					actual,
					role.name,
					prop.name,
				)
			}
			if !prop.required {
				continue
			}
			if role.required {
				return nil, fmt.Errorf(
					"a2ui.catalog: component %q has no property %q for %s; "+
						"set a2ui.components.%s.properties.%s",
					name, prop.name, role.name, role.name, prop.name)
			}
			return nil, nil //nolint:nilnil // 任意の役割は、使えなければ使わない
		}
		spec.props[prop.name] = actual
		if prop.ref {
			spec.refs[actual] = struct{}{}
		}
		if s, ok := asSchemaObject(propSchema); ok {
			if enum, ok := s["enum"].([]any); ok && len(enum) > 0 {
				spec.enums[prop.name] = enum
			}
		}
	}
	if role.name == "Button" {
		_, child := spec.props["child"]
		_, label := spec.props["label"]
		if !child && !label {
			return nil, fmt.Errorf(
				"a2ui.catalog: component %q has neither a child nor a label property for Button; "+
					"set a2ui.components.Button.properties.child or .label", name)
		}
	}
	return spec, nil
}

// a2uiPropertyOverride は役割のプロパティ prop に対する設定の指定を返す。
func a2uiPropertyOverride(properties map[string]string, prop string) (string, bool) {
	for key, actual := range properties {
		if strings.EqualFold(key, prop) && actual != "" {
			return actual, true
		}
	}
	return "", false
}

// a2uiCatalogProperties はカタログの部品の定義から、プロパティ名 → スキーマを集める。
// 部品は properties を直接持つか、allOf の要素の中に持つ。
func a2uiCatalogProperties(schema map[string]any) map[string]any {
	out := map[string]any{}
	if props, ok := schema["properties"].(map[string]any); ok {
		maps.Copy(out, props)
	}
	if all, ok := schema["allOf"].([]any); ok {
		for _, part := range all {
			if sub, ok := asSchemaObject(part); ok {
				maps.Copy(out, a2uiCatalogProperties(sub))
			}
		}
	}
	return out
}

// adapt はカタログに無い種類の入力欄を、ある部品で表せる入力欄に置き換える。
//
//   - チェックボックスが無い真偽値: true / false の単一選択、それも無ければテキスト欄
//   - 選択の部品が無い単一選択・日時の部品が無い日付: テキスト欄
//   - 複数選択を表せない複数選択: 行ごとのテキスト欄
func (v *a2uiVocabulary) adapt(fields []*a2uiField) {
	if v == nil {
		return
	}
	for _, f := range fields {
		v.adaptField(f)
	}
}

func (v *a2uiVocabulary) adaptField(f *a2uiField) {
	choice := v.has("ChoicePicker")
	switch f.kind {
	case a2uiFieldBool:
		switch {
		case v.has("CheckBox"):
		case choice:
			f.kind, f.options = a2uiFieldChoice, []string{"true", "false"}
		default:
			f.kind = a2uiFieldText
		}
	case a2uiFieldChoice:
		if !choice {
			f.kind = a2uiFieldText
		}
	case a2uiFieldMultiChoice:
		if !choice || !v.allows("ChoicePicker", "variant", "multipleSelection") {
			f.kind, f.slots = a2uiFieldList, a2uiListDefaultSlots
			f.item = &a2uiField{kind: a2uiFieldText, schema: map[string]any{}}
		}
	case a2uiFieldDate, a2uiFieldDateTime:
		if !v.has("DateTimeInput") {
			f.kind = a2uiFieldText
		}
	case a2uiFieldText, a2uiFieldInteger, a2uiFieldNumber, a2uiFieldJSON,
		a2uiFieldGroup, a2uiFieldList:
	}
	for _, child := range f.children {
		v.adaptField(child)
	}
	if f.item != nil {
		v.adaptField(f.item)
	}
}

// translate は basic カタログの語彙で組み立てた部品を、カタログの部品名と
// プロパティ名に書き換える。カタログの部品に無いプロパティと、enum に無い値は落とす。
func (v *a2uiVocabulary) translate(components []any) []any {
	if v == nil {
		return components
	}
	byID := make(map[string]map[string]any, len(components))
	for _, raw := range components {
		if c, ok := raw.(map[string]any); ok {
			id, _ := c["id"].(string)
			byID[id] = c
		}
	}
	// 他の部品に取り込まれて、単独では出さない部品の ID。
	absorbed := map[string]struct{}{}
	// ID の付け替え（Card を使わないとき、その子を root にする）。
	renamed := map[string]string{}
	if card, ok := byID["root"]; ok && card["component"] == "Card" && !v.has("Card") {
		child, _ := card["child"].(string)
		absorbed["root"] = struct{}{}
		renamed[child] = "root"
	}
	// ラベルを文字列のプロパティで持つボタンでは、ラベルの部品をボタンに取り込む。
	if _, hasChild := v.components["Button"].props["child"]; !hasChild {
		for _, c := range byID {
			if label, ok := c["child"].(string); ok && c["component"] == "Button" {
				absorbed[label] = struct{}{}
			}
		}
	}

	out := make([]any, 0, len(components))
	for _, raw := range components {
		c, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := c["id"].(string)
		if _, skip := absorbed[id]; skip {
			continue
		}
		if newID, ok := renamed[id]; ok {
			id = newID
		}
		if translated := v.translateComponent(id, c, byID); translated != nil {
			out = append(out, translated)
		}
	}
	return out
}

// translateComponent は 1 つの部品を書き換える。役割に部品が割り当てられていなければ
// nil（adapt が先に置き換えているので、通常は起きない）。
func (v *a2uiVocabulary) translateComponent(
	id string, c map[string]any, byID map[string]map[string]any,
) map[string]any {
	role, _ := c["component"].(string)
	spec, ok := v.components[role]
	if !ok {
		return nil
	}
	translated := map[string]any{"id": id, "component": spec.component}
	for prop, value := range c {
		switch prop {
		case "id", "component":
			continue
		case "@checks":
			if checks := v.supportedChecks(value); len(checks) > 0 {
				translated[prop] = checks
			}
			continue
		}
		actual, mapped := spec.props[prop]
		if !mapped {
			if role == "Button" && prop == "child" {
				// ラベルの部品の文字列を、ボタンの文字列のプロパティへ移す。
				label, _ := value.(string)
				translated[spec.props["label"]] = byID[label]["text"]
			}
			continue
		}
		if enum, limited := spec.enums[prop]; limited && !slices.Contains(enum, value) {
			continue
		}
		translated[actual] = value
	}
	return translated
}

// supportedChecks はチェックのうち、カタログが関数を定義しているものを返す。
func (v *a2uiVocabulary) supportedChecks(value any) []any {
	checks, _ := value.([]any)
	var out []any
	for _, check := range checks {
		rule, _ := check.(map[string]any)
		condition, _ := rule["condition"].(map[string]any)
		name, _ := condition["@call"].(string)
		if _, ok := v.functions[name]; ok {
			out = append(out, check)
		}
	}
	return out
}
