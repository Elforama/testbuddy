package flow

import (
	"reflect"
	"testing"
)

// answerDialogs (issue #91): OK or CANCEL, or a map with button, text, label and optional;
// the text stays raw (the engine interpolates it).
func TestAnswerDialogs(t *testing.T) {
	for _, c := range []struct {
		src  string
		want DialogChoice
	}{
		{"- answerDialogs: CANCEL\n", DialogChoice{Cancel: true}},
		{"- answerDialogs: cancel\n", DialogChoice{Cancel: true}},
		{"- answerDialogs: OK\n", DialogChoice{}},
		{"- answerDialogs: {button: CANCEL}\n", DialogChoice{Cancel: true}},
		{"- answerDialogs: {text: \"${NAME}\"}\n", DialogChoice{Text: "${NAME}", HasText: true}},
		{"- answerDialogs: {button: OK, text: \"\"}\n", DialogChoice{HasText: true}},
		// A map without button means OK.
		{"- answerDialogs: {}\n", DialogChoice{}},
		{"- answerDialogs: {label: Default}\n", DialogChoice{}},
		{"- answerDialogs: {optional: true}\n", DialogChoice{}},
		// A button with ${} is read when the step runs.
		{"- answerDialogs: \"${ANSWER}\"\n", DialogChoice{Button: "${ANSWER}"}},
		{"- answerDialogs: {button: \"${ANSWER}\", text: Ada}\n", DialogChoice{Button: "${ANSWER}", Text: "Ada", HasText: true}},
	} {
		got := step(t, c.src)
		if got.Name != "answerDialogs" || got.Dialogs == nil || !reflect.DeepEqual(*got.Dialogs, c.want) {
			t.Errorf("%q: %+v, want %+v", c.src, got.Dialogs, c.want)
		}
		if got.Mutating() {
			t.Errorf("%q: answerDialogs counts as a device interaction", c.src)
		}
	}
	c := step(t, "- answerDialogs: {button: CANCEL, label: Say no, optional: true}\n")
	if c.Label != "Say no" || !c.Optional {
		t.Errorf("options: %+v", c)
	}
}

func TestAnswerDialogsErrors(t *testing.T) {
	runErrCases(t, hdr, []errCase{
		{"bare", "- answerDialogs\n", "answerDialogs needs a value", 3},
		{"other button", "- answerDialogs: MAYBE\n", "answerDialogs takes OK, CANCEL or a map", 3},
		{"map button", "- answerDialogs: {button: YES}\n", "answerDialogs takes OK, CANCEL or a map", 3},
		{"cancel with text", "- answerDialogs: {button: CANCEL, text: Ada}\n", "CANCEL types no text", 3},
		{"list", "- answerDialogs: [OK]\n", "answerDialogs takes a text or a map, not a list", 3},
		{"text list", "- answerDialogs: {text: [a]}\n", "the text a prompt() gets", 3},
		{"unknown key", "- answerDialogs: {accept: false}\n", `"accept" is not an option of answerDialogs`, 3},
	})
}
