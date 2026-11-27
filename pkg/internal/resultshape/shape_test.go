package resultshape

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// describe は JSON Schema の形を、十分に大きい上限で一覧にする。
func describe(t *testing.T, schema string) string {
	t.Helper()
	return describeWithin(t, schema, 1<<20)
}

func describeWithin(t *testing.T, schema string, limit int) string {
	t.Helper()
	var v any
	require.NoError(t, json.Unmarshal([]byte(schema), &v))
	node, ok := FromJSONSchema(v)
	require.True(t, ok)
	return node.Describe(limit)
}

func TestFromJSONSchema_Describe(t *testing.T) {
	tests := []struct {
		name   string
		schema string
		want   string
	}{
		{
			name: "object fields are listed in name order",
			schema: `{"type": "object", "properties": {
				"name": {"type": "string"}, "id": {"type": "integer"}}}`,
			want: "id, name",
		},
		{
			name: "nested objects and arrays",
			schema: `{"type": "object", "properties": {
				"total": {"type": "integer"},
				"items": {"type": "array", "items": {"type": "object", "properties": {
					"id": {}, "owner": {"type": "object", "properties": {"login": {}, "id": {}}},
					"labels": {"type": "array", "items": {"type": "string"}}}}}}}`,
			want: "items[]{id, labels[], owner{id, login}}, total",
		},
		{
			name: "a root array",
			schema: `{"type": "array", "items": {"type": "object",
				"properties": {"id": {}, "name": {}}}}`,
			want: "[]{id, name}",
		},
		{
			name: "nested arrays",
			schema: `{"type": "object", "properties": {"matrix": {"type": "array",
				"items": {"type": "array", "items": {"type": "object", "properties": {"x": {}}}}}}}`,
			want: "matrix[][]{x}",
		},
		{
			name: "names that are not identifiers are quoted the way an expression writes them",
			schema: `{"properties": {"x-rate": {}, "a, b": {}, "ok_1": {}, "": {},
				"日本語": {"properties": {"1st": {}}}}}`,
			want: `"", "a, b", ok_1, "x-rate", "日本語"{"1st"}`,
		},
		{
			name: "$ref into $defs and definitions",
			schema: `{"type": "object",
				"properties": {"pet": {"$ref": "#/$defs/Pet"}, "owner": {"$ref": "#/definitions/a~1b"}},
				"$defs": {"Pet": {"type": "object", "properties": {"id": {}, "name": {}}}},
				"definitions": {"a/b": {"properties": {"login": {}}}}}`,
			want: "owner{login}, pet{id, name}",
		},
		{
			name: "allOf, anyOf and oneOf are read together",
			schema: `{"allOf": [{"properties": {"id": {}}}, {"$ref": "#/$defs/Named"}],
				"properties": {"owner": {"anyOf": [{"$ref": "#/$defs/Named"}, {"type": "null"}]},
					"target": {"oneOf": [{"properties": {"url": {}}}, {"properties": {"path": {}}}]}},
				"$defs": {"Named": {"properties": {"name": {}}}}}`,
			want: "id, name, owner{name}, target{path, url}",
		},
		{
			name: "a nullable array",
			schema: `{"properties": {"tags": {"anyOf": [
				{"type": "array", "items": {"properties": {"name": {}}}}, {"type": "null"}]}}}`,
			want: "tags[]{name}",
		},
		{
			name: "a recursive schema stops where it comes back to itself",
			schema: `{"$ref": "#/$defs/Node", "$defs": {"Node": {"properties": {
				"value": {}, "next": {"$ref": "#/$defs/Node"},
				"children": {"type": "array", "items": {"$ref": "#/$defs/Node"}}}}}}`,
			want: "children[]{...}, next{...}, value",
		},
		{
			name:   "a schema that points at the root",
			schema: `{"properties": {"name": {}, "parent": {"$ref": "#"}}}`,
			want:   "name, parent{...}",
		},
		{
			name: "mutually recursive schemas",
			schema: `{"properties": {"a": {"$ref": "#/$defs/A"}}, "$defs": {
				"A": {"properties": {"b": {"$ref": "#/$defs/B"}, "x": {}}},
				"B": {"properties": {"a": {"$ref": "#/$defs/A"}, "y": {}}}}}`,
			want: "a{b{a{...}, y}, x}",
		},
		{
			name: "a field of an inherited type is not a cycle, a field of its own type is",
			schema: `{"$ref": "#/$defs/Employee", "$defs": {
				"Person": {"properties": {"name": {}, "spouse": {"$ref": "#/$defs/Person"}}},
				"Employee": {"allOf": [{"$ref": "#/$defs/Person"}],
					"properties": {"manager": {"$ref": "#/$defs/Person"},
						"reports": {"type": "array", "items": {"$ref": "#/$defs/Employee"}}}}}}`,
			want: "manager{name, spouse{...}}, name, reports[]{...}, spouse{...}",
		},
		{
			name: "schemas that include each other",
			schema: `{"$ref": "#/$defs/A", "$defs": {
				"A": {"allOf": [{"$ref": "#/$defs/B"}], "properties": {"a": {}}},
				"B": {"allOf": [{"$ref": "#/$defs/A"}], "properties": {"b": {}}}}}`,
			want: "a, b, ...",
		},
		{
			name: "the same schema used twice side by side is not a cycle",
			schema: `{"properties": {"from": {"$ref": "#/$defs/User"}, "to": {"$ref": "#/$defs/User"}},
				"$defs": {"User": {"properties": {"id": {}}}}}`,
			want: "from{id}, to{id}",
		},
		{
			name: "references that cannot be followed are leaves",
			schema: `{"properties": {
				"external": {"$ref": "https://example.com/schema.json"},
				"missing": {"$ref": "#/$defs/Nope"}, "odd": {"$ref": "#$defs"},
				"scalar": {"$ref": "#/properties/scalar/type"},
				"indexed": {"$ref": "#/allOf/0"}, "past": {"$ref": "#/allOf/7"}},
				"allOf": [{"properties": {"base": {}}}]}`,
			want: "base, external, indexed{base}, missing, odd, past, scalar",
		},
		{
			name: "schemas without fields",
			schema: `{"properties": {"free": {"type": "object"}, "any": true, "never": false,
				"list": {"type": "array"}, "tuple": {"type": "array", "items": true}}}`,
			want: "any, free, list, never, tuple",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, describe(t, tt.schema))
		})
	}
}

// フィールドが分からない形は、一覧にしない。
func TestFromJSONSchema_NothingToDescribe(t *testing.T) {
	for _, schema := range []string{
		`{}`, `{"type": "object"}`, `{"type": "string"}`, `{"type": "array"}`,
		`{"type": "array", "items": {"type": "string"}}`,
		`{"type": "object", "properties": {}}`, `{"properties": "broken", "items": 1}`,
		`{"$ref": "#"}`,
	} {
		require.Empty(t, describe(t, schema), schema)
	}
	for _, schema := range []any{nil, true, "text", 1, []any{}, make(chan int)} {
		_, ok := FromJSONSchema(schema)
		require.False(t, ok, "%v", schema)
	}
}

// map[string]any でないスキーマ（SDK の型や json.RawMessage）も読める。
func TestFromJSONSchema_AcceptsAnyJSONObject(t *testing.T) {
	raw := json.RawMessage(`{"properties": {"id": {}}}`)
	node, ok := FromJSONSchema(raw)
	require.True(t, ok)
	require.Equal(t, "id", node.Describe(100))

	typed := struct {
		Properties map[string]struct{} `json:"properties"`
	}{Properties: map[string]struct{}{"name": {}}}
	node, ok = FromJSONSchema(typed)
	require.True(t, ok)
	require.Equal(t, "name", node.Describe(100))
}

// 一覧は上限に収める。浅い階層を先に載せ、収まらない階層は ... にする。
func TestDescribe_Limit(t *testing.T) {
	const schema = `{"properties": {
		"count": {},
		"items": {"type": "array", "items": {"properties": {
			"id": {}, "title": {},
			"owner": {"properties": {"id": {}, "login": {},
				"profile": {"properties": {"biography": {}}}}}}}},
		"meta": {"properties": {"next": {}, "prev": {}}}}}`
	const full = "count, items[]{id, owner{id, login, profile{biography}}, title}, meta{next, prev}"
	tests := []struct {
		limit int
		want  string
	}{
		{1000, full},
		{len(full), full},
		// 最も深い階層から省く。
		{
			len(full) - 1,
			"count, items[]{id, owner{id, login, profile{...}}, title}, meta{next, prev}",
		},
		{75, "count, items[]{id, owner{id, login, profile{...}}, title}, meta{next, prev}"},
		{74, "count, items[]{id, owner{...}, title}, meta{next, prev}"},
		{55, "count, items[]{id, owner{...}, title}, meta{next, prev}"},
		// 同じ深さでは先のものから開き、収まらないものは飛ばして、収まるものを開く。
		{54, "count, items[]{id, owner{...}, title}, meta{...}"},
		{48, "count, items[]{id, owner{...}, title}, meta{...}"},
		{47, "count, items[]{...}, meta{next, prev}"},
		{37, "count, items[]{...}, meta{next, prev}"},
		{36, "count, items[]{...}, meta{...}"},
		{30, "count, items[]{...}, meta{...}"},
		// ルートのフィールドも収まらなければ、収まるところで切る。
		{29, "count, items[]{...}, ..."},
		{24, "count, items[]{...}, ..."},
		{23, "count, ..."},
		{10, "count, ..."},
		{9, ""},
		{0, ""},
		{-1, ""},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.limit), func(t *testing.T) {
			got := describeWithin(t, schema, tt.limit)
			require.Equal(t, tt.want, got)
			require.LessOrEqual(t, len(got), max(tt.limit, 0))
		})
	}

	// ルートが配列のときは、[]{} の分も数える。
	const array = `{"type": "array", "items": {"properties": {"id": {}, "name": {}}}}`
	require.Equal(t, "[]{id, name}", describeWithin(t, array, 12))
	require.Equal(t, "[]{id, ...}", describeWithin(t, array, 11))
	require.Empty(t, describeWithin(t, array, 10))
}

// 長すぎる名前は載せずに省く。
func TestDescribe_SkipsLongNames(t *testing.T) {
	long := strings.Repeat("k", maxNameBytes+1)
	schema := `{"properties": {"id": {}, "` + long + `": {}, "nested": {"properties": {"` +
		long + `": {}}}}}`
	require.Equal(t, "id, nested{...}, ...", describe(t, schema))
}

// 外から来るスキーマは、深さと読む数に上限がある。
func TestBuild_Limits(t *testing.T) {
	t.Run("depth", func(t *testing.T) {
		schema := `{"properties": {"leaf": {}}}`
		for range maxDepth + 3 {
			schema = `{"properties": {"n": ` + schema + `}}`
		}
		want := strings.Repeat("n{", maxDepth) + "..." + strings.Repeat("}", maxDepth)
		require.Equal(t, want, describe(t, schema))
	})
	t.Run("deeply nested arrays", func(t *testing.T) {
		schema := `{"properties": {"x": {}}}`
		for range maxSchemas + 10 {
			schema = `{"type": "array", "items": ` + schema + `}`
		}
		var v any
		require.NoError(t, json.Unmarshal([]byte(schema), &v))
		node, ok := FromJSONSchema(v)
		require.True(t, ok)
		require.True(t, node.Omitted)
		require.Empty(t, node.Fields)
	})
	t.Run("number of schemas", func(t *testing.T) {
		// 各階層で 10 個のフィールドが同じ形を持つので、上限が無ければ 10^6 個を読む。
		schema := `{}`
		for range maxDepth {
			props := make([]string, 0, 10)
			for i := range 10 {
				props = append(props, fmt.Sprintf(`"f%d": %s`, i, schema))
			}
			schema = `{"properties": {` + strings.Join(props, ",") + `}}`
		}
		var v any
		require.NoError(t, json.Unmarshal([]byte(schema), &v))
		node, ok := FromJSONSchema(v)
		require.True(t, ok)
		require.LessOrEqual(t, countNodes(node), maxSchemas)
		require.Contains(t, node.Describe(1<<20), "...")
	})
	t.Run("many properties", func(t *testing.T) {
		props := make([]string, 0, maxSchemas*2)
		for i := range maxSchemas * 2 {
			props = append(props, fmt.Sprintf(`"f%05d": {}`, i))
		}
		got := describeWithin(t, `{"properties": {`+strings.Join(props, ",")+`}}`, 64)
		require.Equal(t, "f00000, f00001, f00002, f00003, f00004, f00005, f00006, ...", got)
	})
}

func countNodes(n Node) int {
	total := 1
	for _, f := range n.Fields {
		total += countNodes(f.Node)
	}
	return total
}

// スキーマは外から来る（MCP バックエンドの outputSchema）。どんな JSON でも panic せず、
// 一覧は上限に収まる。
func FuzzFromJSONSchema(f *testing.F) {
	for _, seed := range []string{
		`{"properties": {"a": {"$ref": "#"}, "b": {"items": {"$ref": "#/properties/b"}}}}`,
		`{"allOf": [{"$ref": "#/$defs/A"}], "$defs": {"A": {"anyOf": [{"$ref": "#"}], "properties": {"x": {}}}}}`,
		`{"$ref": "#/a/0/~1~0", "a": [{"/~": {"properties": {"": true, "x-y": 1}}}]}`,
		`{"properties": [], "items": {"items": {"items": {"properties": {"p": null}}}}}`,
		`[1, 2]`, `"text"`, `null`,
	} {
		f.Add(seed, 64)
	}
	f.Fuzz(func(t *testing.T, schema string, limit int) {
		var v any
		if json.Unmarshal([]byte(schema), &v) != nil {
			return
		}
		node, ok := FromJSONSchema(v)
		if !ok {
			return
		}
		limit %= 4096
		if got := node.Describe(limit); len(got) > max(limit, 0) {
			t.Fatalf("Describe(%d) returned %d bytes: %s", limit, len(got), got)
		}
		if countNodes(node) > maxSchemas+1 {
			t.Fatalf("read %d nodes", countNodes(node))
		}
	})
}
