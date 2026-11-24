package ios

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/wire"
)

var stallRead = 10 * time.Second

var stallRetryShown = 2 * time.Second

const maxStalls = 16

type stall struct {
	start, end          time.Time
	givenUp             bool
	mainHeld            bool
	mainWait, heldAfter time.Duration
	untimed             bool
	retry               time.Duration
}

func (d *Driver) noteStall(start time.Time, givenUp string, full bool, retry time.Duration, m *wire.Msg) {
	end := time.Now()
	s := stall{start: start, end: end, givenUp: givenUp != "" && full, retry: retry}
	if !s.givenUp && end.Sub(start) < stallRead {
		return
	}
	s.mainHeld = s.givenUp && strings.Contains(givenUp, "main thread")
	wait := mainWait(m)
	s.untimed = s.mainHeld && wait == 0
	switch {
	case s.mainHeld:
		s.mainWait = end.Sub(start) - retry + wait
	case s.givenUp:
		s.mainWait, s.heldAfter = wait, end.Sub(start)-retry
	default:
		s.mainWait = wait
	}
	d.note(fmt.Sprintf("the simulator %s for %.1f s (%s)", stallVerb(s.givenUp), end.Sub(start).Seconds(), strings.Join(stallSigns([]stall{s}), ", ")))
	d.mu.Lock()
	d.stalls = append(d.stalls, s)
	if len(d.stalls) > maxStalls {
		d.stalls = d.stalls[len(d.stalls)-maxStalls:]
	}
	d.mu.Unlock()
}

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

func stallSigns(ss []stall) []string {
	timeouts, mainHeld, timed, slow := 0, false, false, 0
	var retry, slowFor, mainWait time.Duration
	for _, s := range ss {
		mainWait += s.mainWait
		timed = timed || (s.mainWait > 0 && !s.untimed)
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
	case timed:
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

func (d *Driver) StallSince(t time.Time) driver.Stall {
	d.mu.Lock()
	var ss []stall
	for _, s := range d.stalls {
		if s.end.After(t) {
			if s.start.Before(t) {
				cut := t.Sub(s.start)
				if cut > s.heldAfter {
					s.mainWait = max(0, s.mainWait-(cut-s.heldAfter))
					s.heldAfter = 0
				} else {
					s.heldAfter -= cut
				}
				s.start = t
			}
			ss = append(ss, s)
		}
	}
	d.mu.Unlock()
	out := driver.Stall{Device: "the simulator"}
	sort.Slice(ss, func(i, j int) bool { return ss[i].start.Before(ss[j].start) })
	var to time.Time
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
