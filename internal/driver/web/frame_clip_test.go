package web

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/occlusion"
	"github.com/elforama/testbuddy/internal/ui"
)

// Review of #315: a frame element is its document's viewport. What of the frame lies outside it
// (a Submit far down a small payment frame) does not show: it is clipped as a row out of its
// list (the frame element is listed as scrolling), so the page is not asked about it, and the
// page's footer below the frame is no cover of it.
func TestScreenClipsAFramesElementsToTheFrame(t *testing.T) {
	snap := map[string]any{"w": 1000, "h": 1000, "url": "https://shop.example.com/", "nodes": []map[string]any{
		{"d": 0, "b": []int{0, 0, 1000, 1000}, "c": "body", "f": 4098},
		{"d": 1, "b": []int{100, 200, 500, 440}, "c": "iframe", "f": 4098, "frame": map[string]any{"src": "https://pay.example.net/card", "x": 100, "y": 200}},
		{"d": 1, "b": []int{0, 460, 1000, 1000}, "c": "div", "id": "footer", "f": 4099},
	}}
	inner := map[string]any{"nodes": []map[string]any{
		{"d": 0, "b": []int{0, 0, 400, 700}, "c": "body", "f": 4098 | 8},
		{"d": 1, "b": []int{10, 20, 210, 60}, "c": "button", "t": "Pay", "f": 4099},
		{"d": 1, "b": []int{10, 600, 210, 640}, "c": "button", "t": "Submit", "f": 4099},
	}}
	page := newFakeCDP(t)
	var req [][]json.RawMessage
	page.evaluator(func(expr, session string) any {
		switch {
		case session == "" && expr == snapshotExpr:
			return value(jsonArg(snap))
		case session == "S-PAY":
			return value(jsonArg(inner))
		case session == "" && strings.Contains(expr, "frameHits("):
			arg := expr[strings.Index(expr, "frameHits(")+len("frameHits(") : strings.Index(expr, ") :")]
			if err := json.Unmarshal([]byte(arg), &req); err != nil {
				t.Errorf("frameHits argument %s: %v", arg, err)
			}
			out := make([]any, len(req))
			return value(jsonArg(out))
		}
		return nil
	})
	d := newWebDriver(t, page, nil)
	d.mu.Lock()
	d.frames["S-PAY"] = &frame{session: "S-PAY", target: "T1", url: "https://pay.example.net/card", order: 1}
	d.mu.Unlock()
	s, err := d.Screen(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	var frameEl, pay, submit *ui.Node
	for _, n := range s.Nodes {
		switch {
		case n.Class == "iframe":
			frameEl = n
		case n.Text == "Pay":
			pay = n
		case n.Text == "Submit":
			submit = n
		}
	}
	if frameEl == nil || pay == nil || submit == nil {
		t.Fatalf("frame %v, Pay %v, Submit %v", frameEl, pay, submit)
	}
	if !frameEl.Scrollable {
		t.Errorf("the frame element is not listed as scrolling: %+v", frameEl)
	}
	screen := ui.Rect{R: s.Width, B: s.Height}
	if c := submit.ClipRect(screen); c.Contains(submit.Bounds.CX(), submit.Bounds.CY()) {
		t.Errorf("Submit at %v shows outside its frame %v (clip %v)", submit.Bounds, frameEl.Bounds, c)
	}
	if _, ok := occlusion.SamplePoints(s, submit); ok {
		t.Errorf("Submit, outside its frame, has sample points")
	}
	if _, ok := occlusion.SamplePoints(s, pay); !ok {
		t.Errorf("Pay, in its frame, has no sample points")
	}
	// The page is asked about the frame's body and Pay, which show; not about Submit.
	if len(req) != 2 {
		t.Errorf("the page was asked about %d of the frame's elements, want 2 (not Submit, outside the frame)", len(req))
	}
}
