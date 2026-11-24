package session

import (
	"reflect"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/flow"
)

func listScreen() []item {
	return []item{label("Groceries", 0), label("Milk", 100), button("Edit", 150), label("Eggs", 250), button("Edit", 300)}
}

func recorded(t *testing.T, screen []item, between func(*fakeDriver), commands string) []string {
	t.Helper()
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(screen...)
	d := addDev(s, f, "")
	call(t, s.look, nil, "")
	if between != nil {
		between(f)
	}
	resp, _ := call(t, s.do, map[string]any{"commands": commands}, t.TempDir())
	if !resp.OK {
		t.Fatalf("do failed: %+v", resp)
	}
	return d.steps
}

func expectSteps(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("recorded %q\nwant     %q", got, want)
	}
}

func TestRecordPointAsSelector(t *testing.T) {
	for _, cmd := range []string{"tapOn", "doubleTapOn", "longPressOn"} {
		t.Run(cmd, func(t *testing.T) {
			got := recorded(t, listScreen(), nil, "- "+cmd+": {point: \"50%,20%\"}")
			expectSteps(t, got, "- "+cmd+`: {text: "Edit", below: "Milk"}`)
		})
	}
	got := recorded(t, listScreen(), nil, `- tapOn: {point: "200,110"}`)
	expectSteps(t, got, `- tapOn: "Milk"`)
}

func TestRecordIndexAsSelector(t *testing.T) {
	got := recorded(t, listScreen(), nil, "- tapOn: {text: Edit, index: 1}")
	expectSteps(t, got, `- tapOn: {text: "Edit", below: "Eggs"}`)
	got = recorded(t, listScreen(), nil, "- assertVisible: {text: Edit, index: 1}")
	expectSteps(t, got, `- assertVisible: {text: "Edit", below: "Eggs"}`)
	for _, step := range []string{got[0], `- tapOn: {text: "Edit", childOf: {containsChild: "Eggs"}}`, `- tapOn: {childOf: {id: "toolbar"}}`} {
		cmds, err := flow.ParseCommands(t.TempDir(), []byte(step))
		if err != nil || len(cmds) != 1 || cmds[0].Selector == nil {
			t.Errorf("%s does not parse: %v", step, err)
		}
	}
}

func TestRecordFirstOfSeveralStable(t *testing.T) {
	want := `{text: "Edit", below: "Milk"}`
	expectSteps(t, recorded(t, listScreen(), nil, "- tapOn: {text: Edit, index: 0}"), "- tapOn: "+want)
	expectSteps(t, recorded(t, listScreen(), nil, "- assertVisible: {text: Edit, index: 0}"), "- assertVisible: "+want)
	expectSteps(t, recorded(t, listScreen(), nil, `- tapOn: {point: "50%,20%"}`), "- tapOn: "+want)
}

func TestRecordKeepsTypedSelector(t *testing.T) {
	point := `- tapOn: {point: "50%,20%"}`
	stale := func(f *fakeDriver) { f.show(listScreen()...) }
	expectSteps(t, recorded(t, listScreen(), stale, point), point)

	expectSteps(t, recorded(t, listScreen(), nil, "- tapOn: Milk\n"+point), "- tapOn: Milk", point)
	expectSteps(t, recorded(t, listScreen(), nil, "- assertVisible: Milk\n"+point),
		"- assertVisible: Milk", `- tapOn: {text: "Edit", below: "Milk"}`)

	labelled := `- tapOn: {point: "50%,20%", label: open the first item}`
	expectSteps(t, recorded(t, listScreen(), nil, labelled), labelled)
	repeated := "- tapOn:\n    text: Edit\n    index: 1\n    repeat: 2"
	expectSteps(t, recorded(t, listScreen(), nil, repeated), repeated)

	same := []item{button("Remove", 100), button("Remove", 200)}
	expectSteps(t, recorded(t, same, nil, "- tapOn: {text: Remove, index: 1}"), "- tapOn: {text: Remove, index: 1}")

	empty := `- tapOn: {point: "50%,90%"}`
	expectSteps(t, recorded(t, listScreen(), nil, empty), empty)
}
