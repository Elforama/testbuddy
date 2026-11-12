package match

import (
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

// The text rule: a full-match, case-insensitive, dot-matches-newline regex, with literal
// fallbacks for patterns that do not compile and for raw equality.
func TestTextMatchesSemantics(t *testing.T) {
	cases := []struct {
		name     string
		pat, val string
		want     bool
	}{
		{"exact", "Login", "Login", true},
		{"prefix is not enough", "Item 3", "Item 30", false},
		{"suffix is not enough", "tem 3", "Item 3", false},
		{"substring is not enough", "ell", "Hello", false},
		{"wildcard suffix", "Item 3.*", "Item 30", true},
		{"wildcard both sides", ".*ell.*", "Hello", true},
		{"alternation is fully anchored", "Yes|No", "No", true},
		{"alternation does not leak anchors", "Yes|No", "Nope", false},
		{"case-insensitive ascii", "SIGN IN", "sign in", true},
		{"case-insensitive unicode", "ÉCOLE", "école", true},
		{"character class", "[0-9]+ items", "42 items", true},
		{"java digit class", `\d{3}-\d{4}`, "555-1234", true},
		{"optional group", "Colou?r", "Color", true},
		{"lookahead", "(?=.*@).*", "me@example.com", true},
		{"backreference", `(\w+) \1`, "bye bye", true},
		{"inline flags", "(?-i)abc", "abc", true},
		{"dot matches newline", "Hello.*", "Hello\nworld", true},
		{"newline folded to space", "Hello world", "Hello\nworld", true},
		{"several newlines folded", "a b c", "a\nb\nc", true},
		{"newline folded then literal equality", "Total (1)", "Total\n(1)", true},
		{"raw equality beats regex meaning", "Hello (world)", "Hello (world)", true},
		{"regex meaning still applies", "Hello (world)", "Hello world", true},
		{"dollar anchor falls back to raw equality", "$5", "$5", true},
		{"invalid regex: unbalanced paren is literal", "Price ($5", "Price ($5", true},
		{"invalid regex literal is case-insensitive", "Price ($5", "PRICE ($5", true},
		{"invalid regex literal is still a full match", "Price ($", "Price ($5", false},
		{"invalid regex: leading quantifier", "+1 (555) 010", "+1 (555) 010", true},
		{"invalid regex: unclosed class", "[beta", "[beta", true},
		{"invalid regex: unclosed class literal mismatch", "[beta", "b", false},
		{"invalid regex: dangling star", "*required", "*required", true},
		{"empty pattern matches empty value", "", "", true},
		{"empty pattern does not match text", "", "x", false},
		{"pattern longer than value", "Hello world", "Hello", false},
		{"whitespace is significant", "Sign  in", "Sign in", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TextMatches(c.pat, c.val); got != c.want {
				t.Errorf("TextMatches(%q, %q) = %v, want %v", c.pat, c.val, got, c.want)
			}
		})
	}
}

// Compiled patterns are cached: the same pattern compiles once, invalid ones included.
func TestCompileCaches(t *testing.T) {
	for _, p := range []string{"cache-me.*", "cache-me-(invalid"} {
		a, b := compile(p), compile(p)
		if a != b {
			t.Errorf("compile(%q) returned different regexps", p)
		}
		if c, ok := cached(p); !ok || c != a {
			t.Errorf("compile(%q) is not in the cache", p)
		}
	}
}

// A text selector checks the text, hint, accessibility text and error of a node, ignoring
// empty attributes.
func TestNodeTextMatchesAttributes(t *testing.T) {
	cases := []struct {
		name string
		n    ui.Node
		want bool
	}{
		{"text", ui.Node{Text: "Email"}, true},
		{"hint", ui.Node{Hint: "Email"}, true},
		{"accessibility text", ui.Node{Desc: "Email"}, true},
		{"error", ui.Node{Error: "Email"}, true},
		{"one of several", ui.Node{Text: "me@x.io", Hint: "Email", Desc: "field"}, true},
		{"none match", ui.Node{Text: "Name", Hint: "Your name", Desc: "name field", Error: "Required"}, false},
		{"empty node", ui.Node{}, false},
		{"id is not text", ui.Node{ID: "Email"}, false},
		{"class is not text", ui.Node{Class: "Email"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := nodeTextMatches("email", &c.n); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
	// An empty pattern never matches an empty attribute (empty values are skipped).
	if nodeTextMatches("", &ui.Node{}) {
		t.Error("empty pattern matched a node with no text")
	}
}

// An id selector is a full, case-insensitive regex over the whole id or, failing that, the
// part after the last "/" (Android's "package:id/name").
func TestIDMatches(t *testing.T) {
	cases := []struct {
		name, pat, id string
		want          bool
	}{
		{"full android id", "com.app:id/login", "com.app:id/login", true},
		{"short android id", "login", "com.app:id/login", true},
		{"short id is a full match", "log", "com.app:id/login", false},
		{"short id regex", "log.*", "com.app:id/login", true},
		{"full id regex", `com\.app:id/.*`, "com.app:id/login", true},
		{"middle part is not enough", "id/login", "com.app:id/login", false},
		{"case-insensitive", "LOGIN", "com.app:id/login", true},
		{"ios identifier", "LoginButton", "LoginButton", true},
		{"ios identifier case-insensitive", "loginbutton", "LoginButton", true},
		{"several slashes: last part", "c", "a/b/c", true},
		{"several slashes: not the last two", "b/c", "a/b/c", false},
		{"several slashes: whole", "a/b/c", "a/b/c", true},
		{"no id never matches", ".*", "", false},
		{"invalid regex is literal", "btn(", "btn(", true},
		{"alternation", "ok|cancel", "com.app:id/cancel", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := idMatches(c.pat, &ui.Node{ID: c.id}); got != c.want {
				t.Errorf("idMatches(%q, %q) = %v, want %v", c.pat, c.id, got, c.want)
			}
		})
	}
}

// Find with a text selector matches on any of the text attributes.
func TestFindByHintAndAccessibilityText(t *testing.T) {
	email := &ui.Node{Depth: 1, Class: "EditText", Hint: "Email", Bounds: rect(0, 100, 1000, 200), Enabled: true}
	closeBtn := &ui.Node{Depth: 1, Class: "ImageButton", Desc: "Close", Bounds: rect(900, 0, 1000, 100), Clickable: true, Enabled: true}
	field := &ui.Node{Depth: 1, Class: "EditText", Text: "abc", Error: "Too short", Bounds: rect(0, 300, 1000, 400), Enabled: true}
	s := mkScreen(root(), email, closeBtn, field)
	for pat, want := range map[string]*ui.Node{"Email": email, "close": closeBtn, "Too short": field, "too.*": field} {
		if got := mustFind(t, s, &Selector{Text: pat}); got != want {
			t.Errorf("text %q found %s, want %s", pat, nodeName(got), nodeName(want))
		}
	}
}
