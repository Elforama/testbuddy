package engine

// Trace: a flow run's step-by-step log, written as it happens. It records what tb did and
// why, so a failed run can be understood afterwards without running it again: each command
// as it starts and ends, the element each lookup found (or what it saw when it found none),
// where each tap went and what the device said it did, every wait and what it waited for,
// covers and overlays, dialogs answered, and how scrolls went.
//
// `tb test` writes it to log.txt in each flow's debug folder; TB_DEBUG=1 also prints it to
// stderr as it goes.

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

// Trace is safe for concurrent use: a multi-device flow's devices share one, each line
// tagged with its device's role.
type Trace struct {
	mu    sync.Mutex
	w     io.Writer
	start time.Time
}

// NewTrace writes lines to w, timed from now.
func NewTrace(w io.Writer) *Trace { return &Trace{w: w, start: time.Now()} }

// Printf writes one line: seconds since the start, the role (if any), indentation by depth.
func (t *Trace) Printf(role string, depth int, format string, a ...any) {
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
	// A message over several lines keeps the prefix's indentation.
	b.WriteString(strings.ReplaceAll(strings.TrimRight(msg, "\n"), "\n", "\n"+strings.Repeat(" ", 10+2*max(depth, 0))))
	b.WriteByte('\n')
	t.mu.Lock()
	defer t.mu.Unlock()
	io.WriteString(t.w, b.String())
}

// tracef adds a line to the run's trace, at the current command's depth. Without a trace
// (tb do, tb look), TB_DEBUG=1 prints it to stderr.
func (r *Runner) tracef(format string, a ...any) {
	if r.opts.Trace == nil {
		if debug {
			fmt.Fprintf(os.Stderr, "[tb] "+format+"\n", a...)
		}
		return
	}
	r.opts.Trace.Printf(r.opts.Role, r.depth, format, a...)
}

// traceWait notes a wait that took long enough to matter (50 ms or more).
func (r *Runner) traceWait(start time.Time, what string) {
	if time.Since(start) >= 50*time.Millisecond {
		r.tracef("waited %s %s", since(start), what)
	}
}

// similarDesc lists what on s looks like the text a lookup wanted, for the trace.
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

// nodeDesc names a node for the trace: its kind, text, id and bounds.
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

// actionDesc says what the device reported an action did.
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
	return fmt.Sprintf("%s (sent in %d ms, waited %d ms)", what, res.InjectMs, res.SettleMs)
}

// since is a duration for the trace, in ms.
func since(t time.Time) string { return fmt.Sprintf("%d ms", time.Since(t).Milliseconds()) }
