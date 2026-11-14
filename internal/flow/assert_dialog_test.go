package flow

import (
	"reflect"
	"testing"
)

// assertDialog (issue #91): the dialog's text, or a map with text, type, label and optional;
// bare (or {}) for any dialog. The text stays raw (the engine interpolates it).
func TestAssertDialog(t *testing.T) {
	for _, c := range []struct {
		src  string
		want DialogCheck
	}{
		{"- assertDialog: Delete it?\n", DialogCheck{Text: "Delete it?", HasText: true}},
		{"- assertDialog: \"${MSG}\"\n", DialogCheck{Text: "${MSG}", HasText: true}},
		{"- assertDialog: {text: \"Delete .*\", type: confirm}\n", DialogCheck{Text: "Delete .*", HasText: true, Type: "confirm"}},
		{"- assertDialog: {type: Alert}\n", DialogCheck{Type: "alert"}},
		{"- assertDialog: {type: beforeunload}\n", DialogCheck{Type: "beforeunload"}},
		{"- assertDialog: {text: \"\"}\n", DialogCheck{HasText: true}},
		{"- assertDialog\n", DialogCheck{}},
		{"- assertDialog: {}\n", DialogCheck{}},
	} {
		got := step(t, c.src)
		if got.Name != "assertDialog" || got.DialogCheck == nil || !reflect.DeepEqual(*got.DialogCheck, c.want) {
			t.Errorf("%q: %+v, want %+v", c.src, got.DialogCheck, c.want)
		}
		if got.Mutating() {
			t.Errorf("%q: assertDialog counts as a device interaction", c.src)
		}
	}
	c := step(t, "- assertDialog: {text: Saved, label: Saved alert, optional: true}\n")
	if c.Label != "Saved alert" || !c.Optional {
		t.Errorf("options: %+v", c)
	}
}

func TestAssertDialogErrors(t *testing.T) {
	runErrCases(t, hdr, []errCase{
		{"list", "- assertDialog: [a]\n", "assertDialog takes a text or a map, not a list", 3},
		{"text list", "- assertDialog: {text: [a]}\n", "the dialog's message", 3},
		{"other type", "- assertDialog: {type: toast}\n", `assertDialog's type is one of alert, confirm, prompt, beforeunload (got "toast")`, 3},
		{"unknown key", "- assertDialog: {message: Hi}\n", `"message" is not an option of assertDialog`, 3},
	})
}
