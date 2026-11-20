package engine

// Slow mode (Options.Slow, `tb test --record --slow`): a recording a person can follow.
//
// tb acts as soon as the screen lets it, so a recording of a run goes by too fast to see what
// each step did. In slow mode, a step that acts on the app (a tap, typing, a swipe, a
// launch, ...) and took less than slowStep is followed by a hold that makes it last slowStep,
// so its result stays on screen long enough to see. A step that took slowStep or longer is
// not held: it is slow enough already.
//
// Checks (assertVisible, ...), scripts and composites (runFlow, repeat) are not held: their
// steps are, one by one. A step that failed is not held either: the flow ends there.
// The hold comes after the step is reported and is not part of its time or of the next
// step's lookup timeout.

import (
	"context"
	"time"

	"github.com/elforama/testbuddy/internal/flow"
)

// slowStep is how long a step that acts lasts at least in slow mode.
const slowStep = 500 * time.Millisecond

// slowHold is how long slow mode holds the screen after a step that took took: what makes
// it last slowStep, or nothing.
func slowHold(took time.Duration) time.Duration {
	return max(slowStep-took, 0)
}

// holdForViewer holds the screen after step c in slow mode (see above).
func (r *Runner) holdForViewer(ctx context.Context, c *flow.Command, status Status, took time.Duration) {
	if !r.opts.Slow || !actsForDialogs(c) || (status != Passed && status != Warned) {
		return
	}
	d := slowHold(took)
	if d <= 0 {
		return
	}
	r.opts.Trace.Printf(r.opts.Role, r.depth+1, "--slow: holding the screen %s for the recording", d.Round(10*time.Millisecond))
	wait := r.slowWait
	if wait == nil {
		wait = sleepContext
	}
	wait(ctx, d)
	// the next step's lookups count from the end of the hold
	r.lastInteraction = time.Now()
}

func sleepContext(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
