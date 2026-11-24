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

var stallContainers = map[string]bool{"runFlow": true, "repeat": true, "retry": true}

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
	var extra time.Duration
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

type stepClock struct {
	start time.Time
	extra time.Duration
}

func (r *Runner) stallGrace(deadline *time.Time, timeout time.Duration, waitStart time.Time) bool {
	if !ci.On() || r.step == nil || r.step.extra > 0 || timeout <= 0 {
		return false
	}
	sr, ok := r.Drv.(driver.StallReporter)
	if !ok {
		return false
	}
	from := r.step.start
	if waitStart.After(from) {
		from = waitStart
	}
	st := sr.StallSince(from)
	if st.For < r.stallFloor() {
		return false
	}
	extra := min(st.For, timeout)
	r.step.extra = extra
	*deadline = time.Now().Add(extra)
	r.tracef("%s %s for %.1f s: giving the step %.1f s more, once (CI mode)", st.Device, stallVerb(st), st.For.Seconds(), extra.Seconds())
	return true
}
