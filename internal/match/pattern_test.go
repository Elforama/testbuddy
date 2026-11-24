package match

import "testing"

func TestPatternHelpers(t *testing.T) {
	for _, c := range []struct {
		pattern, s      string
		compiles, match bool
	}{
		{"Sign in", "sign in", true, true},
		{"Sign in?", "Sign in?", true, false},
		{"Sign in?", "Sign i", true, true},
		{"Price ($)", "Price ($)", true, false},
		{".*Settings.*", "Open Settings now", true, true},
		{"Hello (world", "Hello (world", false, true},
		{"c++", "c++", true, false},
	} {
		if got := Compiles(c.pattern); got != c.compiles {
			t.Errorf("Compiles(%q) = %v", c.pattern, got)
		}
		if got := RegexMatches(c.pattern, c.s); got != c.match {
			t.Errorf("RegexMatches(%q, %q) = %v", c.pattern, c.s, got)
		}
	}
	if !TextMatches("Sign in?", "Sign in?") {
		t.Error(`TextMatches("Sign in?", "Sign in?") = false`)
	}
}
