package a11y

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type tag struct {
	name          string
	off           int
	end           int
	attrs         map[string]string
	spread        bool
	text          bool
	parent        *tag
	children      []*tag
	attrKids      []*tag
	attr          string
	unread, guess bool
}

func (t *tag) unsure() string {
	switch {
	case t.unread:
		return "its attributes could not all be read: a {…} in them does not close (code being edited, or a regular expression read as a division), and it may have an id further on; check it and add one by hand"
	case t.guess:
		return "it comes after a {…} that does not close (code being edited, or a regular expression read as a division), so whether a loop repeats it is unknown; check it and add an id by hand"
	}
	return ""
}

func (t *tag) has(name string) bool { _, ok := t.attrs[name]; return ok }

func (t *tag) attrTrue(name string) bool {
	v, ok := t.attrs[name]
	if !ok {
		return false
	}
	v = strings.Trim(strings.TrimSpace(v), `{}"'`)
	return v == "" || v == "true"
}

func (t *tag) walk(fn func(*tag) bool) {
	t.visit(fn, false)
}

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

var voidTags = map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true,
	"img": true, "input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true}

type markupParser struct {
	src  string
	html bool
	fold bool

	closed  memo
	blocks  memo
	pairs   map[int]int
	skipped memo
	lits    []span
	steps   int
	broke   bool

	commentEnds, tripleEnds []int
}

func parseMarkup(src string, html bool) *tag {
	return parse(&markupParser{src: src, html: html})
}

func parseSource(src string, html bool) (*tag, func(int) bool) {
	p := &markupParser{src: src, html: html}
	return parse(p), p.inLiteral
}

func parseHTMLPage(src string) *tag {
	return parse(&markupParser{src: src, html: true, fold: true})
}

func parse(p *markupParser) *tag {
	src, html := p.src, p.html
	root := &tag{name: "", attrs: map[string]string{}}
	if html {
		for i := 0; i < len(src); {
			i, _ = p.children(i, len(src), root)
		}
	} else {
		p.js(0, len(src), root, true)
	}
	sort.Slice(p.lits, func(i, j int) bool { return p.lits[i].from < p.lits[j].from })
	return root
}

func (p *markupParser) js(from, to int, parent *tag, stmt bool) {
	p.scan(from, to, parent, stmt, false)
}

func (p *markupParser) guessJS(from, to int, parent *tag) (int, bool) {
	return p.scan(from, to, parent, false, true)
}

func (p *markupParser) scan(from, to int, parent *tag, stmt, guess bool) (int, bool) {
	s := p.src
	last := from - 1
	var open []bool
	after := -1
	eol := -1
	for i := from; i < to; {
		p.steps++
		if guess && s[i] == '<' && i+1 < to && (s[i+1] == '/' && i+2 < to && (isLetter(s[i+2]) || s[i+2] == '>')) {
			if e := strings.IndexByte(s[i:to], '>'); e >= 0 {
				return i + e + 1, true
			}
			return to, false
		}
		end := to
		if guess {
			if i > eol {
				eol = lineEnd(s, i, to)
			}
			end = eol
		}
		if j, value := p.literal(i, from, last, after, end); j != i {
			if guess {
				if k := closeTagIn(s[i+1 : j]); k >= 0 {
					j = i + 1 + k
				}
				parent.text = parent.text || value
			}
			if value {
				last = j - 1
				if s[last] == '\n' && last-1 > i {
					last--
					if s[last] == '\r' && last-1 > i {
						last--
					}
				}
			}
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
		switch s[i] {
		case '(', '[', '{':
			open = append(open, regexFollows(s, i, from, last, stmt))
		case ')', ']', '}':
			if n := len(open); n > 0 {
				if open[n-1] {
					after = i
				}
				open = open[:n-1]
			}
		}
		if !isSpace(s[i]) {
			last = i
			if guess {
				parent.text = true
			}
		}
		i++
	}
	return to, false
}

func closeTagIn(s string) int {
	for k := 0; ; {
		e := strings.Index(s[k:], "</")
		if e < 0 {
			return -1
		}
		k += e
		if k+2 < len(s) && (isLetter(s[k+2]) || s[k+2] == '>') || k+2 == len(s) {
			return k
		}
		k++
	}
}

func regexFollows(s string, i, from, last int, stmt bool) bool {
	switch s[i] {
	case '{':
		return blockOpens(s, from, last, stmt)
	case '(':
		return last >= from && endsWithKeyword(s[from:last+1], condKeywords)
	}
	return false
}

var condKeywords = []string{"if", "while", "for", "with"}

func blockOpens(s string, from, last int, stmt bool) bool {
	if last < from {
		return stmt
	}
	switch c := s[last]; {
	case c == ')' || c == ';' || c == '{' || c == '}':
		return true
	case c == '>':
		return last > from && s[last-1] == '='
	case isLetter(c) || c == '$' || (c >= '0' && c <= '9'):
		return !endsWithKeyword(s[from:last+1], regexKeywords)
	}
	return false
}

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
		return true
	}
	return k > j && strings.HasPrefix(s[k:], "extends") && (k+7 == len(s) || !isNameChar(s[k+7]))
}

func (p *markupParser) literal(i, from, last, after, to int) (int, bool) {
	s := p.src
	switch {
	case s[i] == '`':
		for j := i + 1; j < to; j++ {
			p.steps++
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
	case s[i] == '/' && i+1 < to && s[i+1] != '/' && s[i+1] != '*' && regexStarts(s, from, last, after):
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
	j := p.skip(i)
	if j > to {
		j = to
	}
	return j, j != i && s[i] != '/'
}

func regexStarts(s string, from, last, after int) bool {
	if last < from || last == after {
		return true
	}
	if strings.ContainsRune("(=,?:[!&|;{>+-*%~^<", rune(s[last])) {
		return true
	}
	return endsWithKeyword(s[from:last+1], regexKeywords)
}

var regexKeywords = []string{"return", "typeof", "case", "in", "of", "void", "yield", "await",
	"instanceof", "delete", "new", "throw"}

func endsWithKeyword(s string, words []string) bool {
	for _, w := range words {
		if strings.HasSuffix(s, w) && (len(s) == len(w) || !isNameChar(s[len(s)-len(w)-1])) {
			return true
		}
	}
	return false
}

type span struct{ from, to int }

func (p *markupParser) closing(open, to int) int {
	return p.kept(&p.closed, open, to, p.closeExpr)
}

func (p *markupParser) closingBlock(open, to int) int {
	return p.kept(&p.blocks, open, to, p.closeBlock)
}

type memo struct {
	ok     map[int]span
	failed map[span]bool
}

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
		m.ok[at] = span{e, to}
	}
	return e
}

func (p *markupParser) closeExpr(open, to int) int  { return p.close(open, to, false) }
func (p *markupParser) closeBlock(open, to int) int { return p.close(open, to, true) }

func (p *markupParser) close(open, to int, block bool) int {
	s := p.src
	last := open
	after := -1
	for i := open + 1; i < to; {
		p.steps++
		if j, value := p.literal(i, open, last, after, to); j != i {
			if value {
				last = j - 1
			}
			i = j
			continue
		}
		c := s[i]
		if c == '<' && p.jsxStarts(i, open, last) {
			if e := p.skipElement(i, to); e >= 0 {
				i = e
				last = i - 1
				continue
			}
		}
		switch c {
		case '(', '{', '[':
			follows := regexFollows(s, i, open+1, last, block)
			var e int
			if c == '{' && follows {
				e = p.closingBlock(i, to)
			} else {
				e = p.closing(i, to)
			}
			if e < 0 {
				return -1
			}
			i = e
			last = i - 1
			if follows {
				after = last
			}
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

func (p *markupParser) skipElement(i, to int) int {
	return p.kept(&p.skipped, i, to, p.skipTag)
}

func (p *markupParser) skipTag(i, to int) int {
	s := p.src
	j := i + 1
	if j < to && s[j] == '>' {
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
			j = min(p.skip(j), to)
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
			if s[k-1] == '=' {
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

func (p *markupParser) expr(open, to int) int {
	if p.html {
		return p.matched(open)
	}
	if e := p.closing(open, to); e >= 0 {
		return e
	}
	if e := p.matched(open); e <= to {
		return e
	}
	return -1
}

func (p *markupParser) matched(open int) int {
	if e, ok := p.pairs[open]; ok {
		return e
	}
	s, e := p.src, -1
	for i := open + 1; i < len(s); {
		p.steps++
		if j := p.skip(i); j != i {
			i = j
			continue
		}
		c := s[i]
		if c == '(' || c == '{' || c == '[' {
			if i = p.matched(i); i < 0 {
				break
			}
			continue
		}
		if c == ')' || c == '}' || c == ']' {
			e = i + 1
			break
		}
		i++
	}
	if p.pairs == nil {
		p.pairs = map[int]int{}
	}
	p.pairs[open] = e
	return e
}

func (p *markupParser) skip(i int) int {
	s := p.src
	switch {
	case strings.HasPrefix(s[i:], "/*"):
		return p.past(&p.commentEnds, "*/", i+2)
	case strings.HasPrefix(s[i:], `"""`):
		return p.past(&p.tripleEnds, `"""`, i+3)
	}
	j := skipLiteral(s, i)
	p.steps += j - i
	return j
}

func (p *markupParser) past(ends *[]int, sep string, from int) int {
	if *ends == nil {
		*ends = []int{}
		for k := 0; ; k++ {
			e := strings.Index(p.src[k:], sep)
			if e < 0 {
				break
			}
			k += e
			*ends = append(*ends, k)
		}
	}
	if k := sort.SearchInts(*ends, from); k < len(*ends) {
		return (*ends)[k] + len(sep)
	}
	return len(p.src)
}

func lineEnd(s string, i, to int) int {
	if e := strings.IndexByte(s[i:to], '\n'); e >= 0 {
		return i + e
	}
	return to
}

func jsMatcher(src string) func(string, int) int {
	p := &markupParser{src: src}
	return func(_ string, open int) int { return p.expr(open, len(src)) }
}

func (p *markupParser) inLiteral(off int) bool {
	i := sort.Search(len(p.lits), func(i int) bool { return p.lits[i].to > off })
	return i < len(p.lits) && p.lits[i].from <= off
}

func isLetter(c byte) bool { return c < 128 && unicode.IsLetter(rune(c)) || c == '_' }

func isNameChar(c byte) bool {
	return isLetter(c) || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == ':' || c == '$'
}

func (p *markupParser) element(i, to int, parent *tag) int {
	n := len(parent.children)
	end := p.readElement(i, to, parent)
	parent.children[n].end = end
	return end
}

func (p *markupParser) readElement(i, to int, parent *tag) int {
	s := p.src
	j := i + 1
	for j < to && isNameChar(s[j]) {
		j++
	}
	t := &tag{name: s[i+1 : j], off: i, attrs: map[string]string{}, parent: parent, guess: p.broke}
	if p.fold {
		t.name = strings.ToLower(t.name)
	}
	parent.children = append(parent.children, t)
	if !p.html {
		j = typeArgs(s, j, to)
	}
	selfClose := false
	for j < to {
		c := s[j]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			j++
		case !p.html && c == '/' && j+1 < to && (s[j+1] == '*' || s[j+1] == '/'):
			j = min(p.skip(j), to)
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
				return p.unread(t, "{…}", j, to)
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
			if k == j {
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
							return p.unread(t, name, j, to)
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

func (p *markupParser) unread(t *tag, attr string, j, to int) int {
	t.unread = true
	if p.html {
		return lineEnd(p.src, j, to)
	}
	p.broke = true
	h := &tag{off: j + 1, attrs: map[string]string{}, parent: t, attr: attr}
	end, _ := p.guessJS(j+1, to, h)
	if len(h.children) > 0 {
		t.attrKids = append(t.attrKids, h)
	}
	return end
}

func (p *markupParser) attrJSX(t *tag, attr string, from, to int) {
	h := &tag{off: from, attrs: map[string]string{}, parent: t, attr: attr}
	p.js(from, to, h, false)
	if len(h.children) > 0 {
		t.attrKids = append(t.attrKids, h)
	}
}

func (p *markupParser) children(i, to int, t *tag) (int, bool) {
	s := p.src
	for i < to {
		if p.broke && !p.html {
			return p.guessJS(i, to, t)
		}
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
			if s[i+1] == '>' {
				var end int
				end, _ = p.children(i+2, to, t)
				i = end
				continue
			}
			i = p.element(i, to, t)
		case c == '{':
			e := p.expr(i, to)
			if e < 0 {
				t.text = true
				if p.html {
					i++
				} else {
					p.broke = true
				}
				continue
			}
			inner := strings.TrimSpace(s[i+1 : e-1])
			if !(strings.HasPrefix(inner, "/*") && strings.HasSuffix(inner, "*/")) && inner != "" {
				if !(p.html && len(inner) > 0 && strings.ContainsRune("#/:", rune(inner[0]))) {
					t.text = true
				}
			}
			if !p.html {
				p.js(i+1, e-1, t, false)
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
