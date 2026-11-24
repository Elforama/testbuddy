package timeline

import (
	"strings"
	"sync"
	"time"
)

type Recorder struct {
	mu       sync.Mutex
	start    time.Time
	steps    []*Step
	shots    []*Shot
	gestures []*Gesture
	lines    []*Line
	videos   []*Video
	open     map[string][]*Step
	meta     Meta

	files    sync.RWMutex
	spill    spillFile
	spillEnd int64
	diskErr  error
	closed   bool
	warn     func(msg string)
	newSpill func() (spillFile, error)
}

func New() *Recorder {
	return &Recorder{start: time.Now(), open: map[string][]*Step{}}
}

type Meta struct {
	Flow     string
	File     string
	Status   string
	Error    string
	Version  string
	Devices  []Device
	Started  time.Time
	Duration time.Duration
}

type Device struct {
	Role, Platform, Name, Serial string
}

type Step struct {
	ID       int
	Parent   int
	Role     string
	Depth    int
	Name     string
	Desc     string
	File     string
	Line     int
	Start    time.Duration
	End      time.Duration
	Status   string
	Err      string
	Done     bool
	Group    bool
	Before   int
	After    int
	Gestures []int
	Lines    []int
}

type Shot struct {
	ID   int
	Role string
	At   time.Duration
	Mime string
	Data []byte
	W, H int

	onDisk bool
	off, n int64
}

type Gesture struct {
	ID   int
	Role string
	At   time.Duration
	Kind string
	Pts  [][2]int
	W, H int

	repeat int
}

type Video struct {
	ID         int
	Role       string
	Path       string
	Start, End time.Duration
}

type Line struct {
	Role  string
	At    time.Duration
	Depth int
	Text  string
}

var groupCommands = map[string]bool{"runFlow": true, "repeat": true, "retry": true, "parallel": true, "on": true}

func IsGroup(name string) bool { return groupCommands[name] }

func (r *Recorder) now() time.Duration { return time.Since(r.start) }

func (r *Recorder) Begin(role string, depth int, name, desc, file string, line int) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := &Step{ID: len(r.steps) + 1, Role: role, Depth: depth, Name: name, Desc: desc, File: file, Line: line,
		Start: r.now(), Group: IsGroup(name), Before: r.lastShotLocked(role)}
	if open := r.open[role]; len(open) > 0 {
		s.Parent = open[len(open)-1].ID
	}
	r.steps = append(r.steps, s)
	r.open[role] = append(r.open[role], s)
	return s.ID
}

func (r *Recorder) End(id int, desc, status, err string) {
	if r == nil || id <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if id > len(r.steps) {
		return
	}
	s := r.steps[id-1]
	s.End, s.Status, s.Err, s.Done = r.now(), status, err, true
	if desc != "" {
		s.Desc = desc
	}
	if !s.Group {
		if last := r.lastShotLocked(s.Role); last != s.Before {
			s.After = last
		}
	}
	open := r.open[s.Role]
	for i := len(open) - 1; i >= 0; i-- {
		if open[i] == s {
			r.open[s.Role] = append(open[:i:i], open[i+1:]...)
			break
		}
	}
}

func (r *Recorder) Shot(role, mime string, data []byte, w, h int) {
	if r == nil || len(data) == 0 {
		return
	}
	r.mu.Lock()
	s := &Shot{ID: len(r.shots) + 1, Role: role, At: r.now(), Mime: mime, W: w, H: h}
	warning := r.storeLocked(s, data)
	r.shots = append(r.shots, s)
	warn := r.warn
	r.mu.Unlock()
	if warning != "" && warn != nil {
		warn(warning)
	}
}

func (r *Recorder) Gesture(role, kind string, w, h int, pts ...[2]int) {
	if r == nil || len(pts) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	g := &Gesture{ID: len(r.gestures) + 1, Role: role, At: r.now(), Kind: kind, Pts: pts, W: w, H: h}
	r.gestures = append(r.gestures, g)
	if open := r.open[role]; len(open) > 0 {
		s := open[len(open)-1]
		s.Gestures = append(s.Gestures, g.ID)
	}
}

func (r *Recorder) Line(role string, depth int, text string) {
	if r == nil || strings.TrimSpace(text) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	l := &Line{Role: role, At: r.now(), Depth: depth, Text: text}
	r.lines = append(r.lines, l)
	if open := r.open[role]; len(open) > 0 {
		s := open[len(open)-1]
		s.Lines = append(s.Lines, len(r.lines)-1)
	}
}

func (r *Recorder) Video(role, path string, start, end time.Time) {
	if r == nil || path == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.videos = append(r.videos, &Video{ID: len(r.videos) + 1, Role: role, Path: path,
		Start: max(start.Sub(r.start), 0), End: max(end.Sub(r.start), 0)})
}

func (r *Recorder) Finish(m Meta) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if m.Started.IsZero() {
		m.Started = r.start
	}
	if m.Duration == 0 {
		m.Duration = r.now()
	}
	r.meta = m
	for _, s := range r.steps {
		if !s.Done {
			s.End, s.Done = r.now(), true
			if s.Status == "" {
				s.Status = "unfinished"
			}
		}
	}
	r.open = map[string][]*Step{}
}

func (r *Recorder) lastShotLocked(role string) int {
	for i := len(r.shots) - 1; i >= 0; i-- {
		if r.shots[i].Role == role {
			return r.shots[i].ID
		}
	}
	return 0
}

func (r *Recorder) snapshot() recording {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := recording{meta: r.meta}
	for _, s := range r.steps {
		c := *s
		rec.steps = append(rec.steps, &c)
	}
	rec.shots = append(rec.shots, r.shots...)
	rec.gestures = append(rec.gestures, r.gestures...)
	rec.lines = append(rec.lines, r.lines...)
	for _, v := range r.videos {
		c := *v
		rec.videos = append(rec.videos, &c)
	}
	return rec
}

type recording struct {
	meta     Meta
	steps    []*Step
	shots    []*Shot
	gestures []*Gesture
	lines    []*Line
	videos   []*Video
	dir      string
}
