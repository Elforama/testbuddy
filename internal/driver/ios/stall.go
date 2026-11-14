package ios

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/wire"
)

// Issue #237: on a busy CI runner the whole simulator (SpringBoard, the app, testmanagerd) can
// be starved for tens of seconds. Screen reads are where it shows: one is given up on by the
// agent (the app did not answer accessibility for 30 s), and the one asked once more can wait
// long too. A step that fails meanwhile used to say only "No visible element found"; the driver
// keeps these stalls so the step can name them (driver.StallReporter).

// stallRead is how long a screen read must take to count as the simulator being slow to answer.
// Reads take 0.05-0.5 s, and up to ~5 s on a busy CI runner.
var stallRead = 10 * time.Second

// stallRetryShown: a read asked once more after one given up on that takes this long is shown
// in the stall's signs ("the next read took 3.1 s").
var stallRetryShown = 2 * time.Second

// maxStalls is how many stalls the driver remembers: enough for any one step.
const maxStalls = 16

type stall struct {
	start, end time.Time
	givenUp    bool          // the agent gave up on a read with its full time: it stopped answering
	mainHeld   bool          // ...and said it waited for the app's main thread
	mainWait   time.Duration // how long the read waited for the app's main thread (its mainWaitMs, issue #255; 0: not said)
	retry      time.Duration // how long the read asked once more after it took (0: none)
}

// noteStall records a screen read that began at start, if the simulator did not answer it in
// time. givenUp is the agent's reason for a read it gave up on ("" if it answered), full whether
// that read had its whole time (a read cut short by the run's deadline proves nothing), retry
// how long the read asked once more after it took (0 if none), and m the reply the read got
// (nil if none), whose mainWaitMs says how long it waited for the app's main thread (issue
// #255; an older agent does not send it). That wait is only a sign: it changes no timing.
func (d *Driver) noteStall(start time.Time, givenUp string, full bool, retry time.Duration, m *wire.Msg) {
	end := time.Now()
	s := stall{start: start, end: end, givenUp: givenUp != "" && full, retry: retry}
	if !s.givenUp && end.Sub(start) < stallRead {
		return
	}
	s.mainHeld = s.givenUp && strings.Contains(givenUp, "main thread")
	s.mainWait = mainWait(m)
	d.note(fmt.Sprintf("the simulator %s for %.1f s (%s)", stallVerb(s.givenUp), end.Sub(start).Seconds(), strings.Join(stallSigns([]stall{s}), ", ")))
	d.mu.Lock()
	d.stalls = append(d.stalls, s)
	if len(d.stalls) > maxStalls {
		d.stalls = d.stalls[len(d.stalls)-maxStalls:]
	}
	d.mu.Unlock()
}

// mainWait is the time a tree reply says its read waited for the app's main thread
// (mainWaitMs, issue #255): 0 if it does not say (an older agent, or a wait under 1 s).
func mainWait(m *wire.Msg) time.Duration {
	if m == nil {
		return 0
	}
	var r struct {
		MainWaitMs int64 `json:"mainWaitMs"`
	}
	if m.Decode(&r) != nil || r.MainWaitMs <= 0 {
		return 0
	}
	return time.Duration(r.MainWaitMs) * time.Millisecond
}

func stallVerb(stopped bool) string {
	if stopped {
		return "stopped answering"
	}
	return "was slow to answer"
}

// stallSigns says what showed the stalls: plain facts, as the agent reported them.
func stallSigns(ss []stall) []string {
	timeouts, mainHeld, slow := 0, false, 0
	var retry, slowFor, mainWait time.Duration
	for _, s := range ss {
		mainWait += s.mainWait
		if s.givenUp {
			timeouts++
			mainHeld = mainHeld || s.mainHeld
			retry = max(retry, s.retry)
		} else {
			slow++
			slowFor += s.end.Sub(s.start)
		}
	}
	var signs []string
	switch {
	case timeouts == 1:
		signs = append(signs, "an accessibility timeout")
	case timeouts > 1:
		signs = append(signs, fmt.Sprintf("%d accessibility timeouts", timeouts))
	}
	switch {
	case mainWait > 0: // the agent said how long (issue #255)
		signs = append(signs, fmt.Sprintf("app main thread held for %.1f s", mainWait.Seconds()))
	case mainHeld:
		signs = append(signs, "app main thread held")
	}
	if retry >= stallRetryShown {
		signs = append(signs, fmt.Sprintf("the next read took %.1f s", retry.Seconds()))
	}
	switch {
	case slow == 1:
		signs = append(signs, fmt.Sprintf("one screen read took %.1f s", slowFor.Seconds()))
	case slow > 1:
		signs = append(signs, fmt.Sprintf("%d screen reads took %.1f s", slow, slowFor.Seconds()))
	}
	return signs
}

// StallSince is the time the simulator did not answer, or answered slowly, from t on
// (driver.StallReporter): overlapping stalls count once.
func (d *Driver) StallSince(t time.Time) driver.Stall {
	d.mu.Lock()
	var ss []stall
	for _, s := range d.stalls {
		if s.end.After(t) {
			if s.start.Before(t) {
				s.start = t
			}
			ss = append(ss, s)
		}
	}
	d.mu.Unlock()
	out := driver.Stall{Device: "the simulator"}
	sort.Slice(ss, func(i, j int) bool { return ss[i].start.Before(ss[j].start) })
	var to time.Time // the end of what is counted so far
	for _, s := range ss {
		from := s.start
		if from.Before(to) {
			from = to
		}
		if s.end.After(from) {
			out.For += s.end.Sub(from)
			to = s.end
		}
		out.Stopped = out.Stopped || s.givenUp
	}
	out.Signs = stallSigns(ss)
	return out
}
