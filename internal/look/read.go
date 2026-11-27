package look

import (
	"sort"
	"strings"
	"unicode"

	"github.com/elforama/testbuddy/internal/ui"
)

type Reading struct {
	Label   string `json:"label"`
	Value   string `json:"value"`
	From    string `json:"from"`
	Of      string `json:"of,omitempty"`
	Matches int    `json:"matches"`
}

type labelMatch struct {
	e    *Element
	name string
	rest string
}

const (
	labelTrim       = " \t\n:=-–—,·|"
	labelSeparators = ":=-–—,"
)

func readKey(s string) string {
	return strings.ToLower(strings.Trim(s, labelTrim))
}

func (e *Element) toggles() bool {
	for _, s := range e.States {
		if s == "checked" || s == "unchecked" {
			return true
		}
	}
	return false
}

func (e *Element) isField() bool { return e.Kind == "Field" || e.Kind == "SecureField" }

func (e *Element) ownValue() bool { return e.Value != "" && !e.toggles() && !e.isField() }

func (e *Element) control() bool {
	return e.isField() || e.ownValue() || (e.toggles() && e.Label == "")
}

func (e *Element) notAValue() bool {
	return (e.toggles() && e.Label != "") || e.Kind == "Button" || e.Kind == "Link" || e.Kind == "Heading"
}

func (e *Element) wholeLabel() string {
	if whole := e.Node.Label(); clip(whole, 80) == e.Label {
		return whole
	}
	return e.Label
}

func (e *Element) names() []string {
	if e.isField() {
		return []string{e.Node.Desc, e.Node.Hint}
	}
	return []string{e.wholeLabel()}
}

func (l *Listing) reading(e *Element) (string, bool) {
	n := e.Node
	switch {
	case e.toggles():
		if n.Checked {
			return "on", true
		}
		return "off", true
	case e.isField():
		if l.Platform == "web" {
			return n.Value, true
		}
		if n.ShowingHint || n.Text == n.Hint {
			return "", true
		}
		return n.Text, true
	case e.Value != "":
		return e.Value, true
	}
	whole := e.wholeLabel()
	return whole, whole != ""
}

func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func restOfLabel(label, key string) (string, bool) {
	lab := []rune(strings.TrimLeft(label, " \t\n"))
	n := len([]rune(key))
	if len(lab) < n || strings.ToLower(string(lab[:n])) != key {
		return "", false
	}
	rest := strings.TrimLeft(string(lab[n:]), " \t\n")
	if rest == "" || !strings.ContainsRune(labelSeparators, []rune(rest)[0]) {
		return "", false
	}
	rest = strings.Trim(rest, labelTrim)
	return rest, rest != ""
}

func hasWords(label, key string) bool {
	lab, q := []rune(strings.ToLower(label)), []rune(key)
	for i := 0; len(q) > 0 && i+len(q) <= len(lab); i++ {
		if string(lab[i:i+len(q)]) != key {
			continue
		}
		starts := i == 0 || !wordRune(lab[i-1]) || !wordRune(q[0])
		ends := i+len(q) == len(lab) || !wordRune(lab[i+len(q)]) || !wordRune(q[len(q)-1])
		if starts && ends {
			return true
		}
	}
	return false
}

func (l *Listing) inside(e *Element) *Element {
	for _, o := range l.Elements {
		if o != e && o.Node.IsDescendantOf(e.Node) && (o.toggles() || o.isField() || o.ownValue()) {
			return o
		}
	}
	return nil
}

func rowContainer(n *ui.Node) *ui.Node {
	var row *ui.Node
	for p := n.Parent; p != nil && p.Bounds.H()*2 <= n.Bounds.H()*5; p = p.Parent {
		row = p
	}
	return row
}

func (l *Listing) rightOf(e *Element) *Element {
	b := e.Node.Bounds
	row := rowContainer(e.Node)
	var right []*Element
	crowded := false
	for _, o := range l.Elements {
		ob := o.Node.Bounds
		if related(o.Node, e.Node) || overlap(b.T, b.B, ob.T, ob.B)*2 <= min(b.H(), ob.H()) {
			continue
		}
		if _, readable := l.reading(o); !readable || (row != nil && !o.Node.IsDescendantOf(row)) {
			continue
		}
		switch {
		case ob.L >= b.R-4:
			right = append(right, o)
		case o.Kind != "Button" && o.Kind != "Link":
			crowded = true
		}
	}
	if len(right) == 0 {
		return nil
	}
	sort.SliceStable(right, func(i, j int) bool { return right[i].Node.Bounds.L < right[j].Node.Bounds.L })
	first := right[0]
	if first.control() {
		return first
	}
	for _, o := range right[1:] {
		crowded = crowded || (o.Kind != "Button" && o.Kind != "Link")
	}
	if first.notAValue() || crowded {
		return nil
	}
	return first
}

func (l *Listing) under(e *Element) *Element {
	b := e.Node.Bounds
	var best *Element
	for _, o := range l.Elements {
		ob := o.Node.Bounds
		if related(o.Node, e.Node) || ob.T < b.B-4 || ob.T-b.B > 2*b.H() || overlap(b.L, b.R, ob.L, ob.R) <= 0 {
			continue
		}
		if _, readable := l.reading(o); !readable {
			continue
		}
		if best == nil || ob.T < best.Node.Bounds.T || (ob.T == best.Node.Bounds.T && ob.L < best.Node.Bounds.L) {
			best = o
		}
	}
	if best == nil || !best.control() {
		return nil
	}
	return best
}

func (l *Listing) Read(query string) (*Reading, bool) {
	key := readKey(query)
	if key == "" {
		return nil, false
	}
	if id, ok := strings.CutPrefix(strings.TrimSpace(query), "#"); ok {
		for _, e := range l.Elements {
			if e.ID == id {
				if v, ok := l.reading(e); ok {
					return &Reading{Label: query, Value: v, From: "id", Matches: 1}, true
				}
				return nil, true
			}
		}
	}
	var exact, longer []labelMatch
	onScreen := false
	for _, e := range l.Elements {
		for _, name := range e.names() {
			if name == "" {
				continue
			}
			if readKey(name) == key {
				exact = append(exact, labelMatch{e: e, name: name})
				break
			}
			if rest, ok := restOfLabel(name, key); ok {
				longer = append(longer, labelMatch{e: e, name: name, rest: rest})
				break
			}
			onScreen = onScreen || hasWords(name, key)
		}
	}
	matches := len(exact) + len(longer)
	plain := func(e *Element) bool { return !e.toggles() && !e.isField() && !e.ownValue() }
	for g, group := range [][]labelMatch{exact, longer} {
		for _, rule := range []struct {
			from string
			want func(*Element) bool
		}{{"switch", (*Element).toggles}, {"field", (*Element).isField}, {"value", (*Element).ownValue}} {
			for _, m := range group {
				if rule.want(m.e) {
					v, _ := l.reading(m.e)
					return &Reading{Label: m.name, Value: v, From: rule.from, Matches: matches}, true
				}
			}
		}
		for _, m := range group {
			if !plain(m.e) {
				continue
			}
			if g > 0 {
				return &Reading{Label: m.name, Value: m.rest, From: "label", Matches: matches}, true
			}
			for _, beside := range []struct {
				from string
				find func(*Element) *Element
			}{{"inside", l.inside}, {"right", l.rightOf}, {"below", l.under}} {
				if o := beside.find(m.e); o != nil {
					v, _ := l.reading(o)
					return &Reading{Label: m.name, Value: v, From: beside.from, Of: o.Kind, Matches: matches}, true
				}
			}
		}
	}
	return nil, onScreen || matches > 0
}
