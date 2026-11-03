package a11y

import (
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

// walk visits t's subtree (t first). Returning false skips a tag's children.
func (t *tag) walk(fn func(*tag) bool) {
	if !fn(t) {
		return
	}
	for _, c := range t.children {
		c.walk(fn)
	}
}

// voidTags never have a closing tag in HTML.
var voidTags = map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true,
	"img": true, "input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true}

type markupParser struct {
	src  string
	html bool // HTML-like file: text everywhere, no JavaScript around the tags
}

// parseMarkup returns a root holding every top-level tag.
func parseMarkup(src string, html bool) *tag {
	p := &markupParser{src: src, html: html}
	root := &tag{name: "", attrs: map[string]string{}}
	if html {
		// A stray closing tag ends children(); carry on after it.
		for i := 0; i < len(src); {
			i, _ = p.children(i, len(src), root)
		}
	} else {
		p.js(0, len(src), root)
	}
	return root
}

// js scans JavaScript between from and to, parsing each JSX element it finds into parent.
func (p *markupParser) js(from, to int, parent *tag) {
	s := p.src
	for i := from; i < to; {
		if j := skipLiteral(s, i); j != i {
			i = j
			continue
		}
		if s[i] == '<' && p.jsxStarts(i, from) {
			i = p.element(i, to, parent)
			continue
		}
		i++
	}
}

// jsxStarts tells a JSX tag from a comparison or a TypeScript type argument: a letter or
// '>' follows, and before it comes an operator, a bracket or `return`.
func (p *markupParser) jsxStarts(i, from int) bool {
	s := p.src
	if i+1 >= len(s) || !(isLetter(s[i+1]) || s[i+1] == '>') {
		return false
	}
	j := i - 1
	for j >= from && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
		j--
	}
	if j < from {
		return true
	}
	if strings.ContainsRune("(=,?:{}[!&|>;", rune(s[j])) {
		return true
	}
	return strings.HasSuffix(s[from:j+1], "return")
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
	parent.children = append(parent.children, t)
	selfClose := false
	// Attributes.
	for j < to {
		c := s[j]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			j++
		case c == '>':
			j++
			goto body
		case c == '/' && j+1 < to && s[j+1] == '>':
			j += 2
			selfClose = true
			goto body
		case c == '{':
			e := matching(s, j)
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
						e := matching(s, j)
						if e < 0 {
							return to
						}
						val = s[j:e]
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
			e := matching(s, i)
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
