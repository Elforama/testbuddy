package web

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/occlusion"
	"github.com/elforama/testbuddy/internal/ui"
)

// Issue #315: a cross-origin frame tests its elements in its own process, where the page's
// own covers over the frame do not show. Screen asks the page, in one evaluation, what a click
// at each sample point of the frame's elements reaches instead of the frame element; the page's
// cover replaces the frame's own at that point, its elements pointing at the page's nodes where
// they now are. CoveredAt checks a frame's node in the page, against its frame element.
func TestScreenAddsThePageCoversOverAFrame(t *testing.T) {
	snap := map[string]any{"w": 1000, "h": 800, "url": "https://shop.example.com/", "nodes": []map[string]any{
		{"d": 0, "b": []int{0, 0, 1000, 800}, "c": "body", "f": 4098},
		{"d": 1, "b": []int{100, 200, 500, 400}, "c": "iframe", "f": 4098, "frame": map[string]any{"src": "https://pay.example.net/card", "x": 100, "y": 200}},
		{"d": 1, "b": []int{0, 0, 600, 450}, "c": "div[dialog]", "id": "dialog", "f": 4098},
	}}
	inner := map[string]any{"nodes": []map[string]any{
		{"d": 0, "b": []int{0, 0, 400, 200}, "c": "body", "f": 4098},
		// Pay: under the frame's own banner at its middle (the frame's hitPass).
		{"d": 1, "b": []int{10, 20, 210, 60}, "c": "button", "t": "Pay", "f": 4099,
			"cv": []map[string]any{{"p": 0, "x": 110, "y": 40, "by": map[string]any{"i": 3}}}},
		// Clear in the frame: only the page covers it, at three quarters.
		{"d": 1, "b": []int{10, 100, 210, 140}, "c": "button", "t": "Back", "f": 4099},
		{"d": 1, "b": []int{0, 0, 400, 80}, "c": "div", "id": "banner", "f": 4098},
	}}
	page := newFakeCDP(t)
	var asked string
	page.evaluator(func(expr, session string) any {
		switch {
		case session == "" && expr == snapshotExpr:
			return value(jsonArg(snap))
		case session == "S-PAY":
			return value(jsonArg(inner))
		case session == "" && strings.Contains(expr, "frameHits("):
			asked = expr
			var req [][]json.RawMessage
			arg := expr[strings.Index(expr, "frameHits(")+len("frameHits(") : strings.Index(expr, ") :")]
			if err := json.Unmarshal([]byte(arg), &req); err != nil {
				t.Errorf("frameHits argument %s: %v", arg, err)
				return value("[]")
			}
			// Per frame element asked about: body, Pay, Back, banner (in the frame's order).
			if len(req) != 4 {
				t.Errorf("asked about %d elements, want the frame's 4 on screen: %s", len(req), arg)
				return value("[]")
			}
			dialog := map[string]any{"i": 2}
			cover := func(p, x, y int) map[string]any {
				return map[string]any{"p": p, "x": x, "y": y, "by": dialog, "l": []map[string]any{{"i": 2, "k": "modal"}}}
			}
			return value(jsonArg([]any{
				nil,
				[]any{cover(0, 210, 240)}, // Pay's middle: the page's dialog, not the banner
				[]any{cover(1, 160, 310), cover(2, 260, 310), cover(3, 160, 330)},
				nil,
			}))
		case session == "" && strings.Contains(expr, "covered("):
			asked = expr
			return value("div#dialog")
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
	byID := func(id string) *ui.Node {
		for _, n := range s.Nodes {
			if n.ID == id || n.Text == id {
				return n
			}
		}
		t.Fatalf("%s not listed", id)
		return nil
	}
	pay, back, dialog := byID("Pay"), byID("Back"), byID("dialog")
	// The request names the frame element (main-document index 1) and Pay's sample points.
	pts, _ := occlusion.SamplePoints(s, pay)
	want, _ := json.Marshal([]any{1, pts})
	if !strings.Contains(asked, string(want)) {
		t.Errorf("frameHits asked %s, want Pay's entry %s", asked, want)
	}
	if c, modal := occlusion.Covered(s, pay); c != dialog || !modal {
		t.Errorf("Pay: cover %v modal %v, want the page's dialog, modal (%+v)", c, modal, pay.Covers)
	}
	if len(pay.Covers) != 1 || pay.Covers[0].X != 210 || pay.Covers[0].Y != 240 {
		t.Errorf("Pay's covers %+v: the page's cover replaces the frame's at its middle", pay.Covers)
	}
	if c, modal := occlusion.Covered(s, back); c != dialog || !modal {
		t.Errorf("Back: cover %v modal %v, want the page's dialog at 3 quarters (%+v)", c, modal, back.Covers)
	}
	// CoveredAt checks a frame's node in the page, against the frame element (index 1).
	what, err := d.CoveredAt(ctxT(t), back.Index, 160, 310)
	if err != nil || what != "div#dialog" || !strings.Contains(asked, "covered(1, 160, 310)") {
		t.Errorf("CoveredAt(Back) = %q, %v (asked %s), want the page's check against the frame element", what, err, asked)
	}
}
