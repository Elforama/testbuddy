// Package timeline records a flow run for its HTML timeline (`tb test --timeline`): each
// command as it starts and ends, nested as it ran; the screen after each command; where
// each tap and swipe went; and the trace lines written while each command ran. Render turns
// a recording into one self-contained HTML page.
//
// Every method is safe on a nil *Recorder (it does nothing), so the engine can call them
// whether or not a timeline is being recorded, and safe for concurrent use: the devices of
// a multi-device flow share one recorder, each event tagged with its device's role.
//
// Screenshots are kept in a temp file while the run goes on, not in memory (spill.go), and
// copied from it into the page as it is written; Close removes that file.
package timeline

import (
	"strings"
	"sync"
	"time"
)

// Recorder collects one flow run.
type Recorder struct {
	mu       sync.Mutex
	start    time.Time
	steps    []*Step
	shots    []*Shot
	gestures []*Gesture
	lines    []*Line
	videos   []*Video
	open     map[string][]*Step // each role's commands still running, innermost last
	meta     Meta

	// The screenshots' temp file (spill.go). spill is set and cleared holding both mu and
	// files; a page being written holds files for reading while it copies from it.
	files    sync.RWMutex
	spill    spillFile
	spillEnd int64                     // where the next screenshot goes in it
	diskErr  error                     // the temp file failed: screenshots stay in memory
	closed   bool                      // Close was called: no new temp file
	warn     func(msg string)          // OnWarning
	newSpill func() (spillFile, error) // makes the temp file (tests replace it)
}

// New starts a recording; times are measured from now.
func New() *Recorder {
	return &Recorder{start: time.Now(), open: map[string][]*Step{}}
}

// Meta describes the run as a whole.
type Meta struct {
	Flow     string
	File     string
	Status   string // passed, failed, warned, skipped
	Error    string
	Version  string // tb's
	Devices  []Device
	Started  time.Time
	Duration time.Duration
}

// Device is one device the flow ran on (Role is "" for a single-device flow).
type Device struct {
	Role, Platform, Name, Serial string
}

// Step is one command.
type Step struct {
	ID       int
	Parent   int // the enclosing command's ID, 0 at the top level
	Role     string
	Depth    int
	Name     string // the command's YAML key (tapOn, runFlow, ...)
	Desc     string
	File     string
	Line     int
	Start    time.Duration
	End      time.Duration
	Status   string
	Err      string
	Done     bool
	Group    bool // runs other commands (runFlow, repeat, retry, parallel, a role block)
	Before   int  // the screenshot showing the screen when it started (0: none)
	After    int  // the screenshot taken when it ended (0: none)
	Gestures []int
	Lines    []int
}

// Shot is a screenshot. W and H are the device's size in the coordinates its gestures use
// (Android pixels, iOS points, CSS pixels), which the image is drawn at. Its bytes are in
// the recorder's temp file (off, n), or in Data when that file could not be written.
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

// Gesture is where a tap, long press, double tap or swipe went, in device coordinates.
type Gesture struct {
	ID   int
	Role string
	At   time.Duration
	Kind string // tap, doubleTap, longPress, swipe
	Pts  [][2]int
	W, H int

	repeat int // drawn once for this many identical gestures (render)
}

// Video is a screen recording of the run (tb test --record), from Start to End.
type Video struct {
	ID         int
	Role       string
	Path       string
	Start, End time.Duration
}

// Line is one trace line (log.txt), written while a step ran.
type Line struct {
	Role  string
	At    time.Duration
	Depth int
	Text  string
}

// groupCommands run other commands; their own screenshot would repeat their last child's.
var groupCommands = map[string]bool{"runFlow": true, "repeat": true, "retry": true, "parallel": true, "on": true}

// IsGroup reports whether a command runs other commands.
func IsGroup(name string) bool { return groupCommands[name] }

func (r *Recorder) now() time.Duration { return time.Since(r.start) }

// Begin records a command starting on a role's device and returns its step ID.
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

// End records a command ending. desc replaces the description given at Begin (with ${}
// values filled in) when not empty.
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

// Shot adds a screenshot of a role's device. Its bytes go to the recorder's temp file (the
// recorder keeps no reference to data), or stay in memory when that file cannot be written.
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
		warn(warning) // unlocked: the warning may come back as a Line
	}
}

// Gesture records where a gesture went, for the innermost running step of its role.
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

// Line records a trace line, for the innermost running step of its role (or the run as a
// whole when none runs).
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

// Video records a screen recording of a role's device, made from start to end.
func (r *Recorder) Video(role, path string, start, end time.Time) {
	if r == nil || path == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.videos = append(r.videos, &Video{ID: len(r.videos) + 1, Role: role, Path: path,
		Start: max(start.Sub(r.start), 0), End: max(end.Sub(r.start), 0)})
}

// Finish records how the run ended. Steps still open (the run was cut short) end now.
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

// lastShotLocked is the ID of the latest screenshot of a role's device, 0 for none.
func (r *Recorder) lastShotLocked(role string) int {
	for i := len(r.shots) - 1; i >= 0; i-- {
		if r.shots[i].Role == role {
			return r.shots[i].ID
		}
	}
	return 0
}

// snapshot copies the recording for rendering.
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
	dir      string // where the page goes: videos are linked relative to it
}
