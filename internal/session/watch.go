package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/look"
	"github.com/elforama/testbuddy/internal/ui"
)

type watchArgs struct {
	devArgs
	Timeout int    `json:"timeout"` // ms; 0 = 30 s
	Until   string `json:"until"`   // stop when an element with this text appears
}

// watch streams what changes on screen, as it happens: each time the device reports the UI
// settled after a change, the new listing is diffed against the last one and sent. Nothing
// is read while the screen is still.
func (s *Service) watch(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[watchArgs](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	d, err := s.resolve(ctx, a.devArgs)
	if err != nil {
		return fail(2, "%v", err)
	}
	timeout := time.Duration(a.Timeout) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	events, stop := d.Drv.Events()
	defer stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	d.mu.Lock()
	scr, err := d.Drv.Screen(ctx)
	if err != nil {
		d.mu.Unlock()
		return fail(1, "%v", err)
	}
	prev := look.Build(scr, d.refs)
	d.last = prev
	d.mu.Unlock()
	send(daemon.Response{OK: true, Text: fmt.Sprintf("watching %s (%s)\n", d.label(), timeout)})
	if a.Until != "" && hasText(prev, a.Until) {
		return daemon.Response{OK: true, Text: "found " + a.Until + "\n"}
	}
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			return daemon.Response{OK: true}
		case <-deadline.C:
			if a.Until != "" {
				return daemon.Response{Exit: 1, Text: fmt.Sprintf("%q did not appear within %s\n", a.Until, timeout)}
			}
			return daemon.Response{OK: true, Text: "watch ended\n"}
		case e, ok := <-events:
			if !ok || e.Kind == "disconnected" {
				return fail(1, "lost the device")
			}
			if e.Kind != "settled" {
				continue
			}
			d.mu.Lock()
			scr, err := d.Drv.Screen(ctx)
			if err != nil {
				d.mu.Unlock()
				return fail(1, "%v", err)
			}
			now := look.Build(scr, d.refs)
			d.last = now
			d.mu.Unlock()
			diff := look.Diff(prev, now)
			prev = now
			if diff == "screen unchanged\n" {
				continue
			}
			send(daemon.Response{OK: true, Text: fmt.Sprintf("+%s\n%s", fmtDur(time.Since(start)), diff)})
			if a.Until != "" && hasText(now, a.Until) {
				return daemon.Response{OK: true, Text: "found " + a.Until + "\n"}
			}
		}
	}
}

func hasText(l *look.Listing, text string) bool {
	for _, e := range l.Elements {
		if strings.Contains(strings.ToLower(e.Label), strings.ToLower(text)) || strings.EqualFold(e.ID, text) {
			return true
		}
	}
	return false
}

// dumpNodes lists every node, indented by depth (look --all).
func dumpNodes(s *ui.Screen) string {
	var b strings.Builder
	for _, w := range s.Windows {
		fmt.Fprintf(&b, "window %s %s\n", w.Type, w.Package)
		for _, n := range w.Nodes {
			b.WriteString(strings.Repeat("  ", n.Depth+1))
			b.WriteString(n.ShortClass())
			if n.Text != "" {
				fmt.Fprintf(&b, " %q", n.Text)
			}
			if n.Desc != "" && n.Desc != n.Text {
				fmt.Fprintf(&b, " desc=%q", n.Desc)
			}
			if n.ID != "" {
				b.WriteString(" #" + look.ShortID(n.ID))
			}
			if n.Hint != "" {
				fmt.Fprintf(&b, " hint=%q", n.Hint)
			}
			b.WriteString(" " + n.Bounds.String())
			if n.Clickable {
				b.WriteString(" clickable")
			}
			if !n.Enabled {
				b.WriteString(" disabled")
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}
