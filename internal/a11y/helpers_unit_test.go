package a11y

import "testing"

// The legend lists the rules findings refer to, once each, in Rules order; Errors counts
// findings of severity error.
func TestResultUsedAndErrors(t *testing.T) {
	first, second := Rules[0], Rules[1]
	r := &Result{Findings: []Finding{
		{Rule: second.ID, Severity: second.Severity},
		{Rule: first.ID, Severity: first.Severity},
		{Rule: second.ID, Severity: second.Severity},
	}}
	used := r.Used()
	if len(used) != 2 || used[0].ID != first.ID || used[1].ID != second.ID {
		t.Errorf("Used = %v", used)
	}
	want := 0
	for _, f := range r.Findings {
		if f.Severity == Error {
			want++
		}
	}
	if r.Errors() != want {
		t.Errorf("Errors = %d, want %d", r.Errors(), want)
	}
	empty := &Result{}
	if len(empty.Used()) != 0 || empty.Errors() != 0 {
		t.Error("an empty result")
	}
}

// A rule's severity comes from Rules; an unknown rule counts as a warning.
func TestRuleSeverity(t *testing.T) {
	for _, r := range Rules {
		if got := ruleSeverity(r.ID); got != r.Severity {
			t.Errorf("%s: %s, want %s", r.ID, got, r.Severity)
		}
	}
	if ruleSeverity("no-such-rule") != Warning {
		t.Error("unknown rule")
	}
}

// skipLiteral steps over comments and strings (so their contents are not read as code), and
// stays put elsewhere; an unterminated one runs to the end.
func TestSkipLiteral(t *testing.T) {
	for _, c := range []struct {
		src  string
		at   int
		want int
	}{
		{"a // note\nb", 2, 9},
		{"a // note", 2, 9},
		{"a /* x */ b", 2, 9},
		{"a /* never closed", 2, 17},
		{`a """multi "line" text""" b`, 2, 25},
		{`a """never closed`, 2, 17},
		{`a "say \"hi\"" b`, 2, 14},
		{`a 'c' b`, 2, 5},
		{"a `t` b", 2, 5},
		{`a "open`, 2, 7},
		{"abc", 1, 1},
	} {
		if got := skipLiteral(c.src, c.at); got != c.want {
			t.Errorf("skipLiteral(%q, %d) = %d, want %d", c.src, c.at, got, c.want)
		}
	}
}

// literal reads a plain string literal; anything else (a variable, a call, an escape) is "".
func TestLiteral(t *testing.T) {
	for in, want := range map[string]string{`"Save"`: "Save", `  "Save"  `: "Save", `""`: "", `name`: "",
		`"a\"b"`: "", `stringResource(R.string.x)`: "", `"Save" + x`: "Save"} {
		if got := literal(in); got != want {
			t.Errorf("literal(%q) = %q, want %q", in, got, want)
		}
	}
}

// tagString reads an attribute's text: plain, or a string literal in braces; an expression
// or a template with ${} is unknown ("").
func TestTagString(t *testing.T) {
	tg := &tag{attrs: map[string]string{
		"plain": "Save", "dq": `{"Save"}`, "sq": `{'Save'}`, "bt": "{`Save`}", "tpl": "{`Hi ${name}`}",
		"expr": "{label}", "spaced": `{ "Save" }`, "mixed": `{"Save'}`,
	}}
	for key, want := range map[string]string{"plain": "Save", "dq": "Save", "sq": "Save", "bt": "Save",
		"tpl": "", "expr": "", "spaced": "Save", "mixed": "", "missing": ""} {
		if got := tagString(tg, key); got != want {
			t.Errorf("tagString(%s) = %q, want %q", key, got, want)
		}
	}
}

// lineAt is the rest of the line from an offset.
func TestLineAt(t *testing.T) {
	src := "first\nsecond line\nthird"
	if got := lineAt(src, 6); got != "second line" {
		t.Errorf("%q", got)
	}
	if got := lineAt(src, 18); got != "third" {
		t.Errorf("last line: %q", got)
	}
}

// webHasText: text tb reads inside an element. Icons and images do not count; a component
// counts unless named an icon; an aria-label or title on a child counts.
func TestWebHasText(t *testing.T) {
	mk := func(name string, kids ...*tag) *tag {
		t := &tag{name: name, attrs: map[string]string{}}
		for _, k := range kids {
			k.parent = t
			t.children = append(t.children, k)
		}
		return t
	}
	text := &tag{name: "span", attrs: map[string]string{}, text: true}
	labelled := &tag{name: "span", attrs: map[string]string{"title": "Close"}}
	for name, c := range map[string]struct {
		t    *tag
		want bool
	}{
		"text inside":       {mk("button", mk("div", text)), true},
		"icon only":         {mk("button", mk("svg", &tag{name: "path", text: true, attrs: map[string]string{}})), false},
		"image":             {mk("button", mk("img")), false},
		"a component":       {mk("button", mk("Label")), true},
		"an icon component": {mk("button", mk("TrashIcon")), false},
		"a titled child":    {mk("button", labelled), true},
		"nothing":           {mk("button", mk("div")), false},
		"spread props":      {mk("button", &tag{name: "div", spread: true, attrs: map[string]string{}}), true},
	} {
		if got := webHasText(c.t); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}
