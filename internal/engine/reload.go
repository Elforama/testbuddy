package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

var reloadWait = 3 * time.Second

func screenWindow(s *ui.Screen) *ui.Window {
	for i := len(s.Windows) - 1; i >= 0; i-- {
		if s.Windows[i].Type == "app" {
			return s.Windows[i]
		}
	}
	return nil
}

func loadingWhy(s *ui.Screen) string {
	w := screenWindow(s)
	if w == nil || len(w.Nodes) == 0 {
		return "the app's window has no content yet"
	}
	if s.Unread > 0 {
		return fmt.Sprintf("a window of it cannot be read yet (%d without a root)", s.Unread)
	}
	for _, n := range w.Nodes {
		if emptyList(n) {
			return "its list has no content yet (" + nodeDesc(n) + ")"
		}
		if spinning(n) {
			return "it shows a progress indicator (" + nodeDesc(n) + ")"
		}
	}
	return ""
}

func spinning(n *ui.Node) bool {
	if !busyIndicator(n) && n.Class != "ActivityIndicator" && n.Class != "ProgressIndicator" {
		return false
	}
	return !strings.ContainsAny(n.State+n.Value+n.Text, "0123456789")
}

func emptyList(n *ui.Node) bool {
	if len(n.Children) > 0 || n.Label() != "" {
		return false
	}
	if n.Scrollable || n.Coll != nil {
		return true
	}
	c := n.ShortClass()
	return strings.HasSuffix(c, "RecyclerView") || c == "ListView" || c == "GridView"
}

func reloadContext(s *ui.Screen) string {
	w := screenWindow(s)
	if w == nil {
		return ""
	}
	return fmt.Sprintf("%dx%d|%v", s.Width, s.Height, w.Bounds)
}

func (r *Runner) noteContext(s *ui.Screen) {
	key := reloadContext(s)
	if key == "" {
		return
	}
	win := screenWindow(s).ID
	switch {
	case r.ctxKey == "":
		r.ctxKey, r.ctxReady, r.ctxWin, r.ctxRebuilt = key, true, win, true
	case key != r.ctxKey:
		r.ctxKey, r.ctxReady = key, loadingWhy(s) == ""
		r.ctxRebuilt = win != r.ctxWin
		r.ctxWin = win
	case win != r.ctxWin:
		r.ctxWin = win
		if !r.ctxRebuilt {
			r.ctxRebuilt = true
			r.ctxReady = loadingWhy(s) == ""
		}
	}
	if !r.ctxReady && loadingWhy(s) == "" {
		r.ctxReady = true
	}
}

func (r *Runner) reloading(s *ui.Screen) string {
	r.mu.Lock()
	ready, key := r.ctxReady, r.ctxKey
	r.mu.Unlock()
	if ready || key == "" {
		return ""
	}
	if now := reloadContext(s); now != "" && now != key {
		return ""
	}
	return loadingWhy(s)
}

func (r *Runner) reloadJudged() {
	r.mu.Lock()
	r.ctxReady = true
	r.mu.Unlock()
}
