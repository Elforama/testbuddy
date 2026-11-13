package engine

// The run's recording (Options.Record, `tb test --record`): the device's screen from the
// flow's start to its end, as video in the flow's artifacts folder.
//
// A device makes one recording at a time. When the flow starts its own (startRecording),
// the run's recording stops there and starts again after the flow's stopRecording; the
// flow's file covers the stretch in between, so the run's videos (recording.mp4, the flow's
// own, recording-2.mp4, ...) still cover the whole run, in order.
//
// A recording is a debugging aid, not part of what a flow checks: a device that cannot
// record logs why and the flow goes on.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// Video is one recording of a flow run, from Start to End.
type Video struct {
	Path  string
	Role  string // the device's role in a multi-device flow
	Start time.Time
	End   time.Time
	Flow  bool // the flow's own startRecording, not the run's
}

type runRecording struct {
	on     bool // the run's recording is running
	path   string
	start  time.Time
	n      int     // the run's recordings started
	videos []Video // finished, in order

	flowPath  string // the flow's own recording, while it runs
	flowStart time.Time
}

// stopTimeout bounds stopping a recording: the device writes the file out and tb copies it
// (on the web, encodes it).
const stopTimeout = 2 * time.Minute

// Videos are the recordings this runner's flow made: the run's (Options.Record), with the
// flow's own between them.
func (r *Runner) Videos() []Video { return append([]Video(nil), r.rec.videos...) }

// recordStart starts (or resumes) the run's recording.
func (r *Runner) recordStart(ctx context.Context) {
	if !r.opts.Record || r.rec.on || r.recording {
		return
	}
	name := "recording"
	if r.opts.Role != "" {
		name += "-" + r.opts.Role
	}
	if r.rec.n > 0 {
		name += fmt.Sprintf("-%d", r.rec.n+1)
	}
	path, err := r.artifactPath("", name, ".mp4")
	if err != nil {
		r.logLine("--record: " + err.Error())
		return
	}
	if err := r.Drv.StartRecording(ctx, path); err != nil {
		r.logLine("--record: the screen is not recorded: " + err.Error())
		return
	}
	r.rec.n++
	r.rec.on, r.rec.path, r.rec.start = true, path, time.Now()
	r.tracef("recording the screen to %s", filepath.Base(path))
}

// recordStop stops the run's recording and keeps its video.
func (r *Runner) recordStop() {
	if !r.rec.on {
		return
	}
	r.rec.on = false
	c, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	v := Video{Path: r.rec.path, Role: r.opts.Role, Start: r.rec.start, End: time.Now()}
	if err := r.Drv.StopRecording(c); err != nil {
		// What the device saved before the stop failed is still worth a look (issue #107).
		if r.keepVideos(v, true) > 0 {
			r.logLine("--record: the recording may be incomplete: " + err.Error())
		} else {
			r.logLine("--record: the recording could not be saved: " + err.Error())
		}
		return
	}
	r.keepVideos(v, false)
}

// partLength is how much of a recording each part holds when the device saved it in parts
// (screenrecord's cap before Android 14).
const partLength = 3 * time.Minute

// keepVideos adds a finished recording to the run's videos and its timeline: one video, or
// one per part when the device saved it in parts. onlySaved keeps only the files that are
// there and hold something (a stop that failed may have saved part of it); it returns how
// many it kept.
func (r *Runner) keepVideos(v Video, onlySaved bool) (kept int) {
	parts := []string{v.Path}
	if rp, ok := r.Drv.(driver.RecordingParts); ok {
		if p := rp.RecordingParts(); len(p) > 0 {
			parts = p
		}
	}
	for i, p := range parts {
		if onlySaved {
			if st, err := os.Stat(p); err != nil || st.Size() == 0 {
				continue
			}
		}
		part := v
		part.Path = p
		part.Start = v.Start.Add(time.Duration(i) * partLength)
		if part.Start.After(v.End) {
			part.Start = v.End
		}
		if i < len(parts)-1 && part.Start.Add(partLength).Before(v.End) {
			part.End = part.Start.Add(partLength)
		}
		r.rec.videos = append(r.rec.videos, part)
		r.opts.Timeline.Video(part.Role, part.Path, part.Start, part.End)
		kept++
	}
	return kept
}

// stopFlowRecording stops a recording the flow started (stopRecording, or the flow's end).
// With Options.Record its video joins the run's (the caller resumes the run's recording).
func (r *Runner) stopFlowRecording(ctx context.Context) error {
	if !r.recording {
		return nil
	}
	r.recording = false
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopTimeout)
	defer cancel()
	err := r.Drv.StopRecording(c)
	if err == nil && r.opts.Record && r.rec.flowPath != "" {
		r.keepVideos(Video{Path: r.rec.flowPath, Role: r.opts.Role, Start: r.rec.flowStart, End: time.Now(), Flow: true}, false)
	}
	r.rec.flowPath = ""
	return err
}
