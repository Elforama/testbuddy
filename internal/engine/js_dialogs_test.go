package engine

import (
	"context"
	"reflect"
	"testing"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
)

// dialogFake is a web-like device whose JavaScript dialog answer a flow can choose
// (driver.DialogAnswerer); it records every answer it was given.
type dialogFake struct {
	*fakeDriver
	answers []driver.DialogAnswer
}

func (d *dialogFake) AnswerDialogs(ctx context.Context, a driver.DialogAnswer) error {
	d.answers = append(d.answers, a)
	return nil
}

// answerDialogs (issue #91) passes the flow's choice to the driver, its text interpolated; a
// flow starts and ends with the default answer, even when it fails.
func TestAnswerDialogsStep(t *testing.T) {
	d := &dialogFake{fakeDriver: &fakeDriver{screen: static(), plat: "web"}}
	o := quick
	o.Env = []flow.KV{{Key: "NAME", Value: "Grace"}}
	mustPass(t, runWith(t, d, o, "- answerDialogs: CANCEL\n- answerDialogs: {text: 'Dr ${NAME}'}\n- answerDialogs: OK\n"))
	want := []driver.DialogAnswer{{}, {Cancel: true}, {Text: "Dr Grace", HasText: true}, {}, {}}
	if !reflect.DeepEqual(d.answers, want) {
		t.Errorf("answers %+v\nwant %+v", d.answers, want)
	}

	d.answers = nil
	mustFail(t, runWith(t, d, quick, "- answerDialogs: CANCEL\n- assertVisible: Nowhere\n"), "Nowhere")
	if want := []driver.DialogAnswer{{}, {Cancel: true}, {}}; !reflect.DeepEqual(d.answers, want) {
		t.Errorf("after a failure: answers %+v, want %+v", d.answers, want)
	}
}

// An agent session (tb do) keeps its choice between calls.
func TestAnswerDialogsKeptAcrossDoCalls(t *testing.T) {
	d := &dialogFake{fakeDriver: &fakeDriver{screen: static(), plat: "web"}}
	r := New(d, quick)
	for _, src := range []string{"- answerDialogs: CANCEL\n", "- tapOn: Hello\n"} {
		cmds, err := flow.ParseCommands(t.TempDir(), []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		r.RunCommands(context.Background(), cmds, "app", nil)
	}
	if want := []driver.DialogAnswer{{Cancel: true}}; !reflect.DeepEqual(d.answers, want) {
		t.Errorf("answers %+v, want %+v", d.answers, want)
	}
}

// Android and iOS answer no JavaScript dialogs: the step fails, or warns when optional.
func TestAnswerDialogsUnsupported(t *testing.T) {
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- answerDialogs: CANCEL"),
		"answerDialogs works only in web flows (url:)")
	res := runWith(t, &fakeDriver{screen: static()}, quick, "- answerDialogs: {button: CANCEL, optional: true}")
	mustPass(t, res)
	if res.Steps[0].Status != Warned {
		t.Errorf("status %s", res.Steps[0].Status)
	}
}

func TestDescribeAnswerDialogs(t *testing.T) {
	for src, want := range map[string]string{
		"- answerDialogs: CANCEL\n":        "Answer dialogs with Cancel",
		"- answerDialogs: OK\n":            "Answer dialogs with OK",
		"- answerDialogs: {text: Grace}\n": `Answer dialogs with OK, prompt text "Grace"`,
	} {
		cmds, err := flow.ParseCommands(t.TempDir(), []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		if got := Describe(cmds[0]); got != want {
			t.Errorf("%q: %q, want %q", src, got, want)
		}
	}
}
