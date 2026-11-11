package a11y

import (
	"regexp"
	"strings"
)

var (
	testTagCall   = regexp.MustCompile(`\btestTag\s*\(\s*("([^"\\]*)")?`)
	emptyClearSet = regexp.MustCompile(`\bclearAndSetSemantics\s*\{\s*\}`)
	hideCall      = regexp.MustCompile(`\b(invisibleToUser|hideFromAccessibility)\s*\(\s*\)`)
)

// Compose buttons that are often icon-only.
var composeIconButtons = []string{"IconButton", "IconToggleButton", "FilledIconButton", "FilledTonalIconButton",
	"OutlinedIconButton", "FloatingActionButton", "SmallFloatingActionButton", "LargeFloatingActionButton"}

func checkCompose(f *file, p *project) {
	if !p.testTagsExposed {
		for _, m := range testTagCall.FindAllStringSubmatchIndex(f.src, -1) {
			if inLiteral(f.src, m[0]) {
				continue
			}
			tag := ""
			if m[4] >= 0 {
				tag = f.src[m[4]:m[5]]
			}
			f.add(m[0], "compose-testtag-hidden", Element{Kind: "Compose node", ID: tag})
		}
	}
	for _, m := range emptyClearSet.FindAllStringIndex(f.src, -1) {
		if !inLiteral(f.src, m[0]) {
			f.add(m[0], "compose-hidden", Element{Kind: "Compose node"})
		}
	}
	for _, m := range hideCall.FindAllStringSubmatchIndex(f.src, -1) {
		if !inLiteral(f.src, m[0]) {
			f.add(m[0], "compose-hidden", Element{Kind: "Compose node"})
		}
	}
	loops := ktLoops(f.src)
	for _, name := range composeIconButtons {
		for _, c := range calls(f.src, name) {
			if composeLabelled(c.text()) {
				continue
			}
			composeNoLabel(f, p, loops, c, name, Element{Kind: name})
		}
	}
	// A clickable Image or Icon with no description.
	for _, name := range []string{"Image", "Icon"} {
		for _, c := range calls(f.src, name) {
			if !strings.Contains(c.args, ".clickable") && !strings.Contains(c.args, ".combinedClickable") {
				continue
			}
			if composeLabelled(c.text()) {
				continue
			}
			composeNoLabel(f, p, loops, c, name, Element{Kind: "clickable " + name})
		}
	}
}

var (
	composeIcon     = regexp.MustCompile(`\bIcons\.(?:[A-Za-z]+\.)*([A-Za-z0-9]+)`)
	composeDrawable = regexp.MustCompile(`\bR\.drawable\.(\w+)`)
	modifierArg     = regexp.MustCompile(`^modifier\s*=\s*`)
)

const testTagImport = "androidx.compose.ui.platform.testTag"

// composeNoLabel adds a compose-no-label finding, fixed with a testTag on the modifier when
// tb can see test tags.
func composeNoLabel(f *file, p *project, loops []loop, c call, kind string, el Element) {
	switch {
	case !p.testTagsExposed:
		f.addManual(c.start, "compose-no-label", el, "a testTag would be invisible to tb until the project sets "+
			"testTagsAsResourceId = true (see compose-testtag-hidden); set it and run --fix again, or set a contentDescription")
		return
	case c.argsOff < 0:
		f.addManual(c.start, "compose-no-label", el, "no argument list to add a modifier to: add Modifier.testTag(\"…\") by hand")
		return
	}
	if by := loopAt(loops, c.start); by != "" {
		f.addManual(c.start, "compose-no-label", el, perItem(by, "testTag"))
		return
	}
	base := ""
	if v, ok := namedArg(c.args, "onClick"); ok {
		base = handlerName(v)
	}
	if base == "" {
		if m := composeIcon.FindStringSubmatch(c.text()); m != nil {
			base = m[1]
		} else if m := composeDrawable.FindStringSubmatch(c.text()); m != nil {
			base = resourceName(m[1])
		} else {
			base = kind
		}
	}
	tag := func(name string) string { return `.testTag("` + name + `")` }
	fx := &fix{base: base, role: "button", sep: "-", imprt: testTagImport, insert: tag, added: tag}
	// Onto the modifier it has (named or passed by position), else a new last argument:
	// a named argument before positional ones would not compile.
	spans := argSpans(c.args)
	fx.off = -1
	for _, s := range spans {
		a := c.args[s[0]:s[1]]
		if modifierArg.MatchString(a) || strings.HasPrefix(a, "Modifier") {
			fx.off = c.argsOff + s[1]
			// Anything but one chain (if/else, ?:, then) takes the testTag as a whole, in
			// parentheses: appended bare, it would tag only its last part (issue #60).
			from := s[0] + len(modifierArg.FindString(a))
			if !plainChain(c.args[from:s[1]]) {
				fx.wrap = c.argsOff + from
				fx.insert = func(name string) string { return ")" + tag(name) }
			}
			break
		}
	}
	if fx.off < 0 {
		mod := func(name string) string { return `modifier = Modifier` + tag(name) }
		fx.added = mod
		if len(spans) == 0 {
			fx.off = c.argsOff
			fx.insert = mod
		} else {
			fx.off = c.argsOff + spans[len(spans)-1][1]
			fx.insert = func(name string) string { return ", " + mod(name) }
		}
	}
	f.addFix(c.start, "compose-no-label", el, fx)
}

// kotlinKeywords start an expression that is not a chain of calls.
var kotlinKeywords = map[string]bool{"if": true, "when": true, "try": true, "object": true, "else": true,
	"return": true, "throw": true, "null": true}

// plainChain reports whether a Kotlin expression is one chain of names and calls
// (modifier, Modifier.size(48.dp).clickable { }), so a call appended to it applies to all
// of it.
func plainChain(e string) bool {
	i := 0
	ident := func() string {
		j := i
		for j < len(e) && (isLetter(e[j]) || (j > i && e[j] >= '0' && e[j] <= '9')) {
			j++
		}
		name := e[i:j]
		i = j
		return name
	}
	skipSpace := func() {
		for i < len(e) && isSpace(e[i]) {
			i++
		}
	}
	if n := ident(); n == "" || kotlinKeywords[n] {
		return false
	}
	for {
		skipSpace()
		if i == len(e) {
			return true
		}
		switch e[i] {
		case '.':
			i++
			skipSpace()
			if ident() == "" {
				return false
			}
		case '(', '{':
			end := matching(e, i)
			if end < 0 {
				return false
			}
			i = end
		default:
			return false
		}
	}
}

var (
	nullDescription = regexp.MustCompile(`contentDescription\s*=\s*null\b`)
	anyDescription  = regexp.MustCompile(`contentDescription\s*=`)
	composeText     = regexp.MustCompile(`\bText\s*\(`)
)

// composeLabelled reports whether code inside a call gives tb something to find it by: a
// Text, a non-null contentDescription, a testTag, or its own semantics.
func composeLabelled(body string) bool {
	if composeText.MatchString(body) || strings.Contains(body, "testTag") || strings.Contains(body, "semantics") {
		return true
	}
	if len(anyDescription.FindAllString(body, -1)) > len(nullDescription.FindAllString(body, -1)) {
		return true
	}
	// Icon(icon, "Close") / Image(painter, "Logo"): the description passed by position.
	for _, name := range []string{"Icon", "Image"} {
		for _, c := range calls(body, name) {
			if a := topArgs(c.args); len(a) >= 2 && !strings.Contains(a[1], "=") && a[1] != "null" {
				return true
			}
		}
	}
	return false
}
