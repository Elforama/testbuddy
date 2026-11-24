package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

type deliveryDriver struct {
	*fakeDriver
	mu       sync.Mutex
	verdicts []string
	asked    int
	pending  bool
}

func (d *deliveryDriver) Delivery(ctx context.Context, token int64, wait time.Duration) (driver.DeliveryVerdict, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v := d.verdicts[min(d.asked, len(d.verdicts)-1)]
	d.asked++
	report := 21
	return driver.DeliveryVerdict{Verdict: v, ReportMs: &report, AnswerMs: []int{12, 9}, Pending: d.pending}, nil
}

func (d *deliveryDriver) askedN() int { d.mu.Lock(); defer d.mu.Unlock(); return d.asked }

func tokens(reply func(n int, res driver.ActionResult) driver.ActionResult) func(int, driver.ActionResult) driver.ActionResult {
	return func(n int, res driver.ActionResult) driver.ActionResult {
		res.DeliveryToken = int64(n)
		if reply != nil {
			res = reply(n, res)
		}
		return res
	}
}

func runUndelivered(t *testing.T, d driver.Driver, src string, ciMode bool) (error, string) {
	t.Helper()
	t.Setenv("TB_CI", map[bool]string{true: "1", false: "0"}[ciMode])
	cmds, err := flow.ParseCommands(".", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var buf syncBuf
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = New(d, Options{Trace: NewTrace(&buf)}).RunCommands(ctx, cmds, "app", nil)
	return err, buf.String()
}

func TestTapNotDeliveredIsSentAgain(t *testing.T) {
	for _, tc := range []struct {
		name     string
		flow     string
		ci       bool
		kind     string
		verdicts []string
		taps     int
		asks     int
		fail     bool
		lines    []string
	}{
		{name: "a tap on a named button, in CI", ci: true, flow: "- tapOn: {id: cover_sheet}\n- assertVisible: {id: sheet_title}",
			kind: "tap", verdicts: []string{driver.NotDelivered, driver.Delivered}, taps: 2, asks: 2,
			lines: []string{`the app never got the touch .*: tapping again once the screen settles`}},
		{name: "a double tap, in CI", ci: true, flow: "- doubleTapOn: Open sheet\n- assertVisible: {id: sheet_title}",
			kind: "double", verdicts: []string{driver.NotDelivered, driver.Delivered}, taps: 2, asks: 2,
			lines: []string{`the app never got the touch .*: tapping again once the screen settles`}},
		{name: "not in CI: not asked", flow: "- tapOn: {id: cover_sheet}",
			kind: "tap", verdicts: []string{driver.NotDelivered}, taps: 1, asks: 0},
		{name: "unknown: not sent again", ci: true, flow: "- tapOn: {id: cover_sheet}",
			kind: "tap", verdicts: []string{driver.Unknown}, taps: 1, asks: 1},
		{name: "delivered: not sent again", ci: true, flow: "- tapOn: {id: cover_sheet}",
			kind: "tap", verdicts: []string{driver.Delivered}, taps: 1, asks: 1},
		{name: "a point is not asked about", ci: true, flow: "- tapOn: {point: \"50%,15%\"}",
			kind: "tap", verdicts: []string{driver.NotDelivered}, taps: 1, asks: 0},
		{name: "lost every time", ci: true, flow: "- tapOn: {id: cover_sheet}",
			kind: "tap", verdicts: []string{driver.NotDelivered}, taps: 3, asks: 3,
			lines: []string{`the tap was sent again 2 times already: leaving it`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var shown atomic.Bool
			f := &fakeDriver{plat: "ios"}
			button := &ui.Node{ID: "cover_sheet", Class: "Button", Text: "Open sheet", Enabled: true, Clickable: true, Bounds: ui.Rect{L: 110, T: 100, R: 290, B: 140}}
			f.screen = func() []*ui.Node {
				nodes := []*ui.Node{button}
				if shown.Load() {
					nodes = append(nodes, &ui.Node{ID: "sheet_title", Class: "StaticText", Text: "Sheet", Bounds: ui.Rect{L: 20, T: 300, R: 200, B: 340}})
				}
				return nodes
			}
			d := &deliveryDriver{fakeDriver: f, verdicts: tc.verdicts}
			f.tapReply = tokens(func(n int, res driver.ActionResult) driver.ActionResult {
				if tc.verdicts[min(n, len(tc.verdicts))-1] == driver.Delivered {
					shown.Store(true)
				}
				return res
			})
			err, trace := runUndelivered(t, d, tc.flow, tc.ci)
			if (err != nil) != tc.fail {
				t.Fatalf("err=%v, want failure %t; trace:\n%s", err, tc.fail, trace)
			}
			f.mu.Lock()
			taps, kinds := len(f.taps), append([]string(nil), f.tapKind...)
			f.mu.Unlock()
			if taps != tc.taps {
				t.Fatalf("%d touches sent, want %d; trace:\n%s", taps, tc.taps, trace)
			}
			if tc.asks >= 0 && d.askedN() != tc.asks {
				t.Fatalf("asked for %d verdicts, want %d; trace:\n%s", d.askedN(), tc.asks, trace)
			}
			for _, k := range kinds {
				if k != tc.kind {
					t.Fatalf("touch kinds %v, want all %q", kinds, tc.kind)
				}
			}
			wantLines(t, trace, tc.lines...)
		})
	}
}

func TestTapNotDeliveredReplayWithheldOrCancelled(t *testing.T) {
	for _, tc := range []struct {
		name    string
		replies func(n int, res driver.ActionResult) driver.ActionResult
		taps    int
		lines   []string
	}{
		{name: "withheld every time", taps: 3, replies: func(n int, res driver.ActionResult) driver.ActionResult {
			if n == 1 {
				return res
			}
			return driver.ActionResult{Guarded: true, Withheld: "moved", Settle: driver.NoChange}
		}, lines: []string{`touch withheld at moved`, `resolving the target again`}},
		{name: "cancelled every time", taps: 4, replies: func(n int, res driver.ActionResult) driver.ActionResult {
			if n == 1 {
				return res
			}
			return driver.ActionResult{Guarded: true, Probed: true, Cancelled: true, Settle: driver.Changed}
		}, lines: []string{`touch cancelled`, `tapping again`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDriver{plat: "ios"}
			f.screen = static(&ui.Node{ID: "cover_sheet", Class: "Button", Text: "Open sheet", Enabled: true, Clickable: true, Bounds: ui.Rect{L: 110, T: 100, R: 290, B: 140}})
			d := &deliveryDriver{fakeDriver: f, verdicts: []string{driver.NotDelivered}}
			f.tapReply = tokens(tc.replies)
			err, trace := runUndelivered(t, d, "- tapOn: {id: cover_sheet}", true)
			if err == nil {
				t.Fatalf("passed, want the step to fail; trace:\n%s", trace)
			}
			f.mu.Lock()
			taps := len(f.taps)
			f.mu.Unlock()
			if taps != tc.taps {
				t.Fatalf("%d touches sent, want %d; trace:\n%s", taps, tc.taps, trace)
			}
			wantLines(t, trace, tc.lines...)
		})
	}
}

func TestTapNotDeliveredReplaySkipsATwin(t *testing.T) {
	var presented atomic.Bool
	f := &fakeDriver{plat: "ios"}
	f.screen = func() []*ui.Node {
		root := &ui.Node{Class: "Other", Bounds: ui.Rect{R: 400, B: 800}}
		b := &ui.Node{Class: "Button", Text: "Open sheet", Enabled: true, Clickable: true, Depth: 1, Bounds: ui.Rect{L: 110, T: 100, R: 290, B: 140}}
		if presented.Load() {
			screen := &ui.Node{Class: "Other", Depth: 1, Bounds: ui.Rect{R: 400, B: 800}}
			twin := &ui.Node{Class: "Button", Text: "Open sheet", Enabled: true, Clickable: true, Depth: 2, Bounds: ui.Rect{L: 110, T: 100, R: 290, B: 140}}
			return []*ui.Node{root, screen, twin}
		}
		return []*ui.Node{root, b}
	}
	d := &deliveryDriver{fakeDriver: f, verdicts: []string{driver.NotDelivered}}
	f.tapReply = tokens(func(n int, res driver.ActionResult) driver.ActionResult {
		presented.Store(true)
		return res
	})
	err, trace := runUndelivered(t, d, "- tapOn: Open sheet", true)
	f.mu.Lock()
	taps := len(f.taps)
	f.mu.Unlock()
	if taps != 1 {
		t.Fatalf("%d touches sent, want 1 (no tap on the twin); err=%v; trace:\n%s", taps, err, trace)
	}
	if err == nil {
		t.Fatalf("passed, want the step to fail: the target changed; trace:\n%s", trace)
	}
	wantLines(t, trace, `target changed before tap`)
}

func TestTapNotDeliveredDoesNotStackWithSwallowedReplay(t *testing.T) {
	f := &fakeDriver{plat: "ios", tapSettle: driver.NoChange}
	f.screen = static(&ui.Node{Class: "Button", Text: "Open sheet", Enabled: true, Clickable: true, Bounds: ui.Rect{L: 110, T: 100, R: 290, B: 140}})
	d := &deliveryDriver{fakeDriver: f, verdicts: []string{driver.NotDelivered, driver.Unknown}}
	f.tapReply = tokens(nil)
	err, trace := runUndelivered(t, d, "- tapOn: Open sheet", true)
	if err != nil {
		t.Fatalf("err=%v; trace:\n%s", err, trace)
	}
	f.mu.Lock()
	taps := len(f.taps)
	f.mu.Unlock()
	if taps != 2 {
		t.Fatalf("%d touches sent, want 2 (one replay in all); trace:\n%s", taps, trace)
	}
}

func TestTapNotDeliveredNotAskedOutsideTheApp(t *testing.T) {
	t.Run("a system alert's button", func(t *testing.T) {
		f := &fakeDriver{plat: "ios"}
		f.screen = static(&ui.Node{Class: "StaticText", Text: "Home", Bounds: ui.Rect{L: 0, T: 0, R: 400, B: 40}})
		f.sysAlert = func() []*ui.Node {
			return []*ui.Node{{Class: "Button", Text: "Allow", Enabled: true, Clickable: true, Bounds: ui.Rect{L: 100, T: 400, R: 300, B: 440}}}
		}
		d := &deliveryDriver{fakeDriver: f, verdicts: []string{driver.NotDelivered}}
		f.tapReply = tokens(func(n int, res driver.ActionResult) driver.ActionResult { f.sysAlert = nil; return res })
		if err, trace := runUndelivered(t, d, "- tapOn: Allow", true); err != nil {
			t.Fatalf("err=%v; trace:\n%s", err, trace)
		}
		if d.askedN() != 0 {
			t.Fatalf("asked for %d verdicts, want none", d.askedN())
		}
	})
	t.Run("a keyboard key", func(t *testing.T) {
		f := &fakeDriver{plat: "ios", keyboard: &ui.Rect{L: 0, T: 500, R: 400, B: 800}, kbShown: true}
		f.kbNodes = []*ui.Node{{Class: "Key", Text: "return", Enabled: true, Clickable: true, Bounds: ui.Rect{L: 300, T: 740, R: 400, B: 790}}}
		f.screen = static(&ui.Node{Class: "TextField", Text: "", Enabled: true, Bounds: ui.Rect{L: 0, T: 100, R: 400, B: 140}})
		d := &deliveryDriver{fakeDriver: f, verdicts: []string{driver.NotDelivered}}
		f.tapReply = tokens(nil)
		if err, trace := runUndelivered(t, d, "- tapOn: return", true); err != nil {
			t.Fatalf("err=%v; trace:\n%s", err, trace)
		}
		if d.askedN() != 0 {
			t.Fatalf("asked for %d verdicts, want none", d.askedN())
		}
	})
}

func TestTapNotDeliveredTraceSaysWhy(t *testing.T) {
	for _, v := range []string{driver.NotDelivered, driver.Unknown} {
		f := &fakeDriver{plat: "ios"}
		f.screen = static(&ui.Node{ID: "cover_sheet", Class: "Button", Text: "Open sheet", Enabled: true, Clickable: true, Bounds: ui.Rect{L: 110, T: 100, R: 290, B: 140}})
		d := &deliveryDriver{fakeDriver: f, verdicts: []string{v, driver.Delivered}}
		f.tapReply = tokens(nil)
		_, trace := runUndelivered(t, d, "- tapOn: {id: cover_sheet}", true)
		wantLines(t, trace, `delivery verdict `+v+`: SpringBoard's report 21 ms after the lift, the app's idle answers in 12, 9 ms`)
	}
}

func TestTapNotDeliveredTraceSaysNoVerdictInTime(t *testing.T) {
	f := &fakeDriver{plat: "ios"}
	f.screen = static(&ui.Node{ID: "cover_sheet", Class: "Button", Text: "Open sheet", Enabled: true, Clickable: true, Bounds: ui.Rect{L: 110, T: 100, R: 290, B: 140}})
	d := &deliveryDriver{fakeDriver: f, verdicts: []string{driver.Unknown}, pending: true}
	f.tapReply = tokens(nil)
	_, trace := runUndelivered(t, d, "- tapOn: {id: cover_sheet}", true)
	wantLines(t, trace, `delivery verdict unknown \(no verdict in time\): SpringBoard's report 21 ms after the lift, the app's idle answers in 12, 9 ms`)
}
