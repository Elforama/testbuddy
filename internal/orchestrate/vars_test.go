package orchestrate

import (
	"testing"

	"github.com/elforama/testbuddy/internal/flow"
)

// resolve handles ${NAME} and ${NAME || 'default'}; anything else waits for the engine.
func TestVarsResolve(t *testing.T) {
	v := vars{}.define([]flow.KV{{Key: "A", Value: "a"}, {Key: "EMPTY", Value: ""}})
	for in, want := range map[string]string{
		"plain":              "plain",
		"${A}":               "a",
		"x-${ A }-y":         "x-a-y",
		"${A || 'd'}":        "a",
		`${B || "d"}`:        "d",
		"${B || `d`}":        "d",
		"${EMPTY || 'd'}":    "d",
		"${B || ''}":         "",
		"${EMPTY}":           "",
		"${B}":               "${B}",
		"${A.toUpperCase()}": "${A.toUpperCase()}",
		"${A}/${B}":          "a/${B}",
	} {
		if got := v.resolve(in); got != want {
			t.Errorf("resolve(%q) = %q, want %q", in, got, want)
		}
	}
	// Siblings in one block don't see each other; a later block sees the earlier one.
	v = vars{}.define([]flow.KV{{Key: "X", Value: "1"}, {Key: "Y", Value: "${X}"}})
	if v["Y"] != "${X}" {
		t.Errorf("sibling: Y = %q", v["Y"])
	}
	if w := v.define([]flow.KV{{Key: "Z", Value: "${X}"}}); w["Z"] != "1" || len(v) != 2 {
		t.Errorf("next block: Z = %q, earlier block changed: %v", w["Z"], v)
	}
}
