package engine

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// nameStall puts first in a failed step's message that the device stopped answering for a
// good part of the step (issue #237): a scrollUntilVisible on a CI simulator starved for ~45 s
// failed with "No visible element found ... (after 3 swipes)", as if the element were not
// there. The step's own message is kept after it, and only a step that failed to find or
// assert something is named so; one whose device answered as usual is left as it was.
func (r *Runner) nameStall(err error, start time.Time) error {
	if err == nil || !isFlowError(err) {
		return err
	}
	sr, ok := r.Drv.(driver.StallReporter)
	if !ok {
		return err
	}
	st := sr.StallSince(start)
	// A quarter of the step at least: a stall that ate that much of it may be why it ran out.
	if st.For <= 0 || st.For*4 < time.Since(start) {
		return err
	}
	var msg *string
	var nf *NotFound
	var fe *FlowError
	switch {
	case errors.As(err, &nf):
		msg = &nf.Msg
	case errors.As(err, &fe):
		msg = &fe.Msg
	default:
		return err
	}
	head := fmt.Sprintf("%s stopped answering for %.1f s", st.Device, st.For.Seconds())
	if strings.Contains(*msg, st.Device+" stopped answering for") { // named by an inner step
		return err
	}
	if len(st.Signs) > 0 {
		head += " (" + strings.Join(st.Signs, ", ") + ")"
	}
	r.tracef("%s during this step: the failure below may be the stall's, not the app's", head)
	*msg = head + " during this step: " + *msg
	return err
}
