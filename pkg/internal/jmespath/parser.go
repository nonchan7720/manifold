package jmespath

import (
	"fmt"
)

type nodeKind int

const (
	nIdentity nodeKind = iota // 現在の値（@ と、左辺の無い式の起点）
	nField
	nSubexpression
	nPipe
	nIndex
	nSlice
	nIndexExpression
	nProjection
	nValueProjection
	nFilterProjection
	nFlatten
	nLiteral
	nMultiSelectList
	nMultiSelectHash
	nOr
	nAnd
	nNot
	nComparator
)

// node は構文木の節。
type node struct {
	kind     nodeKind
	children []*node
	// name は nField のフィールド名。
	name string
	// quoted は nField が "..." で書かれたか。
	quoted bool
	// index は nIndex の添字。
	index int
	// slice は nSlice の start / stop / step（省略されたものは nil）。
	slice [3]*int
	// value は nLiteral の値。
	value any
	// op は nComparator の演算子。
	op tokenKind
	// keys は nMultiSelectHash のキー（children と同じ並び）。
	keys []string
	// pos は式の中での位置（エラーメッセージ用）。
	pos int
}

var identityNode = &node{kind: nIdentity}

// bindingPower は演算子の結合の強さ。載っていないトークンは 0。
var bindingPower = map[tokenKind]int{
	tPipe: 1, tOr: 2, tAnd: 3,
	tEQ: 5, tNE: 5, tLT: 5, tLTE: 5, tGT: 5, tGTE: 5,
	tFlatten: 9, tStar: 20, tFilter: 21, tDot: 40, tNot: 45,
	tLbrace: 50, tLbracket: 55, tLparen: 60,
}

// projectionStop 未満の結合の強さを持つトークンが来たら、射影の右辺はそこで終わる。
const projectionStop = 10

// maxParseDepth は式の入れ子の深さの上限。
const maxParseDepth = 200

type parser struct {
	tokens   []token
	pos      int
	depth    int
	features Features
}

func parse(src string, features Features) (*node, error) {
	tokens, err := tokenize(src)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens, features: features}
	root, err := p.expression(0)
	if err != nil {
		return nil, err
	}
	if p.current().kind != tEOF {
		return nil, p.unexpected(p.current())
	}
	return root, nil
}

func (p *parser) current() token { return p.tokens[p.pos] }

func (p *parser) lookahead(n int) tokenKind {
	if p.pos+n >= len(p.tokens) {
		return tEOF
	}
	return p.tokens[p.pos+n].kind
}

func (p *parser) advance() token {
	tok := p.tokens[p.pos]
	if tok.kind != tEOF {
		p.pos++
	}
	return tok
}

func (p *parser) expect(kind tokenKind, what string) error {
	if p.current().kind != kind {
		return p.errorf(p.current(), "expected %s, got %s", what, describeToken(p.current()))
	}
	p.advance()
	return nil
}

func (p *parser) errorf(tok token, format string, args ...any) error {
	return &SyntaxError{Offset: tok.pos, Msg: fmt.Sprintf(format, args...)}
}

// needConditions は、tok が条件の構文で、それが有効でなければエラーを返す。
func (p *parser) needConditions(tok token) error {
	if p.features.Conditions {
		return nil
	}
	return p.errorf(tok, "%s cannot be used: filtering by a condition ([?...] with "+
		"comparison and logical operators) is not enabled", describeToken(tok))
}

// needIndexing は、bracket（'['）が添字かスライスで、それが有効でなければエラーを返す。
func (p *parser) needIndexing(bracket token) error {
	if p.features.Indexing {
		return nil
	}
	return p.errorf(bracket, "indexes and slices ([0], [:5]) are not enabled")
}

func (p *parser) unexpected(tok token) error {
	return p.errorf(tok, "unexpected %s", describeToken(tok))
}

func describeToken(tok token) string {
	if tok.kind == tEOF {
		return "end of expression"
	}
	return fmt.Sprintf("%q", tok.text)
}

// expression は結合の強さが rbp より大きい演算子が続く限り、式を読み進める。
func (p *parser) expression(rbp int) (*node, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxParseDepth {
		return nil, p.errorf(p.current(), "expression is nested too deeply")
	}
	left, err := p.nud(p.advance())
	if err != nil {
		return nil, err
	}
	for rbp < bindingPower[p.current().kind] {
		if left, err = p.led(p.advance(), left); err != nil {
			return nil, err
		}
	}
	return left, nil
}

// nud は式の先頭に来るトークンを読む。
func (p *parser) nud(tok token) (*node, error) {
	switch tok.kind { //nolint:exhaustive // 式の先頭に来られないトークンは、下で構文エラーにする
	case tLiteral:
		return &node{kind: nLiteral, value: tok.value, pos: tok.pos}, nil
	case tUnquotedIdentifier:
		name, _ := tok.value.(string)
		return &node{kind: nField, name: name, pos: tok.pos}, nil
	case tQuotedIdentifier:
		name, _ := tok.value.(string)
		return &node{kind: nField, name: name, quoted: true, pos: tok.pos}, nil
	case tStar:
		right := identityNode
		if p.current().kind != tRbracket {
			var err error
			if right, err = p.projectionRHS(bindingPower[tStar]); err != nil {
				return nil, err
			}
		}
		return &node{kind: nValueProjection, children: []*node{identityNode, right}}, nil
	case tFilter:
		return p.filter(tok, identityNode)
	case tLbrace:
		return p.multiSelectHash()
	case tFlatten:
		return p.flatten(identityNode)
	case tLbracket:
		return p.bracket(tok, identityNode, true)
	case tCurrent:
		return identityNode, nil
	case tExpref:
		return nil, p.errorf(tok, "unexpected '&' (expression references are only for "+
			"functions, which are not supported; use '&&' for a logical and)")
	case tNot:
		if err := p.needConditions(tok); err != nil {
			return nil, err
		}
		expr, err := p.expression(bindingPower[tNot])
		if err != nil {
			return nil, err
		}
		return &node{kind: nNot, children: []*node{expr}}, nil
	case tLparen:
		expr, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		if err := p.expect(tRparen, "')'"); err != nil {
			return nil, err
		}
		return expr, nil
	}
	return nil, p.unexpected(tok)
}

// led は左辺 left に続くトークンを読む。
func (p *parser) led(tok token, left *node) (*node, error) {
	switch tok.kind { //nolint:exhaustive // 式の続きに来られないトークンは、下で構文エラーにする
	case tDot:
		right, err := p.dotRHS(bindingPower[tDot])
		if err != nil {
			return nil, err
		}
		return &node{kind: nSubexpression, children: []*node{left, right}}, nil
	case tPipe:
		return p.binary(nPipe, tok, left)
	case tOr:
		return p.condition(nOr, tok, left)
	case tAnd:
		return p.condition(nAnd, tok, left)
	case tEQ, tNE, tLT, tLTE, tGT, tGTE:
		n, err := p.condition(nComparator, tok, left)
		if err != nil {
			return nil, err
		}
		n.op = tok.kind
		return n, nil
	case tLparen:
		return p.call(tok, left)
	case tFilter:
		return p.filter(tok, left)
	case tFlatten:
		return p.flatten(left)
	case tLbracket:
		return p.bracket(tok, left, false)
	}
	return nil, p.unexpected(tok)
}

// condition は比較・論理の演算子の右辺を読む。条件の構文が有効なときだけ使える。
func (p *parser) condition(kind nodeKind, tok token, left *node) (*node, error) {
	if err := p.needConditions(tok); err != nil {
		return nil, err
	}
	return p.binary(kind, tok, left)
}

func (p *parser) binary(kind nodeKind, tok token, left *node) (*node, error) {
	right, err := p.expression(bindingPower[tok.kind])
	if err != nil {
		return nil, err
	}
	return &node{kind: kind, children: []*node{left, right}, pos: tok.pos}, nil
}

// call は left の直後の '(' を断る。関数（集計・変換・並べ替えなど）には対応しない。式が
// できるのは、結果から値を選ぶことと、条件で絞ることだけ。
func (p *parser) call(lparen token, left *node) (*node, error) {
	if left.kind != nField || left.quoted {
		return nil, p.errorf(lparen, "unexpected '('")
	}
	return nil, p.errorf(token{pos: left.pos},
		"functions are not supported (%s()): an expression can only select and filter", left.name)
}

// flatten は left[] と、それに続く射影の右辺を読む。
func (p *parser) flatten(left *node) (*node, error) {
	right, err := p.projectionRHS(bindingPower[tFlatten])
	if err != nil {
		return nil, err
	}
	flattened := &node{kind: nFlatten, children: []*node{left}}
	return &node{kind: nProjection, children: []*node{flattened, right}}, nil
}

// filter は left[?条件] と、それに続く射影の右辺を読む。open は "[?" のトークン。
func (p *parser) filter(open token, left *node) (*node, error) {
	if err := p.needConditions(open); err != nil {
		return nil, err
	}
	condition, err := p.expression(0)
	if err != nil {
		return nil, err
	}
	if err := p.expect(tRbracket, "']'"); err != nil {
		return nil, err
	}
	right := identityNode
	if p.current().kind != tFlatten {
		if right, err = p.projectionRHS(bindingPower[tFilter]); err != nil {
			return nil, err
		}
	}
	return &node{kind: nFilterProjection, children: []*node{left, right, condition}}, nil
}

// bracket は '['（open）の続きを読む。添字・スライス・[*] のいずれかで、式の先頭
// （atStart）ではそのどれでもなければ複数選択のリストになる。
func (p *parser) bracket(open token, left *node, atStart bool) (*node, error) {
	switch {
	case p.current().kind == tNumber || p.current().kind == tColon:
		if err := p.needIndexing(open); err != nil {
			return nil, err
		}
		return p.indexOrSlice(left)
	case p.current().kind == tStar && p.lookahead(1) == tRbracket:
		p.advance()
		p.advance()
		right, err := p.projectionRHS(bindingPower[tStar])
		if err != nil {
			return nil, err
		}
		return &node{kind: nProjection, children: []*node{left, right}}, nil
	case atStart:
		return p.multiSelectList()
	}
	return nil, p.errorf(p.current(), "expected a number, ':' or '*' after '[', got %s",
		describeToken(p.current()))
}

// indexOrSlice は [n] または [start:stop:step] を読む。
func (p *parser) indexOrSlice(left *node) (*node, error) {
	if p.current().kind == tNumber && p.lookahead(1) == tRbracket {
		index, _ := p.advance().value.(int)
		p.advance()
		return &node{
			kind:     nIndexExpression,
			children: []*node{left, {kind: nIndex, index: index}},
		}, nil
	}
	var parts [3]*int
	part := 0
	for p.current().kind != tRbracket {
		tok := p.advance()
		n, isNumber := tok.value.(int)
		switch {
		case tok.kind == tColon:
			part++
			if part == len(parts) {
				return nil, p.errorf(tok, "a slice has at most three parts")
			}
		case tok.kind == tNumber && isNumber && parts[part] == nil:
			if part == len(parts)-1 && n == 0 {
				return nil, p.errorf(tok, "slice step cannot be 0")
			}
			parts[part] = &n
		default:
			return nil, p.unexpected(tok)
		}
	}
	p.advance()
	sliced := &node{
		kind:     nIndexExpression,
		children: []*node{left, {kind: nSlice, slice: parts}},
	}
	// スライスの結果は射影になる。
	right, err := p.projectionRHS(bindingPower[tStar])
	if err != nil {
		return nil, err
	}
	return &node{kind: nProjection, children: []*node{sliced, right}}, nil
}

// dotRHS は '.' の右辺を読む。
func (p *parser) dotRHS(rbp int) (*node, error) {
	switch p.current().kind { //nolint:exhaustive // '.' の後に来られないトークンは、下で構文エラーにする
	case tUnquotedIdentifier, tQuotedIdentifier, tStar:
		return p.expression(rbp)
	case tLbracket:
		p.advance()
		return p.multiSelectList()
	case tLbrace:
		p.advance()
		return p.multiSelectHash()
	}
	return nil, p.errorf(p.current(), "expected a name, '*', '[' or '{' after '.', got %s",
		describeToken(p.current()))
}

// projectionRHS は射影の各要素に適用する式を読む。
func (p *parser) projectionRHS(rbp int) (*node, error) {
	switch kind := p.current().kind; {
	case bindingPower[kind] < projectionStop:
		return identityNode, nil
	case kind == tLbracket || kind == tFilter:
		return p.expression(rbp)
	case kind == tDot:
		p.advance()
		return p.dotRHS(rbp)
	}
	return nil, p.unexpected(p.current())
}

// multiSelectList は [式, 式, ...] の '[' より後を読む。
func (p *parser) multiSelectList() (*node, error) {
	n := &node{kind: nMultiSelectList}
	for {
		expr, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		n.children = append(n.children, expr)
		if p.current().kind == tRbracket {
			p.advance()
			return n, nil
		}
		if err := p.expect(tComma, "',' or ']'"); err != nil {
			return nil, err
		}
	}
}

// multiSelectHash は {名前: 式, ...} の '{' より後を読む。
func (p *parser) multiSelectHash() (*node, error) {
	n := &node{kind: nMultiSelectHash}
	for {
		key := p.advance()
		if key.kind != tUnquotedIdentifier && key.kind != tQuotedIdentifier {
			return nil, p.errorf(key, "expected a key name, got %s", describeToken(key))
		}
		if err := p.expect(tColon, "':'"); err != nil {
			return nil, err
		}
		expr, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		name, _ := key.value.(string)
		n.keys = append(n.keys, name)
		n.children = append(n.children, expr)
		if p.current().kind == tRbrace {
			p.advance()
			return n, nil
		}
		if err := p.expect(tComma, "',' or '}'"); err != nil {
			return nil, err
		}
	}
}
