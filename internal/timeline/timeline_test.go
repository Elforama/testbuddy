package timeline

import (
	"strings"
	"testing"
)

// A nil recorder (no timeline asked for) does nothing, so the engine can always call it.
func TestNilRecorderDoesNothing(t *testing.T) {
	var r *Recorder
	if id := r.Begin("", 0, "tapOn", "Tap", "", 0); id != 0 {
		t.Errorf("Begin = %d", id)
	}
	r.End(1, "", "passed", "")
	r.Shot("", "image/jpeg", []byte{1}, 10, 10)
	r.Gesture("", "tap", 10, 10, [2]int{1, 1})
	r.Line("", 0, "x")
	r.Finish(Meta{})
	if r.HTML() != nil || r.WriteFile("unused") != nil {
		t.Error("a nil recorder rendered something")
	}
}

// Steps nest as they ran; gestures and trace lines belong to the innermost running step; a
// step's screens are the last screenshot before it started and the one taken as it ended.
func TestStepsShotsGesturesAndLines(t *testing.T) {
	r := New()
	r.Line("", 0, "tb dev: header")
	r.Shot("", "image/jpeg", []byte("s1"), 1080, 2400)
	flow := r.Begin("", 0, "runFlow", "Run login", "/f/main.yaml", 3)
	tap := r.Begin("", 1, "tapOn", "Tap on \"Sign in\"", "/f/login.yaml", 5)
	r.Line("", 2, "found \"Sign in\"")
	r.Gesture("", "tap", 1080, 2400, [2]int{540, 1200})
	r.Shot("", "image/jpeg", []byte("s2"), 1080, 2400)
	r.End(tap, "", "passed", "")
	check := r.Begin("", 1, "assertVisible", "Assert \"Welcome\"", "/f/login.yaml", 6)
	r.End(check, "", "passed", "") // no new screenshot: the screen did not change
	r.End(flow, "", "passed", "")
	r.Line("", 0, "PASSED in 2s")
	r.Finish(Meta{Flow: "Login", Status: "passed"})

	rec := r.snapshot()
	f, tp, as := rec.steps[0], rec.steps[1], rec.steps[2]
	if tp.Parent != f.ID || as.Parent != f.ID || f.Parent != 0 {
		t.Errorf("parents: flow %d, tap %d, assert %d", f.Parent, tp.Parent, as.Parent)
	}
	if !f.Group || tp.Group || f.After != 0 {
		t.Errorf("a group gets no screenshot of its own: group=%v after=%d", f.Group, f.After)
	}
	if tp.Before != 1 || tp.After != 2 {
		t.Errorf("tap: before %d after %d, want 1 and 2", tp.Before, tp.After)
	}
	if as.Before != 2 || as.After != 0 {
		t.Errorf("assert: before %d after %d, want 2 and 0", as.Before, as.After)
	}
	if len(tp.Gestures) != 1 || len(tp.Lines) != 1 || len(f.Lines) != 0 {
		t.Errorf("tap gestures %v lines %v; flow lines %v", tp.Gestures, tp.Lines, f.Lines)
	}
	v := build(rec)
	if strings.Join(v.RunLog, "|") != "tb dev: header|PASSED in 2s" {
		t.Errorf("run log = %q", v.RunLog)
	}
	if got := v.Rows[1].Frames; len(got) != 2 || got[0].Label != "Before" || got[0].Shot != 1 || got[1].Shot != 2 || got[0].Overlay == "" {
		t.Errorf("tap frames = %+v", got)
	}
	if got := v.Rows[2].Frames; len(got) != 1 || got[0].Label != "Screen" || got[0].Shot != 2 {
		t.Errorf("an unchanged step shows the screen it ran on: %+v", got)
	}
	if len(v.Segments) != 1 || v.Counts != "2 steps: 2 passed" {
		t.Errorf("segments %d, counts %q", len(v.Segments), v.Counts)
	}
}

// Each role of a multi-device flow has its own running steps and screens.
func TestRolesAreSeparate(t *testing.T) {
	r := New()
	r.Shot("alice", "image/png", []byte("a"), 100, 200)
	r.Shot("bob", "image/png", []byte("b"), 300, 600)
	a := r.Begin("alice", 0, "tapOn", "Tap", "", 0)
	b := r.Begin("bob", 0, "tapOn", "Tap", "", 0)
	r.Gesture("alice", "tap", 100, 200, [2]int{5, 5})
	r.Line("bob", 1, "bob's line")
	r.End(a, "", "passed", "")
	r.End(b, "", "failed", "boom")
	rec := r.snapshot()
	if rec.steps[0].Before != 1 || rec.steps[1].Before != 2 {
		t.Errorf("before: alice %d bob %d", rec.steps[0].Before, rec.steps[1].Before)
	}
	if len(rec.steps[0].Gestures) != 1 || len(rec.steps[1].Gestures) != 0 || len(rec.steps[1].Lines) != 1 {
		t.Error("a gesture or line went to the other role's step")
	}
	if rec.steps[1].Parent != 0 {
		t.Error("bob's step nested under alice's")
	}
}

// A run cut short ends its open steps as unfinished.
func TestFinishClosesOpenSteps(t *testing.T) {
	r := New()
	r.Begin("", 0, "tapOn", "Tap", "", 0)
	r.Finish(Meta{Status: "failed"})
	if s := r.snapshot().steps[0]; !s.Done || s.Status != "unfinished" {
		t.Errorf("step = %+v", s)
	}
}

// The page embeds each screenshot once, draws gestures in device coordinates, escapes what
// flows wrote, keeps numeric CSS intact and links to the failure.
func TestHTML(t *testing.T) {
	r := New()
	r.Shot("", "image/jpeg", []byte("first"), 1080, 2400)
	tap := r.Begin("", 0, "tapOn", `Tap on "<script>alert(1)</script>"`, "/f/a.yaml", 2)
	r.Gesture("", "tap", 1080, 2400, [2]int{540, 1200})
	r.Shot("", "image/jpeg", []byte("second"), 1080, 2400)
	r.End(tap, "", "passed", "")
	sw := r.Begin("", 0, "swipe", "Swipe up", "/f/a.yaml", 3)
	r.Gesture("", "swipe", 1080, 2400, [2]int{540, 1800}, [2]int{540, 600})
	r.Shot("", "image/jpeg", []byte("third"), 1080, 2400)
	r.End(sw, "", "failed", "nothing scrolled")
	r.Finish(Meta{Flow: "Demo <flow>", Status: "failed", Error: "nothing scrolled", Version: "dev"})
	page := string(r.HTML())

	for _, want := range []string{
		`.shot-1{background-image:url(data:image/jpeg;base64,Zmlyc3Q=)}`,
		`aspect-ratio:1080 / 2400`,
		`<circle cx="540.0" cy="1200.0"`,
		`<line x1="540.0" y1="1800.0" x2="540.0" y2="600.0"`,
		`href="#s2"`, // the failed step
		`&lt;script&gt;alert(1)&lt;/script&gt;`,
		`Demo &lt;flow&gt;`,
		`a.yaml:2`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, "<script>alert(1)") {
		t.Error("a description was not escaped")
	}
	for _, id := range []string{".shot-1{", ".shot-2{", ".shot-3{"} {
		if n := strings.Count(page, id); n != 1 {
			t.Errorf("%s embedded %d times", id, n)
		}
	}
	if strings.Contains(page, "ZgotmplZ") {
		t.Error("the template rejected a value as unsafe")
	}
}

// A scroll's identical swipes are drawn once, counted; different gestures are numbered.
func TestRepeatedGesturesAreDrawnOnce(t *testing.T) {
	r := New()
	r.Shot("", "image/jpeg", []byte("s"), 100, 200)
	s := r.Begin("", 0, "scrollUntilVisible", "Scroll", "", 0)
	for i := 0; i < 3; i++ {
		r.Gesture("", "swipe", 100, 200, [2]int{50, 150}, [2]int{50, 50})
	}
	r.Gesture("", "tap", 100, 200, [2]int{10, 10})
	r.End(s, "", "passed", "")
	page := string(r.HTML())
	if n := strings.Count(page, `class="g-line"`); n != 1 {
		t.Errorf("swipes drawn %d times, want 1", n)
	}
	for _, want := range []string{">1×3</text>", ">2</text>"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}
