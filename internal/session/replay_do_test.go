package session

import (
	"reflect"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/flow"
)

func replayOf(app string, cli, flowEnv []flow.KV) map[string]any {
	return map[string]any{"app": app, "env": cli, "flow_env": flowEnv}
}

func TestDoReplayDefinesEnvAsAReplayDoes(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	addDev(s, f, "")
	resp, _ := call(t, s.do, map[string]any{"commands": "- inputText: ${BTN} ${WHO}\n", "replay": replayOf("com.app",
		[]flow.KV{{Key: "WHO", Value: "Lists"}},
		[]flow.KV{{Key: "WHO", Value: "Zzznope"}, {Key: "BTN", Value: "${WHO}"}})}, "")
	if !resp.OK || !reflect.DeepEqual(f.typed, []string{"Lists Zzznope"}) {
		t.Fatalf("resp %+v typed %v", resp, f.typed)
	}
	call(t, s.do, map[string]any{"commands": "- inputText: \"${typeof WHO}\"\n"}, "")
	if len(f.typed) != 2 || f.typed[1] != "undefined" {
		t.Errorf("the flow's env outlived the replay: typed %v", f.typed)
	}
	resp, _ = call(t, s.do, map[string]any{"commands": "- inputText: x\n", "replay": replayOf("com.app", nil,
		[]flow.KV{{Key: "BAD", Value: "${1 +}"}})}, "")
	if resp.Exit != 2 || len(f.typed) != 2 {
		t.Errorf("an env value that does not evaluate: %+v typed %v", resp, f.typed)
	}
}

func TestDoReplayTakesFlowTextAsWritten(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(loginScreen()...)
	addDev(s, f, "")
	call(t, s.look, nil, "")
	resp, _ := call(t, s.do, map[string]any{"commands": "- inputText: mail me @3 times\n", "replay": replayOf("com.app", nil, nil)}, "")
	if !resp.OK || !reflect.DeepEqual(f.typed, []string{"mail me @3 times"}) {
		t.Errorf("resp %+v typed %q", resp, f.typed)
	}
}

func TestDoReplaySetsTheAppOnlyOnceItIsValid(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	d := addDev(s, f, "")
	d.AppID = "com.before"
	for _, bad := range []map[string]any{
		{"commands": "- launchApp\n", "replay": replayOf("com.${NOPE}", nil, nil)},
		{"commands": "- notACommand: x\n", "replay": replayOf("com.other", nil, nil)},
	} {
		resp, _ := call(t, s.do, bad, "")
		if resp.Exit != 2 || d.AppID != "com.before" || len(f.launched) != 0 {
			t.Errorf("%v: %+v, app %q, launched %v", bad, resp, d.AppID, f.launched)
		}
	}
	resp, _ := call(t, s.do, map[string]any{"commands": "- launchApp\n",
		"replay": replayOf("com.${WHO}", []flow.KV{{Key: "WHO", Value: "sam"}}, nil)}, "")
	if !resp.OK || d.AppID != "com.sam" || !reflect.DeepEqual(f.launched, []string{"com.sam"}) {
		t.Errorf("%+v, app %q, launched %v", resp, d.AppID, f.launched)
	}
}

func TestDoReplayRecordsNoSteps(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	d := addDev(s, f, "")
	resp, _ := call(t, s.do, map[string]any{"commands": "- inputText: hello\n", "replay": replayOf("com.app", nil, nil)}, "")
	if !resp.OK || len(d.steps) != 0 || strings.Contains(resp.Text, "FAILED") {
		t.Errorf("resp %+v steps %q", resp, d.steps)
	}
}
