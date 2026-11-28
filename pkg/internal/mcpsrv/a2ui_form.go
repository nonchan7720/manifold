package mcpsrv

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// 書き込み系ツールの入力スキーマから A2UI（v1.0）のフォームを組み立てる。
//
// 入力スキーマ（OpenAPI から生成した JSON Schema）を a2uiField の木に直し、
// そこから次の 3 つを導出する。どれも LLM を使わない決定的な変換。
//
//   - template: a2ui:// リソースとして配信するレイアウト（createSurface +
//     updateComponents）。値はデータモデルの /input/... に束縛する。
//   - initialData: モデルが渡した引数をフォームの初期値に直したデータモデル。
//   - coerce: フォームが送ってきた値（数値も文字列で届く等）を、入力スキーマの
//     型に戻す。
//
// spec はゲートウェイの外から来るので、想定外の形のスキーマでも panic せず、
// 表現できないものは JSON を書くテキスト欄へ落とす。

const (
	// a2uiProtocolVersion は生成するメッセージの A2UI のバージョン。
	a2uiProtocolVersion = "1.0"
	// a2uiMIMEType は A2UI メッセージの MIME タイプ（v1.0）。
	a2uiMIMEType = "application/a2ui+json"
	// a2uiBasicCatalogID はフォームが使う basic カタログ。
	a2uiBasicCatalogID = "https://a2ui.org/specification/v1_0/catalogs/basic/catalog.json"

	// a2uiInputPath はフォームの入力値を置くデータモデルのパス。
	a2uiInputPath = "/input"

	// maxA2UIFormFields は 1 つのフォームに並べるフィールド数の上限。超える
	// スキーマにはフォームを付けない（巨大な spec でレスポンスを膨らませない）。
	maxA2UIFormFields = 200
	// maxA2UIFormDepth はオブジェクトを入れ子のまま展開する深さの上限。これより
	// 深いオブジェクトは JSON のテキスト欄にする。
	maxA2UIFormDepth = 4
	// a2uiListDefaultSlots は配列の入力欄の既定の行数。
	a2uiListDefaultSlots = 3
	// a2uiListSpareSlots は、既に値のある配列に足す空の行数。
	a2uiListSpareSlots = 2
	// a2uiListMaxSlots は配列の入力欄の行数の上限。
	a2uiListMaxSlots = 20
	// maxA2UIFormTextRunes はフォームに載せる説明文の長さの上限。
	maxA2UIFormTextRunes = 300
	// maxA2UIRegexRunes は A2UI の regex 関数が受け付けるパターン長の上限。
	maxA2UIRegexRunes = 256
)

type a2uiFieldKind int

const (
	a2uiFieldText a2uiFieldKind = iota
	a2uiFieldInteger
	a2uiFieldNumber
	a2uiFieldBool
	a2uiFieldChoice
	a2uiFieldMultiChoice
	a2uiFieldDate
	a2uiFieldDateTime
	// a2uiFieldJSON は専用の部品で表せない値。JSON を書くテキスト欄にする。
	a2uiFieldJSON
	// a2uiFieldGroup は properties を持つオブジェクト。子フィールドを並べる。
	a2uiFieldGroup
	// a2uiFieldList は配列。要素ごとの入力欄を slots 行ぶん並べる。
	a2uiFieldList
)

// a2uiField は入力スキーマの 1 プロパティに対応するフォームの項目。
type a2uiField struct {
	// name は入力スキーマのプロパティ名。
	name string
	// key はデータモデル上のキー。プロパティ名が英数字と "_" だけならそのまま、
	// それ以外（"X-Request-Id" など）は置き換えた別名にする。Express 記法のデータ
	// パス（$/input/...）に書ける文字がそれだけのため。送信時に name へ戻す
	// （a2uiCoerceInput）。
	key         string
	label       string
	description string
	kind        a2uiFieldKind
	required    bool
	// typed は JSON のテキスト欄で、スキーマが object / array と明示しているか。
	// true なら送信時に JSON として解釈できない値をエラーにする。
	typed    bool
	secret   bool
	options  []string
	schema   map[string]any
	children []*a2uiField
	// item は配列（a2uiFieldList）の要素 1 つぶんの項目。スカラーか、properties を
	// 持つオブジェクト（a2uiFieldGroup）。要素の中の項目は必須として扱わない
	// （空の行は「要素なし」を表すので、必須チェックで送信を止めない）。
	item *a2uiField
	// asMap は、配列（a2uiFieldList）の形で入力させる自由形式のオブジェクトかどうか。
	// 行は key と value の組で、送信時にオブジェクトへ戻す。
	asMap bool
	// slots は配列の入力欄の行数。basic カタログにはデータモデルの配列へ行を足す
	// 関数が無いので、行は固定数を並べる。空のままの行は送信時に取り除く。
	slots int
}

// buildA2UIFields は入力スキーマをフォームの項目に直す。スキーマがオブジェクトで
// ない、または項目が多すぎる場合は false を返す（フォームを付けない）。
func buildA2UIFields(inputSchema any) ([]*a2uiField, bool) {
	schema, ok := asSchemaObject(inputSchema)
	if !ok {
		return nil, false
	}
	if t := schemaType(schema); t != "" && t != "object" {
		return nil, false
	}
	budget := maxA2UIFormFields
	fields := buildA2UIChildren(schema, 1, &budget)
	if budget < 0 {
		return nil, false
	}
	return fields, true
}

// asSchemaObject は v を map[string]any として返す。map でなければ JSON を
// 経由して直す（json.RawMessage や型付きのスキーマ）。
func asSchemaObject(v any) (map[string]any, bool) {
	switch s := v.(type) {
	case nil:
		return nil, false
	case map[string]any:
		return s, true
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}

// schemaType はスキーマの type を返す。["string", "null"] のような配列は null を
// 除いて 1 つに決まるときだけその型を返す。決まらなければ空文字。
func schemaType(schema map[string]any) string {
	switch t := schema["type"].(type) {
	case string:
		return t
	case []any:
		found := ""
		for _, v := range t {
			s, ok := v.(string)
			if !ok || s == "null" {
				continue
			}
			if found != "" {
				return ""
			}
			found = s
		}
		return found
	}
	return ""
}

// stringEnum は enum が空でない文字列だけから成るときにその値を返す。
func stringEnum(schema map[string]any) ([]string, bool) {
	raw, ok := schema["enum"].([]any)
	if !ok || len(raw) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok || s == "" {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// buildA2UIChildren は schema の properties を項目に直す。必須の項目を required の
// 並びで先に、残りを名前順に並べ、入れ子のオブジェクトは最後に回す。budget は残りの項目数で、負になったら打ち切る。
func buildA2UIChildren(schema map[string]any, depth int, budget *int) []*a2uiField {
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}
	required := map[string]bool{}
	var order []string
	if list, ok := schema["required"].([]any); ok {
		for _, v := range list {
			name, ok := v.(string)
			if !ok || required[name] {
				continue
			}
			if _, exists := props[name]; !exists {
				continue
			}
			required[name] = true
			order = append(order, name)
		}
	} else if list, ok := schema["required"].([]string); ok {
		for _, name := range list {
			if _, exists := props[name]; !exists || required[name] {
				continue
			}
			required[name] = true
			order = append(order, name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(props)) {
		if !required[name] {
			order = append(order, name)
		}
	}

	fields := make([]*a2uiField, 0, len(order))
	// そのまま使える名前を先に確保し、別名がそれらと重ならないようにする。
	usedKeys := make(map[string]struct{}, len(order))
	for _, name := range order {
		if a2uiSafeDataKey(name) == name {
			usedKeys[name] = struct{}{}
		}
	}
	for _, name := range order {
		*budget--
		if *budget < 0 {
			return fields
		}
		prop, _ := asSchemaObject(props[name])
		if prop == nil {
			prop = map[string]any{}
		}
		field := buildA2UIField(name, prop, required[name], depth, budget)
		field.key = a2uiDataKey(name, usedKeys)
		fields = append(fields, field)
	}
	// 入れ子のオブジェクトは同じ階層の他の項目の後ろに回す。グループには終わりの
	// 目印が無いので、後ろに続く項目がグループの一部に見えてしまうため。
	slices.SortStableFunc(fields, func(a, b *a2uiField) int {
		return cmp.Compare(boolToInt(a.isSection()), boolToInt(b.isSection()))
	})
	return fields
}

// isSection は f が見出しの下に複数の項目を並べるもの（オブジェクト、オブジェクトの
// 配列）かを返す。
func (f *a2uiField) isSection() bool {
	if f.kind == a2uiFieldList && f.item != nil {
		return f.item.kind == a2uiFieldGroup || f.item.kind == a2uiFieldList
	}
	return f.kind == a2uiFieldGroup
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func buildA2UIField(
	name string, prop map[string]any, required bool, depth int, budget *int,
) *a2uiField {
	f := &a2uiField{name: name, label: name, required: required, schema: prop}
	if title, ok := prop["title"].(string); ok && strings.TrimSpace(title) != "" {
		f.label = title
	}
	if desc, ok := prop["description"].(string); ok {
		f.description = truncateRunes(strings.TrimSpace(desc), maxA2UIFormTextRunes)
	}
	typ := schemaType(prop)
	if typ == "" {
		// oneOf / anyOf は、1 つに決まる型か、オブジェクトどうしをまとめた型として扱う。
		if resolved, ok := resolveA2UIUnion(prop); ok {
			return buildA2UIField(name, resolved, required, depth, budget)
		}
	}
	switch typ {
	case "boolean":
		f.kind = a2uiFieldBool
	case "integer":
		f.kind = a2uiFieldInteger
	case "number":
		f.kind = a2uiFieldNumber
	case "string":
		if options, ok := stringEnum(prop); ok {
			f.kind, f.options = a2uiFieldChoice, options
			break
		}
		switch format, _ := prop["format"].(string); format {
		case "date":
			f.kind = a2uiFieldDate
		case "date-time":
			f.kind = a2uiFieldDateTime
		case "password":
			f.kind, f.secret = a2uiFieldText, true
		default:
			f.kind = a2uiFieldText
		}
	case "array":
		f.setArrayKind(prop, depth, budget)
	case "object":
		f.kind, f.typed = a2uiFieldJSON, true
		if depth >= maxA2UIFormDepth {
			break
		}
		if props, _ := prop["properties"].(map[string]any); len(props) > 0 {
			f.kind = a2uiFieldGroup
			f.children = buildA2UIChildren(prop, depth+1, budget)
		} else {
			f.setMapKind(prop, depth, budget)
		}
	default:
		// type が無い・複数ある項目は、値をそのまま書けるテキスト欄にする。
		if options, ok := stringEnum(prop); ok {
			f.kind, f.options = a2uiFieldChoice, options
		} else {
			f.kind = a2uiFieldJSON
		}
	}
	return f
}

// setArrayKind は配列の項目の種類を決める。enum の文字列の配列は複数選択、要素を
// 入力欄にできる配列は行ごとの入力欄、それ以外は JSON のテキスト欄。
func (f *a2uiField) setArrayKind(prop map[string]any, depth int, budget *int) {
	f.kind, f.typed = a2uiFieldJSON, true
	items, ok := asSchemaObject(prop["items"])
	if !ok || depth >= maxA2UIFormDepth {
		return
	}
	if options, ok := stringEnum(items); ok && schemaType(items) == "string" {
		f.kind, f.options = a2uiFieldMultiChoice, options
		return
	}
	if item := buildA2UIListItem(items, depth, budget); item != nil {
		f.kind, f.item, f.slots = a2uiFieldList, item, a2uiListDefaultSlots
		// 残りの行のぶんも項目数に数える。
		*budget -= (f.slots - 1) * max(1, len(item.children))
	}
}

// setMapKind は properties を持たない自由形式のオブジェクトを、key と value の組を
// 行ごとに入力する形にする。value の型は additionalProperties がスカラーならそれに
// 従い、それ以外は型を決めないテキスト（数値や true / false はその型で送る）。
func (f *a2uiField) setMapKind(prop map[string]any, depth int, budget *int) {
	value := &a2uiField{
		name:   "value",
		label:  "value",
		kind:   a2uiFieldJSON,
		schema: map[string]any{},
	}
	if ap, ok := asSchemaObject(prop["additionalProperties"]); ok {
		typed := buildA2UIField("value", ap, false, depth+1, budget)
		switch typed.kind {
		case a2uiFieldText, a2uiFieldInteger, a2uiFieldNumber, a2uiFieldChoice,
			a2uiFieldDate, a2uiFieldDateTime:
			typed.label = "value"
			value = typed
		case a2uiFieldBool, a2uiFieldMultiChoice, a2uiFieldJSON, a2uiFieldGroup, a2uiFieldList:
		}
	}
	value.key = "value"
	key := &a2uiField{
		name:   "key",
		key:    "key",
		label:  "key",
		kind:   a2uiFieldText,
		schema: map[string]any{},
	}
	f.kind, f.typed, f.asMap = a2uiFieldList, false, true
	f.item = &a2uiField{
		kind:     a2uiFieldGroup,
		schema:   map[string]any{},
		children: []*a2uiField{key, value},
	}
	f.slots = a2uiListDefaultSlots
	*budget -= f.slots * 2
}

// resolveA2UIUnion は oneOf / anyOf を 1 つのスキーマに直す。null 以外の選択肢が
// 1 つならそれを、すべて properties を持つオブジェクトなら項目をまとめたオブジェクト
// （どれも必須にしない）を返す。それ以外（型の違うスカラーの組など）は false。
func resolveA2UIUnion(prop map[string]any) (map[string]any, bool) {
	var variants []map[string]any
	for _, key := range []string{"oneOf", "anyOf"} {
		list, _ := prop[key].([]any)
		for _, raw := range list {
			variant, ok := asSchemaObject(raw)
			if !ok || variant["type"] == "null" {
				continue
			}
			variants = append(variants, variant)
		}
	}
	if len(variants) == 0 {
		return nil, false
	}
	resolved := map[string]any{}
	if len(variants) == 1 {
		maps.Copy(resolved, variants[0])
	} else {
		properties := map[string]any{}
		for _, variant := range variants {
			props, _ := variant["properties"].(map[string]any)
			if schemaType(variant) != "object" || len(props) == 0 {
				return nil, false
			}
			for name, schema := range props {
				if _, exists := properties[name]; !exists {
					properties[name] = schema
				}
			}
		}
		resolved["type"], resolved["properties"] = "object", properties
	}
	// 説明と表題は、外側（選択肢を束ねる側）のものを優先する。
	for _, key := range []string{"title", "description", "default"} {
		if v, ok := prop[key]; ok {
			resolved[key] = v
		}
	}
	// 選択肢がさらに oneOf / anyOf だけを持つ場合の無限再帰を避ける。
	if schemaType(resolved) == "" {
		delete(resolved, "oneOf")
		delete(resolved, "anyOf")
	}
	return resolved, true
}

// buildA2UIListItem は配列の要素 1 つぶんの項目を返す。要素がスカラー（文字列・
// 数値・単一選択・日付・型を決めないテキスト）、properties を持つオブジェクト、
// 配列、自由形式のオブジェクトのときに入力欄にできる。それ以外は nil。
func buildA2UIListItem(items map[string]any, depth int, budget *int) *a2uiField {
	item := buildA2UIField("", items, false, depth+1, budget)
	switch item.kind {
	case a2uiFieldText, a2uiFieldInteger, a2uiFieldNumber, a2uiFieldChoice,
		a2uiFieldDate, a2uiFieldDateTime, a2uiFieldGroup, a2uiFieldList:
		clearA2UIRequired(item)
		return item
	case a2uiFieldJSON:
		if !item.typed {
			clearA2UIRequired(item)
			return item
		}
	case a2uiFieldBool, a2uiFieldMultiChoice:
	}
	return nil
}

// clearA2UIRequired は f とその子孫を必須でなくする。
func clearA2UIRequired(f *a2uiField) {
	f.required = false
	for _, child := range f.children {
		clearA2UIRequired(child)
	}
	if f.item != nil {
		clearA2UIRequired(f.item)
	}
}

// a2uiSizeLists は args（モデルが渡した引数）に合わせて配列の入力欄の行数を決める。
// 既にある要素をすべて表示し、その後ろに空の行を足す。配列の要素の中にある配列は
// 既定の行数のまま。
func a2uiSizeLists(fields []*a2uiField, args map[string]any) {
	for _, f := range fields {
		switch v := args[f.name].(type) {
		case map[string]any:
			if f.kind == a2uiFieldGroup {
				a2uiSizeLists(f.children, v)
			} else if f.kind == a2uiFieldList && f.asMap {
				f.slots = min(
					max(a2uiListDefaultSlots, len(v)+a2uiListSpareSlots),
					a2uiListMaxSlots,
				)
			}
		case []any:
			if f.kind == a2uiFieldList {
				f.slots = min(
					max(a2uiListDefaultSlots, len(v)+a2uiListSpareSlots),
					a2uiListMaxSlots,
				)
			}
		}
	}
}

func truncateRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return string(runes[:limit]) + "…"
}

// a2uiSafeDataKey は name の英数字と "_" 以外を "_" に置き換える。数字だけの名前
// （データパスでは配列の添字と解釈される）と空の名前には "_" を前置する。
func a2uiSafeDataKey(name string) string {
	var b strings.Builder
	digitsOnly := true
	for _, r := range name {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_':
			b.WriteRune(r)
			digitsOnly = false
		default:
			b.WriteByte('_')
			digitsOnly = false
		}
	}
	if digitsOnly {
		return "_" + b.String()
	}
	return b.String()
}

// a2uiDataKey は name のデータモデル上のキーを返す。そのまま使える名前はそのまま、
// それ以外は used（同じ階層で使用済みのキー）と重ならない別名にして used に記録する。
func a2uiDataKey(name string, used map[string]struct{}) string {
	base := a2uiSafeDataKey(name)
	if base == name {
		return name
	}
	key := base
	for i := 2; ; i++ {
		if _, taken := used[key]; !taken {
			break
		}
		key = base + "_" + strconv.Itoa(i)
	}
	used[key] = struct{}{}
	return key
}

// a2uiFormSpec はフォームのテンプレートを組み立てるための入力。
type a2uiFormSpec struct {
	surfaceID   string
	title       string
	description string
	// submitTool は送信用ツールの名前。送信ボタンが発火するイベントの名前になる。
	submitTool string
	fields     []*a2uiField
	// vocab はエージェントのカタログに合わせた部品の語彙。nil は basic カタログ。
	vocab *a2uiVocabulary
}

// a2uiTemplateBuilder はテンプレートのコンポーネントを貯める。コンポーネント ID は
// UAX #31 の識別子でなければならないため、プロパティ名からは作らず連番にする。
type a2uiTemplateBuilder struct {
	components []any
	seq        int
	// requiredChecks は必須項目の required チェック。送信ボタンにも付けて、
	// 未入力のまま送信できないようにする。
	requiredChecks []any
}

func (b *a2uiTemplateBuilder) newID(prefix string) string {
	b.seq++
	return prefix + "_" + strconv.Itoa(b.seq)
}

func (b *a2uiTemplateBuilder) add(component map[string]any) string {
	id, _ := component["id"].(string)
	b.components = append(b.components, component)
	return id
}

func (b *a2uiTemplateBuilder) text(text, variant string) string {
	c := map[string]any{"id": b.newID("text"), "component": "Text", "text": text}
	if variant != "" {
		c["variant"] = variant
	}
	return b.add(c)
}

func a2uiPath(path string) map[string]any {
	return map[string]any{"@path": path}
}

func a2uiCall(name string, args map[string]any) map[string]any {
	return map[string]any{"@call": name, "args": args}
}

func a2uiCheck(condition map[string]any, message string) map[string]any {
	return map[string]any{"condition": condition, "message": message}
}

// buildA2UIFormTemplate はフォームのレイアウト（createSurface と
// updateComponents）を A2UI メッセージの配列として返す。
func buildA2UIFormTemplate(spec a2uiFormSpec) []any {
	return []any{
		map[string]any{
			"version": a2uiProtocolVersion,
			"createSurface": map[string]any{
				"surfaceId": spec.surfaceID,
				"catalogId": spec.vocab.id(),
			},
		},
		map[string]any{
			"version": a2uiProtocolVersion,
			"updateComponents": map[string]any{
				"surfaceId":  spec.surfaceID,
				"components": buildA2UIFormComponents(spec),
			},
		},
	}
}

// buildA2UIFormComponents はフォームのコンポーネントの一覧を返す。
func buildA2UIFormComponents(spec a2uiFormSpec) []any {
	b := &a2uiTemplateBuilder{}
	children := []any{b.text("### "+spec.title, "")}
	if spec.description != "" {
		children = append(children, b.text(spec.description, "caption"))
	}
	children = append(children, b.fields(spec.fields, a2uiInputPath)...)

	arguments := map[string]any{}
	for _, f := range spec.fields {
		arguments[f.name] = a2uiPath(a2uiInputPath + "/" + f.key)
	}
	// 送信ボタンは A2UI の action（イベント）を発火するだけで、API は呼ばない。
	// イベント名を送信用ツールの名前に、context をその引数にしておくので、
	// 受け取ったエージェント側（ホスト）はそのまま tools/call に渡せる。
	submitText := b.text("Submit", "")
	submit := map[string]any{
		"id":        b.newID("submit"),
		"component": "Button",
		"child":     submitText,
		"variant":   "primary",
		"action": map[string]any{
			"event": map[string]any{
				"name":    spec.submitTool,
				"context": arguments,
			},
		},
	}
	if len(b.requiredChecks) > 0 {
		submit["@checks"] = b.requiredChecks
	}
	children = append(children, b.add(submit))

	column := b.add(map[string]any{
		"id": b.newID("column"), "component": "Column", "children": children,
	})
	b.add(map[string]any{"id": "root", "component": "Card", "child": column})
	// ここまでは basic カタログの語彙。エージェントのカタログの部品名・プロパティ名に
	// 書き換える。
	return spec.vocab.translate(b.components)
}

// fields は項目のコンポーネントを追加し、親の Column に並べる ID を返す。
func (b *a2uiTemplateBuilder) fields(fields []*a2uiField, basePath string) []any {
	var ids []any
	for _, f := range fields {
		path := basePath + "/" + f.key
		label := f.label
		if f.required {
			label += " *"
		}
		if f.kind == a2uiFieldGroup {
			// 必須の印は太字の外に置く（"**body ***" は太字として解釈されない）。
			heading := "**" + f.label + "**"
			if f.required {
				heading += " *"
			}
			ids = append(ids, b.text(heading, ""))
			if f.description != "" {
				ids = append(ids, b.text(f.description, "caption"))
			}
			ids = append(ids, b.fields(f.children, path)...)
			continue
		}
		if f.kind == a2uiFieldList {
			ids = append(ids, b.list(f, path)...)
			continue
		}
		ids = append(ids, b.add(b.input(f, label, path)))
		if f.description != "" {
			ids = append(ids, b.text(f.description, "caption"))
		}
	}
	return ids
}

// list は配列の入力欄を slots 行ぶん追加する。要素がオブジェクトなら行ごとに見出しを
// 付けて子の項目を並べ、配列ならその入力欄を入れ子にし、スカラーなら行ごとに 1 つの
// 入力欄を置く。値は配列の添字のパス（.../photoUrls/0）に束縛する。
func (b *a2uiTemplateBuilder) list(f *a2uiField, path string) []any {
	var ids []any
	if f.isSection() {
		heading := "**" + f.label + "**"
		if f.required {
			heading += " *"
		}
		ids = append(ids, b.text(heading, ""))
	}
	if f.description != "" {
		ids = append(ids, b.text(f.description, "caption"))
	}
	for i := range f.slots {
		itemPath := path + "/" + strconv.Itoa(i)
		row := fmt.Sprintf("%s #%d", f.label, i+1)
		switch f.item.kind {
		case a2uiFieldGroup:
			ids = append(ids, b.text(row, "caption"))
			ids = append(ids, b.fields(f.item.children, itemPath)...)
		case a2uiFieldList:
			nested := *f.item
			nested.label, nested.description = row, ""
			ids = append(ids, b.list(&nested, itemPath)...)
		case a2uiFieldText, a2uiFieldInteger, a2uiFieldNumber, a2uiFieldBool, a2uiFieldChoice,
			a2uiFieldMultiChoice, a2uiFieldDate, a2uiFieldDateTime, a2uiFieldJSON:
			// 必須の配列は、最初の行だけ必須にする（1 つ以上の要素が要る）。
			item := *f.item
			item.label = f.label
			item.required = f.required && i == 0
			label := fmt.Sprintf("%s %d", f.label, i+1)
			if item.required {
				label += " *"
			}
			ids = append(ids, b.add(b.input(&item, label, itemPath)))
		}
	}
	return ids
}

// input は 1 項目の入力コンポーネントを返す。
func (b *a2uiTemplateBuilder) input(f *a2uiField, label, path string) map[string]any {
	c := map[string]any{"id": b.newID("field"), "label": label, "value": a2uiPath(path)}
	var checks []any
	if f.required && f.kind != a2uiFieldBool {
		check := a2uiCheck(
			a2uiCall("required", map[string]any{"value": a2uiPath(path)}),
			f.label+" is required",
		)
		checks = append(checks, check)
		b.requiredChecks = append(b.requiredChecks, check)
	}
	switch f.kind {
	case a2uiFieldBool:
		c["component"] = "CheckBox"
	case a2uiFieldChoice, a2uiFieldMultiChoice:
		c["component"] = "ChoicePicker"
		c["variant"] = "mutuallyExclusive"
		if f.kind == a2uiFieldMultiChoice {
			c["variant"] = "multipleSelection"
		}
		options := make([]any, 0, len(f.options))
		for _, o := range f.options {
			options = append(options, map[string]any{"label": o, "value": o})
		}
		c["options"] = options
	case a2uiFieldDate, a2uiFieldDateTime:
		c["component"] = "DateTimeInput"
		c["enableDate"] = true
		if f.kind == a2uiFieldDateTime {
			c["enableTime"] = true
		}
	case a2uiFieldInteger, a2uiFieldNumber:
		c["component"] = "TextField"
		c["variant"] = "number"
		// TextField の値は文字列なので、数値の書式は regex で確かめる（空は許す。
		// 必須かどうかは required チェックが見る）。
		pattern, message := `^(-?[0-9]+(\.[0-9]+)?([eE][-+]?[0-9]+)?)?$`, " must be a number"
		if f.kind == a2uiFieldInteger {
			pattern, message = `^(-?[0-9]+)?$`, " must be an integer"
		}
		checks = append(checks, a2uiCheck(
			a2uiCall("regex", map[string]any{"value": a2uiPath(path), "pattern": pattern}),
			f.label+message,
		))
	case a2uiFieldJSON:
		c["component"] = "TextField"
		// 型を決めない項目は普通のテキスト欄。JSON を書かせるのは、深すぎる入れ子など
		// 入力欄に展開できなかったものだけ。
		if f.typed {
			c["variant"] = "longText"
			c["label"] = label + " (JSON)"
		}
	case a2uiFieldText, a2uiFieldGroup, a2uiFieldList:
		// グループと配列は fields が展開するので、ここへは来ない。
		c["component"] = "TextField"
		if f.secret {
			c["variant"] = "obscured"
		}
		checks = append(checks, textChecks(f, path)...)
	}
	if len(checks) > 0 {
		c["@checks"] = checks
	}
	return c
}

// textChecks は文字列の項目の maxLength・minLength・pattern を A2UI のチェックに
// 直す。minLength と pattern は空の値を弾いてしまうので、必須の項目にだけ付ける
// （任意の項目は API 側の検証に任せる）。
func textChecks(f *a2uiField, path string) []any {
	var checks []any
	lengthArgs := map[string]any{"value": a2uiPath(path)}
	message := ""
	if maxLen, ok := nonNegativeInt(f.schema["maxLength"]); ok {
		lengthArgs["max"] = maxLen
		message = fmt.Sprintf("%s must be at most %d characters", f.label, maxLen)
	}
	if minLen, ok := nonNegativeInt(f.schema["minLength"]); ok && minLen > 0 && f.required {
		lengthArgs["min"] = minLen
		if message == "" {
			message = fmt.Sprintf("%s must be at least %d characters", f.label, minLen)
		} else {
			message += fmt.Sprintf(" and at least %d", minLen)
		}
	}
	if len(lengthArgs) > 1 {
		checks = append(checks, a2uiCheck(a2uiCall("length", lengthArgs), message))
	}
	if pattern, ok := f.schema["pattern"].(string); ok && f.required &&
		a2uiRegexSupported(pattern) {
		checks = append(checks, a2uiCheck(
			a2uiCall("regex", map[string]any{"value": a2uiPath(path), "pattern": pattern}),
			f.label+" has an invalid format",
		))
	}
	return checks
}

func nonNegativeInt(v any) (int, bool) {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	case uint64:
		f = float64(n)
	case json.Number:
		parsed, err := n.Float64()
		if err != nil {
			return 0, false
		}
		f = parsed
	default:
		return 0, false
	}
	if f < 0 || f > 1<<31 || f != float64(int(f)) {
		return 0, false
	}
	return int(f), true
}

// a2uiRegexSupported は pattern が A2UI の regex 方言（ECMAScript の移植可能な
// 部分集合）で書けるかを返す。長すぎるもの、後方参照・後読み・名前付きグループ・
// \p{...} を含むものは付けない（レンダラがテンプレートごと拒否しないように）。
func a2uiRegexSupported(pattern string) bool {
	if pattern == "" || utf8.RuneCountInString(pattern) > maxA2UIRegexRunes {
		return false
	}
	if strings.Contains(pattern, "(?<") || strings.Contains(pattern, "(?P") {
		return false
	}
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '\\' {
			continue
		}
		i++
		if i >= len(pattern) {
			return false
		}
		switch c := pattern[i]; {
		case c >= '1' && c <= '9', c == 'p', c == 'P', c == 'k':
			return false
		}
	}
	return true
}

// a2uiInitialData は args（モデルが渡した引数）をフォームの初期値に直した
// データモデルを返す。値が無い項目は、スキーマの default か空の値で埋める
// （入力コンポーネントの束縛先が未定義にならないように）。
func a2uiInitialData(fields []*a2uiField, args map[string]any) map[string]any {
	return map[string]any{
		strings.TrimPrefix(a2uiInputPath, "/"): a2uiInitialValues(fields, args),
	}
}

func a2uiInitialValues(fields []*a2uiField, args map[string]any) map[string]any {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		v, ok := args[f.name]
		if !ok || v == nil {
			v = f.schema["default"]
		}
		out[f.key] = f.initialValue(v)
	}
	return out
}

func (f *a2uiField) initialValue(v any) any {
	switch f.kind {
	case a2uiFieldGroup:
		child, _ := v.(map[string]any)
		return a2uiInitialValues(f.children, child)
	case a2uiFieldList:
		return f.initialList(v)
	case a2uiFieldBool:
		b, _ := v.(bool)
		return b
	case a2uiFieldChoice:
		// 真偽値を true / false の選択で表している場合（a2uiVocabulary.adapt）。
		if b, ok := v.(bool); ok {
			return []any{strconv.FormatBool(b)}
		}
		if s, ok := v.(string); ok && s != "" {
			return []any{s}
		}
		return []any{}
	case a2uiFieldMultiChoice:
		out := []any{}
		if list, ok := v.([]any); ok {
			for _, item := range list {
				if s, ok := item.(string); ok {
					out = append(out, s)
				}
			}
		}
		return out
	case a2uiFieldJSON:
		switch s := v.(type) {
		case nil:
			return ""
		case string:
			if !f.typed {
				return s
			}
		}
		raw, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return ""
		}
		return string(raw)
	case a2uiFieldText, a2uiFieldInteger, a2uiFieldNumber, a2uiFieldDate, a2uiFieldDateTime:
		return scalarText(v)
	}
	return scalarText(v)
}

// initialList は配列の初期値を返す。行数ぶんの要素を用意し（束縛先の添字が未定義に
// ならないように）、行数を超える要素も落とさずに持つ。
func (f *a2uiField) initialList(v any) []any {
	list, _ := v.([]any)
	if object, ok := v.(map[string]any); ok && f.asMap {
		// オブジェクトを key と value の行に直す（キーの昇順）。
		for _, key := range slices.Sorted(maps.Keys(object)) {
			list = append(list, map[string]any{"key": key, "value": object[key]})
		}
	}
	out := make([]any, 0, max(f.slots, len(list)))
	for i := range max(f.slots, len(list)) {
		var element any
		if i < len(list) {
			element = list[i]
		}
		out = append(out, f.item.initialValue(element))
	}
	return out
}

// scalarText はテキスト欄に入れる文字列を返す。
func scalarText(v any) string {
	switch s := v.(type) {
	case nil:
		return ""
	case string:
		return s
	case json.Number:
		return s.String()
	case float64:
		return strconv.FormatFloat(s, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(s)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(raw)
}

// a2uiCoerceInput はフォームが送ってきた値を入力スキーマの型に直す。
//
// フォームでは数値もテキスト欄の文字列で、単一選択も文字列の配列で届く。空のままの
// 任意項目は送らない。データモデルのキーが別名の項目は、プロパティ名に戻す。既に正しい型の値（フォームを介さない呼び出し）と、スキーマに
// 無いキーはそのまま通す。
func a2uiCoerceInput(fields []*a2uiField, in map[string]any) (map[string]any, error) {
	return a2uiCoerceValues(fields, in, "")
}

func a2uiCoerceValues(
	fields []*a2uiField, in map[string]any, prefix string,
) (map[string]any, error) {
	out := maps.Clone(in)
	if out == nil {
		out = map[string]any{}
	}
	for _, f := range fields {
		// フォームはデータモデルのキー（別名）で送ってくる。プロパティ名で届いた値
		// （フォームを介さない呼び出し）を優先する。
		v, ok := in[f.name]
		if !ok && f.key != f.name {
			if v, ok = in[f.key]; ok {
				delete(out, f.key)
			}
		}
		if !ok {
			continue
		}
		coerced, keep, err := f.coerce(v, prefix+f.name)
		if err != nil {
			return nil, err
		}
		if !keep {
			delete(out, f.name)
			continue
		}
		out[f.name] = coerced
	}
	return out, nil
}

// coerce は 1 項目の値を直す。keep が false なら、その項目は送らない。
func (f *a2uiField) coerce(v any, path string) (_ any, keep bool, _ error) {
	if v == nil {
		return nil, false, nil
	}
	switch f.kind {
	case a2uiFieldGroup:
		return f.coerceGroup(v, path)
	case a2uiFieldList:
		return f.coerceList(v, path)
	case a2uiFieldInteger, a2uiFieldNumber:
		return f.coerceNumber(v, path)
	case a2uiFieldChoice:
		value, keep := f.coerceChoice(v)
		return f.boolFromText(value), keep, nil
	case a2uiFieldMultiChoice:
		if list, ok := v.([]any); ok {
			return list, len(list) > 0 || f.required, nil
		}
	case a2uiFieldJSON:
		return f.coerceJSON(v, path)
	case a2uiFieldText, a2uiFieldDate, a2uiFieldDateTime:
		// 空のままの任意項目は送らない。必須項目の空文字は API 側に判断させる。
		if s, ok := v.(string); ok {
			return f.boolFromText(s), s != "" || f.required, nil
		}
	case a2uiFieldBool:
	}
	return v, true, nil
}

// boolFromText は、真偽値の項目をテキストや選択で入力させた場合（カタログに
// チェックボックスが無いとき）の "true" / "false" を真偽値に戻す。
func (f *a2uiField) boolFromText(v any) any {
	if schemaType(f.schema) != "boolean" {
		return v
	}
	if s, ok := v.(string); ok {
		if b, err := strconv.ParseBool(strings.TrimSpace(s)); err == nil {
			return b
		}
	}
	return v
}

func (f *a2uiField) coerceGroup(v any, path string) (any, bool, error) {
	child, ok := v.(map[string]any)
	if !ok {
		return v, true, nil
	}
	coerced, err := a2uiCoerceValues(f.children, child, path+".")
	if err != nil {
		return nil, false, err
	}
	return coerced, len(coerced) > 0 || f.required, nil
}

// coerceList は配列の各要素を直し、空のままの行（要素）を取り除く。
func (f *a2uiField) coerceList(v any, path string) (any, bool, error) {
	if text, isText := v.(string); isText {
		// フォームを介さない呼び出しが JSON の文字列で渡してきた場合。空なら送らない。
		if strings.TrimSpace(text) == "" {
			return nil, false, nil
		}
		var parsed any
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		if err := dec.Decode(&parsed); err != nil || dec.More() {
			return nil, false, fmt.Errorf("%s: must be valid JSON", path)
		}
		v = parsed
	}
	list, ok := v.([]any)
	if !ok {
		// 既にオブジェクトで届いた自由形式のオブジェクトなどは、そのまま通す。
		return v, true, nil
	}
	out := make([]any, 0, len(list))
	for i, element := range list {
		coerced, keep, err := f.item.coerce(element, fmt.Sprintf("%s[%d]", path, i))
		if err != nil {
			return nil, false, err
		}
		if !keep || a2uiBlankValue(coerced) {
			continue
		}
		out = append(out, coerced)
	}
	if f.asMap {
		return f.rowsToObject(out)
	}
	return out, len(out) > 0 || f.required, nil
}

// rowsToObject は key と value の行をオブジェクトに戻す。key が空の行は捨てる。
func (f *a2uiField) rowsToObject(rows []any) (any, bool, error) {
	object := make(map[string]any, len(rows))
	for _, row := range rows {
		entry, _ := row.(map[string]any)
		key, _ := entry["key"].(string)
		if key == "" {
			continue
		}
		value, ok := entry["value"]
		if !ok {
			value = ""
		}
		object[key] = value
	}
	return object, len(object) > 0 || f.required, nil
}

// a2uiBlankValue は v が何も入力されていない要素かを返す。チェックボックスは未操作でも
// false を送ってくるので、false だけから成るオブジェクトは空とみなす。
func a2uiBlankValue(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	for _, value := range m {
		if b, isBool := value.(bool); isBool && !b {
			continue
		}
		if !a2uiBlankValue(value) {
			return false
		}
	}
	return true
}

// coerceNumber はテキスト欄の文字列を数値に直す。空なら送らない。
func (f *a2uiField) coerceNumber(v any, path string) (any, bool, error) {
	s, ok := v.(string)
	if !ok {
		return v, true, nil
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false, nil
	}
	if f.kind == a2uiFieldInteger {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, false, fmt.Errorf("%s: must be an integer", path)
		}
		return n, true, nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, false, fmt.Errorf("%s: must be a number", path)
	}
	return n, true, nil
}

// coerceChoice は単一選択の値（ChoicePicker は文字列の配列で持つ）を 1 つの値に直す。
func (f *a2uiField) coerceChoice(v any) (any, bool) {
	switch s := v.(type) {
	case []any:
		if len(s) == 0 {
			return nil, false
		}
		return s[0], true
	case string:
		return s, s != "" || f.required
	}
	return v, true
}

// coerceJSON はテキスト欄に書かれた JSON を値に直す。空なら送らない。
func (f *a2uiField) coerceJSON(v any, path string) (any, bool, error) {
	s, ok := v.(string)
	if !ok {
		return v, true, nil
	}
	if strings.TrimSpace(s) == "" {
		return nil, false, nil
	}
	var parsed any
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if err := dec.Decode(&parsed); err != nil || dec.More() {
		if f.typed {
			return nil, false, fmt.Errorf("%s: must be valid JSON", path)
		}
		// 型の分からない項目は、JSON でなければ文字列として送る。
		return s, true, nil
	}
	return parsed, true, nil
}
