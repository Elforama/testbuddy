package js

import (
	"fmt"
	"math"
	"time"

	"github.com/dop251/goja"
)

// Timers: setTimeout, setInterval, clearTimeout and clearInterval, for Maestro scripts
// written against GraalJS (which has them). There is no event loop between evaluations:
// an evaluation runs its own code, then drains its timers before it returns —
// repeatedly running the earliest-due timer (ties in creation order) after sleeping until
// it is due. Timers that callbacks schedule join the queue. Pending Promise jobs run
// after the script and after each callback (goja does that whenever control leaves the
// runtime). Semantics:
//   - setTimeout(fn, ms, ...args) / setInterval(fn, ms, ...args) return a numeric id;
//     fn is called with args and `this` undefined.
//   - A missing, NaN, negative or non-finite ms counts as 0.
//   - A non-function callback (including a string of code) throws a TypeError, as
//     Node.js does; strings are not evaluated.
//   - clearTimeout and clearInterval are interchangeable; an unknown id is ignored.
//   - An interval re-queues itself (ms after its callback finished) until cleared.
//   - Draining stops with an error once a timer is still pending timerBudget after the
//     script finished (an interval never cleared, say).
//   - An error thrown by a callback fails the evaluation; its remaining timers are
//     dropped. Timers never carry over to the next evaluation.
//   - Timers sleep holding the engine's lock: other devices' scripts wait meanwhile.

// timerBudget is how long, from the end of the script's own code, an evaluation may keep
// running timers. A variable so tests can shorten it.
var timerBudget = 30 * time.Second

type jsTimer struct {
	id       int64
	seq      int64 // ordering among timers due at the same time: creation (or re-queue) order
	due      time.Time
	interval time.Duration
	repeat   bool // setInterval: re-queued after each run
	fn       goja.Callable
	args     []goja.Value
}

// installTimers defines the timer globals. Called from New.
func (e *Engine) installTimers() {
	add := func(repeat bool) func(goja.FunctionCall) goja.Value {
		name := "setTimeout"
		if repeat {
			name = "setInterval"
		}
		return func(call goja.FunctionCall) goja.Value {
			fn, ok := goja.AssertFunction(call.Argument(0))
			if !ok {
				panic(e.rt.NewTypeError(name + ": the callback must be a function"))
			}
			d := delay(call.Argument(1))
			var args []goja.Value
			if len(call.Arguments) > 2 {
				args = append(args, call.Arguments[2:]...)
			}
			e.timerID++
			e.timerSeq++
			t := &jsTimer{id: e.timerID, seq: e.timerSeq, due: time.Now().Add(d), interval: d, repeat: repeat, fn: fn, args: args}
			if e.timers == nil {
				e.timers = map[int64]*jsTimer{}
			}
			e.timers[t.id] = t
			return e.rt.ToValue(t.id)
		}
	}
	clear := func(call goja.FunctionCall) goja.Value {
		v := call.Argument(0)
		if goja.IsUndefined(v) || goja.IsNull(v) {
			return goja.Undefined()
		}
		f := v.ToFloat()
		if f == math.Trunc(f) {
			delete(e.timers, int64(f))
		}
		return goja.Undefined()
	}
	e.rt.Set("setTimeout", add(false))
	e.rt.Set("setInterval", add(true))
	e.rt.Set("clearTimeout", clear)
	e.rt.Set("clearInterval", clear)
	for _, k := range []string{"setTimeout", "setInterval", "clearTimeout", "clearInterval"} {
		e.permanent[k] = true
	}
}

// delay converts a timer's ms argument; anything that is not a positive finite number is 0.
func delay(v goja.Value) time.Duration {
	if v == nil || goja.IsUndefined(v) {
		return 0
	}
	ms := v.ToFloat()
	if math.IsNaN(ms) || ms <= 0 || math.IsInf(ms, 0) {
		return 0
	}
	return time.Duration(ms * float64(time.Millisecond))
}

// drainTimers runs the pending timers until none is left (see the top of this file).
// Caller holds mu. The queue is always empty when it returns.
func (e *Engine) drainTimers() error {
	defer func() { e.timers = nil }()
	deadline := time.Now().Add(timerBudget)
	for len(e.timers) > 0 {
		var t *jsTimer
		for _, c := range e.timers {
			if t == nil || c.due.Before(t.due) || c.due.Equal(t.due) && c.seq < t.seq {
				t = c
			}
		}
		if t.due.After(deadline) || time.Now().After(deadline) {
			return &Error{Msg: fmt.Sprintf("timers still pending after %s (an interval never cleared?)", timerBudget)}
		}
		if err := e.sleepUntil(t.due); err != nil {
			return &Error{Msg: err.Error()}
		}
		if !t.repeat {
			delete(e.timers, t.id)
		}
		if _, err := t.fn(goja.Undefined(), t.args...); err != nil {
			if ex, ok := err.(*goja.Exception); ok {
				return &Error{Msg: ex.Value().String()}
			}
			return &Error{Msg: err.Error()}
		}
		// An interval that its own callback did not clear goes back in the queue.
		if _, ok := e.timers[t.id]; ok && t.repeat {
			e.timerSeq++
			t.seq = e.timerSeq
			t.due = time.Now().Add(t.interval)
		}
	}
	return nil
}

// sleepUntil waits until t. It is the one place timers wait, so cancellation (a context
// passed to Eval, say) can be added here: select on its Done channel and return its error.
func (e *Engine) sleepUntil(t time.Time) error {
	d := time.Until(t)
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	<-timer.C
	return nil
}
