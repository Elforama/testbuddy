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
	Timeout int    `json:"timeout"`
	Until   string `json:"until"`
}

func (s *Service) watch(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[watchArgs](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	d, err := s.resolve(ctx, a.devArgs)
	if err != nil {
		return fail(2, "%v", err)
	}
	d.watching.Add(1)
	defer d.watching.Add(-1)
	timeout := time.Duration(a.Timeout) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	w := changes.Subscribe(d.Drv, changes.SilentAfter(), true)
	defer w.Stop()

	prevRead := false
	what := describe(req)
	read := func(lockFailed string, fresh bool) (*look.Listing, *daemon.Response) {
		var err error
		if prevRead {
			err = s.lockFor(d, what, false, nil)
		} else {
			err = s.lockOpen(d, req, send)
		}
		prevRead = true
		if err != nil {
			r := fail(1, lockFailed, err)
			return nil, &r
		}
		defer d.mu.Unlock()
		var scr *ui.Screen
		if fr, ok := d.Drv.(freshReader); ok && fresh {
			scr, err = fr.ScreenFresh(ctx)
		} else {
			scr, err = d.Drv.Screen(ctx)
		}
		if err != nil {
			r := fail(1, "%v", err)
			return nil, &r
		}
		l := look.Build(scr, d.refs)
		d.last = l
		return l, nil
	}
	prev, failed := read("%v", false)
	if failed != nil {
		return *failed
	}
	send(daemon.Response{OK: true, Text: fmt.Sprintf("watching %s (%s)\n", s.labelOf(d), timeout)})
	if a.Until != "" && hasText(prev, a.Until) {
		return daemon.Response{OK: true, Text: "found " + a.Until + "\n"}
	}
	start := time.Now()
	for {
		silence, err := w.Wait(ctx, prev.Screen.Seq, deadline, false)
		if err != nil {
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
		now, failed := read("lost the device: %v", silence > 0)
		if failed != nil {
			return *failed
		}
		diff := look.Diff(prev, now)
		prev = now
		if diff == "screen unchanged\n" {
			continue
		}
		header := "+" + fmtDur(time.Since(start))
		if silence > 0 {
			header += " (re-read after " + fmtDur(silence) + " of silence)"
		}
		send(daemon.Response{OK: true, Text: header + "\n" + diff})
		if a.Until != "" && hasText(now, a.Until) {
			return daemon.Response{OK: true, Text: "found " + a.Until + "\n"}
		}
	}
}

type freshReader interface {
	ScreenFresh(ctx context.Context) (*ui.Screen, error)
}

func hasText(l *look.Listing, text string) bool {
	for _, e := range l.Elements {
		if strings.Contains(strings.ToLower(e.Label), strings.ToLower(text)) || strings.EqualFold(e.ID, text) {
			return true
		}
	}
	return false
}

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
			if n.Modal {
				b.WriteString(" modal")
			}
			if n.UnderModal {
				b.WriteString(" under-modal")
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}
