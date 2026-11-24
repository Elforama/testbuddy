package occlusion

import (
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

func covered(s *ui.Screen, n *ui.Node, p int, by *ui.Node, layers ...ui.HitLayer) {
	pt := points(s, n)[p]
	n.Covers = append(n.Covers, ui.Hit{Point: p, X: pt[0], Y: pt[1], By: by, Layers: layers})
}

func points(s *ui.Screen, n *ui.Node) [5][2]int {
	v := VisiblePart(s, n)
	q := func(l, t, r, b int) [2]int { return [2]int{(l + r) / 2, (t + b) / 2} }
	return [5][2]int{q(v.L, v.T, v.R, v.B),
		q(v.L, v.T, v.CX(), v.CY()), q(v.CX(), v.T, v.R, v.CY()),
		q(v.L, v.CY(), v.CX(), v.B), q(v.CX(), v.CY(), v.R, v.B)}
}

func TestWebCoversAreTheHitTests(t *testing.T) {
	{
		body := node(0, 0, 0, 1000, 2000)
		row := tappable(1, 0, 300, 1000, 360)
		backdrop := node(1, 0, 0, 1000, 2000)
		drawer := node(1, 0, 0, 300, 2000)
		item := tappable(2, 0, 300, 300, 360)
		s := screen("web", []*ui.Node{body, row, backdrop, drawer, item})
		covered(s, row, 0, backdrop, ui.HitLayer{Node: backdrop, Kind: "fixed"})
		if c, modal := Covered(s, row); c != backdrop || !modal || Modal(s, row, c) != backdrop {
			t.Errorf("backdrop: cover %v modal %v", c, modal)
		}
		if c, _ := Covered(s, item); c != nil {
			t.Errorf("the drawer's item, clear in the hit test: covered by %v", c)
		}
		if Veil(s, backdrop) != true {
			t.Error("a backdrop with nothing to tap in it is a veil (waited for as a loading overlay)")
		}
		if Veil(s, drawer) {
			t.Error("a drawer with items to tap is no veil")
		}
	}
	{
		body := node(0, 0, 0, 1000, 2000)
		el := tappable(1, 0, 300, 1000, 360)
		later := tappable(1, 0, 0, 1000, 2000)
		s := screen("web", []*ui.Node{body, el, later})
		if c, _ := Covered(s, el); c != nil || At(s, el, 500, 330) != nil {
			t.Errorf("no hit test reported: covered by %v", c)
		}
	}
	{
		body := node(0, 0, 0, 1000, 2000)
		header := node(1, 0, 0, 1000, 150)
		row := tappable(1, 0, 100, 1000, 300)
		s := screen("web", []*ui.Node{body, header, row})
		covered(s, row, 0, header, ui.HitLayer{Node: header, Kind: "sticky"})
		if c, modal := Covered(s, row); c != header || modal {
			t.Errorf("sticky header: cover %v modal %v", c, modal)
		}
		if x, y, c, ok := UncoveredPoint(s, row); c != header || !ok || x != 500 || y != 225 {
			t.Errorf("UncoveredPoint = (%d, %d) %v %v, want (500, 225) below the header", x, y, c, ok)
		}
	}
	{
		body := node(0, 0, 0, 1000, 2000)
		header := node(1, 0, 0, 1000, 150)
		row := tappable(1, 0, 50, 1000, 100)
		s := screen("web", []*ui.Node{body, header, row})
		covered(s, row, 0, header, ui.HitLayer{Node: header, Kind: "sticky"})
		if _, modal := Covered(s, row); modal {
			t.Error("a sticky header strip over a whole row was taken for a modal layer")
		}
	}
	{
		body := node(0, 0, 0, 1000, 2000)
		el := tappable(1, 100, 300, 300, 340)
		dlg := node(1, 50, 250, 600, 500)
		text := node(2, 60, 260, 400, 400)
		s := screen("web", []*ui.Node{body, el, dlg, text})
		covered(s, el, 0, dlg, ui.HitLayer{Node: dlg, Kind: "modal"})
		if c, modal := Covered(s, el); c != dlg || !modal || Modal(s, el, dlg) != dlg {
			t.Errorf("aria-modal: cover %v modal %v", c, modal)
		}
	}
	{
		body := node(0, 0, 0, 1000, 2000)
		card := tappable(1, 100, 300, 600, 700)
		toast := node(1, 200, 450, 900, 550)
		s := screen("web", []*ui.Node{body, card, toast})
		covered(s, card, 0, toast, ui.HitLayer{Node: toast, Kind: "fixed"})
		if c, modal := Covered(s, card); c != toast || modal {
			t.Errorf("toast: cover %v modal %v", c, modal)
		}
	}
	{
		body := node(0, 0, 0, 1000, 2000)
		el := tappable(1, 0, 300, 1000, 500)
		over := node(1, 0, 0, 1000, 2000)
		s := screen("web", []*ui.Node{body, el, over})
		covered(s, el, 1, over)
		covered(s, el, 2, over)
		if c, _ := Covered(s, el); c != nil {
			t.Errorf("2 quarters: covered by %v", c)
		}
		covered(s, el, 4, over)
		if c, modal := Covered(s, el); c != over || modal {
			t.Errorf("3 quarters: cover %v modal %v", c, modal)
		}
		p := points(s, el)[3]
		if c := At(s, el, p[0], p[1]); c != nil {
			t.Errorf("the clear quarter's middle: %v", c)
		}
	}
}
