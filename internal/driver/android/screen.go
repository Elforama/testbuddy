package android

import (
	"context"
	"fmt"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

type rawNode struct {
	D  int    `json:"d"`
	B  [4]int `json:"b"`
	C  string `json:"c"`
	T  string `json:"t"`
	CD string `json:"cd"`
	ID string `json:"id"`
	H  string `json:"h"`
	E  string `json:"e"`
	SD string `json:"sd"`
	F  int    `json:"f"`
	CI []int  `json:"ci"` // collection: rows, columns (-1 unknown)
	II []int  `json:"ii"` // collection item: row, column
	O  *int   `json:"o"`  // drawing order among its siblings (absent from older agents)
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

func (d *Driver) Screen(ctx context.Context) (*ui.Screen, error) { return d.screen(ctx, nil) }

// ScreenFresh reads the screen without the agent's node cache: a view moved by translation
// (sliding, springing in) sends no event, so a cached read shows it where it was. For reads
// that check whether something still moves.
func (d *Driver) ScreenFresh(ctx context.Context) (*ui.Screen, error) {
	return d.screen(ctx, map[string]any{"fresh": true})
}

func (d *Driver) AwaitCoverGone(ctx context.Context, window int, pkg, id, class string, x, y int, timeout time.Duration) (bool, bool, error) {
	m, err := d.call(ctx, "awaitCoverGone", map[string]any{"window": window, "pkg": pkg, "nodeId": id, "class": class,
		"x": x, "y": y, "timeout": timeout.Milliseconds()}, timeout+time.Second)
	if err != nil {
		return false, false, err
	}
	var result struct{ Observed, Gone bool }
	if err := m.Decode(&result); err != nil {
		return false, false, err
	}
	return result.Observed, result.Gone, nil
}

func (d *Driver) screen(ctx context.Context, p map[string]any) (*ui.Screen, error) {
	m, c, err := d.callOn(ctx, "tree", p, 20*time.Second)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Seq      int64       `json:"seq"`
		Keyboard bool        `json:"keyboard"`
		Windows  []rawWindow `json:"windows"`
		Fix      *windowFix  `json:"windowFix"` // absent from older agents and from normal reads
	}
	if err := m.Decode(&raw); err != nil {
		return nil, err
	}
	d.noteWindowFix(raw.Fix)
	w, h := d.Size()
	s := &ui.Screen{Platform: "android", Width: w, Height: h, Seq: d.seqOn(c, raw.Seq), Keyboard: raw.Keyboard}
	for _, rw := range raw.Windows {
		w := &ui.Window{ID: rw.ID, Type: windowType(rw.Type), Package: rw.Pkg, Title: rw.Title,
			Active: rw.Active, Focused: rw.Focused, Bounds: ui.Rect{L: rw.B[0], T: rw.B[1], R: rw.B[2], B: rw.B[3]}}
		for _, rn := range rw.Nodes {
			w.Nodes = append(w.Nodes, convert(rn))
		}
		s.Windows = append(s.Windows, w)
	}
	d.augmentWebView(ctx, s)
	s.Link()
	return s, nil
}

// windowFix is what the agent did when a read had no readable app window, or windows whose
// root it could not read (issue #135: a stale cached window list held the status bar alone).
type windowFix struct {
	NullRoots      int    `json:"nullRoots"`      // windows left out of what was written: no root
	StaleNullRoots int    `json:"staleNullRoots"` // windows without a root in the list before a fix
	How            string `json:"how"`            // "fresh read", "active root" or "none"
	Window         int    `json:"window"`
}

func (f *windowFix) String() string {
	switch f.How {
	case "fresh read":
		return fmt.Sprintf("the device's window list had no readable app window (%d windows without a root); a read past the accessibility cache found app window %d", f.StaleNullRoots, f.Window)
	case "active root":
		return fmt.Sprintf("the device's window list had no readable app window (%d windows without a root), even past the accessibility cache; read window %d through the active window's root", f.StaleNullRoots, f.Window)
	default:
		return fmt.Sprintf("the device could not read the root of an app window (%d windows without a root)", f.NullRoots)
	}
}

// noteWindowFix puts a read's windowFix in the run's trace once per step: a wait reads the
// screen many times, and the seen set empties when the step's notes are handed out (Notes).
func (d *Driver) noteWindowFix(f *windowFix) {
	if f == nil {
		return
	}
	msg := f.String()
	d.notesMu.Lock()
	if d.windowFixSeen == nil {
		d.windowFixSeen = map[string]bool{}
	}
	seen := d.windowFixSeen[msg]
	d.windowFixSeen[msg] = true
	if !seen {
		d.notes = append(d.notes, msg)
	}
	d.notesMu.Unlock()
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
	fMoreAfter
	fMoreBefore
)

func convert(r rawNode) *ui.Node {
	n := &ui.Node{
		Depth: r.D, Bounds: ui.Rect{L: r.B[0], T: r.B[1], R: r.B[2], B: r.B[3]},
		Class: r.C, Text: r.T, Desc: r.CD, ID: r.ID, Hint: r.H, Error: r.E, State: r.SD,
		Clickable: r.F&fClickable != 0, Enabled: r.F&fEnabled != 0, Focused: r.F&fFocused != 0,
		Scrollable: r.F&fScrollable != 0, Checked: r.F&fChecked != 0, Selected: r.F&fSelected != 0,
		Checkable: r.F&fCheckable != 0, Password: r.F&fPassword != 0, Editable: r.F&fEditable != 0,
		LongClickable: r.F&fLongClickable != 0, ShowingHint: r.F&fShowingHint != 0, Visible: r.F&fVisible != 0,
		MoreAfter: r.F&fMoreAfter != 0, MoreBefore: r.F&fMoreBefore != 0,
	}
	if len(r.CI) == 2 {
		n.Coll = &ui.Coll{Rows: r.CI[0], Cols: r.CI[1]}
	}
	if len(r.II) == 2 {
		n.Item = &ui.Coll{Rows: r.II[0], Cols: r.II[1]}
	}
	if r.O != nil {
		n.DrawOrder, n.DrawOrderKnown = *r.O, true
	}
	return n
}
