package js

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func timerEval(t *testing.T, e *Engine, src string, sc *Scope) string {
	t.Helper()
	v, err := e.Eval(src, sc)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	return Display(v)
}

func TestTimersOrder(t *testing.T) {
	e := New("android")
	// by delay, then creation; missing, zero, negative and NaN delays count as 0
	timerEval(t, e, `
		output.log = [];
		setTimeout(function(){ output.log.push('c20') }, 20);
		setTimeout(function(){ output.log.push('a0') }, 0);
		setTimeout(function(){ output.log.push('d5') }, 5);
		setTimeout(function(){ output.log.push('b-neg') }, -10);
		setTimeout(function(){ output.log.push('b-missing') });
		setTimeout(function(){ output.log.push('b-nan') }, 'x');
		setTimeout(function(a, b){ output.log.push('e5-' + a + b) }, 5, 1, 2);
		output.log.push('sync');
	`, nil)
	got := timerEval(t, e, `output.log.join(',')`, nil)
	want := "sync,a0,b-neg,b-missing,b-nan,d5,e5-12,c20"
	if got != want {
		t.Errorf("order: %s, want %s", got, want)
	}
}

func TestTimersNestedAndClear(t *testing.T) {
	e := New("android")
	got := timerEval(t, e, `
		var log = [];
		setTimeout(function(){
			log.push('outer');
			setTimeout(function(){ log.push('inner') }, 1);
		}, 2);
		var gone = setTimeout(function(){ log.push('cleared') }, 1);
		clearTimeout(gone);
		clearTimeout(12345); clearTimeout(); // ignored
		setTimeout(function(){ output.log = log.join(',') }, 10);
		typeof gone
	`, nil)
	if got != "number" {
		t.Errorf("setTimeout returned a %s", got)
	}
	if got := timerEval(t, e, `output.log`, nil); got != "outer,inner" {
		t.Errorf("log: %s", got)
	}
}

func TestSetInterval(t *testing.T) {
	e := New("android")
	timerEval(t, e, `
		var n = 0;
		var id = setInterval(function(step){ n += step; if (n >= 3) clearInterval(id); output.n = n }, 1, 1);
	`, nil)
	if got := timerEval(t, e, `output.n`, nil); got != "3" {
		t.Errorf("n: %s", got)
	}
	// cleared from another timer
	timerEval(t, e, `
		var ticks = 0;
		var iv = setInterval(function(){ ticks++ }, 2);
		setTimeout(function(){ clearInterval(iv); output.ticks = ticks }, 9);
	`, nil)
	if got := timerEval(t, e, `output.ticks > 0 && output.ticks <= 5`, nil); got != "true" {
		t.Errorf("ticks: %s", timerEval(t, e, `output.ticks`, nil))
	}
}

func TestTimerPromises(t *testing.T) {
	e := New("android")
	timerEval(t, e, `
		new Promise(function(res){ setTimeout(res, 1, 'done') }).then(function(v){ output.p = v });
	`, nil)
	if got := timerEval(t, e, `output.p`, nil); got != "done" {
		t.Errorf("promise resolved by a timer: %s", got)
	}
}

func TestTimerLogAndScope(t *testing.T) {
	e := New("android")
	var lines []string
	sc := &Scope{Log: func(s string) { lines = append(lines, s) }}
	if err := e.RunScript(`setTimeout(function(){ console.log('late', 1) }, 1)`, "", sc); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0] != "late 1" {
		t.Errorf("log: %q", lines)
	}
}

func TestTimerErrors(t *testing.T) {
	e := New("android")
	_, err := e.Eval(`setTimeout(function(){ throw new Error('boom') }, 1); 1`, nil)
	var je *Error
	if !errors.As(err, &je) || !strings.Contains(je.Msg, "boom") {
		t.Fatalf("error in callback: %v", err)
	}
	_, err = e.Eval(`setTimeout('output.x = 1', 1)`, nil)
	if !errors.As(err, &je) || !strings.Contains(je.Msg, "TypeError") {
		t.Fatalf("string callback: %v", err)
	}
	// the engine still works, and timers survive as globals across evaluations
	if got := timerEval(t, e, `typeof setTimeout + typeof clearInterval`, nil); got != "functionfunction" {
		t.Errorf("globals: %s", got)
	}
}

func TestTimerBudget(t *testing.T) {
	old := timerBudget
	timerBudget = 30 * time.Millisecond
	defer func() { timerBudget = old }()
	e := New("android")
	start := time.Now()
	_, err := e.Eval(`setInterval(function(){ output.spins = (output.spins || 0) + 1 }, 2)`, nil)
	var je *Error
	if !errors.As(err, &je) || !strings.Contains(je.Msg, "timers still pending") {
		t.Fatalf("never-cleared interval: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("took %s", time.Since(start))
	}
	// a timer due after the budget fails without sleeping for it
	_, err = e.Eval(`setTimeout(function(){}, 60000)`, nil)
	if !errors.As(err, &je) || !strings.Contains(je.Msg, "timers still pending") {
		t.Fatalf("late timer: %v", err)
	}
}

func TestTimersDoNotLeak(t *testing.T) {
	e := New("android")
	// a failing script drops its timers: they neither run now nor in the next evaluation
	if _, err := e.Eval(`setTimeout(function(){ output.leaked = true }, 1); throw new Error('x')`, nil); err == nil {
		t.Fatal("want error")
	}
	if _, err := e.Eval(`setTimeout(function(){ output.leaked2 = true }, 1); setTimeout(function(){ throw 'y' }, 0)`, nil); err == nil {
		t.Fatal("want error")
	}
	if got := timerEval(t, e, `String(output.leaked) + String(output.leaked2)`, nil); got != "undefinedundefined" {
		t.Errorf("leaked: %s", got)
	}
	if len(e.timers) != 0 {
		t.Errorf("%d timers left", len(e.timers))
	}
}

// The Maestro issue's script: an http call from a timer, its result in output.
func TestTimerHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("pong")) }))
	defer srv.Close()
	e := New("android")
	if err := e.RunScript(`setTimeout(() => { output.x = http.get('`+srv.URL+`').body }, 1)`, "", nil); err != nil {
		t.Fatal(err)
	}
	if got := timerEval(t, e, `output.x`, nil); got != "pong" {
		t.Errorf("output.x: %s", got)
	}
}
