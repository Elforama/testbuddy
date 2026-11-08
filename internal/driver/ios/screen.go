package ios

import (
	"context"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
	"github.com/elforama/testbuddy/internal/wire"
)

type rawNode struct {
	D  int    `json:"d"`
	B  [4]int `json:"b"`
	C  string `json:"c"`
	T  string `json:"t"`
	CD string `json:"cd"`
	ID string `json:"id"`
	H  string `json:"h"`
	V  string `json:"v"`
	F  int    `json:"f"`
}

type rawWindow struct {
	ID      int       `json:"id"`
	Type    int       `json:"type"`
	Active  bool      `json:"active"`
	Focused bool      `json:"focused"`
	Pkg     string    `json:"pkg"`
	Title   string    `json:"title"`
	B       [4]int    `json:"b"`
	Nodes   []rawNode `json:"nodes"`
}

// readTimeout is how long the runner may take to read the screen. ciFirstReadTimeout is for the
// first read after a launch in CI (package ci): XCTest opens its automation session with the
// new app on that read and waits up to 60 s for the app to answer, which a slow runner can
// need (34 s on GitHub Actions, at once on a developer's Mac).
var readTimeout, ciFirstReadTimeout = 30 * time.Second, 70 * time.Second

func (d *Driver) Screen(ctx context.Context) (*ui.Screen, error) {
	if s := d.ready.Swap(nil); s != nil && s.Seq == d.Seq() {
		return s, nil
	}
	m, err := d.call(ctx, "tree", nil, readTimeout)
	if err != nil {
		return nil, err
	}
	return d.decodeScreen(m)
}

func (d *Driver) decodeScreen(m *wire.Msg) (*ui.Screen, error) {
	var raw struct {
		Seq      int64       `json:"seq"`
		Keyboard bool        `json:"keyboard"`
		Windows  []rawWindow `json:"windows"`
	}
	if err := m.Decode(&raw); err != nil {
		return nil, err
	}
	w, h := d.Size()
	for _, rw := range raw.Windows {
		if rw.Type == 1 && rw.B[2] > rw.B[0] {
			w, h = rw.B[2]-rw.B[0], rw.B[3]-rw.B[1]
			d.mu.Lock()
			d.w, d.h = w, h
			d.mu.Unlock()
			break
		}
	}
	s := &ui.Screen{Platform: "ios", Width: w, Height: h, Seq: d.seq(raw.Seq), Keyboard: raw.Keyboard}
	for _, rw := range raw.Windows {
		win := &ui.Window{ID: rw.ID, Type: windowType(rw.Type), Package: rw.Pkg, Title: rw.Title,
			Active: rw.Active, Focused: rw.Focused, Bounds: ui.Rect{L: rw.B[0], T: rw.B[1], R: rw.B[2], B: rw.B[3]}}
		for _, rn := range rw.Nodes {
			win.Nodes = append(win.Nodes, convert(rn))
		}
		s.Windows = append(s.Windows, win)
	}
	s.Link()
	return s, nil
}

func windowType(t int) string {
	switch t {
	case 1:
		return "app"
	case 2:
		return "keyboard"
	case 3:
		return "system"
	case 4:
		return "overlay"
	default:
		return "other"
	}
}

const (
	fClickable = 1 << iota
	fEnabled
	fFocused
	fScrollable
	fChecked
	fSelected
	fCheckable
	fPassword
	fEditable
	fLongClickable
	fFocusable
	fShowingHint
	fVisible
)

func convert(r rawNode) *ui.Node {
	return &ui.Node{
		Depth: r.D, Bounds: ui.Rect{L: r.B[0], T: r.B[1], R: r.B[2], B: r.B[3]},
		Class: r.C, Text: r.T, Desc: r.CD, ID: r.ID, Hint: r.H, Value: r.V,
		Clickable: r.F&fClickable != 0, Enabled: r.F&fEnabled != 0, Focused: r.F&fFocused != 0,
		Scrollable: r.F&fScrollable != 0, Checked: r.F&fChecked != 0, Selected: r.F&fSelected != 0,
		Checkable: r.F&fCheckable != 0, Password: r.F&fPassword != 0, Editable: r.F&fEditable != 0,
		LongClickable: r.F&fLongClickable != 0, ShowingHint: r.F&fShowingHint != 0, Visible: r.F&fVisible != 0,
	}
}
