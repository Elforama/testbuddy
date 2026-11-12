package web

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

type gotEvent struct {
	method, session string
	params          string
}

// recorder collects the events a cdp client dispatches.
type recorder struct {
	mu     sync.Mutex
	events []gotEvent
}

func (r *recorder) on(method string, params json.RawMessage, session string) {
	r.mu.Lock()
	r.events = append(r.events, gotEvent{method, session, string(params)})
	r.mu.Unlock()
}

func (r *recorder) list() []gotEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]gotEvent(nil), r.events...)
}

func dialFake(t *testing.T, f *fakeCDP, onEvent func(string, json.RawMessage, string)) *cdp {
	t.Helper()
	c, err := dialCDP(ctxT(t), f.url("/devtools/page/1"), onEvent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.close)
	return c
}

// call sends {id, method, params} and decodes the result; ids increase; nil params are left out.
func TestCDPCall(t *testing.T) {
	f := newFakeCDP(t)
	f.on("Browser.getVersion", func(m cdpMsg) any { return map[string]any{"product": "Chrome/130", "protocolVersion": "1.3"} })
	c := dialFake(t, f, nil)
	var v struct {
		Product string `json:"product"`
	}
	if err := c.call(ctxT(t), "Browser.getVersion", nil, &v); err != nil || v.Product != "Chrome/130" {
		t.Fatalf("call = %+v, %v", v, err)
	}
	if err := c.call(ctxT(t), "Page.enable", map[string]any{"x": 1}, nil); err != nil {
		t.Fatal(err)
	}
	msgs := f.calls("")
	if len(msgs) != 2 || msgs[0].ID != 1 || msgs[1].ID != 2 || msgs[0].SessionID != "" {
		t.Fatalf("messages %+v", msgs)
	}
	if msgs[0].Params != nil {
		t.Errorf("nil params sent as %s", msgs[0].Params)
	}
	if string(msgs[1].Params) != `{"x":1}` {
		t.Errorf("params %s", msgs[1].Params)
	}
}

// callIn addresses an attached target's session.
func TestCDPCallInSession(t *testing.T) {
	f := newFakeCDP(t)
	c := dialFake(t, f, nil)
	if err := c.callIn(ctxT(t), "SESSION-1", "Runtime.enable", nil, nil); err != nil {
		t.Fatal(err)
	}
	if m := f.calls("Runtime.enable"); len(m) != 1 || m[0].SessionID != "SESSION-1" {
		t.Errorf("messages %+v", m)
	}
}

// A protocol error comes back as "Method: message (code)".
func TestCDPError(t *testing.T) {
	f := newFakeCDP(t)
	f.on("DOM.getDocument", func(cdpMsg) any { return errReply{cdpError{Code: -32000, Message: "Not attached"}} })
	c := dialFake(t, f, nil)
	err := c.call(ctxT(t), "DOM.getDocument", nil, nil)
	if err == nil || err.Error() != "DOM.getDocument: Not attached (-32000)" {
		t.Errorf("err = %v", err)
	}
}

// A result that does not fit the output is an error; an empty one leaves the output alone.
func TestCDPDecode(t *testing.T) {
	f := newFakeCDP(t)
	f.on("A.bad", func(cdpMsg) any { return map[string]any{"n": "not a number"} })
	f.on("A.empty", func(cdpMsg) any { return nil })
	c := dialFake(t, f, nil)
	var out struct{ N int }
	if err := c.call(ctxT(t), "A.bad", nil, &out); err == nil {
		t.Error("bad result decoded")
	}
	out.N = 7
	if err := c.call(ctxT(t), "A.empty", nil, &out); err != nil || out.N != 7 {
		t.Errorf("empty result: %v, %+v", err, out)
	}
	if err := c.call(ctxT(t), "A.x", map[string]any{"ch": make(chan int)}, nil); err == nil {
		t.Error("unmarshalable params were sent")
	}
}

// An unanswered command ends with its context.
func TestCDPContext(t *testing.T) {
	f := newFakeCDP(t)
	f.on("Slow.op", func(cdpMsg) any { return noAnswer })
	c := dialFake(t, f, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	err := c.call(ctx, "Slow.op", nil, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "Slow.op: context deadline exceeded") {
		t.Errorf("err = %v", err)
	}
	c.mu.Lock()
	n := len(c.pending)
	c.mu.Unlock()
	if n != 0 {
		t.Errorf("%d pending after the context ended", n)
	}
}

// A command whose context is already done, or ends while it is sent, fails alone: the
// connection stays up for the next one. The websocket closes the whole connection when a
// write's context ends, which a cancelled step (or a screenshot past its bound, issue #163)
// must not do.
func TestCDPCancelledCallKeepsConnection(t *testing.T) {
	f := newFakeCDP(t)
	f.on("Fast.op", func(cdpMsg) any { return map[string]any{"ok": true} })
	c := dialFake(t, f, nil)
	for i := 0; i < 50; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := c.call(ctx, "Fast.op", nil, nil); err == nil {
			t.Fatalf("try %d: a call with a cancelled context succeeded", i)
		}
		var r struct{ OK bool }
		if err := c.call(ctxT(t), "Fast.op", nil, &r); err != nil || !r.OK {
			t.Fatalf("try %d: the next call = %v, %v: the cancelled one closed the connection", i, r, err)
		}
	}
}

// When the browser goes away, waiting commands fail, later ones fail at once, and done closes.
func TestCDPConnectionClosed(t *testing.T) {
	f := newFakeCDP(t)
	f.on("Slow.op", func(cdpMsg) any { return noAnswer })
	c := dialFake(t, f, nil)
	errc := make(chan error, 1)
	go func() { errc <- c.call(ctxT(t), "Slow.op", nil, nil) }()
	eventually(t, "the command to be sent", func() bool { return len(f.calls("Slow.op")) == 1 })
	f.dropConns()
	select {
	case err := <-errc:
		if err == nil || !strings.Contains(err.Error(), "browser connection closed") {
			t.Errorf("waiting command: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the waiting command never failed")
	}
	<-c.done
	if err := c.call(ctxT(t), "Page.enable", nil, nil); err == nil || !strings.Contains(err.Error(), "browser connection closed") {
		t.Errorf("later command: %v", err)
	}
}

// Events go to the handler with their session; junk, stray replies and unknown shapes are skipped.
func TestCDPEvents(t *testing.T) {
	f := newFakeCDP(t)
	var rec recorder
	c := dialFake(t, f, rec.on)
	if err := c.call(ctxT(t), "Page.enable", nil, nil); err != nil { // the connection is up
		t.Fatal(err)
	}
	f.send("not json")
	f.send(map[string]any{"id": 999, "result": map[string]any{}})
	f.send(map[string]any{"params": map[string]any{}})
	f.event("Page.loadEventFired", map[string]any{"timestamp": 1.5}, "")
	f.event("Runtime.bindingCalled", map[string]any{"name": "__tbNotify"}, "S9")
	eventually(t, "two events", func() bool { return len(rec.list()) == 2 })
	got := rec.list()
	if got[0].method != "Page.loadEventFired" || got[0].session != "" || got[0].params != `{"timestamp":1.5}` {
		t.Errorf("first event %+v", got[0])
	}
	if got[1].method != "Runtime.bindingCalled" || got[1].session != "S9" {
		t.Errorf("second event %+v", got[1])
	}
	// The client still works after the junk.
	if err := c.call(ctxT(t), "Page.enable", nil, nil); err != nil {
		t.Error(err)
	}
}

// A client without an event handler ignores events.
func TestCDPNoHandler(t *testing.T) {
	f := newFakeCDP(t)
	c := dialFake(t, f, nil)
	c.call(ctxT(t), "Page.enable", nil, nil)
	f.event("Page.loadEventFired", map[string]any{}, "")
	if err := c.call(ctxT(t), "Page.enable", nil, nil); err != nil {
		t.Error(err)
	}
}

// dialCDP fails for an endpoint that is not there.
func TestDialCDPFails(t *testing.T) {
	f := newFakeCDP(t)
	u := f.url("/x")
	f.close()
	if _, err := dialCDP(ctxT(t), u, nil); err == nil {
		t.Error("dialed a closed endpoint")
	}
}
