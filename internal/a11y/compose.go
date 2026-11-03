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
	for _, name := range composeIconButtons {
		for _, c := range calls(f.src, name) {
			if composeLabelled(c.text()) {
				continue
			}
			f.add(c.start, "compose-no-label", Element{Kind: name})
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
			f.add(c.start, "compose-no-label", Element{Kind: "clickable " + name})
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
