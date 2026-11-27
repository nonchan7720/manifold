package resultshape

import (
	"encoding/json"
	"strconv"
	"strings"
)

// FromJSONSchema は JSON Schema（MCP のツールの outputSchema）から形を作る。schema は
// map[string]any か、JSON にすればオブジェクトになる値。スキーマとして読めなければ false。
//
// $ref は同じ文書の中を指すもの（"#/$defs/Pet" など）だけをたどる。
func FromJSONSchema(schema any) (Node, bool) {
	root, ok := schema.(map[string]any)
	if !ok {
		raw, err := json.Marshal(schema)
		if err != nil || json.Unmarshal(raw, &root) != nil || root == nil {
			return Node{}, false
		}
	}
	return Build(jsonSchema{root: root, schema: root, ref: "#"}), true
}

// jsonSchema は JSON Schema を Schema として見せるアダプタ。
type jsonSchema struct {
	// root は $ref を解決する文書。
	root map[string]any
	// schema はこの位置のスキーマ。
	schema map[string]any
	// ref は、この位置を指した $ref。インラインのスキーマでは空。
	ref string
}

func (s jsonSchema) ID() any {
	if s.ref == "" {
		// インラインのスキーマは木なので、循環しない。
		return nil
	}
	return s.ref
}

func (s jsonSchema) Alternatives() []Schema {
	var out []Schema
	if ref, ok := s.schema["$ref"].(string); ok {
		if target, ok := resolvePointer(s.root, ref); ok {
			out = append(out, jsonSchema{root: s.root, schema: target, ref: ref})
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf"} {
		branches, _ := s.schema[key].([]any)
		for _, branch := range branches {
			if child, ok := s.child(branch); ok {
				out = append(out, child)
			}
		}
	}
	return out
}

func (s jsonSchema) Properties() []string {
	props, _ := s.schema["properties"].(map[string]any)
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	return names
}

func (s jsonSchema) Property(name string) (Schema, bool) {
	props, _ := s.schema["properties"].(map[string]any)
	return s.child(props[name])
}

func (s jsonSchema) Items() (Schema, bool) { return s.child(s.schema["items"]) }

func (s jsonSchema) child(v any) (Schema, bool) {
	schema, ok := v.(map[string]any)
	if !ok {
		// true / false のスキーマには、読めるフィールドが無い。
		return nil, false
	}
	return jsonSchema{root: s.root, schema: schema}, true
}

// pointerToken は JSON Pointer の参照トークンのエスケープを復号する。
var pointerToken = strings.NewReplacer("~1", "/", "~0", "~")

// resolvePointer は ref（"#" か "#/..." の形の $ref）が root の中で指すスキーマを返す。
func resolvePointer(root map[string]any, ref string) (map[string]any, bool) {
	pointer, ok := strings.CutPrefix(ref, "#")
	if !ok || (pointer != "" && !strings.HasPrefix(pointer, "/")) {
		return nil, false
	}
	var cur any = root
	if pointer != "" {
		for token := range strings.SplitSeq(pointer[1:], "/") {
			token = pointerToken.Replace(token)
			switch v := cur.(type) {
			case map[string]any:
				cur = v[token]
			case []any:
				i, err := strconv.Atoi(token)
				if err != nil || i < 0 || i >= len(v) {
					return nil, false
				}
				cur = v[i]
			default:
				return nil, false
			}
		}
	}
	schema, ok := cur.(map[string]any)
	return schema, ok
}
