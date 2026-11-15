package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/changes"
	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/look"
	"github.com/elforama/testbuddy/internal/ui"
)

type watchArgs struct {
	devArgs
	Timeout int    `json:"timeout"` // ms; 0 = 30 s
	Until   string `json:"until"`   // stop when an element with this text appears
}

// silentAfter is the first gap of watch's silence safety net (changes.Watch, as the runner's
// lookups use it): without news from the device for this long, the screen is read again.
var silentAfter = changes.SilentAfter

// watch streams what changes on screen, as it happens: each time the screen may differ from
// the last read, it is read again, diffed against the last listing, and the diff is sent.
// It waits the way the runner's lookups do (changes.Watch): news from the device (a change
// after the last read) wakes it once the change settles, or within changes.RestlessAfter on a
// screen that never stops changing; events without news are ignored; and with no news for
// silentAfter (then twice that), it reads the screen anyway, for content the device does not
// report (iOS's embedded Safari view, an OAuth sheet: another process draws them; issue #288).
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
	deadline := time.Now().Add(timeout)
	// Subscribe before the first read, so no change after it is missed.
	w := changes.Subscribe(d.Drv, silentAfter, true)
	defer w.Stop()

	// read lists the screen and leaves it as the one @refs resolve against. The session's lock
	// is held only for the read, so other commands can run while watch waits.
	read := func(lockFailed string) (*look.Listing, *daemon.Response) {
		if err := s.lockOpen(d); err != nil {
			r := fail(1, lockFailed, err)
			return nil, &r
		}
		defer d.mu.Unlock()
		scr, err := d.Drv.Screen(ctx)
		if err != nil {
			r := fail(1, "%v", err)
			return nil, &r
		}
		l := look.Build(scr, d.refs)
		d.last = l
		return l, nil
	}
	prev, failed := read("%v")
	if failed != nil {
		return *failed
	}
	send(daemon.Response{OK: true, Text: fmt.Sprintf("watching %s (%s)\n", s.labelOf(d), timeout)})
	if a.Until != "" && hasText(prev, a.Until) {
		return daemon.Response{OK: true, Text: "found " + a.Until + "\n"}
	}
	start := time.Now()
	for {
		if _, err := w.Wait(ctx, prev.Screen.Seq, deadline, false); err != nil {
			if ctx.Err() != nil {
				return daemon.Response{OK: true}
			}
			return fail(1, "lost the device")
		}
		if !time.Now().Before(deadline) {
			if a.Until != "" {
				return daemon.Response{Exit: 1, Text: fmt.Sprintf("%q did not appear within %s\n", a.Until, timeout)}
			}
			return daemon.Response{OK: true, Text: "watch ended\n"}
		}
		now, failed := read("lost the device: %v")
		if failed != nil {
			return *failed
		}
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
