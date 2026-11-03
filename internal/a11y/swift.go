package a11y

import (
	"regexp"
	"strings"
)

var (
	swiftHidden         = regexp.MustCompile(`\.accessibilityHidden\(\s*true\s*\)`)
	uikitHidden         = regexp.MustCompile(`\baccessibilityElementsHidden\s*=\s*true\b`)
	swiftChildrenIgnore = regexp.MustCompile(`\.accessibilityElement\(\s*children:\s*\.ignore\s*\)`)
	swiftIdentifierNear = regexp.MustCompile(`\.accessibility(Identifier|Label)\(`)
)

func checkSwift(f *file, _ *project) {
	for _, m := range swiftHidden.FindAllStringIndex(f.src, -1) {
		if !inLiteral(f.src, m[0]) {
			f.add(m[0], "ios-hidden", Element{Kind: "view"})
		}
	}
	for _, m := range uikitHidden.FindAllStringIndex(f.src, -1) {
		if !inLiteral(f.src, m[0]) {
			f.add(m[0], "ios-hidden", Element{Kind: "view"})
		}
	}
	for _, m := range swiftChildrenIgnore.FindAllStringIndex(f.src, -1) {
		if inLiteral(f.src, m[0]) {
			continue
		}
		// Fine when the same view gets its own label or identifier.
		if swiftIdentifierNear.MatchString(modifierChain(f.src, m[1])) {
			continue
		}
		f.add(m[0], "ios-children-ignored", Element{Kind: "view"})
	}
}

// modifierChain is the rest of a SwiftUI modifier chain from off: the rest of the line,
// and each following line that starts with ".".
func modifierChain(src string, off int) string {
	end := off
	for {
		nl := strings.IndexByte(src[end:], '\n')
		if nl < 0 {
			return src[off:]
		}
		end += nl + 1
		if !strings.HasPrefix(strings.TrimSpace(lineAt(src, end)), ".") {
			return src[off:end]
		}
	}
}

func lineAt(src string, off int) string {
	if i := strings.IndexByte(src[off:], '\n'); i >= 0 {
		return src[off : off+i]
	}
	return src[off:]
}
