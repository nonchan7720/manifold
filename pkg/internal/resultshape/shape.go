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
	// maxNames は 1 つの形のために読むプロパティ名の数の上限。スキーマの数だけでは、
	// プロパティの多いスキーマを何度も読む作業を抑えられないので、別に数える。
	maxNames = 20000
	// maxFields は 1 つのオブジェクトに載せるフィールドの数の上限。
	maxFields = 1000
)

// Build は root のスキーマから形を作る。
//
// 浅い階層から順に読む（幅優先）。上限に達しても、それより浅い階層のフィールドは名前が
// そろっている。上限に達したあとの位置は中を読まないので、フィールドを持つオブジェクトでも
// 名前だけになる。
func Build(root Schema) Node {
	b := &builder{}
	var out Node
	queue := []position{{node: &out, decls: []member{{schema: root}}}}
	for len(queue) > 0 {
		pos := queue[0]
		queue = queue[1:]
		queue = append(queue, b.expand(pos)...)
	}
	return out
}

type builder struct {
	// schemas は読んだスキーマの数、names は読んだプロパティ名の数。
	schemas int
	names   int
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

// member は、ある位置（ルート、フィールド、配列の要素）のスキーマの宣言 1 つと、それを
// 宣言したスキーマまでの path。schema が nil なら、あることだけが分かっている（中は
// 分からない）。
type member struct {
	schema Schema
	path   *path
}

// position は、これから読む位置。同じ位置が複数の場所で宣言されることがある（allOf の
// 2 つの枝が、同じ名前のプロパティを別々に宣言する）ので、宣言は列で持ち、重ねて読む。
type position struct {
	node  *Node
	decls []member
	// depth は、ルートからのフィールドの入れ子の深さ。
	depth int
}

// expand は pos の形を読んで pos.node に入れ、その下の、これから読む位置を返す。
func (b *builder) expand(pos position) []position {
	decls := pos.decls
	for {
		r := b.read(decls)
		switch {
		case len(r.props) > 0:
			return b.fields(pos, r)
		case len(r.items) > 0:
			// 配列。要素を同じ位置として読み進める。
			pos.node.Arrays++
			decls = r.items
			continue
		}
		// 循環する参照で止まったなら、この先にフィールドがある（上の階層に載っている）。
		pos.node.Omitted = r.cyclic
		return nil
	}
}

// fields は pos をオブジェクトとして、r のプロパティをフィールドにする。
func (b *builder) fields(pos position, r reading) []position {
	if pos.depth >= maxDepth {
		pos.node.Omitted = true
		return nil
	}
	names := slices.Sorted(maps.Keys(r.props))
	pos.node.Omitted = r.cyclic || r.partial
	if len(names) > maxFields {
		names = names[:maxFields]
		pos.node.Omitted = true
	}
	pos.node.Fields = make([]Field, len(names))
	next := make([]position, len(names))
	for i, name := range names {
		pos.node.Fields[i].Name = name
		next[i] = position{
			node: &pos.node.Fields[i].Node, decls: r.props[name], depth: pos.depth + 1,
		}
	}
	return next
}

// reading は、1 つの位置に重ねたスキーマから読んだもの。
type reading struct {
	// props はプロパティ名ごとの宣言、items は配列の要素の宣言。
	props map[string][]member
	items []member
	// cyclic は、重ねるはずのスキーマの一部が循環する参照だったことを表す。
	cyclic bool
	// partial は、上限のために、重ねるはずのスキーマの一部を読まなかったことを表す。
	partial bool
}

// read は decls を重ねて読む。
func (b *builder) read(decls []member) reading {
	c := collected{}
	for _, decl := range decls {
		if decl.schema != nil {
			b.collect(decl.schema, decl.path, &c)
		}
	}
	r := reading{cyclic: c.cyclic, partial: c.partial}
	for _, v := range c.views {
		if b.names >= maxNames {
			r.partial = true
			break
		}
		names := v.schema.Properties()
		b.names += len(names)
		if len(names) > 0 && r.props == nil {
			r.props = make(map[string][]member, len(names))
		}
		for _, name := range names {
			// 解決できないプロパティも、名前は載せる。
			prop, _ := v.schema.Property(name)
			r.props[name] = append(r.props[name], member{schema: prop, path: v.path})
		}
		if it, ok := v.schema.Items(); ok {
			r.items = append(r.items, member{schema: it, path: v.path})
		}
	}
	return r
}

// collected は、1 つの位置に重ねて読むスキーマ。
type collected struct {
	views []view
	// seen は、この位置で既に集めたスキーマの ID。同じスキーマを何度も重ねない（allOf に
	// 同じ参照が並んでいても、読むのは 1 度）。
	seen    map[any]struct{}
	cyclic  bool
	partial bool
}

// view は、ある位置に重ねて読むスキーマの 1 つと、そこまでの path。
type view struct {
	schema Schema
	path   *path
}

// collect は s と、s に重ねて読むスキーマ（Alternatives を再帰的にたどったもの）を c に
// 集める。p は s を読む位置までの path。
func (b *builder) collect(s Schema, p *path, c *collected) {
	if s == nil {
		return
	}
	if b.schemas >= maxSchemas {
		c.partial = true
		return
	}
	if id := s.ID(); id != nil {
		if p.has(id) {
			c.cyclic = true
			return
		}
		if _, dup := c.seen[id]; dup {
			return
		}
		if c.seen == nil {
			c.seen = map[any]struct{}{}
		}
		c.seen[id] = struct{}{}
		p = &path{id: id, parent: p}
	}
	b.schemas++
	c.views = append(c.views, view{schema: s, path: p})
	for _, alt := range s.Alternatives() {
		b.collect(alt, p, c)
	}
}
