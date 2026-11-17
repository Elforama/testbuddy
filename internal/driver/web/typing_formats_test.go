package web

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
)

// fieldPage is a fake page with one focused text field, driven by the keys tb sends: each key
// changes the field at once (a change event), and the page rewrites it later, as apps do
// after typing: a formatter (a phone mask) applied maskAfter past the last key, and a
// debounced update (a label repeating the value) debounceAfter past the last key.
//
// Both are setTimeouts, re-set by each key (issue #312): the fake sends what page.js sends
// for them, the count of pending short timers (one note per change of the count, after the
// change the callback made). With untracked set, the mask and the debounce come through
// something page.js does not track (a MessageChannel chain, a requestIdleCallback): no note.
type fieldPage struct {
	page *fakeCDP
	d    *Driver

	mu     sync.Mutex
	raw    string // what the keys put in
	value  string // what the field shows
	label  string // the debounced label
	mask   func(string) string
	timers []*time.Timer
	probes int // eraseText probes answered

	untracked bool        // the mask and the debounce are not tracked timers
	pending   int         // tracked short timers pending
	maskT     *time.Timer // the mask's timer, and the debounce's
	debT      *time.Timer
}

func newFieldPage(t *testing.T, mask func(string) string, maskAfter, debounceAfter time.Duration) *fieldPage {
	p := &fieldPage{page: newFakeCDP(t), mask: mask, label: "Saved: "}
	notify := func() {
		p.page.event("Runtime.bindingCalled", map[string]any{"name": "__tbNotify", "payload": `{"a":0,"s":1}`}, "")
	}
	// sendTimers sends page.js's pending-timer count, n.
	sendTimers := func(n int) {
		p.page.event("Runtime.bindingCalled", map[string]any{"name": "__tbNotify", "executionContextId": 1,
			"payload": jsonArg(map[string]any{"k": "t", "t": n})}, "")
	}
	// ran is a timer's callback done: its change, then the count without it.
	ran := func(tracked bool) {
		notify()
		if tracked {
			p.mu.Lock()
			p.pending--
			n := p.pending
			p.mu.Unlock()
			sendTimers(n)
		}
	}
	later := func(after time.Duration, f func()) { // caller holds mu
		before := p.pending
		p.timers = nil // the key clears the timers the last one set
		if p.maskT != nil && p.maskT.Stop() && !p.untracked {
			p.pending--
		}
		if p.debT != nil && p.debT.Stop() && !p.untracked {
			p.pending--
		}
		p.maskT, p.debT = nil, nil
		if mask != nil {
			tracked := !p.untracked
			if tracked {
				p.pending++
			}
			p.maskT = time.AfterFunc(maskAfter, func() {
				p.mu.Lock()
				p.value = p.mask(p.raw)
				p.mu.Unlock()
				ran(tracked)
			})
			p.timers = append(p.timers, p.maskT)
		}
		tracked := !p.untracked
		if tracked {
			p.pending++
		}
		p.debT = time.AfterFunc(after, func() { f(); ran(tracked) })
		p.timers = append(p.timers, p.debT)
		if n := p.pending; n != before {
			go sendTimers(n) // the key's handler holds mu; its change goes the same way (below)
		}
	}
	p.page.on("Input.dispatchKeyEvent", func(m cdpMsg) any {
		pr := m.params()
		p.mu.Lock()
		defer p.mu.Unlock()
		switch {
		case pr["type"] == "keyDown" && pr["text"] != nil:
			p.raw += pr["text"].(string)
		case pr["type"] == "rawKeyDown" && pr["key"] == "Backspace" && p.raw != "":
			p.raw = p.raw[:len(p.raw)-1]
		default:
			return map[string]any{}
		}
		p.value = p.raw
		later(debounceAfter, func() {
			p.mu.Lock()
			p.label = "Saved: " + p.value
			p.mu.Unlock()
		})
		go notify()
		return map[string]any{}
	})
	p.page.evaluator(func(expr, session string) any {
		p.mu.Lock()
		defer p.mu.Unlock()
		switch {
		case expr == snapshotExpr:
			return value(jsonArg(map[string]any{
				"w": 1024, "h": 700, "url": "http://bench.test/", "title": "Form", "loading": false,
				"nodes": []map[string]any{
					{"d": 0, "b": []int{0, 0, 1024, 700}, "c": "body", "f": 4098},
					{"d": 1, "b": []int{10, 60, 300, 90}, "c": "input:text", "id": "phone", "t": p.value, "v": p.value, "f": 2048 | 4096 | 256 | 4 | 2 | 1},
					{"d": 1, "b": []int{10, 100, 300, 130}, "c": "span", "id": "saved", "t": p.label, "f": 4098},
				},
			}))
		case expr == eraseProbe: // eraseText's: the field's length, its caret put at the end
			p.probes++
			return value(jsonArg(map[string]any{"len": len(p.value), "placed": true}))
		}
		return nil
	})
	p.d = newWebDriver(t, p.page, nil)
	t.Cleanup(func() {
		p.mu.Lock()
		for _, tm := range p.timers {
			tm.Stop()
		}
		p.mu.Unlock()
	})
	return p
}

func (p *fieldPage) run(t *testing.T, steps string) (engine.FlowResult, string) {
	t.Helper()
	f, err := flow.ParseBytes(filepath.Join(t.TempDir(), "f.yaml"), []byte("url: http://bench.test/\n---\n"+steps))
	if err != nil {
		t.Fatal(err)
	}
	var trace strings.Builder
	o := engine.Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second, Trace: engine.NewTrace(&trace)}
	return engine.New(p.d, o).RunFlow(ctxT(t), f), trace.String()
}

func phoneMask(s string) string {
	if len(s) != 10 {
		return s
	}
	return "(" + s[:3] + ") " + s[3:6] + "-" + s[6:]
}

// Issue #310 (context; a guard): an assert right after typing sees the final value, whatever
// the page does to it after the keys: a mask rewriting the field 60 ms after the last key, and
// a debounced update (300 ms, longer than the quiet window) showing it elsewhere; after erasing
// too. copyTextFrom reads the field once, without waiting: right after the keys it sees the
// masked value only because typing and erasing wait for the page to settle (the mask comes
// within the quiet window). The debounced label is checked with assertVisible, which waits.
func TestAssertRightAfterTypingSeesFinalValue(t *testing.T) {
	p := newFieldPage(t, phoneMask, 60*time.Millisecond, 300*time.Millisecond)
	res, trace := p.run(t, `- inputText: "5551234567"
- copyTextFrom: {id: phone}
- assertTrue: ${maestro.copiedText == '(555) 123-4567'}
- assertVisible: {id: phone, text: "\\(555\\) 123-4567"}
- assertVisible: "Saved: \\(555\\) 123-4567"
- eraseText: 4
- assertVisible: {id: phone, text: "555123"}
- assertVisible: "Saved: 555123"
- inputText: "45678"
- assertVisible: {id: phone, text: "55512345678"}
- eraseText: 1
- copyTextFrom: {id: phone}
- assertTrue: ${maestro.copiedText == '(555) 123-4567'}
- assertVisible: "Saved: \\(555\\) 123-4567"
`)
	if res.Status != engine.Passed {
		t.Fatalf("%s: %v\n%s", res.Status, res.Err, trace)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.probes != 2 {
		t.Errorf("%d eraseText probes answered, want 2", p.probes)
	}
}

// Issue #312: a mask applied through something page.js does not track (a MessageChannel
// chain, a requestIdleCallback: no timer note). Within the 40 ms quiet window it is caught,
// even by copyTextFrom, which reads once without waiting. Later than that (60 ms) it is not:
// copyTextFrom right after the keys reads the unmasked value (main's 120 ms window caught it
// up to 120 ms). An assertVisible still sees the masked value: it reads again when the mask's
// change settles. The limit is documented in docs/architecture.md (web settle).
func TestUntrackedMaskAfterTyping(t *testing.T) {
	t.Run("within the quiet window", func(t *testing.T) {
		p := newFieldPage(t, phoneMask, 20*time.Millisecond, 300*time.Millisecond)
		p.untracked = true
		res, trace := p.run(t, `- inputText: "5551234567"
- copyTextFrom: {id: phone}
- assertTrue: ${maestro.copiedText == '(555) 123-4567'}
`)
		if res.Status != engine.Passed {
			t.Fatalf("%s: %v\n%s", res.Status, res.Err, trace)
		}
	})
	t.Run("past the quiet window, an assert that waits", func(t *testing.T) {
		p := newFieldPage(t, phoneMask, 60*time.Millisecond, 300*time.Millisecond)
		p.untracked = true
		res, trace := p.run(t, `- inputText: "5551234567"
- assertVisible: {id: phone, text: "\\(555\\) 123-4567"}
- assertVisible: "Saved: \\(555\\) 123-4567"
`)
		if res.Status != engine.Passed {
			t.Fatalf("%s: %v\n%s", res.Status, res.Err, trace)
		}
	})
}
