package a11y

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// A small, forgiving reader for markup: JSX/TSX inside JavaScript, and HTML-like templates
// (HTML, Vue, Svelte). It builds a tree of tags with their attributes and whether they hold
// text, which is all the checks need. Malformed input gives a partial tree, never an error.

type tag struct {
	name     string
	off      int
	attrs    map[string]string // raw values ("" for a bare attribute); Vue's :x and v-bind:x become x
	spread   bool              // {...props}: attributes unknown
	text     bool              // holds text or an {expression} directly
	parent   *tag
	children []*tag
	// attrKids hold the JSX passed in t's attributes (icon={<img/>}, renderItem={() =>
	// <Pressable/>}): one holder per attribute, whose children are that JSX (issue #147).
	attrKids []*tag
	attr     string // on a holder: the attribute it holds; "" on a real tag
}

func (t *tag) has(name string) bool { _, ok := t.attrs[name]; return ok }

// attrTrue: a bare attribute, ={true}, ="true".
func (t *tag) attrTrue(name string) bool {
	v, ok := t.attrs[name]
	if !ok {
		return false
	}
	v = strings.Trim(strings.TrimSpace(v), `{}"'`)
	return v == "" || v == "true"
}

// walk visits what t shows (t first): its children, and the JSX of a children={…}
// attribute. JSX in t's other attributes (badge={<Text/>}, onPress={() => show(<Text/>)})
// is not t's content: it does not label t or name its icon. Returning false skips a tag's
// children.
func (t *tag) walk(fn func(*tag) bool) {
	t.visit(fn, false)
}

// walkAll visits t's subtree with the JSX of every attribute, for the checks that look at
// each element on its own (issue #147).
func (t *tag) walkAll(fn func(*tag) bool) {
	t.visit(fn, true)
}

func (t *tag) visit(fn func(*tag) bool, all bool) {
	if !fn(t) {
		return
	}
	for _, c := range t.attrKids {
		if all || c.attr == "children" {
			c.visit(fn, all)
		}
	}
	for _, c := range t.children {
		c.visit(fn, all)
	}
}

var callbackStart = regexp.MustCompile(`=>|\bfunction\b`)

// callbackAround is the attribute whose callback renders t (renderItem={({item}) =>
// <Pressable/>}), or "": a callback may run once per item, as FlatList's renderItem does.
// Only an attribute expression's head counts (what comes before its first JSX), without
// its strings and comments: a handler on a tag around t is not a callback that renders t.
func callbackAround(src string, t *tag) string {
	for d := t.parent; d != nil; d = d.parent {
		if d.attr == "" || len(d.children) == 0 {
			continue
		}
		if callbackStart.MatchString(code(src[d.off:d.children[0].off])) {
			return d.attr
		}
	}
	return ""
}

// code is s with its strings and comments blanked out.
func code(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if j := skipLiteral(s, i); j != i {
			b.WriteByte(' ')
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// voidTags never have a closing tag in HTML.
var voidTags = map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true,
	"img": true, "input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true}

type markupParser struct {
	src  string
	html bool // HTML-like file: text everywhere, no JavaScript around the tags
	// fold: an HTML page, where tag and attribute names are case-insensitive: they are
	// read in lower case, so <BUTTON> is a button (issue #147). Not in Vue or Svelte, where
	// a capitalised name is a component, as in JSX.
	fold bool

	closed  memo   // closing's results
	skipped memo   // skipElement's results
	lits    []span // the JavaScript strings and comments js met, in order
	steps   int    // the work the JSX-aware scans did, for the tests
}

// parseMarkup returns a root holding every top-level tag.
func parseMarkup(src string, html bool) *tag {
	return parse(&markupParser{src: src, html: html})
}

// parseSource is parseMarkup that also tells the JavaScript strings and comments it met.
func parseSource(src string, html bool) (*tag, func(int) bool) {
	p := &markupParser{src: src, html: html}
	return parse(p), p.inLiteral
}

// parseHTMLPage is parseMarkup for an HTML page: tag and attribute names in any case.
func parseHTMLPage(src string) *tag {
	return parse(&markupParser{src: src, html: true, fold: true})
}

func parse(p *markupParser) *tag {
	src, html := p.src, p.html
	root := &tag{name: "", attrs: map[string]string{}}
	if html {
		// A stray closing tag ends children(); carry on after it.
		for i := 0; i < len(src); {
			i, _ = p.children(i, len(src), root)
		}
	} else {
		p.js(0, len(src), root)
	}
	sort.Slice(p.lits, func(i, j int) bool { return p.lits[i].from < p.lits[j].from })
	return root
}

// js scans JavaScript between from and to, parsing each JSX element it finds into parent.
func (p *markupParser) js(from, to int, parent *tag) {
	s := p.src
	last := from - 1 // the last character of code: not a space, not in a comment
	for i := from; i < to; {
		if j, value := p.literal(i, from, last, to); j != i {
			if value { // a string, not a comment
				last = j - 1
			}
			// Strings and comments are recorded, not regular expressions: a division can
			// read as one (n-- / 2, of / 2), and a .map( it would hide is a loop all the
			// same (issue #180).
			if !(s[i] == '/' && value) {
				p.lits = append(p.lits, span{i, j})
			}
			i = j
			continue
		}
		if s[i] == '<' && p.jsxStarts(i, from, last) {
			i = p.element(i, to, parent)
			last = i - 1
			continue
		}
		if !isSpace(s[i]) {
			last = i
		}
		i++
	}
}

// jsxStarts tells a JSX tag from a comparison or a TypeScript type argument: a letter or
// '>' follows, and before it (at last, spaces and comments skipped) comes an operator, a
// bracket or `return`.
func (p *markupParser) jsxStarts(i, from, last int) bool {
	s := p.src
	if i+1 >= len(s) || !(isLetter(s[i+1]) || s[i+1] == '>') {
		return false
	}
	if typeParams(s, i) {
		return false
	}
	j := last
	if j < from {
		return true
	}
	if strings.ContainsRune("(=,?:{}[!&|>;", rune(s[j])) {
		return true
	}
	return strings.HasSuffix(s[from:j+1], "return")
}

// typeParams: the '<' at i opens TypeScript type parameters, not a tag: <T,>(x: T) => x
// <T extends X> and <T = X>, the forms a .tsx file uses for a generic arrow function.
func typeParams(s string, i int) bool {
	j := i + 1
	for j < len(s) && isNameChar(s[j]) {
		j++
	}
	k := j
	for k < len(s) && isSpace(s[k]) {
		k++
	}
	if k < len(s) && s[k] == ',' {
		return true
	}
	if j > i+1 && k > j && k < len(s) && s[k] == '=' && (k+1 == len(s) || (s[k+1] != '=' && s[k+1] != '>')) {
		return true // <T = X>, a default; a space before '=', so <a={x}> stays a tag
	}
	return k > j && strings.HasPrefix(s[k:], "extends") && (k+7 == len(s) || !isNameChar(s[k+7]))
}

// literal skips the JavaScript string, comment, template literal or regular expression at
// i (from and last as in js), and says whether it is a value (not a comment). It returns
// i when none starts there. A template literal's ${…} is code, read by closing.
func (p *markupParser) literal(i, from, last, to int) (int, bool) {
	s := p.src
	switch {
	case s[i] == '`':
		for j := i + 1; j < to; j++ {
			switch {
			case s[j] == '\\':
				j++
			case s[j] == '`':
				return j + 1, true
			case s[j] == '$' && j+1 < to && s[j+1] == '{':
				e := p.closing(j+1, to)
				if e < 0 {
					return to, true
				}
				j = e - 1
			}
		}
		return to, true
	case s[i] == '/' && i+1 < to && s[i+1] != '/' && s[i+1] != '*' && regexStarts(s, from, last):
		// A regular expression ends at its '/' (not one in a [class]) on the same line; with
		// none, the '/' is not one, and the scan carries on at the next character.
		class := false
		for j := i + 1; j < to && s[j] != '\n'; j++ {
			switch s[j] {
			case '\\':
				j++
			case '[':
				class = true
			case ']':
				class = false
			case '/':
				if !class {
					return j + 1, true
				}
			}
		}
		return i, false
	}
	j := skipLiteral(s, i)
	if j > to {
		j = to
	}
	return j, j != i && s[i] != '/'
}

// regexStarts: a '/' after last starts a regular expression, not a division: nothing
// comes before it, or an operator, an opening bracket or a keyword (return, typeof, …)
// does. A '}' or ')' before it reads as a division.
func regexStarts(s string, from, last int) bool {
	if last < from {
		return true
	}
	if strings.ContainsRune("(=,?:[!&|;{>+-*%~^<", rune(s[last])) {
		return true
	}
	return endsWithKeyword(s[from:last+1], regexKeywords)
}

// regexKeywords are the keywords a regular expression can follow.
var regexKeywords = []string{"return", "typeof", "case", "in", "of", "void", "yield", "await",
	"instanceof", "delete", "new", "throw"}

// endsWithKeyword: s ends with one of words, as a whole word.
func endsWithKeyword(s string, words []string) bool {
	for _, w := range words {
		if strings.HasSuffix(s, w) && (len(s) == len(w) || !isNameChar(s[len(s)-len(w)-1])) {
			return true
		}
	}
	return false
}

// span is a stretch of source, from its first index to just past its last.
type span struct{ from, to int }

// closing returns the index just past the bracket that closes the one at open (before
// to), in JavaScript that may hold JSX, or -1. It is matching that knows JSX: the text of a
// JSX element is text, so Don't and http:// in it are not a string and a comment (issue
// #180), while the code around it and in its {…} still skips strings, comments, template
// literals and regular expressions.
//
// Results, failures too, are kept: a scan that fails inside unclosed code is not redone by
// every scan around it, which made a half-edited file take exponential time.
func (p *markupParser) closing(open, to int) int {
	return p.kept(&p.closed, open, to, p.close)
}

// memo keeps a scan's results. A success is kept by its start, with the range it was found
// in, and holds in every range from its end to that one: a scan reads nothing after its end
// but what fails in a shorter range as well. So deep nesting keeps one entry for each
// bracket, not one for each level around it. A failure is kept for its own range only.
type memo struct {
	ok     map[int]span  // start -> (end, the range's end)
	failed map[span]bool // (start, the range's end)
}

// kept is scan(at, to), through m.
func (p *markupParser) kept(m *memo, at, to int, scan func(int, int) int) int {
	if r, ok := m.ok[at]; ok && r.from <= to && to <= r.to {
		return r.from
	}
	if m.failed[span{at, to}] {
		return -1
	}
	e := scan(at, to)
	if e < 0 {
		if m.failed == nil {
			m.failed = map[span]bool{}
		}
		m.failed[span{at, to}] = true
		return e
	}
	if m.ok == nil {
		m.ok = map[int]span{}
	}
	if r, ok := m.ok[at]; !ok || to > r.to {
		m.ok[at] = span{e, to} // the widest range holds the most
	}
	return e
}

// close scans from the bracket at open. A bracket inside is closed by closing in turn, so
// each is scanned once, and one that cannot close fails every one around it at once.
func (p *markupParser) close(open, to int) int {
	s := p.src
	last := open
	for i := open + 1; i < to; {
		p.steps++
		if j, value := p.literal(i, open, last, to); j != i {
			if value {
				last = j - 1
			}
			i = j
			continue
		}
		c := s[i]
		if c == '<' && p.jsxStarts(i, open, last) {
			// A tag that does not end, or does not read as one, is a '<' and no more: the
			// scan resynchronises at the next character instead of losing the rest.
			if e := p.skipElement(i, to); e >= 0 {
				i = e
				last = i - 1
				continue
			}
		}
		switch c {
		case '(', '{', '[':
			e := p.closing(i, to)
			if e < 0 {
				return -1
			}
			i = e
			last = i - 1
			continue
		case ')', '}', ']':
			return i + 1
		}
		if !isSpace(c) {
			last = i
		}
		i++
	}
	return -1
}

// skipElement returns the index after the JSX element whose '<' is at i, or -1 when it
// does not end before to or is not JSX: a tag's head holds only names, type arguments,
// '=', strings, comments and {…}, and its text no '}' (JSX does not allow one there).
// Results are kept, as closing's are.
func (p *markupParser) skipElement(i, to int) int {
	return p.kept(&p.skipped, i, to, p.skipTag)
}

func (p *markupParser) skipTag(i, to int) int {
	s := p.src
	j := i + 1
	if j < to && s[j] == '>' { // a fragment
		return p.skipChildren(j+1, to)
	}
	for j < to && isNameChar(s[j]) {
		j++
	}
	j = typeArgs(s, j, to)
	for j < to {
		p.steps++
		c := s[j]
		switch {
		case isSpace(c), isNameChar(c), c == '=':
			j++
		case c == '>':
			return p.skipChildren(j+1, to)
		case c == '/' && j+1 < to && s[j+1] == '>':
			return j + 2
		case c == '/' && j+1 < to && (s[j+1] == '*' || s[j+1] == '/'):
			j = min(skipLiteral(s, j), to)
		case c == '{':
			e := p.closing(j, to)
			if e < 0 {
				return -1
			}
			j = e
		case c == '"' || c == '\'':
			e := strings.IndexByte(s[j+1:to], c)
			if e < 0 {
				return -1
			}
			j += e + 2
		default:
			return -1
		}
	}
	return -1
}

// skipChildren returns the index after the closing tag of the element whose children start
// at i (any closing tag, as children reads them), or -1.
func (p *markupParser) skipChildren(i, to int) int {
	s := p.src
	for i < to {
		p.steps++
		switch c := s[i]; {
		case c == '<' && i+1 < to && s[i+1] == '/':
			e := strings.IndexByte(s[i:to], '>')
			if e < 0 {
				return -1
			}
			return i + e + 1
		case c == '<' && i+1 < to && (isLetter(s[i+1]) || s[i+1] == '>'):
			e := p.skipElement(i, to)
			if e < 0 {
				return -1
			}
			i = e
		case c == '{':
			e := p.closing(i, to)
			if e < 0 {
				return -1
			}
			i = e
		case c == '}':
			return -1
		default:
			i++
		}
	}
	return -1
}

// typeArgs returns the index after the TypeScript type arguments of a generic component
// (<Select<Option> value={x}>) at j, or j when there are none or they do not close.
func typeArgs(s string, j, to int) int {
	if j >= to || s[j] != '<' {
		return j
	}
	depth := 0
	for k := j; k < to; k++ {
		switch s[k] {
		case '<':
			depth++
		case '>':
			if s[k-1] == '=' { // an arrow in a function type
				continue
			}
			depth--
			if depth == 0 {
				return k + 1
			}
		case '\n', '{', '}', ';':
			return j
		}
	}
	return j
}

// expr returns the index after the {…} expression at open: closing in JSX, matching in a
// template (Vue, Svelte), whose expressions hold no JSX. When closing cannot close it,
// matching has the last word, once.
func (p *markupParser) expr(open, to int) int {
	if p.html {
		return matching(p.src, open)
	}
	if e := p.closing(open, to); e >= 0 {
		return e
	}
	return matching(p.src[:to], open)
}

// jsMatcher is matching for JavaScript that may hold JSX (see closing), for src. Its
// results are kept across calls.
func jsMatcher(src string) func(string, int) int {
	p := &markupParser{src: src}
	return func(_ string, open int) int { return p.expr(open, len(src)) }
}

// inLiteral reports whether off is inside a JavaScript string or comment the parse met.
// JSX text is in none, whatever quotes or // it holds (issue #180).
func (p *markupParser) inLiteral(off int) bool {
	i := sort.Search(len(p.lits), func(i int) bool { return p.lits[i].to > off })
	return i < len(p.lits) && p.lits[i].from <= off
}

func isLetter(c byte) bool { return c < 128 && unicode.IsLetter(rune(c)) || c == '_' }

func isNameChar(c byte) bool {
	return isLetter(c) || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == ':' || c == '$'
}

// element parses the element whose '<' is at i, adds it to parent, and returns the index
// after it.
func (p *markupParser) element(i, to int, parent *tag) int {
	s := p.src
	j := i + 1
	for j < to && isNameChar(s[j]) {
		j++
	}
	t := &tag{name: s[i+1 : j], off: i, attrs: map[string]string{}, parent: parent}
	if p.fold {
		t.name = strings.ToLower(t.name)
	}
	parent.children = append(parent.children, t)
	if !p.html {
		j = typeArgs(s, j, to) // <Select<Option> …>
	}
	selfClose := false
	// Attributes.
	for j < to {
		c := s[j]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			j++
		case !p.html && c == '/' && j+1 < to && (s[j+1] == '*' || s[j+1] == '/'):
			j = min(skipLiteral(s, j), to) // a comment
		case c == '>':
			j++
			goto body
		case c == '/' && j+1 < to && s[j+1] == '>':
			j += 2
			selfClose = true
			goto body
		case c == '{':
			e := p.expr(j, to)
			if e < 0 {
				return to
			}
			if strings.HasPrefix(strings.TrimSpace(s[j+1:e-1]), "...") {
				t.spread = true
			}
			j = e
		default:
			k := j
			for k < to && !strings.ContainsRune(" \t\r\n=>/", rune(s[k])) {
				k++
			}
			if k == j { // a stray '/'
				j++
				continue
			}
			name := s[j:k]
			name = strings.TrimPrefix(strings.TrimPrefix(name, "v-bind:"), ":")
			if p.fold {
				name = strings.ToLower(name)
			}
			val := ""
			j = k
			if j < to && s[j] == '=' {
				j++
				if j < to {
					switch s[j] {
					case '"', '\'':
						e := strings.IndexByte(s[j+1:to], s[j])
						if e < 0 {
							return to
						}
						val = s[j+1 : j+1+e]
						j = j + 1 + e + 1
					case '{':
						e := p.expr(j, to)
						if e < 0 {
							return to
						}
						val = s[j:e]
						if !p.html {
							p.attrJSX(t, name, j+1, e-1)
						}
						j = e
					default:
						k := j
						for k < to && !strings.ContainsRune(" \t\r\n>", rune(s[k])) {
							k++
						}
						val = s[j:k]
						j = k
					}
				}
			}
			t.attrs[name] = val
		}
	}
	return to
body:
	lower := strings.ToLower(t.name)
	if selfClose || voidTags[t.name] || (p.html && voidTags[lower]) {
		return j
	}
	if lower == "script" || lower == "style" {
		if e := strings.Index(strings.ToLower(s[j:to]), "</"+lower); e >= 0 {
			if g := strings.IndexByte(s[j+e:to], '>'); g >= 0 {
				return j + e + g + 1
			}
		}
		return to
	}
	end, _ := p.children(j, to, t)
	return end
}

// attrJSX parses the JSX in t's attribute expression (between from and to) into a holder
// for the attribute, so those elements are checked too (issue #147).
func (p *markupParser) attrJSX(t *tag, attr string, from, to int) {
	h := &tag{off: from, attrs: map[string]string{}, parent: t, attr: attr}
	p.js(from, to, h)
	if len(h.children) > 0 {
		t.attrKids = append(t.attrKids, h)
	}
}

// children parses markup content into t until a closing tag (any; a mismatched one closes
// t too, which keeps a broken file from swallowing the rest) or to. It returns the index
// after the closing tag.
func (p *markupParser) children(i, to int, t *tag) (int, bool) {
	s := p.src
	for i < to {
		c := s[i]
		switch {
		case strings.HasPrefix(s[i:], "<!--"):
			e := strings.Index(s[i:to], "-->")
			if e < 0 {
				return to, false
			}
			i += e + 3
		case c == '<' && i+1 < to && s[i+1] == '/':
			e := strings.IndexByte(s[i:to], '>')
			if e < 0 {
				return to, false
			}
			return i + e + 1, true
		case c == '<' && i+1 < to && (isLetter(s[i+1]) || s[i+1] == '>'):
			if s[i+1] == '>' { // a fragment <>…</>: its children belong to t
				var end int
				end, _ = p.children(i+2, to, t)
				i = end
				continue
			}
			i = p.element(i, to, t)
		case c == '{':
			e := p.expr(i, to)
			if e < 0 {
				return to, false
			}
			inner := strings.TrimSpace(s[i+1 : e-1])
			if !(strings.HasPrefix(inner, "/*") && strings.HasSuffix(inner, "*/")) && inner != "" {
				// Svelte blocks ({#if}, {/if}, {:else}) are not content.
				if !(p.html && len(inner) > 0 && strings.ContainsRune("#/:", rune(inner[0]))) {
					t.text = true
				}
			}
			if !p.html {
				p.js(i+1, e-1, t)
			}
			i = e
		default:
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				t.text = true
			}
			i++
		}
	}
	return to, false
}
