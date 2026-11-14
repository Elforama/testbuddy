package engine

import (
	"errors"
	"fmt"
	"strings"
	"time"

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
	floor := r.opts.StallFloor
	if floor <= 0 {
		floor = 10 * time.Second
	}
	if st.For < floor || st.For*4 < took {
		return err
	}
	verb := "was slow to answer"
	if st.Stopped {
		verb = "stopped answering"
	}
	head := fmt.Sprintf("%s %s for %.1f s of this step's %.1f s", st.Device, verb, st.For.Seconds(), took.Seconds())
	if len(st.Signs) > 0 {
		head += " (" + strings.Join(st.Signs, ", ") + ")"
	}
	head += ", so the step may have run out of time"
	r.tracef("%s; the element may still really be missing", head)
	fe.Msg = head + "; the step's own failure: " + fe.Msg
	return err
}
