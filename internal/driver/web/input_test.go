package web

import (
	"context"
	"encoding/base64"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

func inputPage(t *testing.T) (*fakeCDP, *Driver) {
	page := newFakeCDP(t)
	return page, newWebDriver(t, page, nil)
}

// mouseEvents lists the Input.dispatchMouseEvent params sent.
func mouseEvents(f *fakeCDP) []map[string]any {
	var out []map[string]any
	for _, m := range f.calls("Input.dispatchMouseEvent") {
		out = append(out, m.params())
	}
	return out
}

func keyEvents(f *fakeCDP) []string {
	var out []string
	for _, m := range f.calls("Input.dispatchKeyEvent") {
		p := m.params()
		s := fmt.Sprintf("%v %v", p["type"], p["key"])
		if p["text"] != nil {
			s += fmt.Sprintf(" text=%q", p["text"])
		}
		if p["code"] != nil {
			s += fmt.Sprintf(" code=%v vk=%v", p["code"], p["windowsVirtualKeyCode"])
		}
		out = append(out, s)
	}
	return out
}

// A tap is a move, a press and a release at the point, with the left button.
func TestTap(t *testing.T) {
	page, d := inputPage(t)
	r, err := d.Tap(ctxT(t), 15, 25, driver.ActionOpts{NoSettle: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Settle != "" || r.Seq != 0 {
		t.Errorf("result %+v (no settle asked for)", r)
	}
	want := []map[string]any{
		{"type": "mouseMoved", "x": 15.0, "y": 25.0, "button": "none", "clickCount": 0.0},
		{"type": "mousePressed", "x": 15.0, "y": 25.0, "button": "left", "clickCount": 1.0, "buttons": 1.0},
		{"type": "mouseReleased", "x": 15.0, "y": 25.0, "button": "left", "clickCount": 1.0},
	}
	if got := mouseEvents(page); !reflect.DeepEqual(got, want) {
		t.Errorf("mouse events %v\nwant %v", got, want)
	}
}

// A tap the page reacts to waits for it to settle; one it ignores ends after the no-change window.
func TestTapSettles(t *testing.T) {
	page, d := inputPage(t)
	page.on("Input.dispatchMouseEvent", func(m cdpMsg) any {
		if m.params()["type"] == "mouseReleased" {
			page.event("Runtime.bindingCalled", map[string]any{"name": "__tbNotify", "payload": `{"a":0,"s":1}`}, "")
		}
		return map[string]any{}
	})
	r, err := d.Tap(ctxT(t), 1, 1, driver.ActionOpts{})
	if err != nil || r.Settle != driver.Changed || r.Seq != 1 || r.SettleMs < int(quietWindow/time.Millisecond)-20 {
		t.Errorf("Tap = %+v, %v", r, err)
	}
	page.on("Input.dispatchMouseEvent", func(cdpMsg) any { return map[string]any{} })
	t0 := time.Now()
	r, err = d.Tap(ctxT(t), 1, 1, driver.ActionOpts{NoChangeMs: 40})
	if err != nil || r.Settle != driver.NoChange || time.Since(t0) < 35*time.Millisecond {
		t.Errorf("ignored tap = %+v, %v after %s", r, err, time.Since(t0))
	}
}

// React returns as soon as the page starts changing, without waiting for it to settle.
func TestTapReact(t *testing.T) {
	page, d := inputPage(t)
	page.on("Input.dispatchMouseEvent", func(m cdpMsg) any {
		if m.params()["type"] == "mouseReleased" {
			page.event("Page.frameStartedLoading", map[string]any{"frameId": "main"}, "")
		}
		return map[string]any{}
	})
	r, err := d.Tap(ctxT(t), 1, 1, driver.ActionOpts{React: true, Timeout: 5 * time.Second})
	if err != nil || r.Settle != driver.Changed || r.SettleMs > 1000 {
		t.Errorf("Tap = %+v, %v (the page is still loading: it must not wait for it)", r, err)
	}
	page.on("Input.dispatchMouseEvent", func(cdpMsg) any { return map[string]any{} })
	if r, _ := d.Tap(ctxT(t), 1, 1, driver.ActionOpts{React: true, NoChangeMs: 30}); r.Settle != driver.NoChange {
		t.Errorf("no reaction: %+v", r)
	}
}

// A failed input is returned before any waiting.
func TestTapFails(t *testing.T) {
	for _, failing := range []string{"mouseMoved", "mousePressed", "mouseReleased"} {
		page, d := inputPage(t)
		page.on("Input.dispatchMouseEvent", func(m cdpMsg) any {
			if m.params()["type"] == failing {
				return errReply{cdpError{Code: -32602, Message: "Invalid parameters"}}
			}
			return map[string]any{}
		})
		if _, err := d.Tap(ctxT(t), 1, 1, driver.ActionOpts{}); err == nil || !strings.Contains(err.Error(), "Invalid parameters") {
			t.Errorf("%s failing: %v", failing, err)
		}
	}
}

// Settle errors (a cancelled context) come back from the action.
func TestActionContextCancelled(t *testing.T) {
	_, d := inputPage(t)
	ctx, cancel := context.WithCancel(ctxT(t))
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	if _, err := d.Tap(ctx, 1, 1, driver.ActionOpts{NoChangeMs: 5000}); err != context.Canceled {
		t.Errorf("err = %v", err)
	}
}

// A double tap is two press/release pairs with click counts 1 then 2.
func TestDoubleTap(t *testing.T) {
	page, d := inputPage(t)
	if _, err := d.DoubleTap(ctxT(t), 7, 8, driver.ActionOpts{NoSettle: true}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range mouseEvents(page) {
		got = append(got, fmt.Sprintf("%v %v", p["type"], p["clickCount"]))
	}
	if want := []string{"mousePressed 1", "mouseReleased 1", "mousePressed 2", "mouseReleased 2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("events %v", got)
	}
	for _, failing := range []string{"mousePressed", "mouseReleased"} {
		page.on("Input.dispatchMouseEvent", func(m cdpMsg) any {
			if m.params()["clickCount"] == 2.0 && m.params()["type"] == failing {
				return errReply{cdpError{Message: "x"}}
			}
			return map[string]any{}
		})
		if _, err := d.DoubleTap(ctxT(t), 7, 8, driver.ActionOpts{NoSettle: true}); err == nil {
			t.Errorf("%s failure not reported", failing)
		}
	}
}

// A long press holds the button for the duration; a cancelled context releases nothing.
func TestLongPress(t *testing.T) {
	page, d := inputPage(t)
	t0 := time.Now()
	if _, err := d.LongPress(ctxT(t), 3, 4, 60*time.Millisecond, driver.ActionOpts{NoSettle: true}); err != nil {
		t.Fatal(err)
	}
	if time.Since(t0) < 55*time.Millisecond {
		t.Errorf("held %s", time.Since(t0))
	}
	ev := mouseEvents(page)
	if len(ev) != 2 || ev[0]["type"] != "mousePressed" || ev[1]["type"] != "mouseReleased" {
		t.Errorf("events %v", ev)
	}
	page.reset()
	ctx, cancel := context.WithTimeout(ctxT(t), 30*time.Millisecond)
	defer cancel()
	if _, err := d.LongPress(ctx, 3, 4, 5*time.Second, driver.ActionOpts{NoSettle: true}); err != context.DeadlineExceeded {
		t.Errorf("err = %v", err)
	}
	if ev := mouseEvents(page); len(ev) != 1 {
		t.Errorf("events after cancel %v", ev)
	}
	page.on("Input.dispatchMouseEvent", func(cdpMsg) any { return errReply{cdpError{Message: "x"}} })
	if _, err := d.LongPress(ctxT(t), 3, 4, time.Millisecond, driver.ActionOpts{NoSettle: true}); err == nil {
		t.Error("press failure not reported")
	}
}

// A swipe is a wheel event at the start point with the swipe's distance as its delta.
func TestSwipeWheel(t *testing.T) {
	page, d := inputPage(t)
	if _, err := d.Swipe(ctxT(t), 500, 600, 480, 200, 300*time.Millisecond, driver.ActionOpts{NoSettle: true}); err != nil {
		t.Fatal(err)
	}
	ev := mouseEvents(page)
	want := map[string]any{"type": "mouseWheel", "x": 500.0, "y": 600.0, "button": "none", "clickCount": 0.0, "deltaX": 20.0, "deltaY": 400.0}
	if len(ev) != 1 || !reflect.DeepEqual(ev[0], want) {
		t.Errorf("events %v", ev)
	}
	// Only the check for something made to be dragged under the start point.
	if ev := page.calls("Runtime.evaluate"); len(ev) != 1 || ev[0].params()["expression"] != fmt.Sprintf(dragTargetJS, 500, 600) {
		t.Errorf("scripts %v: want only the drag-target check", ev)
	}
}

// A swipe that starts on something made to be dragged (a range slider, a handle) is a mouse
// drag: press, moves, release at the end point.
func TestSwipeMouseDrag(t *testing.T) {
	page, d := inputPage(t)
	page.on("Runtime.evaluate", func(cdpMsg) any { return value(true) })
	if _, err := d.Swipe(ctxT(t), 100, 300, 400, 300, 0, driver.ActionOpts{NoSettle: true}); err != nil {
		t.Fatal(err)
	}
	ev := mouseEvents(page)
	if len(ev) < 4 || ev[0]["type"] != "mouseMoved" || ev[1]["type"] != "mousePressed" || ev[len(ev)-1]["type"] != "mouseReleased" ||
		ev[len(ev)-1]["x"] != 400.0 || ev[len(ev)-2]["buttons"] != 1.0 {
		t.Errorf("events %v", ev)
	}
	for _, e := range ev {
		if e["type"] == "mouseWheel" {
			t.Error("a drag also scrolled")
		}
	}
}

// A drag scrolls the element under the point directly; when nothing moved (or the point is over a
// frame, or the script fails) it falls back to the wheel.
func TestSwipeDrag(t *testing.T) {
	cases := []struct {
		answer any
		wheel  bool
	}{
		{value("moved"), false},
		{value("still"), true},
		{value("frame"), true},
		{map[string]any{"exceptionDetails": map[string]any{"text": "boom"}}, true},
	}
	for _, c := range cases {
		page, d := inputPage(t)
		page.on("Runtime.evaluate", func(cdpMsg) any { return c.answer })
		if _, err := d.Swipe(ctxT(t), 100, 700, 100, 300, 0, driver.ActionOpts{Drag: true, NoSettle: true}); err != nil {
			t.Fatal(err)
		}
		ev := page.calls("Runtime.evaluate")
		if len(ev) != 2 || ev[1].params()["expression"] != fmt.Sprintf(scrollByJS, 100, 700, 0, 400) {
			t.Errorf("%v: drag script not sent as expected", c.answer)
		}
		if got := len(mouseEvents(page)) == 1; got != c.wheel {
			t.Errorf("%v: wheel used = %v, want %v", c.answer, got, c.wheel)
		}
	}
}

// Typing sends a key down/up per character (Enter for a newline).
func TestTypeText(t *testing.T) {
	page, d := inputPage(t)
	if _, err := d.TypeText(ctxT(t), "aé\nb", driver.ActionOpts{NoSettle: true}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`keyDown a text="a"`, "keyUp a",
		`keyDown é text="é"`, "keyUp é",
		`keyDown Enter text="\r" code=Enter vk=13`, "keyUp Enter code=Enter vk=13",
		`keyDown b text="b"`, "keyUp b",
	}
	if got := keyEvents(page); !reflect.DeepEqual(got, want) {
		t.Errorf("keys %q\nwant %q", got, want)
	}
	for _, m := range page.calls("Input.dispatchKeyEvent") {
		if p := m.params(); p["type"] == "keyDown" && p["key"] != "Enter" && p["unmodifiedText"] != p["text"] {
			t.Errorf("unmodifiedText %v", p)
		}
	}
}

// Typing stops at the first failed key event.
func TestTypeTextFails(t *testing.T) {
	for _, failAt := range []int{1, 2, 3, 5} {
		page, d := inputPage(t)
		n := 0
		page.on("Input.dispatchKeyEvent", func(cdpMsg) any {
			n++
			if n == failAt {
				return errReply{cdpError{Message: "x"}}
			}
			return map[string]any{}
		})
		if _, err := d.TypeText(ctxT(t), "a\nb", driver.ActionOpts{NoSettle: true}); err == nil {
			t.Errorf("failure at key event %d not reported", failAt)
		}
	}
}

// Erasing presses Backspace n times, but no more than the focused field holds.
func TestEraseText(t *testing.T) {
	cases := []struct {
		length any
		n      int
		want   int
	}{
		{2, 5, 2}, {10, 3, 3}, {0, 4, 0}, {-1, 3, 3},
	}
	for _, c := range cases {
		page, d := inputPage(t)
		page.on("Runtime.evaluate", func(cdpMsg) any { return value(c.length) })
		if _, err := d.EraseText(ctxT(t), c.n, driver.ActionOpts{NoSettle: true}); err != nil {
			t.Fatal(err)
		}
		keys := keyEvents(page)
		if len(keys) != 2*c.want {
			t.Errorf("length %v, n %d: %d key events, want %d", c.length, c.n, len(keys), 2*c.want)
		}
		for i, k := range keys {
			if (i%2 == 0 && k != "rawKeyDown Backspace code=Backspace vk=8") || (i%2 == 1 && k != "keyUp Backspace code=Backspace vk=8") {
				t.Errorf("key %d = %q", i, k)
			}
		}
	}
	page, d := inputPage(t)
	page.on("Runtime.evaluate", func(cdpMsg) any { return value(-1) })
	page.on("Input.dispatchKeyEvent", func(cdpMsg) any { return errReply{cdpError{Message: "x"}} })
	if _, err := d.EraseText(ctxT(t), 2, driver.ActionOpts{NoSettle: true}); err == nil {
		t.Error("key failure not reported")
	}
}

// When the field-length probe fails, EraseText should still press n times.
func TestEraseTextWhenLengthUnknown(t *testing.T) {
	page, d := inputPage(t)
	page.on("Runtime.evaluate", func(cdpMsg) any { return map[string]any{"exceptionDetails": map[string]any{"text": "boom"}} })
	d.EraseText(ctxT(t), 3, driver.ActionOpts{NoSettle: true})
	if got := len(keyEvents(page)); got != 6 {
		t.Errorf("%d key events, want 6", got)
	}
}

// PressKey sends the key's DOM names and virtual key code; text keys get a keyDown, others a rawKeyDown.
func TestPressKey(t *testing.T) {
	cases := map[string][]string{
		"Enter":     {`keyDown Enter text="\r" code=Enter vk=13`, "keyUp Enter code=Enter vk=13"},
		" TAB ":     {"rawKeyDown Tab code=Tab vk=9", "keyUp Tab code=Tab vk=9"},
		"escape":    {"rawKeyDown Escape code=Escape vk=27", "keyUp Escape code=Escape vk=27"},
		"home":      {"rawKeyDown Home code=Home vk=36", "keyUp Home code=Home vk=36"},
		"BACKSPACE": {"rawKeyDown Backspace code=Backspace vk=8", "keyUp Backspace code=Backspace vk=8"},
	}
	for key, want := range cases {
		page, d := inputPage(t)
		if _, err := d.PressKey(ctxT(t), key, driver.ActionOpts{NoSettle: true}); err != nil {
			t.Fatal(err)
		}
		if got := keyEvents(page); !reflect.DeepEqual(got, want) {
			t.Errorf("PressKey(%q) sent %q, want %q", key, got, want)
		}
	}
	page, d := inputPage(t)
	_, err := d.PressKey(ctxT(t), "volume up", driver.ActionOpts{})
	if err == nil || !strings.Contains(err.Error(), `key "volume up" is not supported on web`) || len(page.calls("")) != 0 {
		t.Errorf("unsupported key: %v, sent %v", err, page.methods())
	}
	page.on("Input.dispatchKeyEvent", func(m cdpMsg) any {
		if m.params()["type"] == "keyUp" {
			return errReply{cdpError{Message: "x"}}
		}
		return map[string]any{}
	})
	if _, err := d.PressKey(ctxT(t), "tab", driver.ActionOpts{NoSettle: true}); err == nil {
		t.Error("keyUp failure not reported")
	}
}

// Back goes to the previous history entry, and does nothing on the first one.
func TestPressBack(t *testing.T) {
	page, d := inputPage(t)
	var index atomic.Int32
	index.Store(2)
	page.on("Page.getNavigationHistory", func(cdpMsg) any {
		return map[string]any{"currentIndex": index.Load(), "entries": []map[string]any{{"id": 11}, {"id": 12}, {"id": 13}}}
	})
	if _, err := d.PressKey(ctxT(t), "Back", driver.ActionOpts{NoSettle: true}); err != nil {
		t.Fatal(err)
	}
	nav := page.calls("Page.navigateToHistoryEntry")
	if len(nav) != 1 || nav[0].params()["entryId"] != 12.0 {
		t.Errorf("navigations %v", page.methods())
	}
	index.Store(0)
	page.reset()
	if _, err := d.PressKey(ctxT(t), "back", driver.ActionOpts{NoSettle: true}); err != nil || len(page.calls("Page.navigateToHistoryEntry")) != 0 {
		t.Errorf("back on the first entry: %v, %v", err, page.methods())
	}
	page.on("Page.getNavigationHistory", func(cdpMsg) any { return errReply{cdpError{Message: "no history"}} })
	if _, err := d.PressKey(ctxT(t), "back", driver.ActionOpts{NoSettle: true}); err == nil {
		t.Error("history failure not reported")
	}
}

// Screenshots: PNG by default, JPEG at quality 80, a scaled clip of the viewport below scale 1.
func TestScreenshot(t *testing.T) {
	page, d := inputPage(t)
	img := []byte("\x89PNG fake")
	page.on("Page.captureScreenshot", func(cdpMsg) any { return map[string]any{"data": base64.StdEncoding.EncodeToString(img)} })
	cases := []struct {
		scale  float64
		format string
		want   map[string]any
	}{
		{1, "", map[string]any{"format": "png"}},
		{0, "jpeg", map[string]any{"format": "jpeg", "quality": 80.0}},
		{0.5, "png", map[string]any{"format": "png", "clip": map[string]any{"x": 0.0, "y": 0.0, "width": 1280.0, "height": 800.0, "scale": 0.5}}},
	}
	for _, c := range cases {
		b, err := d.Screenshot(ctxT(t), c.scale, c.format)
		if err != nil || string(b) != string(img) {
			t.Fatalf("Screenshot = %q, %v", b, err)
		}
		calls := page.calls("Page.captureScreenshot")
		if got := calls[len(calls)-1].params(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("scale %v format %q: params %v, want %v", c.scale, c.format, got, c.want)
		}
	}
	page.on("Page.captureScreenshot", func(cdpMsg) any { return map[string]any{"data": "!!not base64"} })
	if _, err := d.Screenshot(ctxT(t), 1, ""); err == nil {
		t.Error("bad base64 accepted")
	}
	page.on("Page.captureScreenshot", func(cdpMsg) any { return errReply{cdpError{Message: "x"}} })
	if _, err := d.Screenshot(ctxT(t), 1, ""); err == nil {
		t.Error("protocol error not reported")
	}
}
