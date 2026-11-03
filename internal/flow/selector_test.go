package flow

import (
	"reflect"
	"testing"
)

// TestSelectorScalarIsText pins that a bare string is a text selector, numbers included.
func TestSelectorScalarIsText(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"- assertVisible: Welcome.*\n", "Welcome.*"},
		{"- assertVisible: \"Item 3\"\n", "Item 3"},
		{"- assertVisible: 42\n", "42"},
		{"- assertVisible: ${USER}\n", "${USER}"},
	} {
		c := step(t, tc.body)
		if c.Selector == nil || c.Selector.Text != tc.want || c.Selector.ID != "" {
			t.Errorf("%q: %+v", tc.body, c.Selector)
		}
	}
}

// TestSelectorAllFields pins every scalar, size, state and trait field of a selector map.
func TestSelectorAllFields(t *testing.T) {
	s := step(t, `- assertVisible:
    text: Welcome.*
    id: header_title
    index: 2
    css: "#main > h1"
    width: 100
    height: 50
    tolerance: 5
    enabled: true
    selected: false
    checked: TRUE
    focused: False
    traits: text long_text Square
`).Selector
	if s.Text != "Welcome.*" || s.ID != "header_title" || s.Index != "2" || s.CSS != "#main > h1" {
		t.Errorf("strings: %+v", s)
	}
	if s.Width == nil || *s.Width != 100 || s.Height == nil || *s.Height != 50 || s.Tolerance == nil || *s.Tolerance != 5 {
		t.Errorf("sizes: %v %v %v", s.Width, s.Height, s.Tolerance)
	}
	if s.Enabled == nil || !*s.Enabled || s.Selected == nil || *s.Selected || s.Checked == nil || !*s.Checked || s.Focused == nil || *s.Focused {
		t.Errorf("states: %v %v %v %v", s.Enabled, s.Selected, s.Checked, s.Focused)
	}
	if !reflect.DeepEqual(s.Traits, []string{"text", "long-text", "square"}) {
		t.Errorf("traits are lower-cased, _ becomes -: %v", s.Traits)
	}
	if s.Optional {
		t.Error("not optional")
	}
}

// TestSelectorUnsetFieldsStayNil pins that absent or null optional fields are nil, not zero.
func TestSelectorUnsetFieldsStayNil(t *testing.T) {
	s := step(t, "- assertVisible:\n    text: X\n    width:\n    enabled: ~\n    traits:\n    below:\n").Selector
	if s.Width != nil || s.Height != nil || s.Tolerance != nil || s.Enabled != nil || s.Selected != nil ||
		s.Checked != nil || s.Focused != nil || s.Traits != nil || s.Below != nil || s.ContainsDescendants != nil {
		t.Errorf("%+v", s)
	}
}

// TestSelectorInterpolatedStrings pins that ${...} is kept raw in string fields.
func TestSelectorInterpolatedStrings(t *testing.T) {
	s := step(t, "- tapOn:\n    text: ${NAME}\n    id: ${ID}\n    index: ${IDX}\n").Selector
	if s.Text != "${NAME}" || s.ID != "${ID}" || s.Index != "${IDX}" {
		t.Errorf("%+v", s)
	}
}

// TestSelectorRelations pins below/above/leftOf/rightOf/childOf/containsChild/containsDescendants.
func TestSelectorRelations(t *testing.T) {
	s := step(t, `- tapOn:
    text: Buy
    below: Header
    above: {id: footer}
    leftOf: {text: Price, index: 1}
    rightOf: {id: icon}
    childOf: {id: list}
    containsChild: {text: Child}
    containsDescendants:
      - Title
      - {id: subtitle}
`).Selector
	if s.Below == nil || s.Below.Text != "Header" {
		t.Errorf("below: %+v", s.Below)
	}
	if s.Above == nil || s.Above.ID != "footer" {
		t.Errorf("above: %+v", s.Above)
	}
	if s.LeftOf == nil || s.LeftOf.Text != "Price" || s.LeftOf.Index != "1" {
		t.Errorf("leftOf: %+v", s.LeftOf)
	}
	if s.RightOf == nil || s.RightOf.ID != "icon" {
		t.Errorf("rightOf: %+v", s.RightOf)
	}
	if s.ChildOf == nil || s.ChildOf.ID != "list" {
		t.Errorf("childOf: %+v", s.ChildOf)
	}
	if s.ContainsChild == nil || s.ContainsChild.Text != "Child" {
		t.Errorf("containsChild: %+v", s.ContainsChild)
	}
	if len(s.ContainsDescendants) != 2 || s.ContainsDescendants[0].Text != "Title" || s.ContainsDescendants[1].ID != "subtitle" {
		t.Errorf("containsDescendants: %+v", s.ContainsDescendants)
	}
}

// TestSelectorRelationsNest pins that relative selectors nest to any depth.
func TestSelectorRelationsNest(t *testing.T) {
	s := step(t, `- tapOn:
    text: A
    below:
      text: B
      childOf:
        id: C
        containsDescendants:
          - {text: D, rightOf: E}
`).Selector
	d := s.Below.ChildOf.ContainsDescendants[0]
	if s.Below.Text != "B" || s.Below.ChildOf.ID != "C" || d.Text != "D" || d.RightOf.Text != "E" {
		t.Errorf("%+v", s)
	}
}

// TestNestedSelectorsAcceptTapOptions pins that tap options inside nested selectors are tolerated.
func TestNestedSelectorsAcceptTapOptions(t *testing.T) {
	s := step(t, `- tapOn:
    text: A
    below: {id: x, optional: true, point: "1,1", repeat: 2, delay: 3, label: L, retryTapIfNoChange: true, waitUntilVisible: true, waitToSettleTimeoutMs: 9}
    containsDescendants:
      - {id: y, optional: false, label: M}
`).Selector
	if s.Below.ID != "x" || !s.Below.Optional {
		t.Errorf("below: %+v", s.Below)
	}
	if s.ContainsDescendants[0].ID != "y" || s.ContainsDescendants[0].Optional {
		t.Errorf("descendant: %+v", s.ContainsDescendants[0])
	}
}

// TestSelectorOptional pins that `optional: true` in a selector makes the command optional.
func TestSelectorOptional(t *testing.T) {
	for _, body := range []string{
		"- tapOn: {text: X, optional: true}\n",
		"- assertVisible: {text: X, optional: true}\n",
		"- assertNotVisible: {id: X, optional: true}\n",
		"- copyTextFrom: {id: X, optional: true}\n",
		"- scrollUntilVisible: {element: {text: X, optional: true}}\n",
		"- swipe: {from: {id: X, optional: true}, direction: UP}\n",
	} {
		c := step(t, body)
		if !c.Optional {
			t.Errorf("%q: command should be optional", body)
		}
	}
}

// TestSelectorErrors pins rejected selector values, with the line of the bad value.
func TestSelectorErrors(t *testing.T) {
	runErrCases(t, hdr, []errCase{
		{"width not a number", "- tapOn:\n    text: X\n    width: wide\n", `expected a number, got "wide"`, 5},
		{"height not a number", "- tapOn: {height: [1]}\n", "expected a number", 3},
		{"tolerance not a number", "- tapOn: {tolerance: lots}\n", "expected a number", 3},
		{"enabled not a bool", "- tapOn:\n    enabled: maybe\n", `expected true or false, got "maybe"`, 4},
		{"selected not a bool", "- tapOn: {selected: 1}\n", "expected true or false", 3},
		{"checked yes is not a bool", "- tapOn: {checked: yes}\n", "expected true or false", 3},
		{"focused not a bool", "- tapOn: {focused: nope}\n", "expected true or false", 3},
		{"unknown trait", "- tapOn: {traits: text shiny}\n", `unknown trait "shiny"`, 3},
		{"selector is a list", "- tapOn: [a, b]\n", "a selector is a text or a map", 3},
		{"relative selector is a list", "- tapOn: {below: [a]}\n", "a selector is a text or a map", 3},
		{"unknown key in below", "- tapOn:\n    below:\n      foo: 1\n", `Unknown Property: "foo" is not an option of below`, 5},
		{"unknown key in above", "- tapOn: {above: {bar: 1}}\n", "is not an option of above", 3},
		{"unknown key in leftOf", "- tapOn: {leftOf: {bar: 1}}\n", "is not an option of leftOf", 3},
		{"unknown key in rightOf", "- tapOn: {rightOf: {bar: 1}}\n", "is not an option of rightOf", 3},
		{"unknown key in childOf", "- tapOn: {childOf: {bar: 1}}\n", "is not an option of childOf", 3},
		{"unknown key in containsChild", "- tapOn: {containsChild: {bar: 1}}\n", "is not an option of containsChild", 3},
		{"containsDescendants not a list", "- tapOn: {containsDescendants: Title}\n", "containsDescendants is a list of selectors", 3},
		{"unknown key in a descendant", "- tapOn:\n    containsDescendants:\n      - {baz: 1}\n", "is not an option of containsDescendants", 5},
		{"descendant is a list", "- tapOn: {containsDescendants: [[x]]}\n", "a selector is a text or a map", 3},
		{"bad value deep inside a relation", "- tapOn:\n    below:\n      above:\n        width: x\n", "expected a number", 6},
		{"misspelled key names the first unknown", "- assertVisible: {txt: X, zz: 1}\n", `"txt" is not an option of assertVisible`, 3},
	})
}

// TestHasCriteria pins which selector fields count as filtering criteria.
func TestHasCriteria(t *testing.T) {
	i, b := 1, true
	sub := &Selector{Text: "x"}
	cases := []struct {
		name string
		s    *Selector
		want bool
	}{
		{"nil", nil, false},
		{"empty", &Selector{}, false},
		{"optional only", &Selector{Optional: true}, false},
		{"tolerance only", &Selector{Tolerance: &i}, false},
		{"text", &Selector{Text: "x"}, true},
		{"id", &Selector{ID: "x"}, true},
		{"index", &Selector{Index: "0"}, true},
		{"css", &Selector{CSS: "a"}, true},
		{"width", &Selector{Width: &i}, true},
		{"height", &Selector{Height: &i}, true},
		{"enabled", &Selector{Enabled: &b}, true},
		{"selected", &Selector{Selected: &b}, true},
		{"checked", &Selector{Checked: &b}, true},
		{"focused", &Selector{Focused: &b}, true},
		{"traits", &Selector{Traits: []string{"text"}}, true},
		{"below", &Selector{Below: sub}, true},
		{"above", &Selector{Above: sub}, true},
		{"leftOf", &Selector{LeftOf: sub}, true},
		{"rightOf", &Selector{RightOf: sub}, true},
		{"containsChild", &Selector{ContainsChild: sub}, true},
		{"childOf", &Selector{ChildOf: sub}, true},
		{"containsDescendants", &Selector{ContainsDescendants: []*Selector{sub}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.HasCriteria(); got != tc.want {
				t.Errorf("HasCriteria() = %v, want %v", got, tc.want)
			}
		})
	}
}
