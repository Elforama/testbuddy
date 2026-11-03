package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/look"
	"github.com/elforama/testbuddy/internal/ui"
)

// ---- daemon wiring ----

// Register serves every session command over the daemon socket; ping answers the build id
// and stop ends the server.
func TestRegisterOverSocket(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	srv := daemon.NewServer(time.Hour)
	New().Register(srv)
	served := make(chan error, 1)
	go func() { served <- srv.Serve() }()

	var c net.Conn
	deadline := time.Now().Add(5 * time.Second)
	for {
		var err error
		if c, err = net.Dial("unix", daemon.SocketPath()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon did not listen: %v", err)
		}
		time.Sleep(10 * time.Millisecond) // server start-up only
	}
	defer c.Close()
	r := bufio.NewReader(c)
	send := func(req daemon.Request) daemon.Response {
		t.Helper()
		b, _ := json.Marshal(req)
		if _, err := c.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
		line, err := r.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var resp daemon.Response
		json.Unmarshal(line, &resp)
		return resp
	}

	if resp := send(daemon.Request{Cmd: "ping"}); !resp.OK || resp.Text != daemon.BuildID() {
		t.Errorf("ping = %+v", resp)
	}
	// every command is registered: with no device open they answer, rather than "unknown command"
	for _, cmd := range []string{"devices", "open", "close", "look", "do", "watch", "shot", "log", "save", "clear"} {
		resp := send(daemon.Request{Cmd: cmd, Args: json.RawMessage(`{"device":"nothing-here"}`)})
		if strings.Contains(resp.Text, "unknown command") {
			t.Errorf("%s is not registered: %+v", cmd, resp)
		}
	}
	if resp := send(daemon.Request{Cmd: "nope"}); resp.Exit != 2 || !strings.Contains(resp.Text, "unknown command") {
		t.Errorf("unknown = %+v", resp)
	}
	if resp := send(daemon.Request{Cmd: "stop"}); !resp.OK || resp.Text != "tb daemon stopped" {
		t.Errorf("stop = %+v", resp)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not stop the server")
	}
}

// Every handler rejects arguments that are not its JSON object with exit 2.
func TestHandlersRejectBadArgs(t *testing.T) {
	s := New()
	for name, h := range map[string]handler{"open": s.open, "close": s.close, "look": s.look, "shot": s.shot, "do": s.do, "save": s.save, "watch": s.watch} {
		resp, _ := call(t, h, `{"device": 3`, "")
		if resp.Exit != 2 || resp.OK {
			t.Errorf("%s: %+v", name, resp)
		}
	}
}

// ---- resolve ----

// A command finds its device by alias, serial or name; with one device open it needs none.
func TestResolve(t *testing.T) {
	s := New()
	a := addDev(s, newFake("emulator-5554", "Pixel 7", device.Android), "alice")
	ctx := context.Background()
	for _, want := range []string{"alice", "emulator-5554", "Pixel 7", "pixel 7", ""} {
		d, err := s.resolve(ctx, devArgs{Device: want})
		if err != nil || d != a {
			t.Errorf("resolve(%q) = %v, %v", want, d, err)
		}
	}
	b := addDev(s, newFake("UDID-1", "iPhone 17", device.IOS), "")
	if d, err := s.resolve(ctx, devArgs{Device: "UDID-1"}); err != nil || d != b {
		t.Errorf("resolve(UDID-1) = %v, %v", d, err)
	}
	_, err := s.resolve(ctx, devArgs{})
	if err == nil || err.Error() != "several devices are open (alice (Pixel 7), iPhone 17 UDID-1): say which with -d" {
		t.Errorf("ambiguous: %v", err)
	}
}

// Unknown devices are looked up among connected ones; with none connected the error says so.
func TestResolveNotOpen(t *testing.T) {
	s := New()
	ctx := context.Background()
	if _, err := s.resolve(ctx, devArgs{}); err == nil || !strings.Contains(err.Error(), "no device is connected") {
		t.Errorf("err = %v", err)
	}
	if _, err := s.resolve(ctx, devArgs{Device: "bob"}); err == nil || !strings.Contains(err.Error(), `no connected device matches "bob"`) {
		t.Errorf("err = %v", err)
	}
	addDev(s, newFake("emulator-5554", "Pixel 7", device.Android), "")
	// a platform with one device open still looks for a matching connected device
	if _, err := s.resolve(ctx, devArgs{Platform: "ios"}); err == nil || !strings.Contains(err.Error(), "no ios device is connected") {
		t.Errorf("err = %v", err)
	}
}

// A name outlives its connection: after the device drops, the name still means that
// device's serial (a reconnect looks for it).
func TestResolveRemembersNames(t *testing.T) {
	s := New()
	s.names["alice"] = "emulator-5554" // her connection dropped
	_, err := s.resolve(context.Background(), devArgs{Device: "alice"})
	if err == nil || !strings.Contains(err.Error(), `no connected device matches "emulator-5554"`) {
		t.Errorf("err = %v, want a lookup of alice's serial", err)
	}
}

// When the named device is connected again (unnamed), the remembered name re-attaches to it.
func TestResolveReattachesName(t *testing.T) {
	adbLists(t, "emulator-5554 Pixel_7")
	s := New()
	d := addDev(s, newFake("emulator-5554", "Pixel 7", device.Android), "")
	s.names["alice"] = "emulator-5554"
	got, err := s.resolve(context.Background(), devArgs{Device: "alice"})
	if err != nil || got != d {
		t.Fatalf("resolve = %v, %v", got, err)
	}
	if d.Name != "alice" {
		t.Errorf("name = %q, want alice", d.Name)
	}
}

// label shows the alias with the device name, or the name and serial.
func TestDevLabel(t *testing.T) {
	d := &Dev{Info: device.Info{Name: "Pixel 7", Serial: "emulator-5554"}}
	if got := d.label(); got != "Pixel 7 emulator-5554" {
		t.Errorf("label = %q", got)
	}
	d.Name = "bob"
	if got := d.label(); got != "bob (Pixel 7)" {
		t.Errorf("label = %q", got)
	}
}

// ---- devices / open / close ----

// devices lists every connected device, marking open ones and their session names.
func TestDevices(t *testing.T) {
	adbLists(t, "emulator-5554 Pixel_7", "emulator-5556 Pixel_8", "R58M Galaxy_S22")
	s := New()
	addDev(s, newFake("emulator-5554", "Pixel 7", device.Android), "alice")
	addDev(s, newFake("R58M", "Galaxy S22", device.Android), "")
	resp, _ := call(t, s.devices, nil, "")
	if !resp.OK {
		t.Fatalf("resp %+v", resp)
	}
	want := `android emulator-5554 "Pixel 7" (emulator)  session: alice
android emulator-5556 "Pixel 8" (emulator)
android R58M "Galaxy S22" (device)  connected
`
	if resp.Text != want {
		t.Errorf("text =\n%s\nwant\n%s", resp.Text, want)
	}
	var out []struct {
		Serial  string `json:"serial"`
		Session string `json:"session"`
		Open    bool   `json:"open"`
	}
	if err := json.Unmarshal(resp.JSON, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || !out[0].Open || out[0].Session != "alice" || out[1].Open || !out[2].Open || out[2].Session != "" {
		t.Errorf("json %+v", out)
	}
}

// With nothing connected, devices says how to get a device.
func TestDevicesNone(t *testing.T) {
	resp, _ := call(t, New().devices, nil, "")
	if !resp.OK || resp.Text != "no devices: start an Android emulator or boot an iOS simulator\n" || string(resp.JSON) != "null" {
		t.Errorf("resp %+v json %s", resp, resp.JSON)
	}
}

// open names an already-connected device and sets its default app.
func TestOpenNamesDevice(t *testing.T) {
	adbLists(t, "emulator-5554 Pixel_7")
	s := New()
	d := addDev(s, newFake("emulator-5554", "Pixel 7", device.Android), "")
	resp, _ := call(t, s.open, map[string]any{"device": "emulator-5554", "name": "alice", "app": "com.chat"}, "")
	if !resp.OK || !strings.HasPrefix(resp.Text, `connected alice: android emulator-5554 "Pixel 7" (emulator) in `) {
		t.Fatalf("resp %+v", resp)
	}
	if d.Name != "alice" || d.AppID != "com.chat" || s.names["alice"] != "emulator-5554" {
		t.Errorf("dev name=%q app=%q names=%v", d.Name, d.AppID, s.names)
	}
	// opening again without a name keeps it
	resp, _ = call(t, s.open, map[string]any{"device": "alice"}, "")
	if !resp.OK || !strings.HasPrefix(resp.Text, "connected alice: ") || d.AppID != "com.chat" {
		t.Errorf("reopen %+v app %q", resp, d.AppID)
	}
}

// open accepts an alias of an open device even when it is not in the device list.
func TestOpenByAlias(t *testing.T) {
	s := New()
	addDev(s, newFake("emulator-5554", "Pixel 7", device.Android), "alice")
	resp, _ := call(t, s.open, map[string]any{"device": "alice"}, "")
	if !resp.OK || !strings.HasPrefix(resp.Text, "connected alice: ") {
		t.Errorf("resp %+v", resp)
	}
}

// A name already used by another device is refused.
func TestOpenNameTaken(t *testing.T) {
	adbLists(t, "emulator-5554 Pixel_7", "emulator-5556 Pixel_8")
	s := New()
	addDev(s, newFake("emulator-5554", "Pixel 7", device.Android), "alice")
	addDev(s, newFake("emulator-5556", "Pixel 8", device.Android), "")
	resp, _ := call(t, s.open, map[string]any{"device": "emulator-5556", "name": "alice"}, "")
	if resp.Exit != 2 || resp.Text != `the name "alice" is already used by Pixel 7` {
		t.Errorf("resp %+v", resp)
	}
}

// open of a device that is not connected fails with exit 2.
func TestOpenUnknown(t *testing.T) {
	resp, _ := call(t, New().open, map[string]any{"device": "Pixel 99"}, "")
	if resp.Exit != 2 || !strings.Contains(resp.Text, `no connected device matches "Pixel 99"`) {
		t.Errorf("resp %+v", resp)
	}
}

// close drops one device, its driver and its name.
func TestCloseOne(t *testing.T) {
	s := New()
	fa, fb := newFake("emulator-5554", "Pixel 7", device.Android), newFake("UDID-1", "iPhone 17", device.IOS)
	addDev(s, fa, "alice")
	addDev(s, fb, "bob")
	resp, _ := call(t, s.close, map[string]any{"device": "alice"}, "")
	if !resp.OK || resp.Text != "closed alice (Pixel 7)\n" {
		t.Errorf("resp %+v", resp)
	}
	if fa.closed != 1 || fb.closed != 0 {
		t.Errorf("closed a=%d b=%d", fa.closed, fb.closed)
	}
	if _, ok := s.devs["emulator-5554"]; ok {
		t.Error("device still open")
	}
	if _, ok := s.names["alice"]; ok {
		t.Error("name kept after close")
	}
	if s.names["bob"] != "UDID-1" {
		t.Error("other name lost")
	}
}

// close all drops every device and every name.
func TestCloseAll(t *testing.T) {
	s := New()
	fa, fb := newFake("a", "A", device.Android), newFake("b", "B", device.IOS)
	addDev(s, fa, "alice")
	addDev(s, fb, "")
	s.names["gone"] = "c"
	resp, _ := call(t, s.close, map[string]any{"device": "all"}, "")
	if !resp.OK || resp.Text != "closed all devices\n" {
		t.Errorf("resp %+v", resp)
	}
	if fa.closed != 1 || fb.closed != 1 || len(s.devs) != 0 || len(s.names) != 0 {
		t.Errorf("closed %d %d devs %v names %v", fa.closed, fb.closed, s.devs, s.names)
	}
}

// close of an unknown device fails with exit 2.
func TestCloseUnknown(t *testing.T) {
	resp, _ := call(t, New().close, map[string]any{"device": "ghost"}, "")
	if resp.Exit != 2 {
		t.Errorf("resp %+v", resp)
	}
}

// ---- look ----

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

// ---- shot ----

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

// ---- do ----

// do runs commands, prints a line per command, then what changed; passing commands are recorded.
func TestDoRunsAndRecords(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(loginScreen()...)
	d := addDev(s, f, "")
	resp, _ := call(t, s.do, map[string]any{"commands": "- tapOn: Sign in\n- inputText: hello\n"}, t.TempDir())
	if !resp.OK || resp.Exit != 0 {
		t.Fatalf("resp %+v", resp)
	}
	lines := strings.Split(resp.Text, "\n")
	if !strings.HasPrefix(lines[0], `ok Tap on "Sign in"  `) || !strings.HasPrefix(lines[1], `ok Input text hello  `) {
		t.Errorf("text:\n%s", resp.Text)
	}
	if !strings.Contains(resp.Text, "screen now:\nandroid 400x800 com.app, 3 elements\n") {
		t.Errorf("first do should show the whole screen:\n%s", resp.Text)
	}
	if !reflect.DeepEqual(d.steps, []string{"- tapOn: Sign in", "- inputText: hello"}) {
		t.Errorf("steps %q", d.steps)
	}
	if len(f.taps) != 1 || f.taps[0] != [2]int{200, 325} {
		t.Errorf("taps %v", f.taps)
	}
	if !reflect.DeepEqual(f.typed, []string{"hello"}) {
		t.Errorf("typed %v", f.typed)
	}
	if d.lastDoEnd == nil || d.last != d.lastDoEnd {
		t.Error("the screen at the end of do should be remembered")
	}
}

// After the first do, do prints only what changed.
func TestDoPrintsDiff(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(label("Count 0", 0), button("Add", 100))
	addDev(s, f, "")
	call(t, s.look, nil, "")
	f.show(label("Count 0", 0), button("Add", 100), label("Added!", 200))
	resp, _ := call(t, s.do, map[string]any{"commands": "tapOn: Add"}, "")
	if !strings.HasSuffix(resp.Text, "+ @3 Text \"Added!\"\n") {
		t.Errorf("text:\n%s", resp.Text)
	}
	resp, _ = call(t, s.do, map[string]any{"commands": "inputText: x"}, "")
	if !strings.HasSuffix(resp.Text, "screen unchanged\n") {
		t.Errorf("text:\n%s", resp.Text)
	}
}

// A failing command is marked FAILED with its error and similar elements, exits 1, and is
// not recorded; the commands before it are.
func TestDoFailure(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(button("Sign in", 100), label("Forgot password?", 200))
	d := addDev(s, f, "")
	resp, _ := call(t, s.do, map[string]any{"commands": "- inputText: a\n- tapOn: Password reset\n- inputText: never\n"}, "")
	if resp.OK || resp.Exit != 1 {
		t.Fatalf("resp %+v", resp)
	}
	if !strings.Contains(resp.Text, "\nFAILED Tap on \"Password reset\"  ") {
		t.Errorf("text:\n%s", resp.Text)
	}
	if !strings.Contains(resp.Text, `  similar: @2 Text "Forgot password?"`) {
		t.Errorf("no similar elements:\n%s", resp.Text)
	}
	if !reflect.DeepEqual(d.steps, []string{"- inputText: a"}) {
		t.Errorf("steps %q", d.steps)
	}
	if !reflect.DeepEqual(f.typed, []string{"a"}) {
		t.Errorf("typed %v: commands after the failure ran", f.typed)
	}
}

// Optional (warned) and skipped commands are marked and still recorded.
func TestDoWarnedAndSkipped(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	d := addDev(s, f, "")
	cwd := t.TempDir()
	os.WriteFile(filepath.Join(cwd, "x.js"), []byte("output.ran = true"), 0o644)
	resp, _ := call(t, s.do, map[string]any{"commands": "- tapOn: {text: Nope, optional: true}\n- runScript:\n    file: x.js\n    when: {true: \"${false}\"}\n"}, cwd)
	if !resp.OK {
		t.Fatalf("resp %+v", resp)
	}
	if !strings.HasPrefix(resp.Text, "warned ") || !strings.Contains(resp.Text, "\nskipped ") {
		t.Errorf("text:\n%s", resp.Text)
	}
	if len(d.steps) != 2 {
		t.Errorf("steps %q", d.steps)
	}
}

// @refs from the last look become selectors, both when run and when recorded.
func TestDoResolvesRefs(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(loginScreen()...)
	d := addDev(s, f, "")
	call(t, s.look, nil, "")
	resp, _ := call(t, s.do, map[string]any{"commands": "- tapOn: @3\n- inputText: \"@3 stays in quotes\"\n"}, "")
	if !resp.OK {
		t.Fatalf("resp %+v", resp)
	}
	if !reflect.DeepEqual(d.steps, []string{`- tapOn: "Sign in"`, `- inputText: "@3 stays in quotes"`}) {
		t.Errorf("steps %q", d.steps)
	}
	if len(f.taps) != 1 || f.taps[0] != [2]int{200, 325} {
		t.Errorf("taps %v", f.taps)
	}
}

// A ref with no listed screen, or not on it, is refused with exit 2 before anything runs.
func TestDoBadRefs(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(button("OK", 0))
	addDev(s, f, "")
	resp, _ := call(t, s.do, map[string]any{"commands": "tapOn: @1"}, "")
	if resp.Exit != 2 || !strings.Contains(resp.Text, "no screen listed yet") {
		t.Errorf("no look: %+v", resp)
	}
	call(t, s.look, nil, "")
	resp, _ = call(t, s.do, map[string]any{"commands": "tapOn: @9"}, "")
	if resp.Exit != 2 || !strings.Contains(resp.Text, "@9 is not on the last listed screen") {
		t.Errorf("unknown ref: %+v", resp)
	}
	if len(f.taps) != 0 {
		t.Error("a command ran")
	}
}

// Command YAML that does not parse is refused with exit 2.
func TestDoParseError(t *testing.T) {
	s := New()
	addDev(s, newFake("e", "E", device.Android), "")
	for _, cmds := range []string{"- notACommand: x", "- tapOn: [unclosed", "- tapOn"} {
		if resp, _ := call(t, s.do, map[string]any{"commands": cmds}, ""); resp.Exit != 2 {
			t.Errorf("%q: %+v", cmds, resp)
		}
	}
	if resp, _ := call(t, s.do, map[string]any{"device": "nobody"}, ""); resp.Exit != 2 {
		t.Errorf("no device: %+v", resp)
	}
}

// The app for launchApp & co is --app (remembered), else the open app, else the foreground app.
func TestDoAppID(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	f.fg = "com.foreground"
	d := addDev(s, f, "")
	call(t, s.do, map[string]any{"commands": "launchApp"}, "")
	call(t, s.do, map[string]any{"commands": "launchApp", "app": "com.given"}, "")
	call(t, s.do, map[string]any{"commands": "launchApp"}, "")
	if !reflect.DeepEqual(f.launched, []string{"com.foreground", "com.given", "com.given"}) {
		t.Errorf("launched %v", f.launched)
	}
	if d.AppID != "com.given" {
		t.Errorf("AppID %q", d.AppID)
	}
	// no app known and no foreground: launchApp fails, and do says so
	f2 := newFake("e2", "E2", device.Android)
	f2.fgErr = errBoom
	s2 := New()
	addDev(s2, f2, "")
	resp, _ := call(t, s2.do, map[string]any{"commands": "launchApp"}, "")
	if resp.Exit != 1 || !strings.Contains(resp.Text, "no appId") {
		t.Errorf("resp %+v", resp)
	}
}

// -e values reach the commands.
func TestDoEnv(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	addDev(s, f, "")
	resp, _ := call(t, s.do, map[string]any{"commands": "inputText: ${WHO}-${N}", "env": map[string]string{"WHO": "bob", "N": "2"}}, "")
	if !resp.OK || !reflect.DeepEqual(f.typed, []string{"bob-2"}) {
		t.Errorf("resp %+v typed %v", resp, f.typed)
	}
}

// do still reports results when the screen cannot be read afterwards (no diff).
func TestDoScreenUnreadable(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	f.screenErr = errBoom
	addDev(s, f, "")
	resp, _ := call(t, s.do, map[string]any{"commands": "inputText: x"}, "")
	if !resp.OK || strings.Contains(resp.Text, "screen") {
		t.Errorf("resp %+v", resp)
	}
}

// Commands for one device run one `do` at a time.
func TestDoSerializedPerDevice(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	var mu sync.Mutex
	cur, peak := 0, 0
	f.onType = func(string) {
		mu.Lock()
		cur++
		peak = max(peak, cur)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		cur--
		mu.Unlock()
	}
	addDev(s, f, "")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			call(t, s.do, map[string]any{"commands": "inputText: x"}, "")
		}()
	}
	wg.Wait()
	if peak != 1 {
		t.Errorf("%d commands ran at once on one device", peak)
	}
}

// splitItems cuts command YAML into one snippet per top-level item; a single command
// without a dash becomes one item.
func TestSplitItems(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"- tapOn: A\n- back\n", []string{"- tapOn: A", "- back"}},
		{"- tapOn:\n    id: x\n    index: 1\n- back", []string{"- tapOn:\n    id: x\n    index: 1", "- back"}},
		{"tapOn: A", []string{"- tapOn: A"}},
		{"tapOn:\n  id: x\n", []string{"- tapOn:\n    id: x"}},
		{"back", []string{"- back"}},
		{"-\n  tapOn: A\n- back", []string{"-\n  tapOn: A", "- back"}},
	}
	for _, c := range cases {
		got, err := splitItems(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitItems(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

// An indented command list (valid YAML that `tb do -` passes through) is recorded item by item.
func TestSplitItemsIndentedList(t *testing.T) {
	got, _ := splitItems("  - tapOn: A\n  - back\n")
	if len(got) != 2 {
		t.Errorf("splitItems = %q, want 2 items", got)
	}
}

// Text before the first list item (a comment, a blank line) does not shift which YAML is
// recorded for which command.
func TestDoRecordsWithLeadingComment(t *testing.T) {
	s := New()
	addDev(s, newFake("e", "E", device.Android), "")
	call(t, s.do, map[string]any{"commands": "# sign in\n- inputText: a\n- inputText: b\n"}, "")
	if resp, _ := call(t, s.log, nil, ""); resp.Text != "- inputText: a\n- inputText: b\n" {
		t.Errorf("log %q", resp.Text)
	}
}

// fmtDur prints milliseconds under a second, else tenths of seconds.
func TestFmtDur(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "0ms", 12 * time.Millisecond: "12ms", 999 * time.Millisecond: "999ms", time.Second: "1.0s", 2345 * time.Millisecond: "2.3s"} {
		if got := fmtDur(d); got != want {
			t.Errorf("fmtDur(%s) = %s, want %s", d, got, want)
		}
	}
}

// fail builds an exit response with a formatted message.
func TestFail(t *testing.T) {
	r := fail(3, "x=%d %s", 7, "y")
	if r.OK || r.Exit != 3 || r.Text != "x=7 y" {
		t.Errorf("fail = %+v", r)
	}
}

// ---- recording: log / save / clear ----

// log prints the recorded commands; clear forgets them.
func TestLogAndClear(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	addDev(s, f, "")
	if resp, _ := call(t, s.log, nil, ""); resp.Text != "no commands recorded yet\n" {
		t.Errorf("empty log %+v", resp)
	}
	call(t, s.do, map[string]any{"commands": "- inputText: a\n- inputText: b"}, "")
	if resp, _ := call(t, s.log, nil, ""); resp.Text != "- inputText: a\n- inputText: b\n" {
		t.Errorf("log %q", resp.Text)
	}
	if resp, _ := call(t, s.clear, nil, ""); !resp.OK || resp.Text != "recording cleared\n" {
		t.Errorf("clear %+v", resp)
	}
	if resp, _ := call(t, s.log, nil, ""); resp.Text != "no commands recorded yet\n" {
		t.Errorf("log after clear %+v", resp)
	}
	for name, h := range map[string]handler{"log": s.log, "clear": s.clear} {
		if resp, _ := call(t, h, map[string]any{"device": "nobody"}, ""); resp.Exit != 2 {
			t.Errorf("%s without device: %+v", name, resp)
		}
	}
}

// Recordings are per device.
func TestRecordingPerDevice(t *testing.T) {
	s := New()
	addDev(s, newFake("a", "A", device.Android), "alice")
	addDev(s, newFake("b", "B", device.Android), "bob")
	call(t, s.do, map[string]any{"device": "alice", "commands": "inputText: from-alice"}, "")
	call(t, s.do, map[string]any{"device": "bob", "commands": "inputText: from-bob"}, "")
	if resp, _ := call(t, s.log, map[string]any{"device": "alice"}, ""); resp.Text != "- inputText: from-alice\n" {
		t.Errorf("alice log %q", resp.Text)
	}
	if resp, _ := call(t, s.log, map[string]any{"device": "bob"}, ""); resp.Text != "- inputText: from-bob\n" {
		t.Errorf("bob log %q", resp.Text)
	}
}

// save writes the passing commands as a flow that parses back to the same commands.
func TestSaveWritesFlow(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	f.show(loginScreen()...)
	addDev(s, f, "")
	call(t, s.look, nil, "")
	call(t, s.do, map[string]any{"commands": "- tapOn: @3\n- inputText: hi\n- tapOn:\n    id: email\n- assertVisible: Missing\n", "app": "com.chat"}, "")
	cwd := t.TempDir()
	resp, _ := call(t, s.save, map[string]any{"path": "flows/sign-in.yaml"}, cwd)
	path := filepath.Join(cwd, "flows", "sign-in.yaml")
	if !resp.OK || resp.Text != "saved 3 commands to "+path+"\n" {
		t.Fatalf("resp %+v", resp)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "appId: com.chat\n---\n- tapOn: \"Sign in\"\n- inputText: hi\n- tapOn:\n    id: email\n"
	if string(b) != want {
		t.Errorf("file =\n%s\nwant\n%s", b, want)
	}
	fl, err := flow.ParseFile(path)
	if err != nil {
		t.Fatalf("saved flow does not parse: %v", err)
	}
	if fl.Config.AppID != "com.chat" || len(fl.Commands) != 3 || fl.Commands[2].Selector.ID != "email" {
		t.Errorf("parsed %+v", fl.Config)
	}
}

// save's app id: --app, else the device's app, else the foreground app.
func TestSaveAppID(t *testing.T) {
	s := New()
	f := newFake("e", "E", device.Android)
	f.fg = "com.fg"
	d := addDev(s, f, "")
	dir := t.TempDir()
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		first, _, _ := strings.Cut(string(b), "\n")
		return first
	}
	call(t, s.save, map[string]any{"path": filepath.Join(dir, "a.yaml")}, "")
	d.AppID = "com.dev"
	call(t, s.save, map[string]any{"path": filepath.Join(dir, "b.yaml")}, "")
	call(t, s.save, map[string]any{"path": filepath.Join(dir, "c.yaml"), "app": "com.arg"}, "")
	got := []string{read("a.yaml"), read("b.yaml"), read("c.yaml")}
	if !reflect.DeepEqual(got, []string{"appId: com.fg", "appId: com.dev", "appId: com.arg"}) {
		t.Errorf("app ids %q", got)
	}
}

// save fails with exit 1 when the file cannot be written, exit 2 without a device.
func TestSaveErrors(t *testing.T) {
	s := New()
	if resp, _ := call(t, s.save, map[string]any{"path": "x.yaml"}, t.TempDir()); resp.Exit != 2 {
		t.Errorf("no device: %+v", resp)
	}
	addDev(s, newFake("e", "E", device.Android), "")
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	os.WriteFile(file, nil, 0o644)
	if resp, _ := call(t, s.save, map[string]any{"path": filepath.Join(file, "sub", "x.yaml")}, ""); resp.Exit != 1 {
		t.Errorf("folder is a file: %+v", resp)
	}
	os.Mkdir(filepath.Join(dir, "taken.yaml"), 0o755)
	if resp, _ := call(t, s.save, map[string]any{"path": filepath.Join(dir, "taken.yaml")}, ""); resp.Exit != 1 {
		t.Errorf("path is a folder: %+v", resp)
	}
}

// ---- dumpNodes ----

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
