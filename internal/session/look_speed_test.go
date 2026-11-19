package session

import (
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
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

// A screenshot already narrow enough is returned as is without being decoded: reading its
// header is enough (decoding a full PNG costs some ms and MBs per look --shot).
func TestDownscalePNGNarrowNotDecoded(t *testing.T) {
	small := solidPNG(400, 800, color.White)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	out := downscalePNG(small, 500)
	runtime.ReadMemStats(&after)
	if string(out) != string(small) {
		t.Error("an image already narrow enough should be returned as is")
	}
	if n := after.TotalAlloc - before.TotalAlloc; n > 64<<10 {
		t.Errorf("%d bytes allocated for an image already narrow enough: decoded", n)
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

// The shots folder keeps a session's last 20 screenshots: older ones of that session go,
// another session's stay.
func TestShotsPruned(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TB_HOME", home)
	dir := filepath.Join(home, "shots")
	os.MkdirAll(dir, 0o755)
	old := time.Now().Add(-time.Hour)
	for i := 0; i < 25; i++ {
		for _, who := range []string{"alice", "bob"} {
			p := filepath.Join(dir, fmt.Sprintf("%s-1200%02d.000.png", who, i))
			os.WriteFile(p, []byte("x"), 0o644)
			at := old.Add(time.Duration(i) * time.Second)
			os.Chtimes(p, at, at)
		}
	}
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	addDev(s, f, "alice")
	resp, _ := call(t, s.look, map[string]any{"shot": true}, "")
	newest := strings.TrimSpace(resp.Text[strings.Index(resp.Text, "screenshot: ")+len("screenshot: "):])
	var alice, bob []string
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		switch {
		case e.Name() == filepath.Base(newest):
		case strings.HasPrefix(e.Name(), "alice-"):
			alice = append(alice, e.Name())
		case strings.HasPrefix(e.Name(), "bob-"):
			bob = append(bob, e.Name())
		}
	}
	sort.Strings(alice)
	if len(alice) != 19 || len(bob) != 25 {
		t.Fatalf("alice %d old shots, bob %d: want 19 (and the new one) and 25", len(alice), len(bob))
	}
	if alice[0] != "alice-120006.000.png" {
		t.Errorf("oldest kept %s: want the 19 newest old ones", alice[0])
	}
	if _, err := os.Stat(newest); err != nil {
		t.Errorf("the new shot is gone: %v", err)
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
