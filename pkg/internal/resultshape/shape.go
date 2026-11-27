// Package resultshape は、ツールの結果の JSON の形（どんなフィールドがあるか）をスキーマ
// から読み取り、短い表記にする。結果の絞り込み（tools.resultFilter）で、呼び出し元が式に
// 書けるフィールドを tools/list から知るために使う。
//
// スキーマには外から来るもの（MCP バックエンドの outputSchema、取得した spec）もあるので、
// 読む量（深さ・スキーマの数）と、作る表記の長さに上限がある。
package resultshape

import (
	"maps"
	"slices"
)

// Node は結果の JSON の 1 か所（ルート、またはフィールドの値）の形。
type Node struct {
	// Arrays は値を包む配列の段数。0 なら配列ではない。
	Arrays int
	// Fields は値（配列ならその要素）がオブジェクトのときの、スキーマから分かるフィールド。
	// 名前順。
	Fields []Field
	// Omitted は、フィールドがあるのに読まなかったことを表す（深さや数の上限、循環する
	// 参照）。
	Omitted bool
}

// Field はオブジェクトのフィールド 1 つ。
type Field struct {
	Name string
	Node
}

// Schema は Build が読むスキーマの中立なビュー。OpenAPI のスキーマと JSON Schema とでは
// 型も $ref の解決方法も違うので、その差をアダプタに閉じ込める。
type Schema interface {
	// ID はスキーマの同一性を表す値。循環する参照を見つけるのに使う。map のキーにできる
	// 値（ポインタや文字列）か、nil（循環しないインラインのスキーマ）を返す。
	ID() any
	// Alternatives は、このスキーマと同じ位置に重ねて読むスキーマ（allOf / anyOf / oneOf の
	// 枝、$ref の先）を返す。
	Alternatives() []Schema
	// Properties はプロパティ名を返す（順不同）。
	Properties() []string
	// Property は name のプロパティのスキーマを返す。解決できなければ false。
	Property(name string) (Schema, bool)
	// Items は配列の要素のスキーマを返す。無いか解決できなければ false。
	Items() (Schema, bool)
}

const (
	// maxDepth は読むフィールドの入れ子の深さの上限。
	maxDepth = 6
	// maxSchemas は 1 つの形のために読むスキーマの数の上限。
	maxSchemas = 2000
)

// Build は root のスキーマから形を作る。
func Build(root Schema) Node {
	b := &builder{}
	return b.node(root, nil, 0)
}

type builder struct {
	// schemas は読んだスキーマの数。
	schemas int
}

// path は、ルートからいま読んでいる位置までにたどったスキーマの ID の列。同じスキーマへ
// 戻る参照（自己参照・相互参照）で止まるために使う。
//
// 位置ごとに持つ（全体で 1 つの集合にしない）のは、継承と循環を区別するため。Employee が
// allOf で Person を取り込み、かつ Person 型のフィールド manager を持つとき、manager は
// Employee 自身が宣言したフィールドなので、その先の Person は循環ではない。
type path struct {
	id     any
	parent *path
}

func (p *path) has(id any) bool {
	for ; p != nil; p = p.parent {
		if p.id == id {
			return true
		}
	}
	return false
}

// view は、ある位置に重ねて読むスキーマの 1 つと、そこまでの path。
type view struct {
	schema Schema
	path   *path
}

// member は、オブジェクトのフィールドか配列の要素のスキーマと、それを宣言したスキーマまでの
// path。schema が nil なら、あることだけが分かっている（中は分からない）。
type member struct {
	schema Schema
	path   *path
}

func (b *builder) node(s Schema, p *path, depth int) Node {
	if s == nil {
		return Node{}
	}
	var c collected
	if !b.collect(s, p, &c) {
		// 循環する参照か、上限。この先にフィールドがあるかは分からないので、省いたと伝える。
		return Node{Omitted: true}
	}

	props := map[string]member{}
	var items *member
	for _, v := range c.views {
		for _, name := range v.schema.Properties() {
			if _, seen := props[name]; seen {
				continue
			}
			// 解決できないプロパティも、名前は載せる。
			prop, _ := v.schema.Property(name)
			props[name] = member{schema: prop, path: v.path}
		}
		if items == nil {
			if it, ok := v.schema.Items(); ok {
				items = &member{schema: it, path: v.path}
			}
		}
	}
	switch {
	case len(props) > 0:
		n := b.object(props, depth)
		n.Omitted = n.Omitted || c.cut
		return n
	case items != nil:
		n := b.node(items.schema, items.path, depth)
		n.Arrays++
		return n
	}
	return Node{Omitted: c.cut}
}

func (b *builder) object(props map[string]member, depth int) Node {
	if depth >= maxDepth {
		return Node{Omitted: true}
	}
	names := slices.Sorted(maps.Keys(props))
	n := Node{Fields: make([]Field, 0, len(names))}
	for _, name := range names {
		if b.schemas >= maxSchemas {
			n.Omitted = true
			break
		}
		prop := props[name]
		n.Fields = append(
			n.Fields,
			Field{Name: name, Node: b.node(prop.schema, prop.path, depth+1)},
		)
	}
	return n
}

// collected は、1 つの位置に重ねて読むスキーマ。
type collected struct {
	views []view
	// cut は、重ねて読むはずのスキーマの一部を読まなかった（循環する参照か、上限）ことを表す。
	cut bool
}

// collect は s と、s に重ねて読むスキーマ（Alternatives を再帰的にたどったもの）を c に
// 集める。p は s を読む位置までの path。s 自体が循環する参照か、上限に達していれば false を
// 返す。
func (b *builder) collect(s Schema, p *path, c *collected) bool {
	if s == nil || b.schemas >= maxSchemas {
		return false
	}
	if id := s.ID(); id != nil {
		if p.has(id) {
			return false
		}
		p = &path{id: id, parent: p}
	}
	b.schemas++
	c.views = append(c.views, view{schema: s, path: p})
	for _, alt := range s.Alternatives() {
		if !b.collect(alt, p, c) {
			c.cut = true
		}
	}
	return true
}
