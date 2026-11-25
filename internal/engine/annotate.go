package engine

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

const defaultAnnotateStep = time.Second

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

func annotateHold(step, took, sinceTouch time.Duration, touched bool) time.Duration {
	if touched {
		return max(step-sinceTouch, 0)
	}
	return max(step-took, 0)
}

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

type stepNote struct {
	start, end time.Time
	text       string
}

type touchNote struct {
	at   time.Time
	kind string
	pts  [][2]float64
}

const captionGap = time.Second

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

const maxVideoEvents = 2000

type AnnotateEvent struct {
	At   time.Time
	Text string
}

func (r *Runner) annotateVideo(v Video) {
	if !r.opts.Annotate {
		return
	}
	caps, touches := videoNotes(r.rec.steps, r.rec.touches, v.Start, v.End, max(r.annotateStep(), captionTail))
	var events []annotate.Event
	if r.opts.AnnotateEvents != nil {
		events = videoEvents(r.opts.AnnotateEvents(r.opts.Role, r.flowBegan, v.Start, v.End), v.Start, v.End)
	}
	if len(caps) == 0 && len(touches) == 0 && len(events) == 0 {
		return
	}
	if n := len(events); n > maxVideoEvents {
		events = events[:maxVideoEvents]
		r.logLine(fmt.Sprintf("--annotate: %s shows its first %d events; %d more came while it recorded", filepath.Base(v.Path), maxVideoEvents, n-maxVideoEvents))
	}
	burn := r.annotateBurn
	if burn == nil {
		burn = annotate.Burn
	}
	c, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	if shift := r.videoShift(c, v, touches); shift != 0 {
		caps, touches = shiftNotes(v, caps, touches, shift)
		events = shiftEvents(v, events, shift)
	}
	t := time.Now()
	if err := burn(c, v.Path, annotate.Drawing{Caps: caps, Touches: touches, Events: events}); err != nil {
		if len(events) == 0 {
			r.logLine("--annotate: the video shows no captions or touches: " + err.Error())
			return
		}
		r.logLine(fmt.Sprintf("--annotate: the video shows none of its %d events: %v", len(events), err))
		events = nil
		if len(caps) == 0 && len(touches) == 0 {
			return
		}
		c2, cancel2 := context.WithTimeout(context.Background(), stopTimeout)
		defer cancel2()
		if err := burn(c2, v.Path, annotate.Drawing{Caps: caps, Touches: touches}); err != nil {
			r.logLine("--annotate: the video shows no captions or touches: " + err.Error())
			return
		}
	}
	r.tracef("--annotate: drew %d captions, %d touches and %d events on %s in %s", len(caps), len(touches), len(events), filepath.Base(v.Path), since(t))
}

func videoEvents(evs []AnnotateEvent, start, end time.Time) []annotate.Event {
	length := end.Sub(start)
	var out []annotate.Event
	for _, e := range evs {
		at := e.At.Sub(start)
		if at > length || e.Text == "" {
			continue
		}
		out = append(out, annotate.Event{At: at, Text: e.Text})
	}
	return out
}

func shiftEvents(v Video, events []annotate.Event, shift time.Duration) []annotate.Event {
	length := v.End.Sub(v.Start)
	var out []annotate.Event
	for _, e := range events {
		e.At += shift
		if e.At <= length {
			out = append(out, e)
		}
	}
	return out
}

const syncTolerance = 150 * time.Millisecond

func (r *Runner) syncToVideo(ctx context.Context, v Video, caps []annotate.Caption, touches []annotate.Touch) ([]annotate.Caption, []annotate.Touch) {
	if shift := r.videoShift(ctx, v, touches); shift != 0 {
		return shiftNotes(v, caps, touches, shift)
	}
	return caps, touches
}

func (r *Runner) videoShift(ctx context.Context, v Video, touches []annotate.Touch) time.Duration {
	if ec, ok := r.Drv.(driver.ExactRecordingClock); ok && ec.RecordingClockExact() {
		r.tracef("--annotate: %s is timed by tb's own clock, so in sync: not checked", filepath.Base(v.Path))
		return 0
	}
	if shift, ok := r.syncToFrameClock(ctx, v); ok {
		return shift
	}
	if shift, ok := r.syncToMark(ctx, v); ok {
		return shift
	}
	if len(touches) < 2 {
		r.tracef("--annotate: %s: too few touches to check the video's sync; drawn by tb's clock", filepath.Base(v.Path))
		return 0
	}
	changesOf := r.annotateChanges
	if changesOf == nil {
		changesOf = annotate.Changes
	}
	changes, err := changesOf(ctx, v.Path)
	if err != nil {
		r.tracef("--annotate: %s: could not check the video's sync: %v", filepath.Base(v.Path), err)
		return 0
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
		return 0
	case s.Shift.Abs() <= syncTolerance:
		r.tracef("--annotate: %s is in sync (%d of %d touches line up, within %d ms)", name, s.Matched, s.Touches, s.Shift.Milliseconds())
		return 0
	}
	way := "behind"
	if s.Shift < 0 {
		way = "ahead of"
	}
	r.logLine(fmt.Sprintf("--annotate: %s runs %d ms %s tb's clock (%d of %d touches line up): captions and touches moved to match",
		name, s.Shift.Abs().Milliseconds(), way, s.Matched, s.Touches))
	return s.Shift
}

func shiftNotes(v Video, caps []annotate.Caption, touches []annotate.Touch, shift time.Duration) ([]annotate.Caption, []annotate.Touch) {
	length := v.End.Sub(v.Start)
	clip := func(d time.Duration) time.Duration { return min(max(d, 0), length) }
	var outCaps []annotate.Caption
	for _, c := range caps {
		c.Start, c.End = clip(c.Start+shift), clip(c.End+shift)
		if c.End > c.Start {
			outCaps = append(outCaps, c)
		}
	}
	var outTouches []annotate.Touch
	for _, t := range touches {
		t.At += shift
		if t.At >= 0 && t.At <= length {
			outTouches = append(outTouches, t)
		}
	}
	return outCaps, outTouches
}

func (r *Runner) syncToFrameClock(ctx context.Context, v Video) (time.Duration, bool) {
	vc, ok := r.Drv.(driver.VideoClock)
	if !ok {
		return 0, false
	}
	name := filepath.Base(v.Path)
	first, ok := vc.VideoStarted(ctx, v.Path)
	if !ok {
		r.tracef("--annotate: %s has no frame times tb can read; checking it another way", name)
		return 0, false
	}
	shift := v.Start.Sub(first)
	if shift.Abs() > frameClockLimit {
		r.tracef("--annotate: %s: its frame times put it %d ms off tb's clock, too far to believe; checking it another way", name, shift.Milliseconds())
		return 0, false
	}
	if shift.Abs() <= syncTolerance {
		r.tracef("--annotate: %s is in sync (its frame times say so, within %d ms)", name, shift.Milliseconds())
		return 0, true
	}
	way := "behind"
	if shift < 0 {
		way = "ahead of"
	}
	r.logLine(fmt.Sprintf("--annotate: %s runs %d ms %s tb's clock (its frame times say so): captions and touches moved to match",
		name, shift.Abs().Milliseconds(), way))
	return shift, true
}

const frameClockLimit = 30 * time.Second

func (r *Runner) syncToMark(ctx context.Context, v Video) (time.Duration, bool) {
	rm, ok := r.Drv.(driver.RecordingMark)
	if !ok {
		return 0, false
	}
	shown, held, ok := rm.RecordingMark()
	if !ok || shown.Before(v.Start.Add(-10*time.Second)) || shown.After(v.End) {
		return 0, false
	}
	marksOf := r.annotateMarks
	if marksOf == nil {
		marksOf = annotate.MarkChanges
	}
	name := filepath.Base(v.Path)
	changes, err := marksOf(ctx, v.Path)
	if err != nil {
		r.tracef("--annotate: %s: could not look for the start mark: %v", name, err)
		return 0, false
	}
	at, ok := annotate.FindMark(changes, held)
	if !ok {
		r.tracef("--annotate: %s: the start mark (held %d ms) is not in the video (status bar changes %v); checking the touches", name, held.Milliseconds(), changes)
		return 0, false
	}
	shift := at - shown.Sub(v.Start)
	if shift.Abs() <= syncTolerance {
		r.tracef("--annotate: %s is in sync (its start mark shows at %.2f s, within %d ms)", name, at.Seconds(), shift.Milliseconds())
		return 0, true
	}
	way := "behind"
	if shift < 0 {
		way = "ahead of"
	}
	r.logLine(fmt.Sprintf("--annotate: %s runs %d ms %s tb's clock (its start mark shows at %.2f s): captions and touches moved to match",
		name, shift.Abs().Milliseconds(), way, at.Seconds()))
	return shift, true
}

const captionTail = 500 * time.Millisecond

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
