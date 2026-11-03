package engine

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// quick has short lookup budgets, so lookups that are meant to fail end fast.
var quick = Options{LookupTimeout: 400 * time.Millisecond, OptionalTimeout: 200 * time.Millisecond}

// runWith runs commands (after an `appId: app` config) on d with options o.
func runWith(t *testing.T, d driver.Driver, o Options, yaml string) FlowResult {
	t.Helper()
	return runSrc(t, d, o, t.TempDir(), "appId: app\n---\n"+yaml)
}

// runSrc runs a whole flow (config included), parsed as if it were dir/flow.yaml.
func runSrc(t *testing.T, d driver.Driver, o Options, dir, src string) FlowResult {
	t.Helper()
	fl, err := flow.ParseBytes(filepath.Join(dir, "flow.yaml"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return New(d, o).RunFlow(context.Background(), fl)
}

// mustPass fails the test when the flow failed.
func mustPass(t *testing.T, res FlowResult) {
	t.Helper()
	if res.Err != nil || res.Status != Passed {
		t.Fatalf("flow %s: %v (steps %+v)", res.Status, res.Err, res.Steps)
	}
}

// mustFail fails the test unless the flow failed with an error containing want.
func mustFail(t *testing.T, res FlowResult, want string) {
	t.Helper()
	if res.Status != Failed || res.Err == nil {
		t.Fatalf("flow %s, want failed with %q (steps %+v)", res.Status, want, res.Steps)
	}
	if !contains(res.Err.Error(), want) {
		t.Fatalf("error %q, want it to contain %q", res.Err, want)
	}
}

func contains(s, sub string) bool { return bytes.Contains([]byte(s), []byte(sub)) }

// writeFile writes content to dir/name and returns its path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// static is a screen that never changes.
func static(nodes ...*ui.Node) func() []*ui.Node {
	return func() []*ui.Node { return nodes }
}

// btn is an enabled, clickable 400x50 row at top with the given text and id.
func btn(text, id string, top int) *ui.Node {
	return &ui.Node{Text: text, ID: id, Bounds: ui.Rect{L: 0, T: top, R: 400, B: top + 50}, Enabled: true, Clickable: true}
}

// stepStatuses lists the top-level steps' statuses.
func stepStatuses(res FlowResult) []Status {
	var out []Status
	for _, s := range res.Steps {
		out = append(out, s.Status)
	}
	return out
}

// recorder is a Reporter that keeps every event.
type recorder struct {
	mu  sync.Mutex
	evs []Event
}

func (r *recorder) Event(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evs = append(r.evs, e)
}

func (r *recorder) all() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.evs...)
}

// logs are the Log lines reported.
func (r *recorder) logs() []string {
	var out []string
	for _, e := range r.all() {
		if e.Log != "" {
			out = append(out, e.Log)
		}
	}
	return out
}

// ends are the events that close a command.
func (r *recorder) ends() []Event {
	var out []Event
	for _, e := range r.all() {
		if e.Cmd != nil && !e.Start {
			out = append(out, e)
		}
	}
	return out
}

// pngOf is a w x h PNG filled with c, with the rectangle in painted p.
func pngOf(t *testing.T, w, h int, c color.Color, in image.Rectangle, p color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if (image.Point{x, y}).In(in) {
				img.Set(x, y, p)
			} else {
				img.Set(x, y, c)
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// darkFake is a device that can read and set dark mode (DarkModer).
type darkFake struct {
	*fakeDriver
	dark bool
	err  error
}

func (d *darkFake) SetDarkMode(ctx context.Context, on bool) error {
	if d.err != nil {
		return d.err
	}
	d.dark = on
	return nil
}

func (d *darkFake) DarkMode(ctx context.Context) (bool, error) { return d.dark, d.err }

// cssFake is a web device that resolves CSS selectors to node indices (CSSQuerier).
type cssFake struct {
	*fakeDriver
	q   map[string][]int
	err error
}

func (c *cssFake) QueryCSS(ctx context.Context, css string) ([]int, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.q[css], nil
}

// linked builds a screen from nodes (Depth set) in one app window, and links it.
func linked(platform string, nodes ...*ui.Node) *ui.Screen {
	w := &ui.Window{Type: "app", Package: "app", Active: true, Nodes: nodes, Bounds: ui.Rect{R: 400, B: 800}}
	s := &ui.Screen{Platform: platform, Width: 400, Height: 800, Windows: []*ui.Window{w}}
	s.Link()
	return s
}

// at is a node with bounds and depth, nothing else.
func at(class string, depth, l, t, r, b int) *ui.Node {
	return &ui.Node{Class: class, Depth: depth, Bounds: ui.Rect{L: l, T: t, R: r, B: b}, Enabled: true}
}

var (
	colorWhite = color.RGBA{255, 255, 255, 255}
	colorRed   = color.RGBA{255, 0, 0, 255}
	colorBlue  = color.RGBA{0, 0, 255, 255}
)

func imageRect(x0, y0, x1, y1 int) image.Rectangle { return image.Rect(x0, y0, x1, y1) }

// decodePNG reads a PNG file.
func decodePNG(t *testing.T, path string) image.Image {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return img
}
