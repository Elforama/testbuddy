package a11y

import (
	"regexp"
	"strings"
)

// Helpers for reading C-like source (Kotlin, Swift, Dart, JavaScript) well enough for these
// checks: matching brackets while skipping strings and comments.

// skipLiteral returns the index just past the string, char or comment starting at i, or i
// when none starts there.
func skipLiteral(s string, i int) int {
	switch {
	case strings.HasPrefix(s[i:], "//"):
		if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
			return i + j
		}
		return len(s)
	case strings.HasPrefix(s[i:], "/*"):
		if j := strings.Index(s[i+2:], "*/"); j >= 0 {
			return i + 2 + j + 2
		}
		return len(s)
	case strings.HasPrefix(s[i:], `"""`):
		if j := strings.Index(s[i+3:], `"""`); j >= 0 {
			return i + 3 + j + 3
		}
		return len(s)
	case s[i] == '"' || s[i] == '\'' || s[i] == '`':
		q := s[i]
		for j := i + 1; j < len(s); j++ {
			if s[j] == '\\' {
				j++
				continue
			}
			if s[j] == q || (s[j] == '\n' && q != '`') {
				return j + 1
			}
		}
		return len(s)
	}
	return i
}

// matching returns the index just past the bracket that closes the one at open, or -1.
func matching(s string, open int) int {
	depth := 0
	for i := open; i < len(s); {
		if j := skipLiteral(s, i); j != i {
			i = j
			continue
		}
		switch s[i] {
		case '(', '{', '[':
			depth++
		case ')', '}', ']':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
		i++
	}
	return -1
}

// call is a function call found in source: Name(args) { trailing lambda }.
type call struct {
	start  int    // offset of the name
	args   string // between the parentheses
	lambda string // the trailing lambda's body, if any
	end    int
}

// text is everything the call holds: its arguments and trailing lambda.
func (c call) text() string { return c.args + "\n" + c.lambda }

// calls finds every call of the named function (a whole word followed by "(" or "{").
func calls(src, name string) []call {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\s*[({]`)
	var out []call
	for _, m := range re.FindAllStringIndex(src, -1) {
		if inLiteral(src, m[0]) || (m[0] > 0 && src[m[0]-1] == '.') {
			continue
		}
		c := call{start: m[0], end: m[1]}
		i := m[1] - 1
		if src[i] == '(' {
			e := matching(src, i)
			if e < 0 {
				continue
			}
			c.args = src[i+1 : e-1]
			c.end = e
			i = e
			for i < len(src) && (src[i] == ' ' || src[i] == '\t') {
				i++
			}
		}
		if i < len(src) && src[i] == '{' {
			if e := matching(src, i); e > 0 {
				c.lambda = src[i+1 : e-1]
				c.end = e
			}
		}
		out = append(out, c)
	}
	return out
}

// inLiteral reports whether offset off is inside a string or comment (scanning from the
// start of its line, which is enough for the one-line cases that matter here).
func inLiteral(s string, off int) bool {
	i := strings.LastIndexByte(s[:off], '\n') + 1
	for i < off {
		j := skipLiteral(s, i)
		if j == i {
			i++
			continue
		}
		if j > off {
			return true
		}
		i = j
	}
	return false
}

// topArgs splits an argument list at its top-level commas.
func topArgs(args string) []string {
	var out []string
	depth, last := 0, 0
	for i := 0; i < len(args); {
		if j := skipLiteral(args, i); j != i {
			i = j
			continue
		}
		switch args[i] {
		case '(', '{', '[':
			depth++
		case ')', '}', ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(args[last:i]))
				last = i + 1
			}
		}
		i++
	}
	if rest := strings.TrimSpace(args[last:]); rest != "" {
		out = append(out, rest)
	}
	return out
}

// namedArg is the value of `name = value` (Kotlin) or `name: value` (Dart, Swift) among a
// call's top-level arguments.
func namedArg(args, name string) (string, bool) {
	for _, a := range topArgs(args) {
		for _, sep := range []string{"=", ":"} {
			if k, v, ok := strings.Cut(a, sep); ok && strings.TrimSpace(k) == name {
				return strings.TrimSpace(v), true
			}
		}
	}
	return "", false
}

var stringLit = regexp.MustCompile(`^"([^"\\]*)"`)

// literal is the text of a plain string literal, or "" for anything else.
func literal(v string) string {
	if m := stringLit.FindStringSubmatch(strings.TrimSpace(v)); m != nil {
		return m[1]
	}
	return ""
}
