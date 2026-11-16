package web

import (
	"fmt"
	"strings"
	"testing"

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
