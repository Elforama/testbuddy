package android

import (
	"encoding/json"
	"testing"

	"github.com/elforama/testbuddy/internal/match"
)

// stackTreeJSON is a navigation stack that keeps its previous screen mounted (issue #320): in
// one container, the screen on top is listed first (drawing order 2) and the previous screen
// after it (drawing order 1), shifted left and drawn under it. Both screens draw a header and
// a body over their whole area. The previous screen's texts are not on screen for a person.
const stackTreeJSON = `{
 "seq": 1,
 "windows": [
  {"id": 7, "type": 1, "active": true, "focused": true, "pkg": "com.example.app", "b": [0, 0, 1080, 2400],
   "nodes": [
    {"d": 0, "b": [0, 0, 1080, 2400], "c": "android.widget.FrameLayout", "f": 4098},
    {"d": 1, "b": [0, 0, 1080, 2400], "c": "android.view.ViewGroup", "f": 4098, "o": 0},
    {"d": 2, "b": [0, 0, 1080, 2400], "c": "android.view.ViewGroup", "f": 4098, "o": 2},
    {"d": 3, "b": [0, 0, 1080, 300], "c": "android.view.ViewGroup", "f": 4098, "o": 1},
    {"d": 4, "b": [200, 100, 880, 200], "c": "android.widget.TextView", "t": "Details", "f": 4098, "o": 0},
    {"d": 3, "b": [0, 300, 1080, 2400], "c": "android.widget.ScrollView", "f": 4106, "o": 0},
    {"d": 4, "b": [63, 1456, 1017, 1561], "c": "android.widget.Button", "t": "Back to home", "f": 4099, "o": 0},
    {"d": 2, "b": [0, 0, 828, 2400], "c": "android.view.ViewGroup", "f": 4098, "o": 1},
    {"d": 3, "b": [0, 0, 828, 300], "c": "android.view.ViewGroup", "f": 4098, "o": 1},
    {"d": 4, "b": [0, 100, 600, 200], "c": "android.widget.TextView", "t": "Recipes", "f": 4098, "o": 0},
    {"d": 3, "b": [0, 300, 828, 2400], "c": "android.widget.ScrollView", "f": 4106, "o": 0},
    {"d": 4, "b": [0, 433, 828, 600], "c": "android.widget.TextView", "t": "Spring Vegetable Frittata", "f": 4098, "o": 0},
    {"d": 4, "b": [0, 1420, 765, 1525], "c": "android.widget.Button", "t": "Navigate to Details", "f": 4099, "o": 1}
   ]}
 ]}`

// Issue #320: the previous screen of a stack, drawn under the screen on top, is not listed:
// neither its texts (assertNotVisible: "Spring Vegetable Frittata" after a push) nor its
// buttons. The screen on top is listed whole.
func TestScreenHidesAScreenDrawnUnderAnother(t *testing.T) {
	a := newFakeAgent(t)
	a.on("tree", func(map[string]any) map[string]any {
		var m map[string]any
		json.Unmarshal([]byte(stackTreeJSON), &m)
		return m
	})
	d := newDriver(t, a)
	s, err := d.Screen(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	texts := map[string]bool{}
	for _, n := range match.Visible(s) {
		if n.Text != "" {
			texts[n.Text] = true
		}
	}
	for _, gone := range []string{"Spring Vegetable Frittata", "Navigate to Details", "Recipes"} {
		if texts[gone] {
			t.Errorf("%q is seen: it is on the screen drawn under the top one", gone)
		}
		if n, _ := match.FindListed(s, &match.Selector{Text: gone}); n != nil {
			t.Errorf("%q is listed: it is on the screen drawn under the top one", gone)
		}
	}
	for _, kept := range []string{"Details", "Back to home"} {
		if !texts[kept] {
			t.Errorf("%q is not seen: it is on the screen on top", kept)
		}
	}
	if len(s.Nodes) != 13 {
		t.Errorf("%d nodes in the tree, want all 13 (a buried branch stays, marked)", len(s.Nodes))
	}
}
