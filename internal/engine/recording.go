package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

type Video struct {
	Path  string
	Role  string
	Start time.Time
	End   time.Time
	Flow  bool
}

type runRecording struct {
	on     bool
	path   string
	start  time.Time
	n      int
	videos []Video

	flowPath  string
	flowStart time.Time

	steps   []stepNote
	touches []touchNote
}

const stopTimeout = 2 * time.Minute

func (r *Runner) Videos() []Video { return append([]Video(nil), r.rec.videos...) }

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

func (r *Runner) recordStop() {
	if !r.rec.on {
		return
	}
	r.rec.on = false
	c, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	v := Video{Path: r.rec.path, Role: r.opts.Role, Start: r.rec.start, End: time.Now()}
	err := r.Drv.StopRecording(c)
	v.Start = r.videoStart(v.Start, v.End)
	if err != nil {
		if r.keepVideos(v, true) > 0 {
			r.logLine("--record: the recording may be incomplete: " + err.Error())
		} else {
			r.logLine("--record: the recording could not be saved: " + err.Error())
		}
		return
	}
	r.keepVideos(v, false)
}

func (r *Runner) videoStart(asked, end time.Time) time.Time {
	rc, ok := r.Drv.(driver.RecordingClock)
	if !ok {
		return asked
	}
	t := rc.RecordingStarted()
	if t.IsZero() || t.After(end) || t.Sub(asked).Abs() > 10*time.Second {
		return asked
	}
	return t
}

const partLength = 3 * time.Minute

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
		r.annotateVideo(part)
		r.rec.videos = append(r.rec.videos, part)
		r.opts.Timeline.Video(part.Role, part.Path, part.Start, part.End)
		kept++
	}
	return kept
}

func (r *Runner) stopFlowRecording(ctx context.Context) error {
	if !r.recording {
		return nil
	}
	r.recording = false
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopTimeout)
	defer cancel()
	err := r.Drv.StopRecording(c)
	if r.opts.Record && r.rec.flowPath != "" {
		end := time.Now()
		r.keepVideos(Video{Path: r.rec.flowPath, Role: r.opts.Role, Start: r.videoStart(r.rec.flowStart, end), End: end, Flow: true}, err != nil)
	}
	r.rec.flowPath = ""
	return err
}
