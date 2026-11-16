package web

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// Issue #326: a drag that starts over a cross-origin frame scrolls in the frame's own session,
// at the point in the frame's viewport, and sends no wheel (which the browser can route to the
// page under a frame that has not drawn yet). The frame element is tied to its session by its
// frame id, the frame's target id. A frame that has nothing to scroll there falls back to the
// wheel, after the frame has drawn.
func TestSwipeDragScrollsACrossOriginFrameInItsSession(t *testing.T) {
	for _, frameAnswer := range []string{"moved", "still"} {
		page, d := inputPage(t)
		d.mu.Lock()
		d.frames["S-PAY"] = &frame{session: "S-PAY", target: "FRAME-1", url: "https://pay.example.net/", tab: d.curSessionLocked()}
		d.mu.Unlock()
		var frameExprs []string
		page.on("Runtime.evaluate", func(m cdpMsg) any {
			expr, _ := m.params()["expression"].(string)
			switch {
			case m.SessionID == "S-PAY" && strings.Contains(expr, "frameAt("):
				return value(nil) // no frame in the frame
			case m.SessionID == "S-PAY":
				frameExprs = append(frameExprs, expr)
				if strings.Contains(expr, "scrollBy(") {
					return value(frameAnswer)
				}
				return value(nil) // awaitDrawn
			case strings.Contains(expr, "frameAt("):
				return map[string]any{"result": map[string]any{"type": "object", "subtype": "node", "objectId": "OBJ-1"}}
			case expr == "window.__tb.frameOrigin":
				return value([]int{40, 60})
			case strings.Contains(expr, "scrollBy("):
				return value("frame")
			}
			return value(false)
		})
		page.on("DOM.describeNode", func(m cdpMsg) any {
			if m.params()["objectId"] != "OBJ-1" {
				t.Errorf("describeNode %v", m.params())
			}
			return map[string]any{"node": map[string]any{"frameId": "FRAME-1"}}
		})
		if _, err := d.Swipe(ctxT(t), 240, 200, 240, 100, 0, driver.ActionOpts{Drag: true, NoSettle: true}); err != nil {
			t.Fatal(err)
		}
		if len(frameExprs) == 0 || frameExprs[0] != fmt.Sprintf(scrollByJS, 200, 140, 0, 100) {
			t.Errorf("%s: frame scripts %q, want the drag at (200, 140) in the frame", frameAnswer, frameExprs)
		}
		wheel := len(mouseEvents(page)) > 0
		if want := frameAnswer == "still"; wheel != want {
			t.Errorf("%s: wheel sent = %v, want %v", frameAnswer, wheel, want)
		}
		if frameAnswer == "still" && (len(frameExprs) < 2 || !strings.Contains(frameExprs[1], "requestAnimationFrame")) {
			t.Errorf("still: the wheel did not wait for the frame to draw: %q", frameExprs)
		}
	}
}

// Issue #326 review: a tap over a cross-origin frame waits for the frame to draw before the
// pointer goes down; with no cross-origin frame attached, a tap evaluates nothing to find one.
func TestPointerInputWaitsForTheFrameUnderItToDraw(t *testing.T) {
	page, d := inputPage(t)
	if _, err := d.Tap(ctxT(t), 10, 20, driver.ActionOpts{NoSettle: true}); err != nil {
		t.Fatal(err)
	}
	if ev := page.calls("Runtime.evaluate"); len(ev) != 0 {
		t.Errorf("no frame attached: tap evaluated %d scripts", len(ev))
	}

	page, d = inputPage(t)
	d.mu.Lock()
	d.frames["S-PAY"] = &frame{session: "S-PAY", target: "FRAME-1", url: "https://pay.example.net/", tab: d.curSessionLocked()}
	d.mu.Unlock()
	var order []string
	page.on("Runtime.evaluate", func(m cdpMsg) any {
		expr, _ := m.params()["expression"].(string)
		switch {
		case m.SessionID == "S-PAY" && strings.Contains(expr, "requestAnimationFrame"):
			order = append(order, "drawn")
			return value("drawn")
		case m.SessionID == "" && strings.Contains(expr, "frameAt("):
			order = append(order, "frameAt")
			return map[string]any{"result": map[string]any{"type": "object", "subtype": "node", "objectId": "OBJ-1"}}
		case expr == "window.__tb.frameOrigin":
			return value([]int{40, 60})
		}
		return value(nil)
	})
	page.on("DOM.describeNode", func(cdpMsg) any { return map[string]any{"node": map[string]any{"frameId": "FRAME-1"}} })
	page.on("Input.dispatchMouseEvent", func(m cdpMsg) any {
		order = append(order, fmt.Sprint(m.params()["type"]))
		return map[string]any{}
	})
	tap := func(want string) {
		t.Helper()
		order = nil
		if _, err := d.Tap(ctxT(t), 140, 200, driver.ActionOpts{NoSettle: true}); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(order, " "); got != want {
			t.Errorf("order %q, want %q", got, want)
		}
	}
	// The frame is drawn before the pointer goes down.
	tap("frameAt drawn mouseMoved mousePressed mouseReleased")
	// Review of #326: drawn once, the same document is not asked again.
	tap("mouseMoved mousePressed mouseReleased")
	// A new document in the frame (it navigated or reloaded): asked again.
	d.onEvent("Page.frameNavigated", json.RawMessage(`{"frame":{"id":"FRAME-1","url":"https://pay.example.net/2"}}`), "S-PAY")
	tap("frameAt drawn mouseMoved mousePressed mouseReleased")
}

// Review of #326: the wait for a frame to draw is bounded on the host's side: a frame that
// never answers (its timers throttled in a hidden tab) holds input back at most drawnWait and
// a little, and is not taken as drawn.
func TestAwaitDrawnIsBoundedOnTheHost(t *testing.T) {
	page, d := inputPage(t)
	d.mu.Lock()
	d.frames["S-PAY"] = &frame{session: "S-PAY", target: "FRAME-1", tab: d.curSessionLocked()}
	d.mu.Unlock()
	page.on("Runtime.evaluate", func(m cdpMsg) any {
		time.Sleep(time.Second) // the frame does not answer in time
		return value("drawn")
	})
	start := time.Now()
	d.awaitDrawn(ctxT(t), "S-PAY")
	if took := time.Since(start); took > drawnWait+200*time.Millisecond {
		t.Errorf("awaitDrawn took %s, want at most about %s", took, drawnWait)
	}
	d.mu.Lock()
	drawn := d.frames["S-PAY"].drawn
	d.mu.Unlock()
	if drawn {
		t.Error("a frame that did not answer is taken as drawn")
	}
}
