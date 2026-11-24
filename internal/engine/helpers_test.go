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

var quick = Options{LookupTimeout: 400 * time.Millisecond, OptionalTimeout: 200 * time.Millisecond}

func runWith(t *testing.T, d driver.Driver, o Options, yaml string) FlowResult {
	t.Helper()
	return runSrc(t, d, o, t.TempDir(), "appId: app\n---\n"+yaml)
}

func runSrc(t *testing.T, d driver.Driver, o Options, dir, src string) FlowResult {
	t.Helper()
	fl, err := flow.ParseBytes(filepath.Join(dir, "flow.yaml"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return New(d, o).RunFlow(context.Background(), fl)
}

func mustPass(t *testing.T, res FlowResult) {
	t.Helper()
	if res.Err != nil || res.Status != Passed {
		t.Fatalf("flow %s: %v (steps %+v)", res.Status, res.Err, res.Steps)
	}
}

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

func static(nodes ...*ui.Node) func() []*ui.Node {
	return func() []*ui.Node { return nodes }
}

func btn(text, id string, top int) *ui.Node {
	return &ui.Node{Text: text, ID: id, Bounds: ui.Rect{L: 0, T: top, R: 400, B: top + 50}, Enabled: true, Clickable: true}
}

func stepStatuses(res FlowResult) []Status {
	var out []Status
	for _, s := range res.Steps {
		out = append(out, s.Status)
	}
	return out
}

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

func (r *recorder) logs() []string {
	var out []string
	for _, e := range r.all() {
		if e.Log != "" {
			out = append(out, e.Log)
		}
	}
	return out
}

func (r *recorder) ends() []Event {
	var out []Event
	for _, e := range r.all() {
		if e.Cmd != nil && !e.Start {
			out = append(out, e)
		}
	}
	return out
}

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

func linked(platform string, nodes ...*ui.Node) *ui.Screen {
	w := &ui.Window{Type: "app", Package: "app", Active: true, Nodes: nodes, Bounds: ui.Rect{R: 400, B: 800}}
	s := &ui.Screen{Platform: platform, Width: 400, Height: 800, Windows: []*ui.Window{w}}
	s.Link()
	return s
}

func at(class string, depth, l, t, r, b int) *ui.Node {
	return &ui.Node{Class: class, Depth: depth, Bounds: ui.Rect{L: l, T: t, R: r, B: b}, Enabled: true}
}

var (
	colorWhite = color.RGBA{255, 255, 255, 255}
	colorRed   = color.RGBA{255, 0, 0, 255}
	colorBlue  = color.RGBA{0, 0, 255, 255}
)

func imageRect(x0, y0, x1, y1 int) image.Rectangle { return image.Rect(x0, y0, x1, y1) }

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
