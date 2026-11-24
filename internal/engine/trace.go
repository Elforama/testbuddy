package engine

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/look"
	"github.com/elforama/testbuddy/internal/ui"
)

type Trace struct {
	mu    sync.Mutex
	w     io.Writer
	start time.Time
	tee   func(role string, depth int, msg string)
}

func (t *Trace) Tee(fn func(role string, depth int, msg string)) {
	if t != nil {
		t.mu.Lock()
		t.tee = fn
		t.mu.Unlock()
	}
}

func NewTrace(w io.Writer) *Trace { return &Trace{w: w, start: time.Now()} }

func (t *Trace) Printf(role string, depth int, format string, a ...any) {
	t.print(true, role, depth, format, a...)
}

func (t *Trace) command(role string, depth int, format string, a ...any) {
	t.print(false, role, depth, format, a...)
}

func (t *Trace) print(tee bool, role string, depth int, format string, a ...any) {
	if t == nil || t.w == nil {
		return
	}
	msg := fmt.Sprintf(format, a...)
	var b strings.Builder
	fmt.Fprintf(&b, "%8.3fs ", time.Since(t.start).Seconds())
	if role != "" {
		b.WriteString("[" + role + "] ")
	}
	b.WriteString(strings.Repeat("  ", max(depth, 0)))
	b.WriteString(strings.ReplaceAll(strings.TrimRight(msg, "\n"), "\n", "\n"+strings.Repeat(" ", 10+2*max(depth, 0))))
	b.WriteByte('\n')
	t.mu.Lock()
	io.WriteString(t.w, b.String())
	fn := t.tee
	t.mu.Unlock()
	if tee && fn != nil {
		fn(role, depth, strings.TrimRight(msg, "\n"))
	}
}

func (r *Runner) tracef(format string, a ...any) {
	if r.opts.Trace == nil {
		if debug {
			fmt.Fprintf(os.Stderr, "[tb] "+format+"\n", a...)
		}
		return
	}
	r.opts.Trace.Printf(r.opts.Role, r.depth, format, a...)
}

func (r *Runner) traceNotes() {
	if n, ok := r.Drv.(driver.Noter); ok {
		for _, note := range n.Notes() {
			r.tracef("%s", note)
		}
	}
}

func (r *Runner) traceWait(start time.Time, what string) {
	if time.Since(start) >= 50*time.Millisecond {
		r.tracef("waited %s %s", since(start), what)
	}
}

func similarDesc(s *ui.Screen, query string) string {
	if s == nil || query == "" {
		return ""
	}
	l := look.Build(s, look.NewRefs())
	var parts []string
	for _, e := range l.Similar(query) {
		parts = append(parts, strings.TrimPrefix(e.Line(), fmt.Sprintf("@%d ", e.Ref)))
	}
	return strings.Join(parts, "; ")
}

func nodeDesc(n *ui.Node) string {
	if n == nil {
		return "nothing"
	}
	var b strings.Builder
	b.WriteString(n.ShortClass())
	if l := n.Label(); l != "" {
		if len(l) > 60 {
			l = l[:57] + "..."
		}
		fmt.Fprintf(&b, " %q", l)
	}
	if n.ID != "" {
		b.WriteString(" #" + n.ID)
	}
	if !n.Enabled {
		b.WriteString(" disabled")
	}
	b.WriteString(" " + n.Bounds.String())
	return b.String()
}

func actionDesc(res driver.ActionResult) string {
	var what string
	switch res.Settle {
	case driver.Changed:
		what = "the screen changed"
	case driver.NoChange:
		what = "nothing changed"
	case driver.Restless:
		what = "the screen was still changing when the wait ran out"
	default:
		what = "no settle report"
	}
	held := ""
	if res.WindowWait > 0 {
		held = fmt.Sprintf(", held %d ms for its new window to take input", res.WindowWait.Milliseconds())
	}
	if d := deliveryDesc(res); d != "" {
		held += "; " + d
	}
	return fmt.Sprintf("%s (sent in %d ms, waited %d ms%s)", what, res.InjectMs, res.SettleMs, held)
}

func deliveryDesc(res driver.ActionResult) string {
	var parts []string
	switch {
	case res.Withheld != "":
		parts = append(parts, "touch withheld at "+res.Withheld)
	case res.Cancelled || res.Touch == "cancelled":
		parts = append(parts, "touch cancelled")
	case res.Targeted:
		parts = append(parts, "touch targeted")
	case res.Touch == "ended":
		parts = append(parts, "touch lifted, not seen on the target")
	case res.Touch == "none":
		parts = append(parts, "no touch receipt")
	case res.Reached:
		parts = append(parts, "touch reached the target")
	case res.Guarded && !res.Probed:
		parts = append(parts, "no probe")
	}
	if res.Guarded && res.Clicked != nil && *res.Clicked {
		parts = append(parts, "clicked")
	}
	return strings.Join(parts, ", ")
}

func since(t time.Time) string { return fmt.Sprintf("%d ms", time.Since(t).Milliseconds()) }
