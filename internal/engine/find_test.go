package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
	"github.com/elforama/testbuddy/internal/wire"
)

func TestAssertVisibleFails(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static(btn("Home", "", 0))}
	res := runWith(t, f, quick, "- assertVisible: {text: Nope, id: x}")
	mustFail(t, res, `Assertion is false: "Nope", id: x is visible`)
	var nf *NotFound
	if !errors.As(res.Err, &nf) || nf.Screen == nil || nf.Query != "Nope" {
		t.Errorf("err %#v, want a NotFound with the screen", res.Err)
	}
	if !isFlowError(res.Err) {
		t.Errorf("a failed assertion is a flow error")
	}
}

func TestScreenReadReused(t *testing.T) {
	f := &fakeDriver{screen: static(btn("Home", "", 0), btn("Next", "", 100))}
	mustPass(t, runWith(t, f, quick, "- assertVisible: Home\n- assertVisible: Next\n- assertNotVisible: Gone"))
	if n := f.reads.Load(); n != 1 {
		t.Errorf("%d screen reads of an unchanged screen, want 1", n)
	}
	f.change(true)
	r := New(f, quick)
	fl, _ := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte("appId: app\n---\n- assertVisible: Home"))
	before := f.reads.Load()
	mustPass(t, r.RunFlow(context.Background(), fl))
	if r.LastScreen() == nil || f.reads.Load() != before+1 {
		t.Errorf("reads %d -> %d; last screen %v", before, f.reads.Load(), r.LastScreen())
	}
}

func TestAssertNotVisibleWaitsForGone(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var gone atomic.Bool
	f.screen = func() []*ui.Node {
		if gone.Load() {
			return nil
		}
		return []*ui.Node{btn("Spinner", "", 0)}
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		gone.Store(true)
		f.change(true)
	}()
	start := time.Now()
	o := quick
	o.LookupTimeout = 2 * time.Second
	mustPass(t, runWith(t, f, o, "- assertNotVisible: Spinner"))
	if d := time.Since(start); d < 100*time.Millisecond || d > time.Second {
		t.Errorf("passed after %s, want right after the change at 150 ms", d)
	}
}

func TestAssertNotVisibleFails(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static(btn("Home", "", 0))}
	start := time.Now()
	mustFail(t, runWith(t, f, quick, "- assertNotVisible: Home"), `Assertion is false: "Home" is not visible`)
	if d := time.Since(start); d < 300*time.Millisecond {
		t.Errorf("gave up after %s, before the budget", d)
	}
}

func TestAssertNotVisibleChangeWhileSettling(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		returns bool
	}{{"stays gone", false}, {"comes back", true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDriver{}
			var back atomic.Bool
			f.screen = func() []*ui.Node {
				if back.Load() {
					return []*ui.Node{btn("Popup", "", 0)}
				}
				return []*ui.Node{btn("Home", "", 100)}
			}
			var once atomic.Bool
			f.onWait = func() {
				if once.CompareAndSwap(false, true) {
					back.Store(tc.returns)
					f.change(true)
				}
			}
			res := runWith(t, f, quick, "- assertNotVisible: Popup")
			if tc.returns {
				mustFail(t, res, "is not visible")
			} else {
				mustPass(t, res)
			}
		})
	}
}

func TestAssertTrue(t *testing.T) {
	for _, tc := range []struct {
		cond string
		ok   bool
	}{
		{"${1 == 1}", true},
		{"yes", true},
		{"${'0.5'}", true},
		{"${1 == 2}", false},
		{"false", false},
		{"${null}", false},
		{"${undefined}", false},
		{"0", false},
		{"' '", false},
	} {
		t.Run(tc.cond, func(t *testing.T) {
			res := runWith(t, &fakeDriver{screen: static()}, quick, "- assertTrue: "+tc.cond)
			if tc.ok {
				mustPass(t, res)
			} else {
				mustFail(t, res, "Assertion is false: ")
				if !strings.Contains(res.Err.Error(), "is true") {
					t.Errorf("err %q: want the condition named", res.Err)
				}
			}
		})
	}
	res := runWith(t, &fakeDriver{screen: static()}, quick, "- assertTrue: {condition: \"${false}\", optional: true}")
	mustPass(t, res)
	if res.Steps[0].Status != Warned {
		t.Errorf("status %s", res.Steps[0].Status)
	}
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- assertTrue: {condition: \"${nope()}\", optional: true}"), "JavaScript error")
}

func TestExtendedWaitUntil(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var shown atomic.Bool
	f.screen = func() []*ui.Node {
		if shown.Load() {
			return []*ui.Node{btn("Ready", "", 0)}
		}
		return []*ui.Node{btn("Loading", "", 0)}
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		shown.Store(true)
		f.change(true)
	}()
	o := quick
	o.LookupTimeout = 50 * time.Millisecond
	mustPass(t, runWith(t, f, o, "- extendedWaitUntil: {visible: Ready, timeout: 2000}\n- extendedWaitUntil: {notVisible: Loading, timeout: \"${'500'}\"}"))

	start := time.Now()
	res := runWith(t, &fakeDriver{screen: static()}, o, "- extendedWaitUntil: {visible: Never, timeout: 300}")
	mustFail(t, res, `Assertion is false: "Never" is visible`)
	if d := time.Since(start); d < 250*time.Millisecond || d > 1500*time.Millisecond {
		t.Errorf("gave up after %s, want 300 ms", d)
	}
	mustFail(t, runWith(t, &fakeDriver{screen: static(btn("Stays", "", 0))}, o, "- extendedWaitUntil: {notVisible: Stays, timeout: 100}"), `"Stays" is not visible`)
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, o, "- extendedWaitUntil: {visible: X, timeout: soon}"), `invalid timeout "soon"`)
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, o, "- extendedWaitUntil: {visible: X, timeout: '${(}'}"), "JavaScript error")
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, o, "- extendedWaitUntil: {visible: '${(}', timeout: 100}"), "JavaScript error")
	start = time.Now()
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- extendedWaitUntil: {visible: X}"), "is visible")
	if d := time.Since(start); d < 300*time.Millisecond {
		t.Errorf("no timeout: gave up after %s, want the 400 ms budget", d)
	}
}

func TestFindReadsFirstChangeBeforeSettle(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	reading := make(chan struct{}, 1)
	var visible atomic.Bool
	f.screen = func() []*ui.Node {
		if visible.Load() {
			return []*ui.Node{btn("Ready", "", 100)}
		}
		select {
		case reading <- struct{}{}:
		default:
		}
		return nil
	}
	go func() {
		<-reading
		visible.Store(true)
		f.change(false)
	}()
	start := time.Now()
	mustPass(t, runWith(t, f, quick, "- extendedWaitUntil: {visible: Ready, timeout: 2000}"))
	if elapsed := time.Since(start); elapsed >= restlessAfter {
		t.Errorf("positive lookup took %s without a settle event", elapsed)
	}
	if reads := f.reads.Load(); reads != 2 {
		t.Errorf("read %d screens; expected one initial read and one on change", reads)
	}
}

func TestFindOnRestlessScreen(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var shown atomic.Bool
	f.screen = func() []*ui.Node {
		if shown.Load() {
			return []*ui.Node{btn("Done", "", 100)}
		}
		return []*ui.Node{btn("Working", "", 100)}
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(30 * time.Millisecond):
				if i == 3 {
					shown.Store(true)
				}
				f.change(false)
			}
		}
	}()
	o := quick
	o.LookupTimeout = 3 * time.Second
	start := time.Now()
	mustPass(t, runWith(t, f, o, "- assertVisible: Done"))
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Errorf("found after %s on a restless screen", d)
	}
	if n := f.reads.Load(); n > 5 {
		t.Errorf("%d reads: the restless wait polled", n)
	}
}

func TestFindSilentDevice(t *testing.T) {
	defer func(d time.Duration) { silentAfter = d }(silentAfter)
	silentAfter = 150 * time.Millisecond
	f := &fakeDriver{}
	var shown atomic.Int64
	f.screen = func() []*ui.Node {
		switch shown.Load() {
		case 0:
			return []*ui.Node{btn("Loading", "", 0)}
		case 1:
			return []*ui.Node{btn("First", "", 0)}
		}
		return []*ui.Node{btn("Second", "", 0)}
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		shown.Store(1)
		time.Sleep(400 * time.Millisecond)
		shown.Store(2)
	}()
	rec := &recorder{}
	o := quick
	o.LookupTimeout = 3 * time.Second
	o.Reporter = rec
	start := time.Now()
	mustPass(t, runWith(t, f, o, "- assertVisible: First\n- assertVisible: Second"))
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %s", d)
	}
	warned := 0
	for _, l := range rec.logs() {
		if strings.Contains(l, "without the device reporting it") {
			warned++
		}
	}
	if warned != 1 {
		t.Errorf("%d silence warnings, want 1: %q", warned, rec.logs())
	}
}

func TestUnknownEventWithNewsWakes(t *testing.T) {
	t.Setenv("TB_CI", "0")
	defer func(d time.Duration) { silentAfter = d }(silentAfter)
	silentAfter = 10 * time.Second
	f := &fakeDriver{}
	var loaded atomic.Bool
	f.screen = func() []*ui.Node {
		if loaded.Load() {
			return []*ui.Node{btn("Login", "", 0)}
		}
		return []*ui.Node{btn("Loading", "", 0)}
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		loaded.Store(true)
		f.mu.Lock()
		f.seq++
		seq := f.seq
		f.mu.Unlock()
		f.push(driver.Event{Kind: "trace", Seq: seq})
	}()
	o := quick
	o.LookupTimeout = 5 * time.Second
	start := time.Now()
	mustPass(t, runWith(t, f, o, "- extendedWaitUntil: {visible: Login, timeout: 5000}"))
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("done after %s: an unknown event with news did not wake the wait", d)
	}
}

func TestSilenceNetIgnoresStaleEvents(t *testing.T) {
	steps := []struct {
		step   string
		absent bool
	}{
		{"- extendedWaitUntil: {visible: Login, timeout: 3000}", false},
		{"- assertVisible: Login", false},
		{"- assertNotVisible: Login", true},
		{"- extendedWaitUntil: {notVisible: Login, timeout: 3000}", true},
	}
	stale := []struct {
		name  string
		kinds []string
		once  bool
	}{
		{"settled once", []string{"settled"}, true},
		{"chatter", []string{"settled", "changed", "trace"}, false},
		{"change chatter", []string{"changed"}, false},
	}
	for _, st := range steps {
		for _, ev := range stale {
			t.Run(ev.name+" "+st.step[2:], func(t *testing.T) {
				t.Setenv("TB_CI", "0")
				defer func(d time.Duration) { silentAfter = d }(silentAfter)
				silentAfter = 100 * time.Millisecond
				f := &fakeDriver{}
				var loaded atomic.Bool
				f.screen = func() []*ui.Node {
					if loaded.Load() != st.absent {
						return []*ui.Node{btn("Login", "", 0)}
					}
					return []*ui.Node{btn("Loading", "", 0)}
				}
				done := make(chan struct{})
				defer close(done)
				go func() {
					start := time.Now()
					for i := 0; ; i++ {
						select {
						case <-done:
							return
						case <-time.After(50 * time.Millisecond):
						}
						if time.Since(start) > 250*time.Millisecond {
							loaded.Store(true)
						}
						if ev.once && i > 0 {
							continue
						}
						f.push(driver.Event{Kind: ev.kinds[i%len(ev.kinds)], Seq: f.Seq()})
					}
				}()
				o := quick
				o.LookupTimeout = 3 * time.Second
				start := time.Now()
				mustPass(t, runWith(t, f, o, st.step))
				if d := time.Since(start); d > 1500*time.Millisecond {
					t.Errorf("done after %s: events with no news switched the silence re-reads off", d)
				}
			})
		}
	}
}

func TestFindSilentNoChange(t *testing.T) {
	defer func(d time.Duration) { silentAfter = d }(silentAfter)
	silentAfter = 50 * time.Millisecond
	f := &fakeDriver{screen: static(btn("Home", "", 0))}
	rec := &recorder{}
	o := quick
	o.Reporter = rec
	mustFail(t, runWith(t, f, o, "- assertVisible: Nope"), "Nope")
	if len(rec.logs()) != 0 {
		t.Errorf("warned %q with no change", rec.logs())
	}
	if n := f.reads.Load(); n < 2 || n > 6 {
		t.Errorf("%d reads", n)
	}
}

func TestFindSilentDeadlineReadsFresh(t *testing.T) {
	t.Run("ci", func(t *testing.T) {
		t.Setenv("TB_CI", "1")
		mustPass(t, silentDeadlineLookup(t))
	})
	t.Run("not ci", func(t *testing.T) {
		t.Setenv("TB_CI", "0")
		mustFail(t, silentDeadlineLookup(t), `"Account login" is visible`)
	})
}

func silentDeadlineLookup(t *testing.T) FlowResult {
	defer func(d time.Duration) { silentAfter = d }(silentAfter)
	silentAfter = 400 * time.Millisecond
	f := &fakeDriver{}
	var shown atomic.Bool
	f.screen = func() []*ui.Node {
		if shown.Load() {
			return []*ui.Node{btn("Account login", "", 0)}
		}
		return []*ui.Node{btn("Loading", "", 0)}
	}
	go func() {
		time.Sleep(2200 * time.Millisecond)
		shown.Store(true)
	}()
	o := quick
	o.LookupTimeout = 2600 * time.Millisecond
	return runWith(t, f, o, "- assertVisible: Account login")
}

func TestFindDisconnected(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static()}
	go func() {
		time.Sleep(50 * time.Millisecond)
		f.push(driver.Event{Kind: "disconnected"})
	}()
	o := quick
	o.LookupTimeout = 3 * time.Second
	o.OptionalTimeout = 3 * time.Second
	start := time.Now()
	mustFail(t, runWith(t, f, o, "- tapOn: {text: Nope, optional: true}"), "lost the connection to the device agent")
	if d := time.Since(start); d > time.Second {
		t.Errorf("noticed after %s", d)
	}
	f2 := &fakeDriver{screen: static(btn("Home", "", 0))}
	go func() {
		time.Sleep(50 * time.Millisecond)
		f2.push(driver.Event{Kind: "disconnected"})
	}()
	mustFail(t, runWith(t, f2, o, "- assertNotVisible: Home"), "lost the connection")
}

func TestFindCancelled(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static()}
	fl, err := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte("appId: app\n---\n- assertVisible: Nope\n- inputText: never"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	res := New(f, Options{LookupTimeout: 5 * time.Second}).RunFlow(ctx, fl)
	if !errors.Is(res.Err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Errorf("err %v after %s", res.Err, time.Since(start))
	}
	if len(f.typed) != 0 {
		t.Errorf("ran on after the cancel")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	fl2, _ := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte("appId: app\n---\n- assertNotVisible: Home"))
	res = New(&fakeDriver{screen: static(btn("Home", "", 0))}, Options{LookupTimeout: 5 * time.Second}).RunFlow(ctx2, fl2)
	if !errors.Is(res.Err, context.DeadlineExceeded) {
		t.Errorf("assertNotVisible err %v", res.Err)
	}
}

func TestFindScreenError(t *testing.T) {
	for _, cmd := range []string{"- assertVisible: X", "- assertNotVisible: X", "- tapOn: X", "- scrollUntilVisible: {element: X}"} {
		f := &fakeDriver{screen: static()}
		f.fail("Screen", errors.New("dump failed"))
		mustFail(t, runWith(t, f, quick, cmd), "dump failed")
	}
}

func TestFindTransientScreenError(t *testing.T) {
	for _, cmd := range []string{"- assertVisible: Home", "- assertNotVisible: Gone", "- tapOn: Home"} {
		f := &fakeDriver{screen: static(btn("Home", "", 0))}
		f.screenErr = []error{treeBlip, treeBlip}
		mustPass(t, runWith(t, f, quick, cmd))
	}
}

var treeBlip = &wire.AgentError{Op: "tree", Msg: "Collection contains no element matching the predicate."}

func TestFindLostDeviceFailsAtOnce(t *testing.T) {
	f := &fakeDriver{screen: static()}
	f.fail("Screen", errors.New("android driver: closed"))
	start := time.Now()
	mustFail(t, runWith(t, f, Options{LookupTimeout: 2 * time.Second}, "- assertVisible: X"), "closed")
	if n := f.reads.Load(); n != 1 {
		t.Errorf("%d reads, want 1", n)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("failed after %s, want at once", d)
	}
}

func TestAbsenceNotJudgedOnAnUnreadScreen(t *testing.T) {
	blind := func() *fakeDriver {
		f := &fakeDriver{screen: static()}
		f.noApp.Store(true)
		f.unread.Store(1)
		return f
	}
	mustFail(t, runWith(t, blind(), quick, "- assertNotVisible: Home"), "Could not check")
	mustFail(t, runWith(t, blind(), quick, "- extendedWaitUntil: {notVisible: Home, timeout: 300}"), "")
	f := blind()
	mustPass(t, runWith(t, f, quick, "- runFlow:\n    when: {notVisible: Home}\n    commands:\n      - setOrientation: PORTRAIT"))
	if len(f.orients) != 0 {
		t.Errorf("when: block ran on an unread screen: %v", f.orients)
	}
	f = blind()
	time.AfterFunc(150*time.Millisecond, func() { f.noApp.Store(false); f.unread.Store(0); f.change(true) })
	mustPass(t, runWith(t, f, Options{LookupTimeout: 2 * time.Second}, "- assertNotVisible: Home"))
}

func TestAbsenceConfirmReadRetried(t *testing.T) {
	f := &fakeDriver{screen: static(btn("Other", "", 0))}
	var once atomic.Bool
	f.onWait = func() {
		if once.CompareAndSwap(false, true) {
			f.mu.Lock()
			f.seq++
			f.screenErr = []error{treeBlip}
			f.mu.Unlock()
		}
	}
	mustPass(t, runWith(t, f, quick, "- assertNotVisible: Home"))
}

func TestFailedFreshReadIsRetriedFresh(t *testing.T) {
	c := newNodeCacheFake(func() []*ui.Node { return []*ui.Node{btn("Home", "", 0)} })
	c.screenErr = []error{treeBlip}
	mustPass(t, runWith(t, c, quick, "- assertVisible: Home"))
	if n := c.fresh.Load(); n != 2 {
		t.Errorf("%d fresh reads, want 2 (the failed one and its retry)", n)
	}
}

func TestInvalidIndexFails(t *testing.T) {
	for _, cmd := range []string{
		"- tapOn: {text: Home, index: abc, optional: true}",
		"- assertVisible: {text: Home, index: abc, optional: true}",
		"- scrollUntilVisible: {element: {text: Home, index: abc}, optional: true}",
	} {
		res := runWith(t, &fakeDriver{screen: static(btn("Home", "", 0))}, quick, cmd)
		mustFail(t, res, `invalid index "abc"`)
	}
}

func TestIndexPicks(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static(btn("Row", "a", 0), btn("Row", "b", 100), btn("Row", "c", 200))}
	mustPass(t, runWith(t, f, quick, "- tapOn: {text: Row, index: 1}\n- tapOn: {text: Row, index: \"${-1}\"}"))
	if want := [][2]int{{200, 125}, {200, 225}}; !reflect.DeepEqual(f.taps, want) {
		t.Errorf("taps %v, want %v", f.taps, want)
	}
	res := runWith(t, f, quick, "- assertVisible: {text: Row, index: 5, optional: true}")
	if res.Steps[0].Status != Warned {
		t.Errorf("index past the end: %s", res.Steps[0].Status)
	}
}

func TestCSSSelectors(t *testing.T) {
	t.Parallel()
	nodes := []*ui.Node{btn("A", "", 0), btn("B", "", 100), btn("B", "", 200)}
	d := &cssFake{fakeDriver: &fakeDriver{screen: static(nodes...), plat: "web"}, q: map[string][]int{".b": {2}, ".a": {0}}}
	mustPass(t, runWith(t, d, quick, "- tapOn: {css: .b}\n- assertVisible: {text: B, below: {css: .a}}"))
	if !reflect.DeepEqual(d.taps, [][2]int{{200, 225}}) {
		t.Errorf("taps %v, want the .b element", d.taps)
	}
	d.err = errors.New("bad selector")
	mustFail(t, runWith(t, d, quick, "- assertVisible: {css: '..'}"), `css "..": bad selector`)
	res := runWith(t, d, quick, "- assertVisible: {css: '..', optional: true}")
	if res.Steps[0].Status != Warned {
		t.Errorf("optional css error: %s", res.Steps[0].Status)
	}
	mustFail(t, runWith(t, &fakeDriver{screen: static(nodes...)}, quick, "- assertVisible: {css: .b}"), "css: .b")
}

func TestResolveSelector(t *testing.T) {
	r := New(&fakeDriver{screen: static()}, quick)
	r.pushEnv()
	r.env()["V"] = "v"
	w, tol, on := 10, 2, true
	s := &flow.Selector{Text: "${V}t", ID: "${V}i", Index: "${1+1}", CSS: ".${V}", Width: &w, Tolerance: &tol, Enabled: &on, Traits: []string{"text"},
		Below: &flow.Selector{Text: "${V}below"}, Above: &flow.Selector{Text: "${V}above"},
		LeftOf: &flow.Selector{Text: "${V}left"}, RightOf: &flow.Selector{Text: "${V}right"},
		ContainsChild: &flow.Selector{Text: "${V}child"}, ChildOf: &flow.Selector{ID: "${V}parent"},
		ContainsDescendants: []*flow.Selector{{Text: "${V}d1"}, {ID: "${V}d2"}}}
	m, err := r.resolveSelector(s)
	if err != nil {
		t.Fatal(err)
	}
	if m.Text != "vt" || m.ID != "vi" || m.Index != "2" || m.CSS != ".v" || m.Width != &w || m.Tolerance != &tol || m.Enabled != &on || len(m.Traits) != 1 {
		t.Errorf("fields %+v", m)
	}
	if m.Below.Text != "vbelow" || m.Above.Text != "vabove" || m.LeftOf.Text != "vleft" || m.RightOf.Text != "vright" ||
		m.ContainsChild.Text != "vchild" || m.ChildOf.ID != "vparent" || len(m.ContainsDescendants) != 2 ||
		m.ContainsDescendants[0].Text != "vd1" || m.ContainsDescendants[1].ID != "vd2" {
		t.Errorf("relatives %+v", m)
	}
	if m, err := r.resolveSelector(nil); m != nil || err != nil {
		t.Errorf("nil selector: %v %v", m, err)
	}
	bad := "${(}"
	for name, s := range map[string]*flow.Selector{
		"text": {Text: bad}, "id": {ID: bad}, "index": {Index: bad}, "css": {CSS: bad},
		"below": {Below: &flow.Selector{Text: bad}}, "childOf": {ChildOf: &flow.Selector{ID: bad}},
		"descendants": {ContainsDescendants: []*flow.Selector{{Text: "ok"}, {Text: bad}}},
	} {
		if _, err := r.resolveSelector(s); err == nil || !strings.Contains(err.Error(), "JavaScript error") {
			t.Errorf("%s: err %v", name, err)
		}
	}
}

func TestRelativeSelectors(t *testing.T) {
	list := at("List", 0, 0, 0, 400, 800)
	card := at("Card", 1, 0, 300, 400, 400)
	title := btn("Title", "title", 300)
	title.Depth = 2
	buy := btn("Buy", "buy", 350)
	buy.Depth = 2
	buy.Bounds = ui.Rect{L: 200, T: 350, R: 400, B: 400}
	info := btn("Info", "info", 350)
	info.Depth = 2
	info.Bounds = ui.Rect{L: 0, T: 350, R: 200, B: 400}
	header := btn("Header", "", 0)
	header.Depth = 1
	f := &fakeDriver{screen: static(list, header, card, title, info, buy)}
	mustPass(t, runWith(t, f, quick, `
- assertVisible: {text: Buy, below: {text: Title}}
- assertVisible: {text: Header, above: {text: Title}}
- assertVisible: {text: Info, leftOf: {text: Buy}}
- assertVisible: {text: Buy, rightOf: {text: Info}}
- assertVisible: {id: buy, childOf: {containsChild: {text: Title}}}
- assertVisible: {containsDescendants: [Title, Buy]}
- assertNotVisible: {text: Header, below: {text: Title}}
`))
}

func TestAbsentErrors(t *testing.T) {
	d := &cssFake{fakeDriver: &fakeDriver{screen: static(), plat: "web"}, err: errors.New("bad selector")}
	mustFail(t, runWith(t, d, quick, "- assertNotVisible: {css: '..'}"), "bad selector")
	mustFail(t, runWith(t, &fakeDriver{screen: static(btn("A", "", 0))}, quick, "- assertNotVisible: {text: A, index: x}"), `invalid index "x"`)
	f := &fakeDriver{screen: static()}
	f.onWait = func() {
		f.fail("Screen", errors.New("read failed"))
		f.change(true)
	}
	mustFail(t, runWith(t, f, quick, "- assertNotVisible: Nope"), "read failed")
}

func TestAbsentSilentDevice(t *testing.T) {
	defer func(d time.Duration) { silentAfter = d }(silentAfter)
	silentAfter = 100 * time.Millisecond
	f := &fakeDriver{}
	var gone atomic.Bool
	f.screen = func() []*ui.Node {
		if gone.Load() {
			return nil
		}
		return []*ui.Node{btn("Toast", "", 0)}
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		gone.Store(true)
	}()
	o := quick
	o.LookupTimeout = 2 * time.Second
	start := time.Now()
	mustPass(t, runWith(t, f, o, "- assertNotVisible: Toast"))
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %s", d)
	}
}

func TestFindSilentGapCapped(t *testing.T) {
	for _, mode := range []string{"1", "0"} {
		t.Run("TB_CI="+mode, func(t *testing.T) {
			t.Setenv("TB_CI", mode)
			defer func(d time.Duration) { silentAfter = d }(silentAfter)
			silentAfter = 100 * time.Millisecond
			f := &fakeDriver{}
			var shown atomic.Bool
			f.screen = func() []*ui.Node {
				if shown.Load() {
					return []*ui.Node{btn("Account login", "", 0)}
				}
				return []*ui.Node{btn("Loading", "", 0)}
			}
			go func() {
				time.Sleep(2 * time.Second)
				shown.Store(true)
			}()
			start := time.Now()
			mustPass(t, runWith(t, f, quick, "- extendedWaitUntil: {visible: Account login, timeout: 3000}"))
			if d := time.Since(start); d > 2600*time.Millisecond {
				t.Errorf("found after %s: the re-reads on a silent screen came too far apart", d)
			}
		})
	}
}

func TestAbsentSilentDeadlineReadsFresh(t *testing.T) {
	for _, step := range []string{
		"- assertNotVisible: Toast",
		"- extendedWaitUntil: {notVisible: Toast, timeout: 1000}",
	} {
		for _, mode := range []string{"1", "0"} {
			t.Run("TB_CI="+mode+" "+step[2:], func(t *testing.T) {
				t.Setenv("TB_CI", mode)
				defer func(d time.Duration) { silentAfter = d }(silentAfter)
				silentAfter = 500 * time.Millisecond
				f := &fakeDriver{}
				var gone atomic.Bool
				f.screen = func() []*ui.Node {
					if gone.Load() {
						return nil
					}
					return []*ui.Node{btn("Toast", "", 0)}
				}
				go func() {
					time.Sleep(650 * time.Millisecond)
					gone.Store(true)
				}()
				o := quick
				o.LookupTimeout = 1000 * time.Millisecond
				res := runWith(t, f, o, step)
				if mode == "1" {
					mustPass(t, res)
				} else {
					mustFail(t, res, `"Toast" is not visible`)
				}
			})
		}
	}
}

func TestDefineWithoutScope(t *testing.T) {
	r := New(&fakeDriver{}, quick)
	if err := r.define([]flow.KV{{Key: "A", Value: "${'a'}"}}); err != nil {
		t.Fatal(err)
	}
	if r.env()["A"] != "a" {
		t.Errorf("env %v", r.env())
	}
	r.popEnv()
	r.popEnv()
	if len(r.env()) != 0 {
		t.Errorf("env %v", r.env())
	}
}

func TestLargerAndCutOff(t *testing.T) {
	full := ui.Rect{L: 300, T: 100, R: 547, B: 226}
	if got := larger(full, ui.Rect{L: 42, T: 100, R: 94, B: 226}); got != full {
		t.Errorf("a narrower read kept: %v", got)
	}
	if got := larger(ui.Rect{L: 42, T: 100, R: 94, B: 226}, full); got != full {
		t.Errorf("the wider read not kept: %v", got)
	}
	if got := larger(ui.Rect{R: 100, B: 10}, ui.Rect{R: 10, B: 100}); got != (ui.Rect{R: 100, B: 100}) {
		t.Errorf("width from one, height from the other: %v", got)
	}
	for _, c := range []struct {
		r    ui.Rect
		want bool
	}{
		{ui.Rect{L: 42, T: 100, R: 94, B: 226}, true},
		{ui.Rect{L: 300, T: 140, R: 547, B: 226}, true},
		{ui.Rect{L: 301, T: 101, R: 546, B: 225}, false},
		{full, false},
		{ui.Rect{L: 200, T: 100, R: 447, B: 226}, false},
	} {
		if got := cutOff(c.r, full); got != c.want {
			t.Errorf("cutOff(%v) = %v, want %v", c.r, got, c.want)
		}
	}
}
