package engine

// The run's timeline (Options.Timeline, `tb test --timeline`): each command's start and end,
// a screenshot of the screen each command left, and where each gesture went.
//
// A tap returns as soon as the screen starts reacting, so a screenshot taken right away
// would still show the screen the tap acted on. The screenshot waits for the screen to
// settle first, at most timelineSettle. That only adds waiting: tb never acts sooner or on
// another screen because of it, and no screenshot is taken between finding a target and
// acting on it. Recording a timeline makes a run slower, not different in what it does.

import (
	"context"
	"time"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/timeline"
)

const (
	// timelineScale and timelineFormat keep a timeline's screenshots small enough to embed.
	timelineScale  = 0.5
	timelineFormat = "jpeg"
	// timelineSettle is the longest a screenshot waits for the screen to settle (a screen
	// that never does, a ticker, is shown as it is then).
	timelineSettle = time.Second
)

// timelineStart takes the screenshot the first command's "before" shows.
func (r *Runner) timelineStart(ctx context.Context) {
	r.shotDone = false
	r.timelineShot(ctx)
}

// timelineEnd records a command's end, with a screenshot of the screen it left when it is a
// step of its own (not a group of commands) and the screen changed since the last one.
func (r *Runner) timelineEnd(ctx context.Context, step int, c *flow.Command, desc string, status Status, err error) {
	tl := r.opts.Timeline
	if tl == nil {
		return
	}
	if status != Skipped && !timeline.IsGroup(c.Name) && c.On == "" {
		r.timelineShot(ctx)
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	tl.End(step, desc, string(status), msg)
}

// timelineShot screenshots the device once its screen settles, unless nothing changed since
// the last screenshot.
func (r *Runner) timelineShot(ctx context.Context) {
	tl := r.opts.Timeline
	if tl == nil {
		return
	}
	// A failed command's context may be done; its screen is still wanted.
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	start := time.Now()
	r.Drv.WaitSettled(c, timelineSettle)
	if waited := time.Since(start); waited >= 50*time.Millisecond {
		r.tracef("timeline: waited %d ms for the screen to settle before its screenshot", waited.Milliseconds())
	}
	seq := r.Drv.Seq()
	if r.shotDone && seq == r.shotSeq {
		return
	}
	img, err := r.Drv.Screenshot(c, timelineScale, timelineFormat)
	if err != nil {
		r.tracef("timeline screenshot failed: %v", err)
		return
	}
	w, h := r.Drv.Size()
	tl.Shot(r.opts.Role, "image/jpeg", img, w, h)
	r.shotSeq, r.shotDone = seq, true
}

// mark records where a gesture went, in the device's coordinates, for the timeline.
func (r *Runner) mark(kind string, pts ...[2]int) {
	if r.opts.Timeline == nil {
		return
	}
	w, h := r.Drv.Size()
	r.opts.Timeline.Gesture(r.opts.Role, kind, w, h, pts...)
}
