package session

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/look"
	"github.com/elforama/testbuddy/internal/ui"
)

func loginScreen() []item {
	return []item{label("Welcome back", 100), {class: "android.widget.EditText", hint: "Email", id: "com.app:id/email", top: 200}, button("Sign in", 300)}
}

// look lists the screen with refs, and remembers it for later @refs.
func TestLook(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(loginScreen()...)
	d := addDev(s, f, "")
	resp, _ := call(t, s.look, map[string]any{}, "")
	want := `android 400x800 com.app, 3 elements
@1 Text "Welcome back"
@2 Field #email hint="Email"
@3 Button "Sign in"
`
	if !resp.OK || resp.Text != want {
		t.Errorf("text =\n%s\nwant\n%s", resp.Text, want)
	}
	if d.last == nil || len(d.last.Elements) != 3 {
		t.Errorf("last listing %+v", d.last)
	}
	if f.waits != 0 {
		t.Error("look without --wait should not wait")
	}
	if resp.JSON != nil {
		t.Error("JSON only on request")
	}
}

// Refs stay stable across looks while elements stay.
func TestLookStableRefs(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(label("Top", 0), button("OK", 100))
	addDev(s, f, "")
	call(t, s.look, nil, "")
	f.show(label("New", 0), button("OK", 100))
	resp, _ := call(t, s.look, nil, "")
	if !strings.Contains(resp.Text, `@2 Button "OK"`) || !strings.Contains(resp.Text, `@3 Text "New"`) {
		t.Errorf("text %s", resp.Text)
	}
}

// look options: --wait settles first, --all adds every node, --json the listing as JSON.
func TestLookOptions(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(loginScreen()...)
	addDev(s, f, "")
	resp, _ := call(t, s.look, map[string]any{"wait": true, "all": true, "json": true}, "")
	if f.waits != 1 {
		t.Errorf("waits = %d", f.waits)
	}
	if !strings.Contains(resp.Text, "\nall nodes:\nwindow app com.app\n  FrameLayout [0,0][400,800]\n    TextView \"Welcome back\" [0,100][400,150]\n") {
		t.Errorf("all nodes missing:\n%s", resp.Text)
	}
	var l look.Listing
	if err := json.Unmarshal(resp.JSON, &l); err != nil || len(l.Elements) != 3 || l.Platform != "android" {
		t.Errorf("json %s (%v)", resp.JSON, err)
	}
}

// look --shot saves a small screenshot under TB_HOME/shots, or says why it could not.
func TestLookShot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TB_HOME", home)
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	addDev(s, f, "alice")
	resp, _ := call(t, s.look, map[string]any{"shot": true}, "")
	i := strings.Index(resp.Text, "screenshot: ")
	if i < 0 {
		t.Fatalf("text %s", resp.Text)
	}
	path := strings.TrimSpace(resp.Text[i+len("screenshot: "):])
	if filepath.Dir(path) != filepath.Join(home, "shots") || !strings.HasPrefix(filepath.Base(path), "alice-") {
		t.Errorf("path %s", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error(err)
	}
	f.shotErr = errBoom
	resp, _ = call(t, s.look, map[string]any{"shot": true}, "")
	if !resp.OK || !strings.HasSuffix(resp.Text, "screenshot failed: boom\n") {
		t.Errorf("text %s", resp.Text)
	}
}

// look fails with exit 1 when the screen cannot be read, exit 2 without a device.
func TestLookErrors(t *testing.T) {
	s := New()
	if resp, _ := call(t, s.look, nil, ""); resp.Exit != 2 {
		t.Errorf("no device: %+v", resp)
	}
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.screenErr = errBoom
	addDev(s, f, "")
	if resp, _ := call(t, s.look, nil, ""); resp.Exit != 1 || resp.Text != "boom" {
		t.Errorf("screen error: %+v", resp)
	}
}

// The default scale makes screenshots about 500 px wide: Android sizes are pixels, iOS
// sizes points (shots ~3x), never upscaled.
func TestShotScale(t *testing.T) {
	cases := []struct {
		name string
		plat device.Platform
		w    int
		arg  float64
		want float64
	}{
		{"android wide", device.Android, 1000, 0, 0.5},
		{"android narrow", device.Android, 300, 0, 1},
		{"ios points", device.IOS, 250, 0, 500.0 / 750},
		{"ios big", device.IOS, 100, 0, 1},
		{"web", device.Web, 1000, 0, 500.0 / 3000},
		{"unknown size", device.Android, 0, 0, 1},
		{"explicit scale", device.Android, 1000, 0.25, 0.25},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New()
			f := newFake("s", "Dev", c.plat)
			f.w = c.w
			f.shotW = 100
			addDev(s, f, "")
			resp, _ := call(t, s.shot, map[string]any{"scale": c.arg, "path": filepath.Join(t.TempDir(), "x.png")}, "")
			if !resp.OK {
				t.Fatalf("resp %+v", resp)
			}
			if len(f.shotScales) != 1 || f.shotScales[0] != c.want {
				t.Errorf("scale %v, want %v", f.shotScales, c.want)
			}
		})
	}
}

// shot writes to the given path (relative to the caller's folder), else under TB_HOME/shots
// named after the device.
func TestShotPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TB_HOME", home)
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	addDev(s, f, "")
	cwd := t.TempDir()
	resp, _ := call(t, s.shot, map[string]any{"path": "screen.png"}, cwd)
	if want := filepath.Join(cwd, "screen.png") + "\n"; !resp.OK || resp.Text != want {
		t.Errorf("relative: %+v", resp)
	}
	abs := filepath.Join(t.TempDir(), "abs.png")
	if resp, _ := call(t, s.shot, map[string]any{"path": abs}, cwd); resp.Text != abs+"\n" {
		t.Errorf("absolute: %+v", resp)
	}
	resp, _ = call(t, s.shot, nil, cwd)
	p := strings.TrimSpace(resp.Text)
	if filepath.Dir(p) != filepath.Join(home, "shots") || !strings.HasPrefix(filepath.Base(p), "emulator-5554-") || !strings.HasSuffix(p, ".png") {
		t.Errorf("default path %s", p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	decodePNG(t, b)
}

// shot fails with exit 1 when the device or the file system says no, exit 2 without a device.
func TestShotErrors(t *testing.T) {
	s := New()
	if resp, _ := call(t, s.shot, nil, ""); resp.Exit != 2 {
		t.Errorf("no device: %+v", resp)
	}
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	addDev(s, f, "")
	if resp, _ := call(t, s.shot, map[string]any{"path": filepath.Join(t.TempDir(), "missing-dir", "x.png")}, ""); resp.Exit != 1 {
		t.Errorf("unwritable: %+v", resp)
	}
	f.shotErr = errBoom
	if resp, _ := call(t, s.shot, nil, ""); resp.Exit != 1 || resp.Text != "boom" {
		t.Errorf("driver error: %+v", resp)
	}
}

// iOS screenshots taken at reduced scale are shrunk to 500 px wide.
func TestShotIOSDownscales(t *testing.T) {
	s := New()
	f := newFake("UDID", "iPhone", device.IOS)
	f.w, f.shotW = 400, 1200 // points; the simulator returns full pixels
	addDev(s, f, "")
	p := filepath.Join(t.TempDir(), "x.png")
	call(t, s.shot, map[string]any{"path": p}, "")
	b, _ := os.ReadFile(p)
	if w := decodePNG(t, b).Bounds().Dx(); w != 500 {
		t.Errorf("width %d, want 500", w)
	}
}

// downscalePNG box-averages to the target width and leaves small or bad images alone.
func TestDownscalePNG(t *testing.T) {
	src := solidPNG(1000, 400, color.RGBA{200, 100, 50, 255})
	out := decodePNG(t, downscalePNG(src, 500))
	if b := out.Bounds(); b.Dx() != 500 || b.Dy() != 200 {
		t.Errorf("size %v", b)
	}
	if r, g, bl, a := out.At(250, 100).RGBA(); r>>8 != 200 || g>>8 != 100 || bl>>8 != 50 || a>>8 != 255 {
		t.Errorf("color %d %d %d %d", r>>8, g>>8, bl>>8, a>>8)
	}
	small := solidPNG(300, 10, color.White)
	if got := downscalePNG(small, 500); string(got) != string(small) {
		t.Error("an image already narrow enough should be returned as is")
	}
	if got := downscalePNG([]byte("not an image"), 500); string(got) != "not an image" {
		t.Error("undecodable data should be returned as is")
	}
	// alternating black and white columns average to grey
	img := image.NewRGBA(image.Rect(0, 0, 1000, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 1000; x++ {
			if x%2 == 0 {
				img.Set(x, y, color.White)
			} else {
				img.Set(x, y, color.Black)
			}
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	grey := decodePNG(t, downscalePNG(buf.Bytes(), 500))
	if b := grey.Bounds(); b.Dx() != 500 || b.Dy() != 2 {
		t.Fatalf("size %v", b)
	}
	if r, _, _, _ := grey.At(10, 1).RGBA(); r>>8 < 120 || r>>8 > 135 {
		t.Errorf("stripes averaged to %d, want ~127", r>>8)
	}
}

// dumpNodes prints every node indented by depth, with text, desc, id, hint, bounds and flags.
func TestDumpNodes(t *testing.T) {
	s := &ui.Screen{Windows: []*ui.Window{{Type: "app", Package: "com.app", Nodes: []*ui.Node{
		{Class: "android.widget.FrameLayout", Bounds: ui.Rect{R: 10, B: 10}, Enabled: true},
		{Class: "android.widget.Button", Text: "Go", Desc: "Go now", ID: "com.app:id/go", Depth: 1, Clickable: true, Enabled: true, Bounds: ui.Rect{L: 1, T: 2, R: 3, B: 4}},
		{Class: "android.widget.EditText", Hint: "Name", Depth: 2, Bounds: ui.Rect{}},
		{Class: "Text", Text: "same", Desc: "same", Depth: 1, Enabled: true},
	}}, {Type: "keyboard", Package: "ime"}}}
	want := `window app com.app
  FrameLayout [0,0][10,10]
    Button "Go" desc="Go now" #go [1,2][3,4] clickable
      EditText hint="Name" [0,0][0,0] disabled
    Text "same" [0,0][0,0]
window keyboard ime
`
	if got := dumpNodes(s); got != want {
		t.Errorf("dumpNodes =\n%s\nwant\n%s", got, want)
	}
	if allNodes(s) != want {
		t.Error("allNodes should be dumpNodes")
	}
}
