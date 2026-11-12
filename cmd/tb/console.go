package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/look"
	"github.com/elforama/testbuddy/internal/orchestrate"
	"github.com/elforama/testbuddy/internal/ui"
)

// console prints a run as it happens: one line per finished command, indented by depth,
// with its time. With several devices at once, lines carry the device in brackets.
type console struct {
	mu     sync.Mutex
	w      io.Writer
	quiet  bool
	prefix bool
	color  bool
	start  time.Time
}

func newConsole(w io.Writer, quiet, prefix bool) *console {
	color := false
	if f, ok := w.(*os.File); ok {
		if st, err := f.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 && os.Getenv("NO_COLOR") == "" {
			color = true
		}
	}
	return &console{w: w, quiet: quiet, prefix: prefix, color: color, start: time.Now()}
}

func (c *console) paint(code, s string) string {
	if !c.color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func dur(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return d.Round(time.Second).String()
}

func (c *console) printf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(c.w, format, args...)
}

func (c *console) reporter(flowName, device string) engine.Reporter {
	return engine.ReporterFunc(func(e engine.Event) {
		if c.quiet {
			return
		}
		tag := ""
		if c.prefix || e.Device != "" {
			who := device
			if e.Device != "" {
				who = e.Device
			}
			tag = c.paint("2", "["+who+"] ")
		}
		if e.Log != "" {
			c.printf("%s    %s\n", tag, c.paint("2", "log: "+e.Log))
			return
		}
		if e.Note != "" {
			c.printf("%s    %s\n", tag, c.paint("2", "note: "+e.Note))
			return
		}
		if e.Start {
			return
		}
		indent := strings.Repeat("  ", e.Depth+1)
		mark := map[engine.Status]string{
			engine.Passed: c.paint("32", "✓"), engine.Warned: c.paint("33", "⚠"),
			engine.Skipped: c.paint("2", "○"), engine.Failed: c.paint("31", "✗"),
		}[e.Status]
		c.printf("%s%s%s %s  %s\n", tag, indent, mark, e.Desc, c.paint("2", dur(e.Elapsed)))
		if e.Err != nil && e.Status != engine.Passed {
			for _, line := range strings.Split(strings.TrimSpace(e.Err.Error()), "\n") {
				c.printf("%s%s    %s\n", tag, indent, c.paint("2", line))
			}
		}
	})
}

func (c *console) flowStart(j *orchestrate.Job, device string) {
	if c.quiet {
		return
	}
	retry := ""
	if j.Attempt > 0 {
		retry = fmt.Sprintf(" (retry %d)", j.Attempt)
	}
	c.printf("%s %s %s%s\n", c.paint("1", "▶"), c.paint("1", j.Flow.Name()), c.paint("2", "on "+device), retry)
}

func (c *console) flowEnd(r orchestrate.Result, debugDir string) {
	name := r.Job.Flow.Name()
	switch r.Status {
	case engine.Failed:
		c.printf("%s %s failed in %s on %s\n", c.paint("31", "✗"), c.paint("1", name), dur(r.Duration), orchestrate.DeviceLabel(r.Devices))
		if r.Err != nil {
			c.printf("  %s\n", strings.ReplaceAll(strings.TrimSpace(r.Err.Error()), "\n", "\n  "))
		}
		var nf *engine.NotFound
		if errors.As(r.Err, &nf) && nf.Screen != nil {
			c.printScreen(nf.Screen, nf.Query)
		}
		c.printf("  debug: %s (log.txt: what tb did, step by step)\n", debugDir)
	case engine.Skipped:
		if !c.quiet {
			c.printf("%s %s skipped\n", c.paint("2", "○"), name)
		}
	default:
		if !c.quiet {
			c.printf("%s %s passed in %s\n", c.paint("32", "✓"), c.paint("1", name), dur(r.Duration))
		}
	}
}

// printScreen shows what was on screen when an element was not found, plus similar
// elements: often all an agent needs to fix the selector.
func (c *console) printScreen(s *ui.Screen, query string) {
	l := look.Build(s, look.NewRefs())
	if sim := l.Similar(query); len(sim) > 0 && query != "" {
		var parts []string
		for _, e := range sim {
			parts = append(parts, strings.TrimPrefix(e.Line(), fmt.Sprintf("@%d ", e.Ref)))
		}
		c.printf("  similar: %s\n", strings.Join(parts, "; "))
	}
	c.printf("  screen was:\n")
	for _, line := range strings.Split(strings.TrimSpace(l.Text()), "\n") {
		c.printf("    %s\n", line)
	}
}

func (c *console) summary(results []orchestrate.Result) {
	pass, fail, skip := 0, 0, 0
	for _, r := range results {
		switch r.Status {
		case engine.Failed:
			fail++
		case engine.Skipped:
			skip++
		default:
			pass++
		}
	}
	total := time.Since(c.start)
	line := fmt.Sprintf("%d passed", pass)
	if fail > 0 {
		line += ", " + c.paint("31", fmt.Sprintf("%d failed", fail))
	}
	if skip > 0 {
		line += fmt.Sprintf(", %d skipped", skip)
	}
	c.printf("\n%s in %s\n", line, dur(total))
}
