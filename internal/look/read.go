package look

import (
	"strings"
	"unicode"
)

type Reading struct {
	Label   string `json:"label"`
	Value   string `json:"value"`
	From    string `json:"from"`
	Matches int    `json:"matches"`
}

type labelMatch struct {
	e    *Element
	name string
	rest string
	ends bool
}

const labelSeparators = " \t\n:=-–—,·|"

func readKey(s string) string {
	return strings.ToLower(strings.Trim(s, labelSeparators))
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

func (l *Listing) beside(o *Element) (string, bool) {
	if o == nil || (o.toggles() && o.Label != "") || o.Kind == "Button" || o.Kind == "Link" || o.Kind == "Heading" {
		return "", false
	}
	return l.reading(o)
}

func (e *Element) ownValue() bool { return e.Value != "" && !e.toggles() && !e.isField() }

func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func restOfLabel(label, query string) (rest string, after, ok bool) {
	lab, q := []rune(label), []rune(strings.ToLower(query))
	for i := 0; len(q) > 0 && i+len(q) <= len(lab); i++ {
		if i > 0 && wordRune(lab[i-1]) && wordRune(q[0]) {
			continue
		}
		if end := i + len(q); end < len(lab) && wordRune(lab[end]) && wordRune(q[len(q)-1]) {
			continue
		}
		same := true
		for j := range q {
			if unicode.ToLower(lab[i+j]) != q[j] {
				same = false
				break
			}
		}
		if !same {
			continue
		}
		if rest := strings.Trim(string(lab[i+len(q):]), labelSeparators); rest != "" {
			return rest, true, true
		}
		rest := strings.Trim(string(lab[:i]), labelSeparators)
		return rest, false, rest != ""
	}
	return "", false, false
}

func (l *Listing) inside(e *Element) (string, bool) {
	for _, o := range l.Elements {
		if o != e && o.Node.IsDescendantOf(e.Node) && (o.toggles() || o.isField() || o.ownValue()) {
			return l.reading(o)
		}
	}
	return "", false
}

func (l *Listing) rightOf(e *Element) (string, bool) {
	b := e.Node.Bounds
	var best *Element
	for _, o := range l.Elements {
		ob := o.Node.Bounds
		if related(o.Node, e.Node) || ob.L < b.R-4 || overlap(b.T, b.B, ob.T, ob.B)*2 <= min(b.H(), ob.H()) {
			continue
		}
		if _, ok := l.reading(o); ok && (best == nil || ob.L < best.Node.Bounds.L) {
			best = o
		}
	}
	return l.beside(best)
}

func (l *Listing) under(e *Element) (string, bool) {
	b := e.Node.Bounds
	var best *Element
	for _, o := range l.Elements {
		ob := o.Node.Bounds
		if related(o.Node, e.Node) || ob.T < b.B-4 || ob.T-b.B > 2*b.H() || overlap(b.L, b.R, ob.L, ob.R) <= 0 {
			continue
		}
		if _, ok := l.reading(o); !ok {
			continue
		}
		if best == nil || ob.T < best.Node.Bounds.T || (ob.T == best.Node.Bounds.T && ob.L < best.Node.Bounds.L) {
			best = o
		}
	}
	return l.beside(best)
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
	var exact, longer, ending []labelMatch
	for _, e := range l.Elements {
		var found *labelMatch
		for _, name := range e.names() {
			if name == "" {
				continue
			}
			if readKey(name) == key {
				found = &labelMatch{e: e, name: name}
				break
			}
			if rest, after, ok := restOfLabel(name, key); ok && found == nil {
				found = &labelMatch{e: e, name: name, rest: rest, ends: !after}
			}
		}
		switch {
		case found == nil:
		case found.rest == "":
			exact = append(exact, *found)
		case !found.ends:
			longer = append(longer, *found)
		default:
			ending = append(ending, *found)
		}
	}
	of := func(from []labelMatch, want func(*Element) bool) []labelMatch {
		var out []labelMatch
		for _, m := range from {
			if want(m.e) {
				out = append(out, m)
			}
		}
		return out
	}
	plain := func(e *Element) bool { return !e.toggles() && !e.isField() && !e.ownValue() }
	for g, group := range [][]labelMatch{exact, longer, ending} {
		for _, rule := range []struct {
			from string
			want func(*Element) bool
		}{{"switch", (*Element).toggles}, {"field", (*Element).isField}, {"value", (*Element).ownValue}} {
			if ms := of(group, rule.want); len(ms) > 0 {
				v, _ := l.reading(ms[0].e)
				return &Reading{Label: ms[0].name, Value: v, From: rule.from, Matches: len(ms)}, true
			}
		}
		labels := of(group, plain)
		if g > 0 && len(labels) > 0 {
			return &Reading{Label: labels[0].name, Value: labels[0].rest, From: "label", Matches: len(labels)}, true
		}
		for _, m := range labels {
			for _, beside := range []struct {
				from string
				read func(*Element) (string, bool)
			}{{"inside", l.inside}, {"right", l.rightOf}, {"below", l.under}} {
				if v, ok := beside.read(m.e); ok {
					return &Reading{Label: m.name, Value: v, From: beside.from, Matches: len(labels)}, true
				}
			}
		}
	}
	return nil, len(exact)+len(longer)+len(ending) > 0
}
