package annotate

// Keeping the drawings in sync with the video.
//
// The runner times captions and touches from when the video began, as the driver reports it
// (driver.RecordingClock). On the web that is exact: tb takes the frames itself. A phone or
// simulator records on its own and only says when it started, so its video can begin a few
// hundred milliseconds off from what tb believes, and a long Android recording in parts can
// drift further. Off by that much, a ring shows after the screen already moved on.
//
// So tb checks against the video itself: every tap is a clapperboard. The screen changes
// just after tb touches it, so in a video in sync each touch lands shortly before a change
// of the picture. Changes lists when the picture changes; Offset finds the shift that puts
// the touches just before them, and says whether enough of them agree to trust it.

import (
	"context"
	"errors"
	"math"
	"regexp"
	"sort"
	"strconv"
	"time"
)

const (
	// sceneThreshold is how much of the picture must change for Changes to see it (ffmpeg's
	// scene score, 0 to 1, on a small copy): a screen that reacts to a tap, not a blinking
	// cursor.
	sceneThreshold = 0.01
	// reactWithin is how soon after a touch its change must come for the touch to count as
	// matched.
	reactWithin = 500 * time.Millisecond
	// searchSpan is the largest shift Offset tries, either way.
	searchSpan = 3 * time.Second
	searchStep = 10 * time.Millisecond
)

var ptsRE = regexp.MustCompile(`pts_time:\s*([0-9.]+)`)

// Changes lists when the picture in the video at path changes noticeably, in times from its
// start.
func Changes(ctx context.Context, path string) ([]time.Duration, error) {
	ffmpeg, err := FFmpegPath()
	if err != nil || ffmpeg == "" {
		return nil, errors.New("ffmpeg is not on PATH (or TB_FFMPEG)")
	}
	vf := "scale=160:-2,select='gt(scene," + strconv.FormatFloat(sceneThreshold, 'f', -1, 64) + ")',showinfo"
	out, err := runFFmpeg(ctx, "", ffmpeg, []string{"-hide_banner", "-nostats", "-i", path, "-an", "-vf", vf, "-f", "null", "-"})
	if err != nil {
		return nil, err
	}
	var ts []time.Duration
	for _, m := range ptsRE.FindAllStringSubmatch(string(out), -1) {
		if f, err := strconv.ParseFloat(m[1], 64); err == nil {
			ts = append(ts, time.Duration(f*float64(time.Second)))
		}
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
	return ts, nil
}

// Sync is what Offset found.
type Sync struct {
	Shift   time.Duration // add to the runner's times to get the video's
	Matched int           // touches followed by a change within reactWithin at Shift
	Touches int
	OK      bool // enough touches agree to trust Shift
}

// Offset finds the shift that puts the touches (times by the runner's clock, from the
// video's start) each shortly before a change of the picture: the one that leaves the least
// time between each touch and the next change. That puts the quickest reaction's touch right
// on its change, so the shift found is the video's lag plus at most the app's quickest
// reaction (tens of milliseconds, under a frame or two). Between equal fits it takes the
// smallest shift. OK when at least three touches, and four in five of them, are matched.
func Offset(touches, changes []time.Duration) Sync {
	s := Sync{Touches: len(touches)}
	if len(touches) < 2 || len(changes) == 0 {
		return s
	}
	bestCost := time.Duration(math.MaxInt64)
	for shift := -searchSpan; shift <= searchSpan; shift += searchStep {
		cost, matched := fit(touches, changes, shift)
		if cost < bestCost || cost == bestCost && shift.Abs() < s.Shift.Abs() {
			bestCost, s.Shift, s.Matched = cost, shift, matched
		}
	}
	// Most touches, four in five, and at least three: barely more than half agreeing is chance on
	// a busy screen (CI, PR #391: 17 of 27 web touches moved a right video 1060 ms off).
	s.OK = s.Matched >= 3 && s.Matched*10 >= len(touches)*8
	return s
}

// fit is how long, in all, the touches wait for their changes at shift (reactWithin for a
// touch with none in time), and how many have one in time.
func fit(touches, changes []time.Duration, shift time.Duration) (cost time.Duration, matched int) {
	for _, t := range touches {
		at := t + shift
		i := sort.Search(len(changes), func(i int) bool { return changes[i] >= at })
		wait := reactWithin
		if i < len(changes) {
			if d := changes[i] - at; d < reactWithin {
				wait, matched = d, matched+1
			}
		}
		cost += wait
	}
	return cost, matched
}
