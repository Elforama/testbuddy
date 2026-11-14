package ios

import (
	"fmt"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// Issue #237: on a busy CI runner the whole simulator (SpringBoard, the app, testmanagerd) can
// be starved for tens of seconds. Screen reads are where it shows: one is given up on by the
// agent (the app did not answer accessibility for 30 s), and the one asked once more waits for
// the app's main thread, still held by the read given up on. A step that fails meanwhile used to
// say only "No visible element found"; the driver keeps these stalls so the step can name them
// (driver.StallReporter).

// stallRead is how long a screen read must take to count as the simulator not answering. Reads
// take 0.05-0.5 s, and up to ~5 s on a busy CI runner; a read the agent gave up on always counts.
var stallRead = 10 * time.Second

// stallRetryHeld: a read asked once more after one given up on that takes this long waited for
// the app's main thread, which the given-up read still held (issue #130's "busy").
var stallRetryHeld = 2 * time.Second

// maxStalls is how many stalls the driver remembers: enough for any one step.
const maxStalls = 16

type stall struct {
	start, end time.Time
	signs      []string
}

// noteStall records a screen read that began at start, if the simulator did not answer it in
// time. givenUp is the agent's reason for a read it gave up on ("" if it answered), retry how
// long the read asked once more after it took (0 if none).
func (d *Driver) noteStall(start time.Time, givenUp string, retry time.Duration) {
	end := time.Now()
	took := end.Sub(start)
	if givenUp == "" && took < stallRead {
		return
	}
	var signs []string
	if givenUp != "" {
		signs = append(signs, "accessibility timeouts")
	}
	if strings.Contains(givenUp, "main thread") || (givenUp != "" && retry >= stallRetryHeld) {
		signs = append(signs, "app main thread held")
	}
	if len(signs) == 0 {
		signs = append(signs, fmt.Sprintf("a screen read took %.1f s", took.Seconds()))
	}
	d.note(fmt.Sprintf("the simulator stopped answering for %.1f s (%s)", took.Seconds(), strings.Join(signs, ", ")))
	d.mu.Lock()
	d.stalls = append(d.stalls, stall{start: start, end: end, signs: signs})
	if len(d.stalls) > maxStalls {
		d.stalls = d.stalls[len(d.stalls)-maxStalls:]
	}
	d.mu.Unlock()
}

// StallSince sums the time the simulator did not answer from t on (driver.StallReporter).
func (d *Driver) StallSince(t time.Time) driver.Stall {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := driver.Stall{Device: "the simulator"}
	seen := map[string]bool{}
	for _, s := range d.stalls {
		if !s.end.After(t) {
			continue
		}
		from := s.start
		if from.Before(t) {
			from = t
		}
		out.For += s.end.Sub(from)
		for _, sign := range s.signs {
			if !seen[sign] {
				seen[sign] = true
				out.Signs = append(out.Signs, sign)
			}
		}
	}
	return out
}
