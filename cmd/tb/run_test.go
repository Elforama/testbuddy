package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/connect"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// fakeApp is a two-screen app on a fake Android device: a Home screen with a "Next" button,
// and a Details screen with a "Back" button. tb test runs real flows against it.
type fakeApp struct {
	serial string

	mu       sync.Mutex
	screen   string // "home" or "details"
	seq      int64
	launched []string
	typed    []string
	closed   bool
	subs     []chan driver.Event
}

func (f *fakeApp) nodes() []*ui.Node {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.screen == "details" {
		return []*ui.Node{
			{Text: "Details", Bounds: ui.Rect{L: 0, T: 100, R: 1080, B: 200}, Enabled: true},
			{Text: "Back", ID: "back", Bounds: ui.Rect{L: 0, T: 300, R: 1080, B: 400}, Enabled: true, Clickable: true},
		}
	}
	return []*ui.Node{
		{Text: "Home", Bounds: ui.Rect{L: 0, T: 100, R: 1080, B: 200}, Enabled: true},
		{Text: "Next", ID: "next", Bounds: ui.Rect{L: 0, T: 300, R: 1080, B: 400}, Enabled: true, Clickable: true},
	}
}

func (f *fakeApp) changed() {
	f.mu.Lock()
	f.seq++
	seq, subs := f.seq, append([]chan driver.Event(nil), f.subs...)
	f.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- driver.Event{Kind: "changed", Seq: seq}:
		default:
		}
		select {
		case ch <- driver.Event{Kind: "settled", Seq: seq}:
		default:
		}
	}
}

func (f *fakeApp) Platform() string { return "android" }
func (f *fakeApp) Serial() string   { return f.serial }
func (f *fakeApp) Name() string     { return "Fake phone" }
func (f *fakeApp) Size() (int, int) { return 1080, 2400 }
func (f *fakeApp) Seq() int64       { f.mu.Lock(); defer f.mu.Unlock(); return f.seq }
func (f *fakeApp) Screen(ctx context.Context) (*ui.Screen, error) {
	w := &ui.Window{Type: "app", Package: "com.example.app", Active: true, Focused: true,
		Bounds: ui.Rect{R: 1080, B: 2400}, Nodes: f.nodes()}
	s := &ui.Screen{Platform: "android", Width: 1080, Height: 2400, Seq: f.Seq(), Windows: []*ui.Window{w}}
	s.Link()
	return s, nil
}
func (f *fakeApp) Events() (<-chan driver.Event, func()) {
	ch := make(chan driver.Event, 64)
	f.mu.Lock()
	f.subs = append(f.subs, ch)
	f.mu.Unlock()
	return ch, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for i, c := range f.subs {
			if c == ch {
				f.subs = append(f.subs[:i], f.subs[i+1:]...)
				break
			}
		}
	}
}
func (f *fakeApp) WaitSettled(ctx context.Context, t time.Duration) error { return nil }
func (f *fakeApp) Settle(ctx context.Context, since int64, n int, t time.Duration) (driver.SettleResult, error) {
	return driver.Changed, nil
}
func (f *fakeApp) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	if y >= 300 && y < 400 {
		f.mu.Lock()
		if f.screen == "details" {
			f.screen = "home"
		} else {
			f.screen = "details"
		}
		f.mu.Unlock()
		f.changed()
		return driver.ActionResult{Settle: driver.Changed, Seq: f.Seq()}, nil
	}
	return driver.ActionResult{Settle: driver.NoChange, Seq: f.Seq()}, nil
}
func (f *fakeApp) DoubleTap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	return f.Tap(ctx, x, y, o)
}
func (f *fakeApp) LongPress(ctx context.Context, x, y int, h time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	return f.Tap(ctx, x, y, o)
}
func (f *fakeApp) Swipe(ctx context.Context, x1, y1, x2, y2 int, d time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	return driver.ActionResult{Settle: driver.NoChange}, nil
}
func (f *fakeApp) TypeText(ctx context.Context, t string, o driver.ActionOpts) (driver.ActionResult, error) {
	f.mu.Lock()
	f.typed = append(f.typed, t)
	f.mu.Unlock()
	return driver.ActionResult{Settle: driver.NoChange}, nil
}
func (f *fakeApp) EraseText(ctx context.Context, n int, o driver.ActionOpts) (driver.ActionResult, error) {
	return driver.ActionResult{Settle: driver.NoChange}, nil
}
func (f *fakeApp) PressKey(ctx context.Context, k string, o driver.ActionOpts) (driver.ActionResult, error) {
	return driver.ActionResult{Settle: driver.NoChange}, nil
}
func (f *fakeApp) HideKeyboard(ctx context.Context) error { return nil }
func (f *fakeApp) Screenshot(ctx context.Context, s float64, fm string) ([]byte, error) {
	return []byte("\x89PNG fake"), nil
}
func (f *fakeApp) LaunchApp(ctx context.Context, a string, o driver.LaunchOpts) error {
	f.mu.Lock()
	f.launched = append(f.launched, a)
	f.screen = "home"
	f.mu.Unlock()
	f.changed()
	return nil
}
func (f *fakeApp) StopApp(ctx context.Context, a string) error                { return nil }
func (f *fakeApp) KillApp(ctx context.Context, a string) error                { return nil }
func (f *fakeApp) ClearState(ctx context.Context, a string) error             { return nil }
func (f *fakeApp) ClearKeychain(ctx context.Context) error                    { return nil }
func (f *fakeApp) OpenLink(ctx context.Context, l, a string, v, b bool) error { return nil }
func (f *fakeApp) SetPermissions(ctx context.Context, a string, p map[string]string) error {
	return nil
}
func (f *fakeApp) SetLocation(ctx context.Context, la, lo float64) error { return nil }
func (f *fakeApp) SetOrientation(ctx context.Context, o string) error    { return nil }
func (f *fakeApp) SetAirplaneMode(ctx context.Context, on bool) error    { return nil }
func (f *fakeApp) ToggleAirplaneMode(ctx context.Context) error          { return nil }
func (f *fakeApp) AddMedia(ctx context.Context, p []string) error        { return nil }
func (f *fakeApp) InstallApp(ctx context.Context, p string) error        { return nil }
func (f *fakeApp) Foreground(ctx context.Context) (string, error)        { return "com.example.app", nil }
func (f *fakeApp) WaitForApp(ctx context.Context, a string, t time.Duration) error {
	return nil
}
func (f *fakeApp) StartRecording(ctx context.Context, p string) error { return nil }
func (f *fakeApp) StopRecording(ctx context.Context) error            { return nil }
func (f *fakeApp) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// withFakePhone makes tb see one Android device, served by a fakeApp.
func withFakePhone(t *testing.T) *fakeApp {
	t.Helper()
	app := &fakeApp{serial: "emulator-5554", screen: "home"}
	info := device.Info{Platform: device.Android, Serial: app.serial, Name: "Fake phone", Emulator: true, State: "device"}
	oldList := listDevices
	listDevices = func(ctx context.Context) []device.Info { return []device.Info{info} }
	restore := connect.Override(device.Android, func(ctx context.Context, i device.Info) (driver.Driver, error) {
		return app, nil
	})
	t.Cleanup(func() { listDevices = oldList; restore() })
	return app
}

func writeFlow(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const passingFlow = `appId: com.example.app
name: Go to details and back
---
- launchApp
- assertVisible: Home
- tapOn: Next
- assertVisible: Details
- tapOn: {id: back}
- assertVisible: Home
`

const failingFlow = `appId: com.example.app
name: Looks for a missing button
---
- launchApp
- extendedWaitUntil: {visible: Checkout, timeout: 300}
`

// tb test runs a passing flow end to end: exit 0, a JSON summary with the flow, and the app
// launched; the device is closed when the run ends.
func TestCmdTestPasses(t *testing.T) {
	app := withFakePhone(t)
	dir := t.TempDir()
	flow := writeFlow(t, dir, "pass.yaml", passingFlow)
	var code int
	out, errOut := capture(t, func() {
		code = cmdTest([]string{flow, "--json", "--debug-output", filepath.Join(dir, "debug")})
	})
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	var sum struct {
		Passed, Failed int
		Flows          []struct {
			Name, Device, Status string
			Seconds              float64
		}
	}
	if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &sum); err != nil {
		t.Fatalf("summary %q: %v", out, err)
	}
	if sum.Passed != 1 || sum.Failed != 0 || len(sum.Flows) != 1 || sum.Flows[0].Status != "SUCCESS" ||
		sum.Flows[0].Name != "Go to details and back" || sum.Flows[0].Device != "Fake phone" {
		t.Errorf("summary %+v", sum)
	}
	if len(app.launched) != 1 || app.launched[0] != "com.example.app" {
		t.Errorf("launched %v", app.launched)
	}
	if !app.closed {
		t.Error("the device was not closed when the run ended")
	}
}

// A failing flow: exit 1, a JUnit report naming the failure, and the failure's files (the
// error, the screen as text, a screenshot, the hierarchy) next to its log.
func TestCmdTestFailureArtifacts(t *testing.T) {
	withFakePhone(t)
	dir := t.TempDir()
	flow := writeFlow(t, dir, "fail.yaml", failingFlow)
	report := filepath.Join(dir, "report.xml")
	debug := filepath.Join(dir, "debug")
	var code int
	out, errOut := capture(t, func() {
		code = cmdTest([]string{flow, "--format", "junit", "--output", report, "--debug-output", debug,
			"--flatten-debug-output"})
	})
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out+errOut, "Checkout") {
		t.Errorf("the output does not say what was not found:\n%s%s", out, errOut)
	}
	b, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var suites struct {
		Suites []struct {
			Tests    int `xml:"tests,attr"`
			Failures int `xml:"failures,attr"`
			Cases    []struct {
				Name    string `xml:"name,attr"`
				Failure *struct {
					Text string `xml:",chardata"`
				} `xml:"failure"`
			} `xml:"testcase"`
		} `xml:"testsuite"`
	}
	if err := xml.Unmarshal(b, &suites); err != nil {
		t.Fatalf("report: %v\n%s", err, b)
	}
	if len(suites.Suites) != 1 || suites.Suites[0].Failures != 1 || len(suites.Suites[0].Cases) != 1 ||
		suites.Suites[0].Cases[0].Failure == nil || !strings.Contains(suites.Suites[0].Cases[0].Failure.Text, "Checkout") {
		t.Errorf("report:\n%s", b)
	}
	var found []string
	filepath.Walk(debug, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			found = append(found, filepath.Base(p))
		}
		return nil
	})
	for _, want := range []string{"log.txt", "error.txt", "screen.txt", "screenshot.png", "hierarchy.json"} {
		ok := false
		for _, f := range found {
			if f == want {
				ok = true
			}
		}
		if !ok {
			t.Errorf("no %s in the debug output (%v)", want, found)
		}
	}
}

// No device of the platform asked for: exit with a message, no run.
func TestCmdTestNoDevice(t *testing.T) {
	withFakePhone(t)
	dir := t.TempDir()
	flow := writeFlow(t, dir, "pass.yaml", passingFlow)
	var code int
	out, errOut := capture(t, func() { code = cmdTest([]string{flow, "-p", "ios"}) })
	if code == 0 || !strings.Contains(strings.ToLower(out+errOut), "ios") {
		t.Errorf("exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
}

// --wait-for-device takes "5m", "90s" or plain seconds; anything else is refused.
func TestParseWait(t *testing.T) {
	for in, want := range map[string]time.Duration{"5m": 5 * time.Minute, "90s": 90 * time.Second, "30": 30 * time.Second, " 2 ": 2 * time.Second, "0": 0} {
		if got, err := parseWait(in); err != nil || got != want {
			t.Errorf("parseWait(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"soon", "-5", "-1m", ""} {
		if _, err := parseWait(in); err == nil {
			t.Errorf("parseWait(%q) accepted", in)
		}
	}
}

// tb devices lists what it finds, as text or JSON.
func TestCmdDevicesLists(t *testing.T) {
	withFakePhone(t)
	var code int
	out, _ := capture(t, func() { code = cmdDevices(nil) })
	if code != 0 || !strings.Contains(out, "emulator-5554") {
		t.Errorf("text: exit %d, %q", code, out)
	}
	out, _ = capture(t, func() { code = cmdDevices([]string{"--json"}) })
	var devs []device.Info
	if code != 0 || json.Unmarshal([]byte(out), &devs) != nil || len(devs) != 1 || devs[0].Serial != "emulator-5554" {
		t.Errorf("json: exit %d, %q", code, out)
	}
	listDevices = func(ctx context.Context) []device.Info { return nil }
	out, _ = capture(t, func() { code = cmdDevices(nil) })
	if code != 0 || !strings.Contains(out, "no devices") {
		t.Errorf("none: exit %d, %q", code, out)
	}
}

// tb hierarchy prints the screen outline of a device, or its raw nodes as JSON.
func TestCmdHierarchy(t *testing.T) {
	withFakePhone(t)
	var code int
	out, errOut := capture(t, func() { code = cmdHierarchy(nil) })
	if code != 0 || !strings.Contains(out, `"Next" #next`) || !strings.Contains(out, `"Home"`) {
		t.Errorf("outline: exit %d\n%s%s", code, out, errOut)
	}
	out, _ = capture(t, func() { code = cmdHierarchy([]string{"--json"}) })
	if code != 0 || !strings.Contains(out, `"Next"`) || !json.Valid([]byte(out)) {
		t.Errorf("json: exit %d\n%s", code, out)
	}
	out, errOut = capture(t, func() { code = cmdHierarchy([]string{"-d", "nope"}) })
	if code != 2 || errOut == "" {
		t.Errorf("unknown device: exit %d, %q", code, errOut)
	}
}
