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
type fieldPage struct {
	page *fakeCDP
	d    *Driver

	mu     sync.Mutex
	raw    string // what the keys put in
	value  string // what the field shows
	label  string // the debounced label
	mask   func(string) string
	timers []*time.Timer
}

func newFieldPage(t *testing.T, mask func(string) string, maskAfter, debounceAfter time.Duration) *fieldPage {
	p := &fieldPage{page: newFakeCDP(t), mask: mask, label: "Saved: "}
	notify := func() {
		p.page.event("Runtime.bindingCalled", map[string]any{"name": "__tbNotify", "payload": `{"a":0,"s":1}`}, "")
	}
	later := func(after time.Duration, f func()) {
		for _, tm := range p.timers {
			tm.Stop()
		}
		p.timers = nil
		if mask != nil {
			p.timers = append(p.timers, time.AfterFunc(maskAfter, func() {
				p.mu.Lock()
				p.value = p.mask(p.raw)
				p.mu.Unlock()
				notify()
			}))
		}
		p.timers = append(p.timers, time.AfterFunc(after, f))
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
			notify()
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
		case strings.Contains(expr, "setSelectionRange"): // eraseText's probe
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
// the page does to it after the keys: a mask rewriting the field, and a debounced update
// (300 ms, longer than the quiet window) showing it elsewhere; after erasing too.
func TestAssertRightAfterTypingSeesFinalValue(t *testing.T) {
	p := newFieldPage(t, phoneMask, 60*time.Millisecond, 300*time.Millisecond)
	res, trace := p.run(t, `- inputText: "5551234567"
- assertVisible: {id: phone, text: "\\(555\\) 123-4567"}
- assertVisible: "Saved: \\(555\\) 123-4567"
- eraseText: 4
- assertVisible: {id: phone, text: "555123"}
- assertVisible: "Saved: 555123"
- inputText: "4567"
- assertVisible: "Saved: \\(555\\) 123-4567"
- copyTextFrom: {id: phone}
- assertTrue: ${maestro.copiedText == '(555) 123-4567'}
`)
	if res.Status != engine.Passed {
		t.Fatalf("%s: %v\n%s", res.Status, res.Err, trace)
	}
}
