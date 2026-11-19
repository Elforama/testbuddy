package session

import (
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/ui"
)

// The web's sizes are CSS pixels and its screenshots are taken at 1x: a look --shot on a
// 1280 px wide page is about 500 px wide, not divided by 3 as iOS points are (it was 167 px).
func TestLookShotWebIsReadable(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	s := New()
	f := newFake("chrome", "Chrome", device.Web)
	f.w, f.h = 1280, 713
	addDev(s, f, "")
	call(t, s.look, map[string]any{"shot": true}, "")
	if len(f.shotScales) != 1 || f.shotScales[0] != 500.0/1280 {
		t.Errorf("scale %v, want %v", f.shotScales, 500.0/1280)
	}
}

// look --shot reads the tree and takes the screenshot at the same time: each waits for the
// other to start, which a read then a shot one after the other never lets happen.
func TestLookShotReadsAndShootsTogether(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(loginScreen()...)
	addDev(s, f, "")
	reading, shooting := make(chan struct{}), make(chan struct{})
	var apart [2]bool
	wait := func(c chan struct{}, i int) {
		select {
		case <-c:
		case <-time.After(time.Second):
			apart[i] = true
		}
	}
	f.onScreen = func() { close(reading); wait(shooting, 0) }
	f.onShot = func() { close(shooting); wait(reading, 1) }
	resp, _ := call(t, s.look, map[string]any{"shot": true}, "")
	if apart[0] || apart[1] {
		t.Error("the tree read and the screenshot ran one after the other")
	}
	if !strings.Contains(resp.Text, `@3 Button "Sign in"`) || !strings.Contains(resp.Text, "\nscreenshot: ") {
		t.Errorf("text %s", resp.Text)
	}
}

// look --json builds only the JSON (the text listing goes unused); a --shot's path is in it.
func TestLookJSONOnly(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(loginScreen()...)
	addDev(s, f, "")
	resp, _ := call(t, s.look, map[string]any{"json": true, "shot": true}, "")
	if strings.Contains(resp.Text, "Sign in") {
		t.Errorf("text listing built for --json: %q", resp.Text)
	}
	var got struct {
		Elements []json.RawMessage `json:"elements"`
		Shot     string            `json:"shot"`
	}
	if err := json.Unmarshal(resp.JSON, &got); err != nil || len(got.Elements) != 3 {
		t.Fatalf("json %s (%v)", resp.JSON, err)
	}
	if _, err := os.Stat(got.Shot); err != nil {
		t.Errorf("shot %q: %v", got.Shot, err)
	}
}

// look and the end of do read past Android's node cache: agents use them to check state, and
// a label whose text changed with no accessibility event stayed stale there (issues #322,
// #331).
func TestLookAndDoReadPastNodeCache(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(label("Count 0", 100), button("Add", 200))
	d := addDev(s, f, "")
	d.Drv = &nodeCacheDriver{fakeDriver: f}
	call(t, s.look, nil, "")
	f.showSilently(label("Count 1", 100), button("Add", 200))
	if resp, _ := call(t, s.look, nil, ""); !strings.Contains(resp.Text, `"Count 1"`) {
		t.Errorf("look read the node cache:\n%s", resp.Text)
	}
	f.onType = func(string) { f.showSilently(label("Count 2", 100), button("Add", 200)) }
	if resp, _ := call(t, s.do, map[string]any{"commands": "inputText: x"}, ""); !strings.Contains(resp.Text, `"Count 2"`) {
		t.Errorf("do's last read came from the node cache:\n%s", resp.Text)
	}
}

// look --all shows which node is an open modal dialog's root and which are under it.
func TestDumpNodesModal(t *testing.T) {
	s := &ui.Screen{Windows: []*ui.Window{{Type: "app", Package: "web", Nodes: []*ui.Node{
		{Class: "body", Enabled: true},
		{Class: "button", Text: "Buy", Depth: 1, Enabled: true, UnderModal: true},
		{Class: "div[dialog]", Depth: 1, Enabled: true, Modal: true},
	}}}}
	want := `window app web
  body [0,0][0,0]
    button "Buy" [0,0][0,0] under-modal
    div[dialog] [0,0][0,0] modal
`
	if got := dumpNodes(s); got != want {
		t.Errorf("dumpNodes =\n%s\nwant\n%s", got, want)
	}
}

// A screenshot already narrow enough is returned as is without being decoded: reading its
// header is enough (decoding a full PNG costs some ms and MBs per look --shot).
func TestDownscalePNGNarrowNotDecoded(t *testing.T) {
	small := solidPNG(400, 800, color.White)
	if string(downscalePNG(small, 500)) != string(small) {
		t.Error("an image already narrow enough should be returned as is")
	}
	r := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			downscalePNG(small, 500)
		}
	})
	if n := r.AllocedBytesPerOp(); n > 64<<10 {
		t.Errorf("%d bytes allocated per call for an image already narrow enough: decoded", n)
	}
}

// The web's screenshot scale assumes a device pixel ratio of 1: headful Chrome on a Retina
// screen gives twice the pixels. look --shot brings it to 500 px wide whatever the platform.
func TestLookShotRetinaWebIs500Wide(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	s := New()
	f := newFake("chrome", "Chrome", device.Web)
	f.w, f.h = 1280, 713
	f.shotW = 1000 // 500/1280 of 2560 device pixels
	addDev(s, f, "")
	resp, _ := call(t, s.look, map[string]any{"shot": true}, "")
	p := strings.TrimSpace(resp.Text[strings.Index(resp.Text, "screenshot: ")+len("screenshot: "):])
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if w := decodePNG(t, b).Bounds().Dx(); w != 500 {
		t.Errorf("width %d, want 500", w)
	}
}

// A look whose read fails leaves no screenshot behind: it is cancelled, or removed.
func TestLookShotGoneWhenReadFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TB_HOME", home)
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.screenErr = errBoom
	addDev(s, f, "")
	if resp, _ := call(t, s.look, map[string]any{"shot": true}, ""); resp.Exit != 1 {
		t.Fatalf("resp %+v", resp)
	}
	if ents, _ := os.ReadDir(filepath.Join(home, "shots")); len(ents) != 0 {
		t.Errorf("%d screenshots left by a failed look", len(ents))
	}
}

// The shots folder keeps a session's last 20 look screenshots: older ones of that session go;
// another session's, and the screenshots asked for with tb screenshot, stay.
func TestShotsPruned(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TB_HOME", home)
	dir := filepath.Join(home, "shots")
	os.MkdirAll(dir, 0o755)
	old := time.Now().Add(-time.Hour)
	for i := 0; i < 25; i++ {
		for _, who := range []string{"look-alice", "look-bob", "alice"} {
			p := filepath.Join(dir, fmt.Sprintf("%s-1200%02d.000.png", who, i))
			os.WriteFile(p, []byte("x"), 0o644)
			at := old.Add(time.Duration(i) * time.Second)
			os.Chtimes(p, at, at)
		}
	}
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	addDev(s, f, "alice")
	call(t, s.shot, nil, "") // asked for: kept, and not counted
	resp, _ := call(t, s.look, map[string]any{"shot": true}, "")
	newest := strings.TrimSpace(resp.Text[strings.Index(resp.Text, "screenshot: ")+len("screenshot: "):])
	count := map[string][]string{}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if e.Name() == filepath.Base(newest) {
			continue
		}
		who := e.Name()[:strings.LastIndexByte(e.Name(), '-')]
		count[who] = append(count[who], e.Name())
	}
	if len(count["look-alice"]) != 19 || len(count["look-bob"]) != 25 || len(count["alice"]) != 26 {
		t.Fatalf("look-alice %d old shots, look-bob %d, alice %d: want 19 (and the new one), 25, 26",
			len(count["look-alice"]), len(count["look-bob"]), len(count["alice"]))
	}
	sort.Strings(count["look-alice"])
	if count["look-alice"][0] != "look-alice-120006.000.png" {
		t.Errorf("oldest kept %s: want the 19 newest old ones", count["look-alice"][0])
	}
	if _, err := os.Stat(newest); err != nil {
		t.Errorf("the new shot is gone: %v", err)
	}
}
