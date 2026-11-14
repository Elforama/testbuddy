package engine

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/ci"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
)

// stallContainers run other steps: a failure inside one was decided at the step that failed, and
// the container's own time includes its passing steps, whose stalls are not this failure's.
var stallContainers = map[string]bool{"runFlow": true, "repeat": true, "retry": true}

// nameStall puts first in a failed step's message that its device stopped answering, or was
// slow to answer, for a good part of the step (issue #237): a scrollUntilVisible on a CI
// simulator starved for ~45 s failed with "No visible element found ... (after 3 swipes)", as if
// the element were not there. The step's own message is kept after it: the element may really
// be missing. Only a step that failed to find or assert something is named so, once, by the
// step that failed (never by a container around it), and only for a stall of StallFloor or more
// that took at least a quarter of the step; one whose device answered as usual is left as it was.
func (r *Runner) nameStall(c *flow.Command, err error, start time.Time) error {
	if err == nil || !isFlowError(err) {
		return err
	}
	var fe *FlowError
	var nf *NotFound
	switch {
	case errors.As(err, &nf):
		fe = &nf.FlowError
	case errors.As(err, &fe):
	default:
		return err
	}
	if fe.stallChecked {
		return err
	}
	fe.stallChecked = true
	if stallContainers[c.Name] {
		return err
	}
	sr, ok := r.Drv.(driver.StallReporter)
	if !ok {
		return err
	}
	took := time.Since(start)
	st := sr.StallSince(start)
	var extra time.Duration // the CI-mode time the step was given for a stall (issue #243)
	if r.step != nil {
		extra = r.step.extra
	}
	if st.For < r.stallFloor() || (st.For*4 < took && extra == 0) {
		return err
	}
	head := fmt.Sprintf("%s %s for %.1f s of this step's %.1f s", st.Device, stallVerb(st), st.For.Seconds(), took.Seconds())
	if len(st.Signs) > 0 {
		head += " (" + strings.Join(st.Signs, ", ") + ")"
	}
	head += ", so the step may have run out of time"
	if extra > 0 {
		head += fmt.Sprintf(" even with %.1f s more (CI mode)", extra.Seconds())
	}
	r.tracef("%s; the element may still really be missing", head)
	fe.Msg = head + "; the step's own failure: " + fe.Msg
	return err
}

func (r *Runner) stallFloor() time.Duration {
	if r.opts.StallFloor > 0 {
		return r.opts.StallFloor
	}
	return 10 * time.Second
}

func stallVerb(st driver.Stall) string {
	if st.Stopped {
		return "stopped answering"
	}
	return "was slow to answer"
}

// stepClock is the running step's start, and the time stallGrace gave it (0: none yet).
type stepClock struct {
	start time.Time
	extra time.Duration
}

// stallGrace is a CI allowance (package ci; issue #243), asked by a find, scroll or assert wait
// whose deadline has passed. On a starved CI runner the whole simulator can stop answering for
// tens of seconds (issue #237: ~45 s in a 20 s scrollUntilVisible), and the step's time runs out
// while nothing could have answered. When the device stalled for StallFloor (10 s) or more since
// the step began, the wait's deadline moves to the stalled time from now, capped at the wait's
// own timeout: once per step, so an element that is really missing still fails, at most one
// stall's length later, naming the stall and the extra time (nameStall). It reports whether it
// moved the deadline. Outside CI, or with no such stall, the wait ends at its deadline as before.
func (r *Runner) stallGrace(deadline *time.Time, timeout time.Duration) bool {
	if !ci.On() || r.step == nil || r.step.extra > 0 || timeout <= 0 {
		return false
	}
	sr, ok := r.Drv.(driver.StallReporter)
	if !ok {
		return false
	}
	st := sr.StallSince(r.step.start)
	if st.For < r.stallFloor() {
		return false
	}
	extra := min(st.For, timeout)
	r.step.extra = extra
	*deadline = time.Now().Add(extra)
	r.tracef("%s %s for %.1f s: giving the step %.1f s more, once (CI mode)", st.Device, stallVerb(st), st.For.Seconds(), extra.Seconds())
	return true
}
