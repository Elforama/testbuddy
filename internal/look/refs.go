package look

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

// SelectorFor builds a flow selector (flow-style YAML) that finds e on screen s.
// For actions it prefers the id, else the text, adding an index when needed; for checks
// (assertions) an element with both id and text is checked on both.
func SelectorFor(s *ui.Screen, e *Element, check bool) string {
	n := e.Node
	quote := func(v string) string { return strconv.Quote(v) }
	try := func(sel *match.Selector) (int, bool) {
		_, all, err := match.Find(s, sel)
		if err != nil {
			return 0, false
		}
		for i, m := range all {
			if m == n || n.IsDescendantOf(m) || m.IsDescendantOf(n) {
				return i, true
			}
		}
		return 0, false
	}
	label := n.Text
	if label == "" || (n.ShowingHint && n.Text == n.Hint) {
		label = n.Desc
	}
	if check && n.ID != "" && label != "" && !n.Editable {
		return fmt.Sprintf("{id: %s, text: %s}", quote(regexp.QuoteMeta(ShortID(n.ID))), quote(regexp.QuoteMeta(label)))
	}
	if n.ID != "" {
		id := regexp.QuoteMeta(ShortID(n.ID))
		if i, ok := try(&match.Selector{ID: id}); ok {
			if i == 0 {
				return fmt.Sprintf("{id: %s}", quote(id))
			}
			return fmt.Sprintf("{id: %s, index: %d}", quote(id), positionIndex(s, &match.Selector{ID: id}, n))
		}
	}
	// Its own text, else the texts inside it (a list row labelled by its children).
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
		if i, ok := try(&match.Selector{Text: pat}); ok {
			if i == 0 {
				return quote(pat)
			}
			return fmt.Sprintf("{text: %s, index: %d}", quote(pat), positionIndex(s, &match.Selector{Text: pat}, n))
		}
	}
	if check && len(texts) > 0 {
		return quote(regexp.QuoteMeta(texts[0]))
	}
	// Last resort: the element's centre as a screen percentage.
	if s.Width > 0 && s.Height > 0 {
		return fmt.Sprintf("{point: \"%d%%,%d%%\"}", n.Bounds.CX()*100/s.Width, n.Bounds.CY()*100/s.Height)
	}
	return fmt.Sprintf("{point: \"%d,%d\"}", n.Bounds.CX(), n.Bounds.CY())
}

// positionIndex is n's index among the selector's matches in the format's index order (y, x).
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

// ResolveRefs replaces @N refs (outside quotes) in YAML command text with selectors for the
// elements of listing l on screen s. Assertions get check selectors.
func ResolveRefs(text string, l *Listing, s *ui.Screen) (string, error) {
	if !strings.Contains(text, "@") {
		return text, nil
	}
	var out strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
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
			if ch == '@' {
				if m := refPattern.FindStringSubmatchIndex(line[i:]); m != nil && m[0] == 0 {
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
