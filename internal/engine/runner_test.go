package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/js"
)

func TestNewDefaults(t *testing.T) {
	r := New(&fakeDriver{}, Options{})
	if r.opts.LookupTimeout != 17*time.Second || r.opts.OptionalTimeout != 7*time.Second || r.opts.Reporter == nil {
		t.Errorf("options %+v", r.opts)
	}
	r.opts.Reporter.Event(Event{})
	if r.LastScreen() != nil {
		t.Error("a screen before any read")
	}
}

func TestErrorKinds(t *testing.T) {
	fe := failf("x %d", 1)
	if fe.Error() != "x 1" || !isFlowError(fe) {
		t.Errorf("failf: %v", fe)
	}
	nf := &NotFound{FlowError: FlowError{Msg: "gone"}}
	if nf.Error() != "gone" || !isFlowError(nf) {
		t.Errorf("NotFound: %v", nf)
	}
	if isFlowError(errors.New("io")) || isFlowError(&js.Error{Msg: "x"}) || isFlowError(&invalidField{msg: "bad"}) {
		t.Error("non-test errors counted as test failures")
	}
	if (&invalidField{msg: "bad"}).Error() != "bad" {
		t.Error("invalidField message")
	}
}

func TestAdjustedTimeout(t *testing.T) {
	r := New(&fakeDriver{}, Options{LookupTimeout: time.Second, OptionalTimeout: 500 * time.Millisecond})
	r.lastInteraction = time.Now().Add(-700 * time.Millisecond)
	if d := r.adjusted(time.Second); d <= 250*time.Millisecond || d > 300*time.Millisecond {
		t.Errorf("adjusted %s, want ~300 ms", d)
	}
	if d := r.adjusted(500 * time.Millisecond); d != 0 {
		t.Errorf("past the budget: %s", d)
	}
	if d := r.lookupTimeout(false); d <= 250*time.Millisecond || d > 300*time.Millisecond {
		t.Errorf("lookup %s", d)
	}
	if d := r.lookupTimeout(true); d != 0 {
		t.Errorf("optional lookup %s", d)
	}
}

func TestLookupBudgetAfterInteraction(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		between  string
		min, max time.Duration
	}{
		{"", 450 * time.Millisecond, 700 * time.Millisecond},
		{"- inputText: x\n", 750 * time.Millisecond, 1200 * time.Millisecond},
	} {
		sig := NewSignals()
		go func() {
			time.Sleep(300 * time.Millisecond)
			sig.Send("go")
		}()
		o := Options{LookupTimeout: 500 * time.Millisecond, OptionalTimeout: 200 * time.Millisecond, Signals: sig}
		start := time.Now()
		mustFail(t, runWith(t, &fakeDriver{screen: static()}, o, "- waitForSignal: go\n"+tc.between+"- assertVisible: Nope"), "Nope")
		if d := time.Since(start); d < tc.min || d > tc.max {
			t.Errorf("%q: took %s, want %s-%s", tc.between, d, tc.min, tc.max)
		}
	}
}

func TestReporterEvents(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	o := quick
	o.Reporter = rec
	o.Role = "alice"
	res := runWith(t, &fakeDriver{screen: static(btn("Home", "", 0))}, o, "- runFlow:\n    commands:\n      - assertVisible: Home\n      - assertVisible: {text: Nope, optional: true}\n- assertTrue: 'false'")
	if res.Status != Failed {
		t.Fatalf("status %s", res.Status)
	}
	type ev struct {
		desc   string
		depth  int
		start  bool
		status Status
	}
	var got []ev
	for _, e := range rec.all() {
		if e.Device != "alice" {
			t.Errorf("event without the role: %+v", e)
		}
		if e.Cmd != nil {
			got = append(got, ev{e.Desc, e.Depth, e.Start, e.Status})
		}
	}
	want := []ev{
		{"Run flow", 0, true, ""},
		{`Assert that "Home" is visible`, 1, true, ""},
		{`Assert that "Home" is visible`, 1, false, Passed},
		{`Assert that "Nope" is visible`, 1, true, ""},
		{`Assert that "Nope" is visible`, 1, false, Warned},
		{"Run flow", 0, false, Passed},
		{`Assert that "false" is true`, 0, true, ""},
		{`Assert that "false" is true`, 0, false, Failed},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events\n%+v\nwant\n%+v", got, want)
	}
	for _, e := range rec.ends() {
		if e.Status == Warned || e.Status == Failed {
			if e.Err == nil {
				t.Errorf("%s %s without its error", e.Desc, e.Status)
			}
		}
		if e.Elapsed < 0 {
			t.Errorf("elapsed %s", e.Elapsed)
		}
	}
	if len(res.Steps) != 2 || res.Steps[0].Status != Passed || res.Steps[1].Err == "" {
		t.Errorf("steps %+v", res.Steps)
	}
}

func TestBeginAndRunList(t *testing.T) {
	f := &fakeDriver{screen: static()}
	o := quick
	o.Env = []flow.KV{{Key: "A", Value: "cli"}, {Key: "B", Value: "cli"}, {Key: "C", Value: "cli"}}
	r := New(f, o)
	fl, err := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte("appId: ${B}.app\nenv:\n  C: flow\n---\n- inputText: never"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Begin(fl, []flow.KV{{Key: "B", Value: "role"}, {Key: "C", Value: "role"}}); err != nil {
		t.Fatal(err)
	}
	if r.appID != "role.app" {
		t.Errorf("appId %q", r.appID)
	}
	if err := r.SetEnv([]flow.KV{{Key: "D", Value: "${A}+"}}); err != nil {
		t.Fatal(err)
	}
	cmds, _ := flow.ParseCommands(t.TempDir(), []byte("- inputText: ${A}/${B}/${C}/${D}\n- launchApp"))
	if err := r.RunList(context.Background(), cmds, fl); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.typed, []string{"cli/role/role/cli+"}) {
		t.Errorf("typed %q", f.typed)
	}
	r.SetAppID("com.role")
	cmds, _ = flow.ParseCommands(t.TempDir(), []byte("- launchApp\n- assertTrue: 'false'\n- inputText: after"))
	if err := r.RunList(context.Background(), cmds, fl); err == nil || !strings.Contains(err.Error(), "Assertion is false") {
		t.Errorf("err %v", err)
	}
	if got := f.called("LaunchApp"); !reflect.DeepEqual(got, []string{"LaunchApp role.app", "LaunchApp com.role"}) {
		t.Errorf("launches %q", got)
	}
	if len(f.typed) != 1 {
		t.Errorf("ran past the failure: %q", f.typed)
	}
	if err := r.SetEnv(nil); err != nil {
		t.Errorf("empty SetEnv: %v", err)
	}
}

func TestBeginErrors(t *testing.T) {
	bad := []flow.KV{{Key: "X", Value: "${(}"}}
	fl, _ := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte("appId: app\n---\n- back"))
	badEnv, _ := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte("appId: app\nenv:\n  X: ${(}\n---\n- back"))
	badApp, _ := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte("appId: ${(}\n---\n- back"))
	for name, err := range map[string]error{
		"-e":    New(&fakeDriver{}, Options{Env: bad}).Begin(fl, nil),
		"role":  New(&fakeDriver{}, quick).Begin(fl, bad),
		"flow":  New(&fakeDriver{}, quick).Begin(badEnv, nil),
		"appId": New(&fakeDriver{}, quick).Begin(badApp, nil),
	} {
		if err == nil || !strings.Contains(err.Error(), "JavaScript error") {
			t.Errorf("%s: err %v", name, err)
		}
	}
}

func TestSetEnvFirst(t *testing.T) {
	f := &fakeDriver{screen: static()}
	r := New(f, quick)
	if err := r.SetEnv([]flow.KV{{Key: "X", Value: "${1+2}"}}); err != nil {
		t.Fatal(err)
	}
	cmds, _ := flow.ParseCommands(t.TempDir(), []byte("- inputText: ${X}"))
	if err := r.RunCommands(context.Background(), cmds, "app", nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.typed, []string{"3"}) {
		t.Errorf("typed %q", f.typed)
	}
	if err := r.SetEnv([]flow.KV{{Key: "Y", Value: "${(}"}}); err == nil {
		t.Error("bad value accepted")
	}
}

func TestRunCommands(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static(btn("Home", "", 0))}
	o := quick
	o.Env = []flow.KV{{Key: "E", Value: "env"}}
	r := New(f, o)
	cmds, err := flow.ParseCommands(t.TempDir(), []byte("- inputText: ${E}\n- assertVisible: {text: Nope, optional: true}\n- launchApp\n- assertVisible: Gone\n- inputText: never"))
	if err != nil {
		t.Fatal(err)
	}
	var steps []StepResult
	err = r.RunCommands(context.Background(), cmds, "com.agent", func(s StepResult) { steps = append(steps, s) })
	if err == nil || !strings.Contains(err.Error(), "Gone") {
		t.Fatalf("err %v", err)
	}
	var got []string
	for _, s := range steps {
		got = append(got, string(s.Status)+" "+s.Desc)
	}
	want := []string{"passed Input text ${E}", `warned Assert that "Nope" is visible`, `passed Launch app "com.agent"`, `failed Assert that "Gone" is visible`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("steps %q, want %q", got, want)
	}
	if steps[1].Err == "" || steps[0].Err != "" {
		t.Errorf("step errors %+v", steps)
	}
	if !reflect.DeepEqual(f.typed, []string{"env"}) {
		t.Errorf("typed %q", f.typed)
	}
	cmds, _ = flow.ParseCommands(t.TempDir(), []byte("- inputText: ${E}2"))
	if err := r.RunCommands(context.Background(), cmds, "com.agent", nil); err != nil {
		t.Fatal(err)
	}
	if f.typed[len(f.typed)-1] != "env2" {
		t.Errorf("typed %q", f.typed)
	}
}

func TestEnd(t *testing.T) {
	o := quick
	o.ArtifactsDir = t.TempDir()
	f := &fakeDriver{screen: static()}
	r := New(f, o)
	r.End()
	if len(f.called("StopRecording")) != 0 {
		t.Error("stopped with nothing running")
	}
	cmds, _ := flow.ParseCommands(t.TempDir(), []byte("- startRecording: r"))
	if err := r.RunCommands(context.Background(), cmds, "app", nil); err != nil {
		t.Fatal(err)
	}
	r.End()
	r.End()
	if n := len(f.called("StopRecording")); n != 1 {
		t.Errorf("%d stops, want 1", n)
	}
}

func TestSharedJSEngine(t *testing.T) {
	e := js.New("android")
	a := New(&fakeDriver{screen: static()}, Options{JS: e, LookupTimeout: time.Second})
	b := New(&fakeDriver{screen: static(), plat: "ios"}, Options{JS: e, LookupTimeout: time.Second})
	set, _ := flow.ParseCommands(t.TempDir(), []byte("- evalScript: ${output.from = tb.platform}"))
	check, _ := flow.ParseCommands(t.TempDir(), []byte("- assertTrue: ${output.from == 'android' && tb.platform == 'ios'}"))
	if err := a.RunCommands(context.Background(), set, "app", nil); err != nil {
		t.Fatal(err)
	}
	if err := b.RunCommands(context.Background(), check, "app", nil); err != nil {
		t.Errorf("b did not see a's output: %v", err)
	}
	if e.Output()["from"] != "android" {
		t.Errorf("output %v", e.Output())
	}
}

func TestSignalCommands(t *testing.T) {
	t.Parallel()
	sig := NewSignals()
	o := quick
	o.Signals = sig
	mustPass(t, runWith(t, &fakeDriver{screen: static()}, o, "- signal: ready\n- waitForSignal: ready\n- waitForSignal: {name: ready, timeout: 1000}"))
	go func() {
		time.Sleep(100 * time.Millisecond)
		sig.Send("later")
	}()
	start := time.Now()
	mustPass(t, runWith(t, &fakeDriver{screen: static()}, o, "- waitForSignal: later"))
	if d := time.Since(start); d < 80*time.Millisecond || d > time.Second {
		t.Errorf("woke after %s, want at the signal", d)
	}
	start = time.Now()
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, o, "- waitForSignal: {name: never, timeout: \"${100}\"}"), `signal "never" did not arrive within 100ms`)
	if d := time.Since(start); d > time.Second {
		t.Errorf("timed out after %s", d)
	}
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, o, "- waitForSignal: {name: x, timeout: soon}"), `invalid timeout "soon"`)
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, o, "- waitForSignal: {name: x, timeout: '${(}'}"), "JavaScript error")
}

func TestSignals(t *testing.T) {
	s := NewSignals()
	s.Send("a")
	s.Send("a")
	if err := s.Wait(context.Background(), "a", time.Second); err != nil {
		t.Errorf("raised signal: %v", err)
	}
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { done <- s.Wait(context.Background(), "b", 2*time.Second) }()
	}
	time.Sleep(20 * time.Millisecond)
	s.Send("b")
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Errorf("waiter %d: %v", i, err)
		}
	}
	err := s.Wait(context.Background(), "c", 30*time.Millisecond)
	if err == nil || !isFlowError(err) {
		t.Errorf("timeout: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Wait(ctx, "c", time.Second); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}

func TestAssertScreenshot(t *testing.T) {
	dir := t.TempDir()
	ref := pngOf(t, 40, 80, colorWhite, imageRect(0, 0, 0, 0), colorRed)
	half := pngOf(t, 40, 80, colorWhite, imageRect(0, 0, 40, 40), colorBlue)
	writeFile(t, dir, "ref.png", string(ref))
	writeFile(t, dir, "shots/ref2.png", string(ref))
	writeFile(t, dir, "broken.png", "not an image")
	writeFile(t, dir, "small.png", string(pngOf(t, 20, 20, colorWhite, imageRect(0, 0, 0, 0), colorRed)))
	run := func(shot []byte, o Options, cmd string) FlowResult {
		return runSrc(t, &fakeDriver{screen: static(btn("Logo", "logo", 0)), shot: shot}, o, dir, "appId: app\n---\n- "+cmd)
	}
	mustPass(t, run(ref, quick, "assertScreenshot: ref"))
	mustPass(t, run(ref, quick, "assertScreenshot: ref.png"))
	mustPass(t, run(ref, quick, "assertScreenshot: "+filepath.Join(dir, "ref")))
	mustPass(t, run(ref, quick, "assertScreenshot: shots/ref2"))
	mustFail(t, run(half, quick, "assertScreenshot: ref"), "screenshot matches 50.0% of the reference, below the threshold 95.0%")
	mustPass(t, run(half, quick, "assertScreenshot: {path: ref, thresholdPercentage: 40}"))
	mustFail(t, run(ref, quick, "assertScreenshot: small"), "differs from the reference")
	mustFail(t, run(ref, quick, "assertScreenshot: missing"), "reference screenshot not found: "+filepath.Join(dir, "missing.png"))
	mustFail(t, run(ref, quick, "assertScreenshot: missing.jpg"), "missing.jpg")
	mustFail(t, run(ref, quick, "assertScreenshot: broken"), "could not decode screenshots")
	mustFail(t, run([]byte("junk"), quick, "assertScreenshot: ref"), "could not decode screenshots")
	mustFail(t, run(ref, quick, "assertScreenshot: {path: ref, thresholdPercentage: most}"), `invalid thresholdPercentage "most"`)
	mustFail(t, run(ref, quick, "assertScreenshot: {path: ref, thresholdPercentage: '${(}'}"), "JavaScript error")

	o := quick
	o.ArtifactsDir = t.TempDir()
	writeFile(t, o.ArtifactsDir, "takeScreenshot/saved.png", string(ref))
	mustPass(t, run(ref, o, "assertScreenshot: saved"))

	f := &fakeDriver{screen: static()}
	f.fail("Screenshot", errors.New("secure window"))
	mustFail(t, runSrc(t, f, quick, dir, "appId: app\n---\n- assertScreenshot: ref"), "secure window")
}

func TestAssertScreenshotOtherFormats(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ref.heic", "heic bytes")
	ref := pngOf(t, 40, 80, colorWhite, imageRect(0, 0, 0, 0), colorRed)
	res := runSrc(t, &fakeDriver{screen: static(), shot: ref}, quick, dir, "appId: app\n---\n- assertScreenshot: ref.heic")
	mustFail(t, res, "ref.heic")
	if msg := res.Err.Error(); strings.Contains(msg, "not found") || strings.Contains(msg, "heic.png") || !strings.Contains(msg, "PNG and JPEG") {
		t.Errorf("error %q: want the .heic reference found and a note that tb reads PNG and JPEG", msg)
	}
}

func TestAssertScreenshotCropOn(t *testing.T) {
	dir := t.TempDir()
	logo := pngOf(t, 400, 50, colorRed, imageRect(0, 0, 0, 0), colorRed)
	writeFile(t, dir, "logo.png", string(logo))
	shot := pngOf(t, 400, 800, colorBlue, imageRect(0, 0, 400, 50), colorRed)
	f := &fakeDriver{screen: static(btn("Logo", "logo", 0)), shot: shot}
	mustPass(t, runSrc(t, f, quick, dir, "appId: app\n---\n- assertScreenshot: {path: logo, cropOn: {id: logo}}"))
	mustFail(t, runSrc(t, f, quick, dir, "appId: app\n---\n- assertScreenshot: logo"), "differs from the reference")
}

func TestArtifactPath(t *testing.T) {
	dir := t.TempDir()
	r := New(&fakeDriver{}, Options{ArtifactsDir: dir})
	p, err := r.artifactPath("takeScreenshot", " a/b ", ".png")
	if err != nil || p != filepath.Join(dir, "takeScreenshot", "a", "b.png") {
		t.Errorf("%q %v", p, err)
	}
	if st, err := os.Stat(filepath.Dir(p)); err != nil || !st.IsDir() {
		t.Errorf("folder not made: %v", err)
	}
	abs := filepath.Join(t.TempDir(), "x")
	if p, err := r.artifactPath("takeScreenshot", abs, ".png"); err != nil || p != abs+".png" {
		t.Errorf("absolute: %q %v", p, err)
	}
	wd := t.TempDir()
	t.Chdir(wd)
	r2 := New(&fakeDriver{}, Options{})
	if p, err := r2.artifactPath("startRecording", "rec/one", ".mp4"); err != nil || p != filepath.Join("rec", "one.mp4") {
		t.Errorf("relative: %q %v", p, err)
	}
	if _, err := os.Stat(filepath.Join(wd, "rec")); err != nil {
		t.Errorf("relative folder not made: %v", err)
	}
	blocker := writeFile(t, dir, "file", "x")
	if _, err := r.artifactPath("takeScreenshot", filepath.Join(blocker, "sub", "x"), ".png"); err == nil {
		t.Error("made a folder inside a file")
	}
}
