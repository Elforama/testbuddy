package a11y

import (
	"regexp"
	"strings"
)

var excludingFalse = regexp.MustCompile(`excluding\s*:\s*false`)

func checkFlutter(f *file, _ *project) {
	for _, c := range calls(f.src, "ExcludeSemantics") {
		if excludingFalse.MatchString(c.args) {
			continue
		}
		f.add(c.start, "flutter-hidden", Element{Kind: "ExcludeSemantics"})
	}
	for _, name := range []string{"IconButton", "FloatingActionButton", "FloatingActionButton.small", "FloatingActionButton.large"} {
		for _, c := range calls(f.src, name) {
			if v, ok := namedArg(c.args, "tooltip"); ok && v != "null" {
				continue
			}
			if strings.Contains(c.args, "Text(") || strings.Contains(c.args, "semanticLabel") || strings.Contains(c.args, "Semantics(") {
				continue
			}
			f.add(c.start, "flutter-no-label", Element{Kind: name})
		}
	}
}
