package js

import (
	"fmt"
	"math"
	"time"

	"github.com/dop251/goja"
)

var timerBudget = 30 * time.Second

type jsTimer struct {
	id       int64
	seq      int64
	due      time.Time
	interval time.Duration
	repeat   bool
	fn       goja.Callable
	args     []goja.Value
}

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
		if _, ok := e.timers[t.id]; ok && t.repeat {
			e.timerSeq++
			t.seq = e.timerSeq
			t.due = time.Now().Add(t.interval)
		}
	}
	return nil
}

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
