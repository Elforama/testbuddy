package annotate

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
	sceneThreshold = 0.01
	reactWithin    = 500 * time.Millisecond
	searchSpan     = 3 * time.Second
	searchStep     = 10 * time.Millisecond
)

var ptsRE = regexp.MustCompile(`pts_time:\s*([0-9.]+)`)

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

type Sync struct {
	Shift   time.Duration
	Matched int
	Touches int
	OK      bool
}

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
	s.OK = s.Matched >= 2 && s.Matched*10 >= len(touches)*6
	return s
}

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

const (
	markSearch    = 20 * time.Second
	markEarly     = 400 * time.Millisecond
	markLate      = 1500 * time.Millisecond
	markThreshold = 0.005
)

func MarkChanges(ctx context.Context, path string) ([]time.Duration, error) {
	ffmpeg, err := FFmpegPath()
	if err != nil || ffmpeg == "" {
		return nil, errors.New("ffmpeg is not on PATH (or TB_FFMPEG)")
	}
	vf := "crop=iw*0.35:ih*0.045:0:0,select='gt(scene," + strconv.FormatFloat(markThreshold, 'f', -1, 64) + ")',showinfo"
	out, err := runFFmpeg(ctx, "", ffmpeg, []string{"-hide_banner", "-nostats", "-t", secs(markSearch), "-i", path, "-an", "-vf", vf, "-f", "null", "-"})
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

func FindMark(changes []time.Duration, held time.Duration) (time.Duration, bool) {
	if held <= 0 {
		return 0, false
	}
	near := func(d time.Duration) bool { return d >= held-markEarly && d <= held+markLate }
	for i, c := range changes {
		for _, c2 := range changes[i+1:] {
			if c2-c > held+markLate {
				break
			}
			if near(c2 - c) {
				return c, true
			}
		}
	}
	if len(changes) > 0 && near(changes[0]) {
		return 0, true
	}
	return 0, false
}
