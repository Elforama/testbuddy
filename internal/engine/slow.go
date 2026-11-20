package engine

// Slow mode (Options.Slow, `tb test --record --slow`): a recording a person can follow.
//
// It does three things: it holds quick steps (below), and it draws on the finished videos a
// caption naming each step and a ring where each touch went (annotateVideo, with ffmpeg;
// package annotate says why tb draws them rather than the device).
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
	"path/filepath"
	"time"

	"github.com/elforama/testbuddy/internal/annotate"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/timeline"
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

// stepNote is a step for the videos' captions: its text, from its start to its end (its
// hold included).
type stepNote struct {
	start, end time.Time
	text       string
}

// touchNote is a gesture for the videos: when, its kind, and its points as fractions of the
// screen.
type touchNote struct {
	at   time.Time
	kind string
	pts  [][2]float64
}

// captionGap: a gap this short between two steps' captions is closed (the first stays up
// until the next begins), so the caption does not flicker off between steps.
const captionGap = time.Second

// noteStep notes a step that ran for the captions: each step of its own (not a group such as
// runFlow, whose steps are noted one by one).
func (r *Runner) noteStep(c *flow.Command, desc string, status Status, err error, start time.Time) {
	if !r.opts.Slow || status == Skipped || timeline.IsGroup(c.Name) {
		return
	}
	text := desc
	switch status {
	case Failed:
		text = "✗ " + desc
		if err != nil {
			text += " — " + err.Error()
		}
	case Warned:
		text = desc + " (optional: did not hold)"
	}
	r.rec.steps = append(r.rec.steps, stepNote{start: start, end: time.Now(), text: text})
}

// noteTouch notes a gesture for the videos; w and h are the screen's size, in the points'
// coordinates.
func (r *Runner) noteTouch(kind string, w, h int, pts [][2]int) {
	if !r.opts.Slow || w <= 0 || h <= 0 || len(pts) == 0 {
		return
	}
	t := touchNote{at: time.Now(), kind: kind}
	for _, p := range pts {
		t.pts = append(t.pts, [2]float64{float64(p[0]) / float64(w), float64(p[1]) / float64(h)})
	}
	r.rec.touches = append(r.rec.touches, t)
}

// annotateVideo draws on video v, in place, the captions and touches of its stretch. A video
// that cannot be drawn on (no ffmpeg) is kept as recorded, and the log says why.
func (r *Runner) annotateVideo(v Video) {
	if !r.opts.Slow {
		return
	}
	caps, touches := videoNotes(r.rec.steps, r.rec.touches, v.Start, v.End)
	if len(caps) == 0 && len(touches) == 0 {
		return
	}
	burn := r.slowBurn
	if burn == nil {
		burn = annotate.Burn
	}
	c, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	t := time.Now()
	if err := burn(c, v.Path, caps, touches); err != nil {
		r.logLine("--slow: the video shows no captions or touches: " + err.Error())
		return
	}
	r.tracef("--slow: drew %d captions and %d touches on %s in %s", len(caps), len(touches), filepath.Base(v.Path), since(t))
}

// videoNotes turns the notes into a video's captions and touches, in times from its start,
// keeping those in its stretch [start, end].
func videoNotes(steps []stepNote, touches []touchNote, start, end time.Time) ([]annotate.Caption, []annotate.Touch) {
	length := end.Sub(start)
	var caps []annotate.Caption
	for i, s := range steps {
		e := s.end
		if i+1 < len(steps) && steps[i+1].start.Sub(e) < captionGap {
			e = steps[i+1].start
		} else if i+1 == len(steps) {
			e = e.Add(slowStep)
		}
		a, b := max(s.start.Sub(start), 0), min(e.Sub(start), length)
		if b > a {
			caps = append(caps, annotate.Caption{Start: a, End: b, Text: s.text})
		}
	}
	var ts []annotate.Touch
	for _, t := range touches {
		at := t.at.Sub(start)
		if at < 0 || at > length {
			continue
		}
		ts = append(ts, annotate.Touch{At: at, Kind: t.kind, Pts: t.pts})
	}
	return caps, ts
}
