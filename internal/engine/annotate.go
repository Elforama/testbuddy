package engine

// Annotate mode (Options.Annotate, `tb test --record --annotate`): a recording a person can follow.
//
// It does three things: it holds quick steps (below), and it draws on the finished videos a
// caption naming each step and a ring where each touch went (annotateVideo, with ffmpeg;
// package annotate says why tb draws them rather than the device).
//
// tb acts as soon as the screen lets it, so a recording of a run goes by too fast to see what
// each step did. In annotate mode a step that acts on the app is held after it, so its result
// stays on screen, under its own caption, for the step time (Options.AnnotateStep, 1 s by
// default):
//
//   - a step that touched the screen (a tap, double tap, long press or swipe) is held until the
//     step time has passed since its last touch. Most of a slow step is the wait for its
//     target to show; the touch comes at its end, and without the hold the next step's caption
//     would be up as the touch's result appeared;
//   - another step that acts (typing, a launch, back, ...) is held until it has lasted the step
//     time from its start; one that took that long already is not held.
//
// Checks (assertVisible, ...), scripts and composites (runFlow, repeat) are not held: their
// steps are, one by one. A step that failed is not held either: the flow ends there.
// The hold comes after the step is reported and is not part of its time or of the next
// step's lookup timeout.

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/elforama/testbuddy/internal/annotate"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/timeline"
)

// defaultAnnotateStep is how long a step that acts lasts at least in annotate mode when
// Options.AnnotateStep does not say.
const defaultAnnotateStep = time.Second

// annotateStep is how long a step that acts lasts at least: Options.AnnotateStep, 1 s when
// that is 0, none when it is negative.
func (r *Runner) annotateStep() time.Duration {
	switch d := r.opts.AnnotateStep; {
	case d == 0:
		return defaultAnnotateStep
	case d < 0:
		return 0
	default:
		return d
	}
}

// annotateHold is how long annotate mode holds the screen after a step: what makes step pass
// since its last touch (sinceTouch, when touched), else since its start (took); or nothing.
func annotateHold(step, took, sinceTouch time.Duration, touched bool) time.Duration {
	if touched {
		return max(step-sinceTouch, 0)
	}
	return max(step-took, 0)
}

// holdForViewer holds the screen after step c, which began at start, in annotate mode (see
// above).
func (r *Runner) holdForViewer(ctx context.Context, c *flow.Command, status Status, start time.Time) {
	if !r.opts.Annotate || !actsForDialogs(c) || (status != Passed && status != Warned) {
		return
	}
	touched := !r.lastTouch.Before(start)
	d := annotateHold(r.annotateStep(), time.Since(start), time.Since(r.lastTouch), touched)
	if d <= 0 {
		return
	}
	r.opts.Trace.Printf(r.opts.Role, r.depth+1, "--annotate: holding the screen %s for the recording", d.Round(10*time.Millisecond))
	wait := r.annotateWait
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
	if !r.opts.Annotate || status == Skipped || timeline.IsGroup(c.Name) {
		return
	}
	text := desc
	switch status {
	case Failed:
		text = "Failed: " + desc
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
	if !r.opts.Annotate || w <= 0 || h <= 0 || len(pts) == 0 {
		return
	}
	t := touchNote{at: time.Now(), kind: kind}
	r.lastTouch = t.at
	for _, p := range pts {
		t.pts = append(t.pts, [2]float64{float64(p[0]) / float64(w), float64(p[1]) / float64(h)})
	}
	r.rec.touches = append(r.rec.touches, t)
}

// annotateVideo draws on video v, in place, the captions and touches of its stretch. A video
// that cannot be drawn on (no ffmpeg) is kept as recorded, and the log says why.
func (r *Runner) annotateVideo(v Video) {
	if !r.opts.Annotate {
		return
	}
	caps, touches := videoNotes(r.rec.steps, r.rec.touches, v.Start, v.End, max(r.annotateStep(), captionTail))
	if len(caps) == 0 && len(touches) == 0 {
		return
	}
	burn := r.annotateBurn
	if burn == nil {
		burn = annotate.Burn
	}
	c, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	caps, touches = r.syncToVideo(c, v, caps, touches)
	t := time.Now()
	if err := burn(c, v.Path, caps, touches); err != nil {
		r.logLine("--annotate: the video shows no captions or touches: " + err.Error())
		return
	}
	r.tracef("--annotate: drew %d captions and %d touches on %s in %s", len(caps), len(touches), filepath.Base(v.Path), since(t))
}

// syncTolerance: a shift this small between tb's clock and the video is left alone. It is
// about what Offset cannot tell apart from the app's quickest reaction, and under what a
// person sees.
const syncTolerance = 150 * time.Millisecond

// syncToVideo checks the captions and touches against the video before they are drawn
// (package annotate's sync.go): when the touches line up with the video's changes only
// shifted, and enough of them agree, everything moves by that shift. The log says what it
// found either way.
func (r *Runner) syncToVideo(ctx context.Context, v Video, caps []annotate.Caption, touches []annotate.Touch) ([]annotate.Caption, []annotate.Touch) {
	if ec, ok := r.Drv.(driver.ExactRecordingClock); ok && ec.RecordingClockExact() {
		r.tracef("--annotate: %s is timed by tb's own clock, so in sync: not checked", filepath.Base(v.Path))
		return caps, touches
	}
	if len(touches) < 2 {
		r.tracef("--annotate: %s: too few touches to check the video's sync; drawn by tb's clock", filepath.Base(v.Path))
		return caps, touches
	}
	changesOf := r.annotateChanges
	if changesOf == nil {
		changesOf = annotate.Changes
	}
	changes, err := changesOf(ctx, v.Path)
	if err != nil {
		r.tracef("--annotate: %s: could not check the video's sync: %v", filepath.Base(v.Path), err)
		return caps, touches
	}
	var at []time.Duration
	for _, t := range touches {
		at = append(at, t.At)
	}
	s := annotate.Offset(at, changes)
	name := filepath.Base(v.Path)
	switch {
	case !s.OK:
		r.tracef("--annotate: %s: could not check the video's sync (%d of %d touches line up with a change on screen); drawn by tb's clock", name, s.Matched, s.Touches)
		return caps, touches
	case s.Shift.Abs() <= syncTolerance:
		r.tracef("--annotate: %s is in sync (%d of %d touches line up, within %d ms)", name, s.Matched, s.Touches, s.Shift.Milliseconds())
		return caps, touches
	}
	way := "behind"
	if s.Shift < 0 {
		way = "ahead of"
	}
	r.logLine(fmt.Sprintf("--annotate: %s runs %d ms %s tb's clock (%d of %d touches line up): captions and touches moved to match",
		name, s.Shift.Abs().Milliseconds(), way, s.Matched, s.Touches))
	length := v.End.Sub(v.Start)
	clip := func(d time.Duration) time.Duration { return min(max(d, 0), length) }
	var outCaps []annotate.Caption
	for _, c := range caps {
		c.Start, c.End = clip(c.Start+s.Shift), clip(c.End+s.Shift)
		if c.End > c.Start {
			outCaps = append(outCaps, c)
		}
	}
	var outTouches []annotate.Touch
	for _, t := range touches {
		t.At += s.Shift
		if t.At >= 0 && t.At <= length {
			outTouches = append(outTouches, t)
		}
	}
	return outCaps, outTouches
}

// captionTail is the least the last caption stays after its step (the step time, when longer).
const captionTail = 500 * time.Millisecond

// videoNotes turns the notes into a video's captions and touches, in times from its start,
// keeping those in its stretch [start, end]. The last caption stays tail after its step.
func videoNotes(steps []stepNote, touches []touchNote, start, end time.Time, tail time.Duration) ([]annotate.Caption, []annotate.Touch) {
	length := end.Sub(start)
	var caps []annotate.Caption
	for i, s := range steps {
		e := s.end
		if i+1 < len(steps) && steps[i+1].start.Sub(e) < captionGap {
			e = steps[i+1].start
		} else if i+1 == len(steps) {
			e = e.Add(tail)
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
