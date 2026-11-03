package match

import (
	"testing"
)

// Describe renders a selector for error messages, fields in a fixed order.
func TestDescribe(t *testing.T) {
	cases := []struct {
		name string
		sel  Selector
		want string
	}{
		{"empty", Selector{}, "any element"},
		{"text is quoted", Selector{Text: "Sign in"}, `"Sign in"`},
		{"text escapes quotes", Selector{Text: `say "hi"`}, `"say \"hi\""`},
		{"id", Selector{ID: "login"}, "id: login"},
		{"css", Selector{CSS: "button.primary"}, "css: button.primary"},
		{"width only", Selector{Width: ptr(100)}, "size: 100x*"},
		{"height only", Selector{Height: ptr(50)}, "size: *x50"},
		{"width and height", Selector{Width: ptr(100), Height: ptr(50)}, "size: 100x50"},
		{"tolerance alone says nothing", Selector{Tolerance: ptr(5)}, "any element"},
		{"enabled false", Selector{Enabled: ptr(false)}, "enabled: false"},
		{"all states in order", Selector{Focused: ptr(true), Checked: ptr(false), Selected: ptr(true), Enabled: ptr(true)},
			"enabled: true, selected: true, checked: false, focused: true"},
		{"traits", Selector{Traits: []string{"text", "square"}}, "traits: text square"},
		{"index", Selector{Index: "2"}, "index: 2"},
		{"below", Selector{Below: &Selector{Text: "Title"}}, `below "Title"`},
		{"above", Selector{Above: &Selector{ID: "footer"}}, "above id: footer"},
		{"left of", Selector{LeftOf: &Selector{Text: "B"}}, `left of "B"`},
		{"right of", Selector{RightOf: &Selector{Text: "A"}}, `right of "A"`},
		{"containing child", Selector{ContainsChild: &Selector{Text: "x"}}, `containing child "x"`},
		{"child of", Selector{ChildOf: &Selector{ID: "list"}}, "child of id: list"},
		{"descendants", Selector{ContainsDescendants: []*Selector{{Text: "a"}, {ID: "b"}}}, `containing "a", containing id: b`},
		{"nested empty", Selector{Below: &Selector{}}, "below any element"},
		{"nested relative", Selector{Text: "Buy", Below: &Selector{Text: "Title", ChildOf: &Selector{ID: "card"}}},
			`"Buy", below "Title", child of id: card`},
		{"everything, in order", Selector{
			Index: "1", Text: "OK", ID: "ok", CSS: ".ok", Width: ptr(10), Height: ptr(20),
			Enabled: ptr(true), Traits: []string{"text"},
			Below: &Selector{Text: "b"}, Above: &Selector{Text: "a"}, LeftOf: &Selector{Text: "l"}, RightOf: &Selector{Text: "r"},
			ContainsChild: &Selector{Text: "c"}, ChildOf: &Selector{Text: "p"},
			ContainsDescendants: []*Selector{{Text: "d"}},
		}, `"OK", id: ok, css: .ok, size: 10x20, enabled: true, traits: text, below "b", above "a", left of "l", right of "r", containing child "c", child of "p", containing "d", index: 1`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.sel.Describe(); got != c.want {
				t.Errorf("Describe() = %q\nwant         %q", got, c.want)
			}
		})
	}
}

// Walk visits the selector and every nested one: itself, the relative and tree relations,
// then the descendants, each depth-first; a nil selector visits nothing.
func TestSelectorWalk(t *testing.T) {
	sel := &Selector{
		Text:  "root",
		Below: &Selector{Text: "below", ChildOf: &Selector{Text: "below.childOf"}},
		Above: &Selector{Text: "above"}, LeftOf: &Selector{Text: "leftOf"}, RightOf: &Selector{Text: "rightOf"},
		ContainsChild: &Selector{Text: "containsChild"}, ChildOf: &Selector{Text: "childOf"},
		ContainsDescendants: []*Selector{{Text: "d1", Above: &Selector{Text: "d1.above"}}, {Text: "d2"}},
	}
	var got []string
	sel.Walk(func(s *Selector) { got = append(got, s.Text) })
	want := []string{"root", "below", "below.childOf", "above", "leftOf", "rightOf", "containsChild", "childOf", "d1", "d1.above", "d2"}
	if !sameNames(got, want) {
		t.Errorf("Walk = %v\nwant %v", got, want)
	}

	var none *Selector
	none.Walk(func(*Selector) { t.Error("visited a nil selector") })

	count := 0
	(&Selector{}).Walk(func(*Selector) { count++ })
	if count != 1 {
		t.Errorf("a bare selector visited %d times", count)
	}
}

// Walk lets the engine fill CSS results into every nested selector before matching.
func TestSelectorWalkCanMutate(t *testing.T) {
	sel := &Selector{CSS: "a", Below: &Selector{CSS: "b"}, ContainsDescendants: []*Selector{{CSS: "c"}}}
	sel.Walk(func(s *Selector) {
		if s.CSS != "" {
			s.CSSNodes = map[int]bool{len(s.CSS): true}
		}
	})
	for _, s := range []*Selector{sel, sel.Below, sel.ContainsDescendants[0]} {
		if s.CSSNodes == nil {
			t.Errorf("selector %q was not filled", s.CSS)
		}
	}
}
