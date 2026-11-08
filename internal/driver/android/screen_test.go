package android

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/elforama/testbuddy/internal/wire"
)

const treeJSON = `{
 "seq": 12, "keyboard": true,
 "windows": [
  {"id": 40, "type": 2, "active": false, "focused": false, "pkg": "com.google.android.inputmethod.latin", "b": [0, 1600, 1080, 2400],
   "nodes": [{"d": 0, "b": [0, 1600, 1080, 2400], "c": "android.widget.FrameLayout", "f": 4096}]},
  {"id": 7, "type": 1, "active": true, "focused": true, "pkg": "com.example.app", "title": "Example", "b": [0, 0, 1080, 2400],
   "nodes": [
    {"d": 0, "b": [0, 0, 1080, 2400], "c": "android.widget.FrameLayout", "f": 4098},
    {"d": 1, "b": [0, 0, 1080, 200], "c": "android.widget.TextView", "t": "Sign in", "id": "com.example.app:id/title", "f": 4098},
    {"d": 1, "b": [40, 400, 1040, 520], "c": "android.widget.EditText", "h": "Email", "t": "Email", "e": "Required", "f": 7431},
    {"d": 2, "b": [40, 400, 100, 520], "c": "android.widget.ImageView", "cd": "clear", "f": 4099},
    {"d": 1, "b": [40, 600, 1040, 720], "c": "android.widget.Switch", "t": "Remember", "sd": "on", "f": 4179}
   ]},
  {"id": 3, "type": 3, "pkg": "com.android.systemui", "b": [0, 0, 1080, 80], "nodes": []},
  {"id": 9, "type": 4, "pkg": "dev.overlay", "b": [0, 0, 10, 10]},
  {"id": 11, "type": 6, "pkg": "x", "b": [0, 0, 1, 1]}
 ]}`

// Screen turns the agent's tree into a linked ui.Screen: window types, depths into a tree, flags, keyboard.
func TestScreen(t *testing.T) {
	a := newFakeAgent(t)
	a.on("tree", func(map[string]any) map[string]any {
		var m map[string]any
		json.Unmarshal([]byte(treeJSON), &m)
		return m
	})
	d := newDriver(t, a)
	s, err := d.Screen(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	if s.Platform != "android" || s.Width != 1080 || s.Height != 2400 || s.Seq != 12 || !s.Keyboard {
		t.Errorf("screen header %+v", s)
	}
	var types []string
	for _, w := range s.Windows {
		types = append(types, w.Type)
	}
	if !reflect.DeepEqual(types, []string{"keyboard", "app", "system", "overlay", "other"}) {
		t.Errorf("window types %v", types)
	}
	app := s.Windows[1]
	if app.ID != 7 || app.Package != "com.example.app" || app.Title != "Example" || !app.Active || !app.Focused || app.Bounds.B != 2400 {
		t.Errorf("app window %+v", app)
	}
	if s.Foreground() != "com.example.app" {
		t.Errorf("Foreground = %q", s.Foreground())
	}
	if len(s.Nodes) != 6 {
		t.Fatalf("%d nodes, want 6", len(s.Nodes))
	}
	root := app.Roots[0]
	if len(app.Roots) != 1 || len(root.Children) != 3 {
		t.Fatalf("app tree: %d roots, %d children", len(app.Roots), len(root.Children))
	}
	title, field, sw := root.Children[0], root.Children[1], root.Children[2]
	if title.Text != "Sign in" || title.ID != "com.example.app:id/title" || title.Parent != root || title.Window != 1 {
		t.Errorf("title %+v", title)
	}
	if !field.Editable || !field.Focused || !field.ShowingHint || field.Hint != "Email" || field.Error != "Required" || !field.Clickable {
		t.Errorf("field %+v", field)
	}
	if field.Label() != "" {
		t.Errorf("a field showing its hint has no label, got %q", field.Label())
	}
	if len(field.Children) != 1 || field.Children[0].Desc != "clear" || !field.Children[0].Clickable {
		t.Errorf("field's child %+v", field.Children)
	}
	if !sw.Checkable || !sw.Checked || sw.State != "on" || sw.Index != 5 {
		t.Errorf("switch %+v", sw)
	}
	if s.Nodes[0].Window != 0 || s.Nodes[0].Class != "android.widget.FrameLayout" {
		t.Errorf("first node is the keyboard's: %+v", s.Nodes[0])
	}
}

// Screen maps the tree's seq through seqBase like events do.
func TestScreenSeqUsesBase(t *testing.T) {
	a := newFakeAgent(t)
	a.on("tree", func(map[string]any) map[string]any { return map[string]any{"seq": 4, "windows": []any{}} })
	d := newDriver(t, a)
	d.seqBase = 100
	s, err := d.Screen(ctxT(t))
	if err != nil || s.Seq != 104 || len(s.Windows) != 0 {
		t.Errorf("Screen = %+v, %v; want seq 104", s, err)
	}
}

// A malformed tree or an agent error is returned, not a partial screen.
func TestScreenErrors(t *testing.T) {
	a := newFakeAgent(t)
	d := newDriver(t, a)
	a.on("tree", func(map[string]any) map[string]any { return map[string]any{"windows": "nope"} })
	if s, err := d.Screen(ctxT(t)); err == nil {
		t.Errorf("bad windows: got %+v", s)
	}
	a.on("tree", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "no root"} })
	_, err := d.Screen(ctxT(t))
	var ae *wire.AgentError
	if !errors.As(err, &ae) || ae.Msg != "no root" {
		t.Errorf("agent error: %v", err)
	}
}
