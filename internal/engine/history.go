package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

const historyLimit = 16
const historyLifetime = 30 * time.Second
const historyNodeLimit = 8192

type screenSnapshot struct {
	screen  *ui.Screen
	at      time.Time
	context string
}

// historyContext deliberately errs toward forgetting. Window changes, headers changing,
// dialogs and keyboard transitions separate histories. Content inside a scroll container
// may move; labels outside it identify the surrounding screen. Identical-looking routes
// cannot be distinguished without app-provided navigation identity.
func historyContext(s *ui.Screen) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%dx%d:%t|", s.Platform, s.Width, s.Height, s.Keyboard)
	for _, w := range s.Windows {
		fmt.Fprintf(&b, "%d:%q:%q:%q:%t|", w.ID, w.Type, w.Package, w.Title, w.Active)
	}
	return b.String() + appContext(s)
}

// appLabels: appContext's entries, as a set.
func appLabels(s *ui.Screen) map[string]bool {
	out := map[string]bool{}
	for _, e := range appEntries(s) {
		out[e] = true
	}
	return out
}

// appContext: the labels outside scroll containers in app windows, historyContext's part
// that names the screen.
func appContext(s *ui.Screen) string {
	var b strings.Builder
	for _, e := range appEntries(s) {
		b.WriteString(e)
		b.WriteByte('|')
	}
	return b.String()
}

// appEntries: each app-window node outside scroll containers with an id or a label, as
// kind, id and label, in tree order.
func appEntries(s *ui.Screen) []string {
	var out []string
	for _, n := range s.Nodes {
		if s.Windows[n.Window].Type != "app" {
			continue
		}
		inside := n.Scrollable
		for p := n.Parent; p != nil; p = p.Parent {
			inside = inside || p.Scrollable
		}
		if !inside && (n.ID != "" || n.Label() != "") {
			out = append(out, fmt.Sprintf("%q:%q:%q", n.Class, n.ID, n.Label()))
		}
	}
	return out
}

// Own the snapshot: drivers and subsequent Link calls must not mutate our history.
func copyScreen(s *ui.Screen) *ui.Screen {
	out := *s
	out.Nodes = nil
	out.Windows = nil
	for _, w := range s.Windows {
		wc := *w
		wc.Nodes = nil
		wc.Roots = nil
		for _, n := range w.Nodes {
			nc := *n
			nc.Parent = nil
			nc.Children = nil
			if n.Coll != nil {
				c := *n.Coll
				nc.Coll = &c
			}
			if n.Item != nil {
				c := *n.Item
				nc.Item = &c
			}
			wc.Nodes = append(wc.Nodes, &nc)
		}
		out.Windows = append(out.Windows, &wc)
	}
	out.Link()
	return &out
}

// Called under Runner.mu, only for actual device reads (not cache hits).
func (r *Runner) rememberScreen(s *ui.Screen) {
	now := time.Now()
	key := historyContext(s)
	if len(s.Nodes) > historyNodeLimit {
		r.history = nil
		return
	}
	if len(r.history) > 0 && r.history[len(r.history)-1].context != key {
		r.tracef("snapshot history: screen context changed; discarding %d snapshots", len(r.history))
		r.history = nil
	}
	for len(r.history) > 0 && now.Sub(r.history[0].at) > historyLifetime {
		r.history = r.history[1:]
	}
	if len(r.history) > 0 && r.history[len(r.history)-1].screen.Signature() == s.Signature() {
		r.history[len(r.history)-1].at = now
		return
	}
	if len(r.history) >= historyLimit {
		copy(r.history, r.history[1:])
		r.history = r.history[:historyLimit-1]
	}
	r.history = append(r.history, screenSnapshot{copyScreen(s), now, key})
}

func (r *Runner) forgetHistory() { r.mu.Lock(); r.history = nil; r.mu.Unlock() }

// History only suggests a search direction, never an input coordinate. Require a unique
// ID-selected target and a single surviving scroll container; don't reinterpret CSS or
// positional selectors against old trees, and don't guess between multiple lists.
func (r *Runner) previouslySeen(sel *match.Selector, now *ui.Screen) (*ui.Node, *ui.Screen, string) {
	if now == nil || sel.ID == "" || sel.Index != "" {
		return nil, nil, ""
	}
	complex := false
	sel.Walk(func(q *match.Selector) {
		if q != sel || q.CSS != "" {
			complex = true
		}
	})
	if complex {
		return nil, nil, ""
	}
	lists := 0
	for _, n := range now.Nodes {
		if n.Scrollable {
			lists++
		}
	}
	if lists != 1 {
		r.tracef("snapshot history: recovery skipped with %d scroll containers", lists)
		return nil, nil, ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := historyContext(now)
	for i := len(r.history) - 1; i >= 0; i-- {
		h := r.history[i]
		if h.context != key || time.Since(h.at) > historyLifetime {
			break
		}
		n, all, err := match.Find(h.screen, sel)
		if err != nil || n == nil {
			continue
		}
		if len(all) != 1 || !n.Clickable {
			return nil, nil, ""
		}
		if dir := awayDir(h.screen, n, n.Bounds, now); dir != "" {
			r.tracef("snapshot history: %s last seen %s ago at %s; its scroll container remains, searching %s", sel.Describe(), time.Since(h.at).Round(time.Millisecond), n.Bounds, dir)
			return n, h.screen, dir
		}
		return nil, nil, ""
	}
	r.tracef("snapshot history: no eligible prior observation for %s (%d snapshots)", sel.Describe(), len(r.history))
	return nil, nil, ""
}

// Bounds may have been clipped before the scroll. Match identity and container instead
// of size, and never accept the same ID with a different label/action.
func sameRecoveredTarget(was *ui.Node, before *ui.Screen, n *ui.Node, now *ui.Screen) bool {
	if was == nil || n == nil || was.ID != n.ID || was.Class != n.Class || was.Label() != n.Label() {
		return false
	}
	a, b := before.Windows[was.Window], now.Windows[n.Window]
	if a.ID != b.ID || a.Type != b.Type || a.Package != b.Package {
		return false
	}
	container := func(n *ui.Node) string {
		for p := n.Parent; p != nil; p = p.Parent {
			if p.Scrollable {
				return listKey(p)
			}
		}
		return ""
	}
	return container(was) != "" && container(was) == container(n)
}
