package session

import (
	"reflect"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/flow"
)

// listScreen has two rows, each a title and an "Edit" button, under a heading.
func listScreen() []item {
	return []item{label("Groceries", 0), label("Milk", 100), button("Edit", 150), label("Eggs", 250), button("Edit", 300)}
}

// recorded opens a device on screen, lists it, runs commands (after between, if set) and
// returns what was recorded.
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

// A tap typed as a screen point is recorded as the element listed there.
func TestRecordPointAsSelector(t *testing.T) {
	// 400x800 screen: 50%,20% is (200, 160), on the first Edit (150-200).
	for _, cmd := range []string{"tapOn", "doubleTapOn", "longPressOn"} {
		t.Run(cmd, func(t *testing.T) {
			got := recorded(t, listScreen(), nil, "- "+cmd+": {point: \"50%,20%\"}")
			expectSteps(t, got, "- "+cmd+": \"Edit\"")
		})
	}
	got := recorded(t, listScreen(), nil, `- tapOn: {point: "200,110"}`)
	expectSteps(t, got, `- tapOn: "Milk"`)
}

// A tap typed with a match index is recorded with a selector that needs none.
func TestRecordIndexAsSelector(t *testing.T) {
	got := recorded(t, listScreen(), nil, "- tapOn: {text: Edit, index: 1}")
	expectSteps(t, got, `- tapOn: {text: "Edit", below: "Eggs"}`)
	got = recorded(t, listScreen(), nil, "- assertVisible: {text: Edit, index: 1}")
	expectSteps(t, got, `- assertVisible: {text: "Edit", below: "Eggs"}`)
	// The recorded selectors are flow YAML, nested relations included.
	for _, step := range []string{got[0], `- tapOn: {text: "Edit", childOf: {containsChild: "Eggs"}}`, `- tapOn: {childOf: {id: "toolbar"}}`} {
		cmds, err := flow.ParseCommands(t.TempDir(), []byte(step))
		if err != nil || len(cmds) != 1 || cmds[0].Selector == nil {
			t.Errorf("%s does not parse: %v", step, err)
		}
	}
}

// Commands are kept as typed when the rewrite could pick the wrong element or drop what
// the command says.
func TestRecordKeepsTypedSelector(t *testing.T) {
	point := `- tapOn: {point: "50%,20%"}`
	// The screen changed after it was listed.
	stale := func(f *fakeDriver) { f.show(listScreen()...) }
	expectSteps(t, recorded(t, listScreen(), stale, point), point)

	// An earlier command in the batch may have changed the screen.
	expectSteps(t, recorded(t, listScreen(), nil, "- tapOn: Milk\n"+point), "- tapOn: Milk", point)
	// An assertion does not change it.
	expectSteps(t, recorded(t, listScreen(), nil, "- assertVisible: Milk\n"+point),
		"- assertVisible: Milk", `- tapOn: "Edit"`)

	// Other options stay as typed.
	labelled := `- tapOn: {point: "50%,20%", label: open the first item}`
	expectSteps(t, recorded(t, listScreen(), nil, labelled), labelled)
	repeated := "- tapOn:\n    text: Edit\n    index: 1\n    repeat: 2"
	expectSteps(t, recorded(t, listScreen(), nil, repeated), repeated)

	// No stable selector: the index stays.
	same := []item{button("Remove", 100), button("Remove", 200)}
	expectSteps(t, recorded(t, same, nil, "- tapOn: {text: Remove, index: 1}"), "- tapOn: {text: Remove, index: 1}")

	// A point with nothing listed under it stays a point.
	empty := `- tapOn: {point: "50%,90%"}`
	expectSteps(t, recorded(t, listScreen(), nil, empty), empty)
}
