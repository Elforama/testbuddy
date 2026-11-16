package web

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/elforama/testbuddy/internal/ui"
)

func (d *Driver) eval(ctx context.Context, expr string, out any) error {
	var r struct {
		Result struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := d.page.call(ctx, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true, "awaitPromise": true}, &r); err != nil {
		return err
	}
	if r.Exception != nil {
		msg := r.Exception.Exception.Description
		if msg == "" {
			msg = r.Exception.Text
		}
		return fmt.Errorf("page script: %s", msg)
	}
	if out != nil && len(r.Result.Value) > 0 {
		return json.Unmarshal(r.Result.Value, out)
	}
	return nil
}

type rawNode struct {
	D  int    `json:"d"`
	B  [4]int `json:"b"`
	C  string `json:"c"`
	T  string `json:"t"`
	ID string `json:"id"`
	CD string `json:"cd"`
	H  string `json:"h"`
	V  string `json:"v"`
	R  string `json:"r"`
	F  int    `json:"f"`

	Frame *struct {
		Src  string `json:"src"`
		X, Y int
	} `json:"frame"`

	// CV is what covers the element on screen, from the page's hit test (page.js hitPass).
	CV []rawHit `json:"cv"`

	main bool // from the main document (spliceFrames)
}

// rawHit is a cover at one sample point of an element (ui.Hit).
type rawHit struct {
	P  int        `json:"p"`
	X  int        `json:"x"`
	Y  int        `json:"y"`
	By rawRef     `json:"by"`
	L  []rawLayer `json:"l"`
}

// rawRef is an element of a snapshot: its index, or (-1) its box and tag when not listed.
type rawRef struct {
	I int     `json:"i"`
	B *[4]int `json:"b"`
	C string  `json:"c"`
}

type rawLayer struct {
	rawRef
	K string `json:"k"`
}

// moved is the hit with its snapshot indices shifted by base and its points by (dx, dy): a
// frame's own snapshot spliced into the page's.
func (h rawHit) moved(base, dx, dy int) rawHit {
	ref := func(r rawRef) rawRef {
		if r.I >= 0 {
			r.I += base
		} else if r.B != nil {
			b := [4]int{r.B[0] + dx, r.B[1] + dy, r.B[2] + dx, r.B[3] + dy}
			r.B = &b
		}
		return r
	}
	h.X, h.Y, h.By = h.X+dx, h.Y+dy, ref(h.By)
	l := make([]rawLayer, len(h.L))
	for i, x := range h.L {
		l[i] = rawLayer{ref(x.rawRef), x.K}
	}
	h.L = l
	return h
}

// snapshotIn evaluates the helper's snapshot in a frame session.
func (d *Driver) snapshotIn(ctx context.Context, session string) ([]rawNode, error) {
	var r struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	expr := "window.__tb ? window.__tb.snapshot() : ''"
	if err := d.page.callIn(ctx, session, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true}, &r); err != nil {
		return nil, err
	}
	if r.Result.Value == "" {
		d.page.callIn(ctx, session, "Runtime.evaluate", map[string]any{"expression": pageJS}, nil)
		if err := d.page.callIn(ctx, session, "Runtime.evaluate", map[string]any{"expression": "window.__tb.snapshot()", "returnByValue": true}, &r); err != nil {
			return nil, err
		}
	}
	var snap struct {
		Nodes []rawNode `json:"nodes"`
	}
	err := json.Unmarshal([]byte(r.Result.Value), &snap)
	return snap.Nodes, err
}

// frameFor finds the attached session for an iframe placeholder.
func (d *Driver) frameFor(src string, used map[string]bool) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	best, bestOrder := "", 1<<30
	cur := d.curSessionLocked()
	for id, f := range d.frames {
		if used[id] || f.tab != cur { // another tab's frame (issue #78)
			continue
		}
		if f.url == src {
			return id
		}
		if (f.url == "" || origin(f.url) == origin(src)) && f.order < bestOrder {
			best, bestOrder = id, f.order
		}
	}
	return best
}

// spliceFrames replaces cross-origin iframe placeholders' (empty) content with the frame's
// own snapshot, shifted to where the frame sits on the page. The covers each node carries
// (rawNode.CV) are kept pointing at the same elements in the spliced list.
func (d *Driver) spliceFrames(ctx context.Context, nodes []rawNode) []rawNode {
	has := false
	for _, n := range nodes {
		if n.Frame != nil {
			has = true
			break
		}
	}
	if !has {
		for i := range nodes {
			nodes[i].main = true
		}
		return nodes
	}
	out := make([]rawNode, 0, len(nodes))
	pos := make([]int, len(nodes)) // where each main-document node lands in out
	used := map[string]bool{}
	for i, n := range nodes {
		n.main = true
		pos[i] = len(out)
		out = append(out, n)
		if n.Frame == nil {
			continue
		}
		sess := d.frameFor(n.Frame.Src, used)
		if sess == "" {
			continue
		}
		used[sess] = true
		inner, err := d.snapshotIn(ctx, sess)
		if err != nil {
			continue
		}
		base := len(out)
		for _, c := range inner {
			c.D += n.D + 1
			c.B = [4]int{c.B[0] + n.Frame.X, c.B[1] + n.Frame.Y, c.B[2] + n.Frame.X, c.B[3] + n.Frame.Y}
			c.Frame = nil
			for k, h := range c.CV {
				c.CV[k] = h.moved(base, n.Frame.X, n.Frame.Y)
			}
			out = append(out, c)
		}
	}
	// The main document's covers point at its own nodes: where they now are.
	for i := range out {
		if !out[i].main {
			continue
		}
		for k, h := range out[i].CV {
			re := func(r rawRef) rawRef {
				if r.I >= 0 && r.I < len(pos) {
					r.I = pos[r.I]
				} else if r.I >= 0 {
					r.I = -1
				}
				return r
			}
			h.By = re(h.By)
			for j := range h.L {
				h.L[j].rawRef = re(h.L[j].rawRef)
			}
			out[i].CV[k] = h
		}
	}
	return out
}

func (d *Driver) Screen(ctx context.Context) (*ui.Screen, error) {
	seq := d.Seq()
	var js string
	err := d.eval(ctx, "window.__tb ? window.__tb.snapshot() : null", &js)
	if err == nil && js == "" {
		// a document that predates the helper (about:blank): install it now
		d.eval(ctx, pageJS, nil)
		err = d.eval(ctx, "window.__tb.snapshot()", &js)
	}
	if err != nil {
		return nil, err
	}
	var snap struct {
		W, H    int
		URL     string
		Title   string
		Loading bool
		Nodes   []rawNode
	}
	if err := json.Unmarshal([]byte(js), &snap); err != nil {
		return nil, err
	}
	d.mu.Lock()
	if snap.W > 0 {
		d.w, d.h = snap.W, snap.H
	}
	d.mu.Unlock()
	snap.Nodes = d.spliceFrames(ctx, snap.Nodes)
	cssMap := make([]int, 0, len(snap.Nodes))
	for i, n := range snap.Nodes {
		if n.main {
			cssMap = append(cssMap, i)
		}
	}
	d.mu.Lock()
	d.cssMap = cssMap
	d.mu.Unlock()
	win := &ui.Window{ID: 1, Type: "app", Package: snap.URL, Title: snap.Title, Active: true, Focused: true,
		Bounds: ui.Rect{R: snap.W, B: snap.H}}
	for _, r := range snap.Nodes {
		win.Nodes = append(win.Nodes, r.node())
	}
	s := &ui.Screen{Platform: "web", Width: snap.W, Height: snap.H, Seq: seq, Windows: []*ui.Window{win}}
	s.Link()
	linkCovers(win.Nodes, snap.Nodes)
	return s, nil
}

// linkCovers gives each node the covers its raw read carries (ui.Hit), pointing at the nodes
// listed (or at stand-ins for elements that are not).
func linkCovers(nodes []*ui.Node, raw []rawNode) {
	ref := func(r rawRef) *ui.Node {
		if r.I >= 0 && r.I < len(nodes) {
			return nodes[r.I]
		}
		n := &ui.Node{Class: r.C, Enabled: true, Visible: true, Index: -1}
		if r.B != nil {
			n.Bounds = ui.Rect{L: r.B[0], T: r.B[1], R: r.B[2], B: r.B[3]}
		}
		return n
	}
	for i, r := range raw {
		if len(r.CV) == 0 || i >= len(nodes) {
			continue
		}
		n := nodes[i]
		for _, h := range r.CV {
			if h.By.I < 0 && h.By.B == nil {
				continue
			}
			hit := ui.Hit{Point: h.P, X: h.X, Y: h.Y, By: ref(h.By)}
			if hit.By.Index < 0 {
				hit.By.Window = n.Window
			}
			for _, l := range h.L {
				if l.I < 0 && l.B == nil {
					continue
				}
				ln := ref(l.rawRef)
				if ln.Index < 0 {
					ln.Window = n.Window
				}
				hit.Layers = append(hit.Layers, ui.HitLayer{Node: ln, Kind: l.K})
			}
			n.Covers = append(n.Covers, hit)
		}
	}
}

func (r rawNode) node() *ui.Node {
	hint := r.H
	if r.V != "" {
		hint = "" // the placeholder only counts while the field is empty
	}
	n := &ui.Node{Depth: r.D, Bounds: ui.Rect{L: r.B[0], T: r.B[1], R: r.B[2], B: r.B[3]}, Class: r.C,
		Text: r.T, ID: r.ID, Desc: r.CD, Hint: hint, Value: r.V,
		Clickable: r.F&1 != 0, Enabled: r.F&2 != 0, Focused: r.F&4 != 0, Scrollable: r.F&8 != 0,
		Checked: r.F&16 != 0, Selected: r.F&32 != 0, Checkable: r.F&64 != 0, Password: r.F&128 != 0,
		Editable: r.F&256 != 0, ShowingHint: r.F&2048 != 0, Visible: true}
	if r.R != "" && n.Class != "" {
		n.Class += "[" + r.R + "]"
	}
	return n
}

// ReadDevToolsPage reads a single document from a debuggable embedded WebView.
// Coordinates are relative to its viewport, in CSS pixels.
func ReadDevToolsPage(ctx context.Context, socket string) (int, []*ui.Node, error) {
	conn, err := dialCDP(ctx, socket, nil)
	if err != nil {
		return 0, nil, err
	}
	defer conn.ws.CloseNow()
	d := &Driver{page: conn}
	var snapshot string
	expr := pageJS + "\n;window.__tb.snapshot(false)"
	if err := d.eval(ctx, expr, &snapshot); err != nil {
		return 0, nil, err
	}
	var raw struct {
		W     int
		Nodes []rawNode
	}
	if err := json.Unmarshal([]byte(snapshot), &raw); err != nil {
		return 0, nil, err
	}
	nodes := make([]*ui.Node, 0, len(raw.Nodes))
	for _, r := range raw.Nodes {
		nodes = append(nodes, r.node())
	}
	return raw.W, nodes, nil
}

// QueryCSS returns the indices (into the last Screen's nodes) of the elements a CSS
// selector matches.
func (d *Driver) QueryCSS(ctx context.Context, sel string) ([]int, error) {
	b, _ := json.Marshal(sel)
	var js string
	if err := d.eval(ctx, "window.__tb ? window.__tb.css("+string(b)+") : '[]'", &js); err != nil {
		return nil, err
	}
	var idx []int
	if err := json.Unmarshal([]byte(js), &idx); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	out := idx[:0]
	for _, i := range idx {
		if i >= 0 && i < len(d.cssMap) {
			out = append(out, d.cssMap[i])
		}
	}
	return out, nil
}

// CoveredAt says what a click at (x, y) would reach instead of the screen node at index (a
// veil, a dialog's backdrop, a toast over it), or "" when it reaches the node. Nodes inside
// cross-origin frames are not checked (""); same-origin ones are, through the frame.
func (d *Driver) CoveredAt(ctx context.Context, index, x, y int) (string, error) {
	d.mu.Lock()
	snap := -1
	for i, n := range d.cssMap {
		if n == index {
			snap = i
			break
		}
	}
	d.mu.Unlock()
	if snap < 0 {
		return "", nil
	}
	var what string
	err := d.eval(ctx, fmt.Sprintf("window.__tb && window.__tb.covered ? window.__tb.covered(%d, %d, %d) : ''", snap, x, y), &what)
	return what, err
}
