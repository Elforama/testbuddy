package session

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/look"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

// stableItems is what `do` records for its commands: each as typed, except a tap typed as a
// screen point, or a tap or assertion typed with a match index, which is recorded with a
// selector that finds the same element without one (a coordinate or an index breaks when
// the layout or a list changes).
//
// The element is looked up on the last listed screen, so a command is rewritten only while
// that screen is current: the device reported no change since it was read (fresh), and no
// earlier command in the batch may have changed it.
func stableItems(l *look.Listing, fresh bool, cmds []*flow.Command, items []string) []string {
	out := append([]string(nil), items...)
	if l == nil || l.Screen == nil || !fresh || len(cmds) != len(items) {
		return out
	}
	for i, c := range cmds {
		if r, ok := stableItem(l, c, items[i]); ok {
			out[i] = r
		}
		if c.Mutating() {
			break
		}
	}
	return out
}

// stableItem rewrites one command, or says it cannot.
func stableItem(l *look.Listing, c *flow.Command, item string) (string, bool) {
	tap := c.Name == "tapOn" || c.Name == "doubleTapOn" || c.Name == "longPressOn"
	if !tap && c.Name != "assertVisible" {
		return "", false
	}
	keys, ok := selectorKeys(item, c.Name)
	if !ok {
		return "", false
	}
	var el *look.Element
	switch {
	case tap && sameKeys(keys, "point") && c.Selector == nil:
		x, y, ok := screenPoint(c.Point, l.Screen)
		if !ok {
			return "", false
		}
		el = elementAt(l, x, y)
	case keys["index"] && sameKeys(keys, "text", "id", "index"):
		sel := c.Selector
		if sel == nil || strings.Contains(sel.Text+sel.ID+sel.Index, "${") {
			return "", false
		}
		n, _, err := match.Find(l.Screen, &match.Selector{Text: sel.Text, ID: sel.ID, Index: sel.Index})
		if err != nil || n == nil {
			return "", false
		}
		el = &look.Element{Node: n}
	}
	if el == nil {
		return "", false
	}
	sel := look.SelectorFor(l.Screen, el, !tap)
	if strings.Contains(sel, "point:") || strings.Contains(sel, "index:") {
		return "", false
	}
	return "- " + c.Name + ": " + sel, true
}

// selectorKeys are the keys of the selector map the item gives the command (`- tapOn:
// {point: …}`), or false when the item is not one command with a map.
func selectorKeys(item, name string) (map[string]bool, bool) {
	var list []map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(item), &list); err != nil || len(list) != 1 || len(list[0]) != 1 {
		return nil, false
	}
	v, ok := list[0][name]
	if !ok || v.Kind != yaml.MappingNode {
		return nil, false
	}
	keys := map[string]bool{}
	for i := 0; i+1 < len(v.Content); i += 2 {
		keys[v.Content[i].Value] = true
	}
	return keys, true
}

// sameKeys reports whether every key is one of these.
func sameKeys(keys map[string]bool, allowed ...string) bool {
	for k := range keys {
		ok := false
		for _, a := range allowed {
			ok = ok || k == a
		}
		if !ok {
			return false
		}
	}
	return len(keys) > 0
}

// screenPoint reads a flow point ("x,y" in pixels, or "x%,y%" of the screen).
func screenPoint(spec string, s *ui.Screen) (int, int, bool) {
	a, b, ok := strings.Cut(spec, ",")
	if !ok || strings.Contains(spec, "${") {
		return 0, 0, false
	}
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if strings.HasSuffix(a, "%") || strings.HasSuffix(b, "%") {
		px, e1 := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(a, "%")), 64)
		py, e2 := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(b, "%")), 64)
		if e1 != nil || e2 != nil || px < 0 || px > 100 || py < 0 || py > 100 || s.Width <= 0 || s.Height <= 0 {
			return 0, 0, false
		}
		return int(float64(s.Width) * px / 100), int(float64(s.Height) * py / 100), true
	}
	x, e1 := strconv.Atoi(a)
	y, e2 := strconv.Atoi(b)
	return x, y, e1 == nil && e2 == nil && x >= 0 && y >= 0
}

// elementAt is the innermost listed element under the point, in the top-most window that
// has one there (windows are listed top-most first).
func elementAt(l *look.Listing, x, y int) *look.Element {
	var best *look.Element
	bestRank, bestArea := 0, 0
	for _, e := range l.Elements {
		if e.Node == nil {
			continue
		}
		b := e.Node.Bounds
		if x < b.L || x >= b.R || y < b.T || y >= b.B {
			continue
		}
		rank, area := e.Node.Window, b.W()*b.H()
		if best == nil || rank < bestRank || (rank == bestRank && area < bestArea) {
			best, bestRank, bestArea = e, rank, area
		}
	}
	return best
}
