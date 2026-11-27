package look

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

func SelectorFor(s *ui.Screen, e *Element, check bool) string {
	n := e.Node
	quote := func(v string) string { return strconv.Quote(v) }
	try := func(sel *match.Selector) (int, bool, bool) { return findsAt(s, sel, n) }
	label := n.Text
	if label == "" || (n.ShowingHint && n.Text == n.Hint) || valueShownAsText(n, s.Platform, n.Editable) {
		label = n.Desc
	}
	if label == "" {
		label = n.Hint
	}
	if check && n.ID != "" && label != "" && !n.Editable {
		return fmt.Sprintf("{id: %s, text: %s}", quote(regexp.QuoteMeta(ShortID(n.ID))), quote(regexp.QuoteMeta(label)))
	}
	var first, fallback string
	var bases []*match.Selector
	if n.ID != "" {
		id := regexp.QuoteMeta(ShortID(n.ID))
		if i, alone, ok := try(&match.Selector{ID: id}); ok {
			if alone {
				return fmt.Sprintf("{id: %s}", quote(id))
			}
			if i == 0 {
				first = fmt.Sprintf("{id: %s}", quote(id))
			} else {
				fallback = fmt.Sprintf("{id: %s, index: %d}", quote(id), positionIndex(s, &match.Selector{ID: id}, n))
			}
			bases = append(bases, &match.Selector{ID: id})
			if label != "" && !n.Editable {
				both := &match.Selector{ID: id, Text: regexp.QuoteMeta(label)}
				if i, alone, ok := try(both); ok && i == 0 {
					if alone {
						return render(both)
					}
					if first == "" {
						first = render(both)
					}
				}
			}
		}
	}
	texts := []string{}
	if label != "" {
		texts = append(texts, label)
	}
	n.Walk(func(d *ui.Node) bool {
		if d != n && len(texts) < 4 {
			if t := d.Label(); t != "" {
				texts = append(texts, t)
			}
		}
		return len(texts) < 4
	})
	for _, t := range texts {
		pat := regexp.QuoteMeta(t)
		if i, alone, ok := try(&match.Selector{Text: pat}); ok {
			if alone {
				return quote(pat)
			}
			if i == 0 {
				if first == "" {
					first = quote(pat)
				}
			} else if fallback == "" {
				fallback = fmt.Sprintf("{text: %s, index: %d}", quote(pat), positionIndex(s, &match.Selector{Text: pat}, n))
			}
			if len(bases) < 2 {
				bases = append(bases, &match.Selector{Text: pat})
			}
		}
	}
	if check && len(texts) > 0 && first == "" && fallback == "" {
		return quote(regexp.QuoteMeta(texts[0]))
	}
	switch {
	case len(bases) > 0:
		if sel := placed(s, n, bases); sel != nil {
			return render(sel)
		}
	case n.ID == "" && len(texts) == 0:
		if sel := soleInContainer(s, n); sel != nil {
			return render(sel)
		}
	}
	if first != "" {
		return first
	}
	if fallback != "" {
		return fallback
	}
	if s.Width > 0 && s.Height > 0 {
		return fmt.Sprintf("{point: \"%d%%,%d%%\"}", n.Bounds.CX()*100/s.Width, n.Bounds.CY()*100/s.Height)
	}
	return fmt.Sprintf("{point: \"%d,%d\"}", n.Bounds.CX(), n.Bounds.CY())
}

func positionIndex(s *ui.Screen, sel *match.Selector, n *ui.Node) int {
	for i := 0; i < 50; i++ {
		sel.Index = strconv.Itoa(i)
		got, _, _ := match.Find(s, sel)
		if got == nil {
			break
		}
		if got == n || n.IsDescendantOf(got) || got.IsDescendantOf(n) {
			return i
		}
	}
	return 0
}

var refPattern = regexp.MustCompile(`@(\d+)`)

func wordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.IndexByte("_.-@", b) >= 0
}

var hashIDPattern = regexp.MustCompile(`^(\s*(?:- )?(\w+):\s+)#([\w.:/-]+)(\s*)$`)

func hashID(line string) string {
	m := hashIDPattern.FindStringSubmatch(line)
	if m == nil || m[2] == "inputText" {
		return line
	}
	return m[1] + fmt.Sprintf("{id: %s}", strconv.Quote(regexp.QuoteMeta(m[3]))) + m[4]
}

func ResolveRefs(text string, l *Listing, s *ui.Screen) (string, error) {
	if !strings.ContainsAny(text, "@#") {
		return text, nil
	}
	var out strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
		line = hashID(line)
		check := strings.Contains(line, "assert") || strings.Contains(line, "isible")
		var b strings.Builder
		inQuote := byte(0)
		i := 0
		for i < len(line) {
			ch := line[i]
			if inQuote != 0 {
				if ch == inQuote {
					inQuote = 0
				}
				b.WriteByte(ch)
				i++
				continue
			}
			if ch == '"' || ch == '\'' {
				inQuote = ch
				b.WriteByte(ch)
				i++
				continue
			}
			if ch == '@' && (i == 0 || !wordByte(line[i-1])) {
				if m := refPattern.FindStringSubmatchIndex(line[i:]); m != nil && m[0] == 0 &&
					(i+m[1] == len(line) || !wordByte(line[i+m[1]])) {
					ref, _ := strconv.Atoi(line[i+m[2] : i+m[3]])
					if l == nil {
						return "", fmt.Errorf("@%d: no screen listed yet; run `tb look` first", ref)
					}
					e := l.ByRef(ref)
					if e == nil {
						return "", fmt.Errorf("@%d is not on the last listed screen; run `tb look` again", ref)
					}
					b.WriteString(SelectorFor(s, e, check))
					i += m[1]
					continue
				}
			}
			b.WriteByte(ch)
			i++
		}
		out.WriteString(b.String())
	}
	return out.String(), nil
}
