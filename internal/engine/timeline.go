package engine

import (
	"context"
	"time"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/timeline"
)

const (
	timelineScale  = 0.5
	timelineFormat = "jpeg"
	timelineSettle = time.Second
)

func (r *Runner) timelineStart(ctx context.Context) {
	r.shotDone = false
	r.timelineShot(ctx)
}

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

func (r *Runner) timelineShot(ctx context.Context) {
	tl := r.opts.Timeline
	if tl == nil {
		return
	}
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

func (r *Runner) mark(kind string, pts ...[2]int) {
	if r.opts.Timeline == nil && !r.opts.Annotate {
		return
	}
	w, h := r.Drv.Size()
	r.opts.Timeline.Gesture(r.opts.Role, kind, w, h, pts...)
	r.noteTouch(kind, w, h, pts)
}
