package resultshape

import (
	"strings"

	"github.com/nonchan7720/manifold/pkg/internal/jmespath"
)

const (
	// omittedMark は、載せなかったフィールドがあることを表す。
	omittedMark = "..."
	// separator はフィールドの区切り。
	separator = ", "
	// maxNameBytes は載せるフィールド名の長さの上限。これより長い名前は載せずに省く
	// （名前 1 つで一覧を使い切らないようにする）。
	maxNameBytes = 64
)

// Describe は形を、フィールドの一覧として返す。フィールドが分からなければ空文字列。
//
//	id, name, owner{id, login}, tags[]{name}, urls[]
//
// 名前は式に書く形（識別子として書けないものは引用符つき）、[] は配列、{} はその中の
// フィールド。載せなかったフィールドがあるところは ... と書く（owner{...}）。
//
// 一覧は limit バイトに収める。浅い階層を先に載せ、収まらない階層は ... にする。
func (n Node) Describe(limit int) string {
	root := newItem(strings.Repeat("[]", n.Arrays), n)
	if len(root.children) == 0 {
		return ""
	}
	// ルートは必ず開く。ルートのフィールドだけで limit を超えるなら、収まるところで切る。
	root.expanded = true
	root.truncate(limit - len(root.label) - root.braces())
	if len(root.children) == 0 {
		return ""
	}
	used := len(root.label) + root.braces() + root.listLen()

	// 浅い階層から順に、収まるものを開いていく。
	queue := root.expandable()
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		// 開くと、"{...}" が "{" + 一覧 + "}" に置き換わる。
		delta := it.listLen() - len(omittedMark)
		if used+delta > limit {
			continue
		}
		it.expanded = true
		used += delta
		queue = append(queue, it.expandable()...)
	}

	var sb strings.Builder
	sb.Grow(used)
	root.render(&sb, true)
	return sb.String()
}

// item は一覧に載せる 1 か所。
type item struct {
	// label は名前（ルートでは空）と、配列を表す "[]"。
	label    string
	children []*item
	// omitted は、children のほかにも載せていないフィールドがあることを表す。
	omitted bool
	// expanded は children を載せるかどうか。
	expanded bool
}

func newItem(label string, n Node) *item {
	it := &item{label: label, omitted: n.Omitted}
	for _, f := range n.Fields {
		if len(f.Name) > maxNameBytes {
			it.omitted = true
			continue
		}
		label := jmespath.Identifier(f.Name) + strings.Repeat("[]", f.Arrays)
		it.children = append(it.children, newItem(label, f.Node))
	}
	return it
}

// nested は、この下にフィールドがある（載せるか、省いたと書く）かを返す。
func (it *item) nested() bool { return len(it.children) > 0 || it.omitted }

// braces は、一覧を囲む {} の長さを返す。ルートのオブジェクトは囲まない。
func (it *item) braces() int {
	if it.label == "" {
		return 0
	}
	return len("{}")
}

// listLen は、開いたときの一覧（{} の内側）の長さを返す。子はどれも閉じているものとする。
func (it *item) listLen() int {
	n := 0
	for i, child := range it.children {
		if i > 0 {
			n += len(separator)
		}
		n += child.closedLen()
	}
	if it.omitted {
		if len(it.children) > 0 {
			n += len(separator)
		}
		n += len(omittedMark)
	}
	return n
}

// closedLen は、閉じた状態（name または name{...}）の長さを返す。
func (it *item) closedLen() int {
	if it.nested() {
		return len(it.label) + len("{}") + len(omittedMark)
	}
	return len(it.label)
}

// truncate は、一覧が budget バイトに収まるように、後ろの子を省く。
func (it *item) truncate(budget int) {
	if it.listLen() <= budget {
		return
	}
	// 省いた印（", ..."）の分を残して、収まるところまで載せる。
	used, keep := 0, 0
	for i, child := range it.children {
		size := child.closedLen()
		if i > 0 {
			size += len(separator)
		}
		if used+size+len(separator)+len(omittedMark) > budget {
			break
		}
		used += size
		keep = i + 1
	}
	it.children = it.children[:keep]
	it.omitted = true
}

// expandable は、子のうち、開くと一覧が増えるものを返す。
func (it *item) expandable() []*item {
	var out []*item
	for _, child := range it.children {
		if len(child.children) > 0 {
			out = append(out, child)
		}
	}
	return out
}

func (it *item) render(sb *strings.Builder, root bool) {
	sb.WriteString(it.label)
	if !it.nested() {
		return
	}
	// ルートのオブジェクトは {} で囲まない。
	bare := root && it.label == ""
	if !bare {
		sb.WriteByte('{')
	}
	it.renderList(sb)
	if !bare {
		sb.WriteByte('}')
	}
}

// renderList は {} の内側を書く。
func (it *item) renderList(sb *strings.Builder) {
	if !it.expanded {
		sb.WriteString(omittedMark)
		return
	}
	for i, child := range it.children {
		if i > 0 {
			sb.WriteString(separator)
		}
		child.render(sb, false)
	}
	if it.omitted {
		if len(it.children) > 0 {
			sb.WriteString(separator)
		}
		sb.WriteString(omittedMark)
	}
}
