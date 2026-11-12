package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/orchestrate"
	"github.com/elforama/testbuddy/internal/ui"
)

func plainConsole(quiet, prefix bool) (*console, *bytes.Buffer) {
	var b bytes.Buffer
	return newConsole(&b, quiet, prefix), &b
}

// Output that is not a terminal gets no color; a terminal does unless NO_COLOR is set.
func TestConsoleColorDetection(t *testing.T) {
	c, _ := plainConsole(false, false)
	if c.color {
		t.Error("buffer console is colored")
	}
	f, err := os.Create(t.TempDir() + "/out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if newConsole(f, false, false).color {
		t.Error("regular file console is colored")
	}
	tty, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Skip("no null device")
	}
	defer tty.Close()
	t.Setenv("NO_COLOR", "")
	if !newConsole(tty, false, false).color {
		t.Error("character device console is not colored")
	}
	t.Setenv("NO_COLOR", "1")
	if newConsole(tty, false, false).color {
		t.Error("NO_COLOR ignored")
	}
}

// paint wraps text in an ANSI code only when color is on.
func TestPaint(t *testing.T) {
	c, _ := plainConsole(false, false)
	if c.paint("31", "x") != "x" {
		t.Error("paint without color changed the text")
	}
	c.color = true
	if got := c.paint("31", "x"); got != "\x1b[31mx\x1b[0m" {
		t.Errorf("paint = %q", got)
	}
}

// The reporter prints one line per finished command: mark, description, time, indented by depth.
func TestReporterLines(t *testing.T) {
	cases := []struct {
		name string
		ev   engine.Event
		want string
	}{
		{"passed", engine.Event{Desc: "tapOn: Login", Status: engine.Passed, Elapsed: 12 * time.Millisecond}, "  ✓ tapOn: Login  12ms\n"},
		{"nested", engine.Event{Desc: "back", Status: engine.Passed, Depth: 2, Elapsed: 1500 * time.Millisecond}, "      ✓ back  1.5s\n"},
		{"warned shows its error", engine.Event{Desc: "tapOn: X", Status: engine.Warned, Err: errors.New("optional: not found")},
			"  ⚠ tapOn: X  0ms\n      optional: not found\n"},
		{"skipped", engine.Event{Desc: "runFlow: a.yaml", Status: engine.Skipped}, "  ○ runFlow: a.yaml  0ms\n"},
		{"failed shows each error line", engine.Event{Desc: "assertVisible: Hi", Status: engine.Failed, Depth: 1,
			Err: errors.New("  not found\nscreen changed  \n")}, "    ✗ assertVisible: Hi  0ms\n        not found\n        screen changed\n"},
		{"passed hides its error", engine.Event{Desc: "x", Status: engine.Passed, Err: errors.New("ignored")}, "  ✓ x  0ms\n"},
		{"start events print nothing", engine.Event{Desc: "x", Start: true}, ""},
		{"log lines", engine.Event{Log: "hello from js"}, "    log: hello from js\n"},
		// issue #213: a hint goes under its step (Depth is the step's plus one), where its
		// error would.
		{"hint under a top-level step", engine.Event{Hint: "back does nothing", Depth: 1}, "      hint: back does nothing\n"},
		{"hint under a nested step", engine.Event{Hint: "back does nothing", Depth: 2}, "        hint: back does nothing\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			con, out := plainConsole(false, false)
			con.reporter("flow", "Pixel").Event(c.ev)
			if out.String() != c.want {
				t.Errorf("got %q, want %q", out.String(), c.want)
			}
		})
	}
}

// With several devices, lines carry the device (or the event's own role) in brackets.
func TestReporterDeviceTags(t *testing.T) {
	con, out := plainConsole(false, true)
	r := con.reporter("flow", "Pixel 9")
	r.Event(engine.Event{Desc: "a", Status: engine.Passed})
	r.Event(engine.Event{Desc: "b", Status: engine.Passed, Device: "alice"})
	r.Event(engine.Event{Log: "hi", Device: "bob"})
	want := "[Pixel 9]   ✓ a  0ms\n[alice]   ✓ b  0ms\n[bob]     log: hi\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
	// Without prefix mode, an event's device still tags it (multi-device flows).
	con2, out2 := plainConsole(false, false)
	con2.reporter("flow", "Pixel").Event(engine.Event{Desc: "c", Status: engine.Failed, Device: "alice", Err: errors.New("e")})
	if out2.String() != "[alice]   ✗ c  0ms\n[alice]       e\n" {
		t.Errorf("got %q", out2.String())
	}
}

// Quiet consoles print nothing from the reporter, not even logs.
func TestReporterQuiet(t *testing.T) {
	con, out := plainConsole(true, true)
	r := con.reporter("flow", "Pixel")
	r.Event(engine.Event{Desc: "a", Status: engine.Failed, Err: errors.New("x")})
	r.Event(engine.Event{Log: "log"})
	r.Event(engine.Event{Hint: "hint", Depth: 1})
	if out.Len() != 0 {
		t.Errorf("quiet reporter printed %q", out.String())
	}
}

// Colored reporter lines paint the mark, the time and the tag.
func TestReporterColor(t *testing.T) {
	con, out := plainConsole(false, true)
	con.color = true
	con.reporter("f", "dev").Event(engine.Event{Desc: "a", Status: engine.Failed, Elapsed: time.Millisecond})
	want := "\x1b[2m[dev] \x1b[0m  \x1b[31m✗\x1b[0m a  \x1b[2m1ms\x1b[0m\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

func named(name string, attempt int) *orchestrate.Job { j := job(name, "", 0, attempt); return j }

// flowStart announces a flow and its device, noting retries.
func TestFlowStart(t *testing.T) {
	con, out := plainConsole(false, false)
	con.flowStart(named("Login", 0), "Pixel")
	con.flowStart(named("Login", 2), "alice=iPhone, bob=Pixel")
	want := "▶ Login on Pixel\n▶ Login on alice=iPhone, bob=Pixel (retry 2)\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
	q, qout := plainConsole(true, false)
	q.flowStart(named("Login", 0), "Pixel")
	if qout.Len() != 0 {
		t.Errorf("quiet flowStart printed %q", qout.String())
	}
}

func result(name string, st engine.Status, err error, d time.Duration) orchestrate.Result {
	return orchestrate.Result{Job: named(name, 0), Status: st, Err: err, Duration: d,
		Devices: map[string]device.Info{"": {Name: "Pixel"}}}
}

// flowEnd prints a failure with its error lines and debug dir, a skip, or a pass.
func TestFlowEnd(t *testing.T) {
	cases := []struct {
		name string
		r    orchestrate.Result
		want string
	}{
		{"failed", result("Login", engine.Failed, errors.New("line1\n line2\n"), 1500*time.Millisecond),
			"✗ Login failed in 1.5s on Pixel\n  line1\n   line2\n  debug: /dbg/Login (log.txt: what tb did, step by step)\n"},
		{"failed without error", result("Login", engine.Failed, nil, time.Second), "✗ Login failed in 1.0s on Pixel\n  debug: /dbg/Login (log.txt: what tb did, step by step)\n"},
		{"skipped", result("Login", engine.Skipped, nil, 0), "○ Login skipped\n"},
		{"passed", result("Login", engine.Passed, nil, 20*time.Millisecond), "✓ Login passed in 20ms\n"},
		{"warned counts as passed", result("Login", engine.Warned, nil, 20*time.Millisecond), "✓ Login passed in 20ms\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			con, out := plainConsole(false, false)
			con.flowEnd(c.r, "/dbg/Login")
			if out.String() != c.want {
				t.Errorf("got %q, want %q", out.String(), c.want)
			}
		})
	}
	// Multi-device results name every role.
	con, out := plainConsole(false, false)
	r := result("Chat", engine.Failed, errors.New("x"), time.Second)
	r.Devices = map[string]device.Info{"bob": {Name: "Pixel"}, "alice": {Name: "iPhone"}}
	con.flowEnd(r, "/d")
	if !strings.HasPrefix(out.String(), "✗ Chat failed in 1.0s on alice=iPhone, bob=Pixel\n") {
		t.Errorf("got %q", out.String())
	}
}

// Quiet mode (the --quiet and --json runs) still prints failures, but not passes.
func TestFlowEndQuiet(t *testing.T) {
	con, out := plainConsole(true, false)
	con.flowEnd(result("A", engine.Passed, nil, 0), "/d")
	if out.Len() != 0 {
		t.Errorf("quiet pass printed %q", out.String())
	}
	con.flowEnd(result("B", engine.Failed, errors.New("boom"), 0), "/d")
	if !strings.Contains(out.String(), "✗ B failed") || !strings.Contains(out.String(), "boom") {
		t.Errorf("quiet failure = %q", out.String())
	}
}

// --quiet prints "only failures and the summary", so skipped flows stay silent too.
func TestFlowEndQuietSkipped(t *testing.T) {
	con, out := plainConsole(true, false)
	con.flowEnd(result("S", engine.Skipped, nil, 0), "/d")
	if out.Len() != 0 {
		t.Errorf("quiet skipped flow printed %q", out.String())
	}
}

// With --json, stdout is only the JSON: the console writes to stderr instead.
func TestConsoleWriterJSON(t *testing.T) {
	if consoleWriter(true) != os.Stderr || consoleWriter(false) != os.Stdout {
		t.Error("--json console must go to stderr, otherwise stdout")
	}
}

func notFoundScreen() *ui.Screen {
	nodes := []*ui.Node{
		{Class: "android.widget.FrameLayout", Bounds: ui.Rect{R: 400, B: 800}, Depth: 0},
		{Class: "android.widget.Button", Text: "Login now", Bounds: ui.Rect{L: 10, T: 100, R: 200, B: 150}, Clickable: true, Enabled: true, Visible: true, Depth: 1},
		{Class: "android.widget.TextView", Text: "Welcome", Bounds: ui.Rect{L: 10, T: 10, R: 200, B: 50}, Enabled: true, Visible: true, Depth: 1},
	}
	s := &ui.Screen{Platform: "android", Width: 400, Height: 800, Windows: []*ui.Window{
		{Type: "app", Package: "com.app", Active: true, Bounds: ui.Rect{R: 400, B: 800}, Nodes: nodes},
	}}
	s.Link()
	return s
}

// A not-found failure also prints similar elements and the screen it looked at.
func TestFlowEndNotFoundScreen(t *testing.T) {
	con, out := plainConsole(false, false)
	nf := &engine.NotFound{FlowError: engine.FlowError{Msg: `Element not found: "Login"`}, Screen: notFoundScreen(), Query: "Login"}
	con.flowEnd(result("F", engine.Failed, fmt.Errorf("step 3: %w", nf), time.Second), "/dbg")
	got := out.String()
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if lines[0] != "✗ F failed in 1.0s on Pixel" || lines[1] != `  step 3: Element not found: "Login"` {
		t.Fatalf("head = %q", lines[:2])
	}
	if !regexp.MustCompile(`(?m)^  similar: .*"Login now"`).MatchString(got) {
		t.Errorf("no similar line in %q", got)
	}
	if strings.Contains(got, "similar: @") {
		t.Errorf("similar elements keep their @ref: %q", got)
	}
	i := strings.Index(got, "  screen was:\n")
	if i < 0 {
		t.Fatalf("no screen in %q", got)
	}
	screen := got[i+len("  screen was:\n") : strings.Index(got, "  debug: ")]
	if !strings.HasPrefix(screen, "    android 400x800 com.app,") || !strings.Contains(screen, `"Welcome"`) {
		t.Errorf("screen = %q", screen)
	}
	for _, l := range strings.Split(strings.TrimSuffix(screen, "\n"), "\n") {
		if !strings.HasPrefix(l, "    ") {
			t.Errorf("screen line not indented: %q", l)
		}
	}
	if !strings.HasSuffix(got, "  debug: /dbg (log.txt: what tb did, step by step)\n") {
		t.Errorf("debug line missing: %q", got)
	}
}

// Without a query (or with no similar labels) the similar line is left out.
func TestPrintScreenNoSimilar(t *testing.T) {
	for _, q := range []string{"", "zzzzzz"} {
		con, out := plainConsole(false, false)
		con.printScreen(notFoundScreen(), q)
		if strings.Contains(out.String(), "similar:") {
			t.Errorf("query %q printed a similar line: %q", q, out.String())
		}
		if !strings.HasPrefix(out.String(), "  screen was:\n") {
			t.Errorf("query %q: %q", q, out.String())
		}
	}
}

// summary counts passes (including warnings), failures and skips, with the run's time.
func TestSummary(t *testing.T) {
	cases := []struct {
		statuses []engine.Status
		want     string
	}{
		{nil, `^\n0 passed in \d+ms\n$`},
		{[]engine.Status{engine.Passed, engine.Warned}, `^\n2 passed in \d+ms\n$`},
		{[]engine.Status{engine.Passed, engine.Failed, engine.Failed}, `^\n1 passed, 2 failed in \d+ms\n$`},
		{[]engine.Status{engine.Skipped, engine.Passed}, `^\n1 passed, 1 skipped in \d+ms\n$`},
		{[]engine.Status{engine.Failed, engine.Skipped, engine.Passed, engine.Warned}, `^\n2 passed, 1 failed, 1 skipped in \d+ms\n$`},
	}
	for _, c := range cases {
		con, out := plainConsole(true, false) // the summary prints even when quiet
		var rs []orchestrate.Result
		for _, st := range c.statuses {
			rs = append(rs, orchestrate.Result{Status: st})
		}
		con.summary(rs)
		if !regexp.MustCompile(c.want).MatchString(out.String()) {
			t.Errorf("summary(%v) = %q", c.statuses, out.String())
		}
	}
	// Failures are painted red when color is on.
	con, out := plainConsole(false, false)
	con.color = true
	con.summary([]orchestrate.Result{{Status: engine.Failed}})
	if !strings.Contains(out.String(), "0 passed, \x1b[31m1 failed\x1b[0m in ") {
		t.Errorf("colored summary = %q", out.String())
	}
	// The time is measured from the console's creation.
	con, out = plainConsole(false, false)
	con.start = time.Now().Add(-90 * time.Second)
	con.summary(nil)
	if !strings.HasSuffix(out.String(), " in 1m30s\n") {
		t.Errorf("summary time = %q", out.String())
	}
}

// printf is safe to use from many goroutines (flows on several devices report at once).
func TestConsoleConcurrent(t *testing.T) {
	con, out := plainConsole(false, true)
	done := make(chan bool)
	for g := 0; g < 8; g++ {
		go func(g int) {
			r := con.reporter("f", fmt.Sprint("dev", g))
			for i := 0; i < 50; i++ {
				r.Event(engine.Event{Desc: "step", Status: engine.Passed})
			}
			done <- true
		}(g)
	}
	for g := 0; g < 8; g++ {
		<-done
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 400 {
		t.Fatalf("lines = %d", len(lines))
	}
	for _, l := range lines {
		if !regexp.MustCompile(`^\[dev\d\]   ✓ step  0ms$`).MatchString(l) {
			t.Fatalf("interleaved line %q", l)
		}
	}
}
