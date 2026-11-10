package timeline

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html/template"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WriteFile renders the recording to path (the folder is created).
func (r *Recorder) WriteFile(path string) error {
	if r == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, r.render(filepath.Dir(path)), 0o644)
}

// HTML renders the recording as one self-contained page: every screenshot is embedded once.
func (r *Recorder) HTML() []byte {
	if r == nil {
		return nil
	}
	return r.render("")
}

// render renders the page for a file in dir ("" = links stay as recorded).
func (r *Recorder) render(dir string) []byte {
	rec := r.snapshot()
	rec.dir = dir
	v := build(rec)
	var b bytes.Buffer
	if err := page.Execute(&b, v); err != nil {
		return []byte("<!doctype html><title>timeline</title><pre>" + template.HTMLEscapeString(err.Error()) + "</pre>")
	}
	return b.Bytes()
}

type view struct {
	Title    string
	Status   string
	Error    string
	File     string
	Devices  string
	Version  string
	Started  string
	Duration string
	Counts   string
	Failed   int // the failing step's ID, 0 for none
	Shots    template.CSS
	Segments []segment
	Rows     []row
	RunLog   []string
	Multi    bool
	Videos   []video
}

type video struct {
	ID    int
	Src   string
	Label string
}

type segment struct {
	ID     int
	Status string
	Pct    template.CSS
	Title  string
}

type row struct {
	ID       int
	Depth    int
	Role     string
	Status   string
	Mark     string
	Desc     string
	Where    string
	At       string
	Took     string
	Err      string
	Group    bool
	Frames   []frame
	Lines    []string
	Gestures int
	Video    int    // the recording showing this step, 0 for none
	Seek     string // where in it the step starts, in seconds
}

type frame struct {
	Label   string
	Shot    int
	Ratio   template.CSS // CSS aspect-ratio, from numbers only
	Overlay template.HTML
	Note    string // no screenshot: why
}

func marks(status string) string {
	switch status {
	case "passed":
		return "✓"
	case "failed":
		return "✗"
	case "warned":
		return "⚠"
	case "skipped":
		return "○"
	}
	return "…"
}

func secs(d time.Duration) string { return fmt.Sprintf("%.2fs", d.Seconds()) }

func took(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%d ms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1f s", d.Seconds())
}

func build(rec recording) view {
	m := rec.meta
	v := view{Title: m.Flow, Status: m.Status, Error: m.Error, File: m.File, Version: m.Version,
		Duration: took(m.Duration), Multi: len(m.Devices) > 1}
	if v.Title == "" {
		v.Title = "Flow"
	}
	if v.Status == "" {
		v.Status = "unfinished"
	}
	if !m.Started.IsZero() {
		v.Started = m.Started.Format("2006-01-02 15:04:05")
	}
	var devs []string
	for _, d := range m.Devices {
		s := strings.TrimSpace(d.Platform + " " + d.Name)
		if d.Role != "" {
			s = d.Role + ": " + s
		}
		devs = append(devs, s)
	}
	v.Devices = strings.Join(devs, " · ")

	shots := map[int]*Shot{}
	var css strings.Builder
	for _, s := range rec.shots {
		shots[s.ID] = s
		fmt.Fprintf(&css, ".shot-%d{background-image:url(data:%s;base64,%s)}\n", s.ID, s.Mime, base64.StdEncoding.EncodeToString(s.Data))
	}
	v.Shots = template.CSS(css.String())
	gestures := map[int]*Gesture{}
	for _, g := range rec.gestures {
		gestures[g.ID] = g
	}

	// The overview: top-level steps, each as wide as its share of the run.
	var total time.Duration
	for _, s := range rec.steps {
		if s.Parent == 0 {
			total += max(s.End-s.Start, 0)
		}
	}
	passed, failed, other := 0, 0, 0
	for _, s := range rec.steps {
		if s.Parent == 0 {
			pct := 100.0
			if total > 0 {
				pct = math.Max(0.4, 100*float64(s.End-s.Start)/float64(total))
			}
			v.Segments = append(v.Segments, segment{s.ID, s.Status, template.CSS(fmt.Sprintf("%.2f%%", pct)),
				fmt.Sprintf("%s %s · %s", marks(s.Status), s.Desc, took(s.End-s.Start))})
		}
		if !s.Group {
			switch s.Status {
			case "passed":
				passed++
			case "failed":
				failed++
			default:
				other++
			}
		}
		if s.Status == "failed" && !s.Group && v.Failed == 0 {
			v.Failed = s.ID
		}
	}
	v.Counts = fmt.Sprintf("%d steps: %d passed", passed+failed+other, passed)
	if failed > 0 {
		v.Counts += fmt.Sprintf(", %d failed", failed)
	}
	if other > 0 {
		v.Counts += fmt.Sprintf(", %d other", other)
	}

	for _, vd := range rec.videos {
		label := fmt.Sprintf("%s from +%s, %s", filepath.Base(vd.Path), secs(vd.Start), took(vd.End-vd.Start))
		if vd.Role != "" {
			label = vd.Role + ": " + label
		}
		v.Videos = append(v.Videos, video{vd.ID, href(rec.dir, vd.Path), label})
	}
	for _, s := range rec.steps {
		rw := row{ID: s.ID, Depth: s.Depth, Role: s.Role, Status: s.Status, Mark: marks(s.Status), Desc: s.Desc,
			At: "+" + secs(s.Start), Took: took(s.End - s.Start), Err: s.Err, Group: s.Group, Gestures: len(s.Gestures)}
		if s.File != "" {
			rw.Where = filepath.Base(s.File)
			if s.Line > 0 {
				rw.Where += fmt.Sprintf(":%d", s.Line)
			}
		}
		for _, i := range s.Lines {
			rw.Lines = append(rw.Lines, rec.lines[i].Text)
		}
		if !s.Group {
			rw.Frames = frames(s, shots, gestures)
		}
		for _, vd := range rec.videos {
			if vd.Role == s.Role && s.Start >= vd.Start && s.Start < vd.End {
				rw.Video, rw.Seek = vd.ID, fmt.Sprintf("%.2f", (s.Start-vd.Start).Seconds())
				break
			}
		}
		v.Rows = append(v.Rows, rw)
	}
	attached := map[int]bool{}
	for _, s := range rec.steps {
		for _, i := range s.Lines {
			attached[i] = true
		}
	}
	for i, l := range rec.lines {
		if !attached[i] {
			v.RunLog = append(v.RunLog, l.Text)
		}
	}
	return v
}

// href links a file from a page in dir: relative when it is under dir, else a file URL.
func href(dir, path string) string {
	if dir != "" {
		if rel, err := filepath.Rel(dir, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	if filepath.IsAbs(path) {
		return "file://" + filepath.ToSlash(path)
	}
	return filepath.ToSlash(path)
}

// frames are the screenshots shown for a step: with a gesture, the screen it acted on with
// the gesture drawn on it, then the screen after; otherwise the screen after it (or, when
// the step changed nothing, the screen it ran on).
func frames(s *Step, shots map[int]*Shot, gestures map[int]*Gesture) []frame {
	mk := func(label string, id int, gs []*Gesture) frame {
		sh := shots[id]
		if sh == nil {
			return frame{Label: label, Note: "no screenshot"}
		}
		f := frame{Label: label, Shot: id, Ratio: template.CSS(fmt.Sprintf("%d / %d", max(sh.W, 1), max(sh.H, 1)))}
		if len(gs) > 0 {
			f.Overlay = overlay(sh, gs)
		}
		return f
	}
	var gs []*Gesture
	for _, id := range s.Gestures {
		if g := gestures[id]; g != nil {
			gs = append(gs, g)
		}
	}
	gs = merged(gs)
	switch {
	case len(gs) > 0:
		before := mk("Before", s.Before, gs)
		if s.Before == 0 {
			before.Note = "no screenshot before this step"
		}
		after := frame{Label: "After", Note: "the screen did not change"}
		if s.After != 0 {
			after = mk("After", s.After, nil)
		}
		return []frame{before, after}
	case s.After != 0:
		return []frame{mk("After", s.After, nil)}
	case s.Before != 0:
		return []frame{mk("Screen", s.Before, nil)}
	}
	return nil
}

// merged draws a gesture repeated with the same points (a scroll's swipes) once, counted.
func merged(gs []*Gesture) []*Gesture {
	var out []*Gesture
	for _, g := range gs {
		if n := len(out); n > 0 && same(out[n-1], g) {
			c := *out[n-1]
			c.repeat = max(c.repeat, 1) + 1
			out[n-1] = &c
			continue
		}
		out = append(out, g)
	}
	return out
}

func same(a, b *Gesture) bool {
	if a.Kind != b.Kind || len(a.Pts) != len(b.Pts) {
		return false
	}
	for i := range a.Pts {
		if a.Pts[i] != b.Pts[i] {
			return false
		}
	}
	return true
}

// overlay draws gestures over a screenshot, in the device's coordinates: a ring per tap
// (two for a double tap, a filled one for a long press), an arrow per swipe, numbered when
// a step made more than one.
func overlay(sh *Shot, gs []*Gesture) template.HTML {
	w, h := float64(max(sh.W, 1)), float64(max(sh.H, 1))
	r := math.Min(w, h) * 0.035
	sw := math.Min(w, h) * 0.008
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="overlay" viewBox="0 0 %d %d" preserveAspectRatio="none" aria-hidden="true">`, sh.W, sh.H)
	for i, g := range gs {
		// A gesture recorded for another size (a rotation mid-step) is scaled to this shot.
		sx, sy := 1.0, 1.0
		if g.W > 0 && g.H > 0 {
			sx, sy = w/float64(g.W), h/float64(g.H)
		}
		p := func(k int) (float64, float64) { return float64(g.Pts[k][0]) * sx, float64(g.Pts[k][1]) * sy }
		x, y := p(0)
		switch g.Kind {
		case "swipe":
			if len(g.Pts) < 2 {
				continue
			}
			x2, y2 := p(len(g.Pts) - 1)
			fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke-width="%.1f" class="g-line"/>`, x, y, x2, y2, sw*1.4)
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.1f" class="g-start"/>`, x, y, r*0.45)
			// Arrowhead at the end.
			ang := math.Atan2(y2-y, x2-x)
			a := r * 1.1
			fmt.Fprintf(&b, `<polygon points="%.1f,%.1f %.1f,%.1f %.1f,%.1f" class="g-head"/>`, x2, y2,
				x2-a*math.Cos(ang-0.45), y2-a*math.Sin(ang-0.45), x2-a*math.Cos(ang+0.45), y2-a*math.Sin(ang+0.45))
		case "longPress":
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.1f" class="g-fill"/>`, x, y, r)
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.1f" stroke-width="%.1f" class="g-ring"/>`, x, y, r*1.5, sw)
		case "doubleTap":
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.1f" stroke-width="%.1f" class="g-ring"/>`, x, y, r, sw)
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.1f" stroke-width="%.1f" class="g-ring"/>`, x, y, r*1.6, sw)
		default:
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.1f" stroke-width="%.1f" class="g-ring"/>`, x, y, r, sw)
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.1f" class="g-dot"/>`, x, y, r*0.22)
		}
		label := ""
		if len(gs) > 1 {
			label = fmt.Sprint(i + 1)
		}
		if g.repeat > 1 {
			label += fmt.Sprintf("×%d", g.repeat)
		}
		if label != "" {
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="%.1f" class="g-num">%s</text>`, x+r*1.2, y-r*1.2, r*1.3, label)
		}
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

var page = template.Must(template.New("timeline").Funcs(template.FuncMap{
	"indent": func(d int) template.CSS { return template.CSS(fmt.Sprintf("%dpx", min(d, 8)*22)) },
}).Parse(pageHTML))
