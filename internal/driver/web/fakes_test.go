package web

// Test doubles: a fake DevTools endpoint (httptest + websocket) that records every command and
// answers it with per-method handlers, and pushes events; a Driver wired to it without Chrome.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

type cdpMsg struct {
	ID        int64           `json:"id"`
	Method    string          `json:"method"`
	SessionID string          `json:"sessionId"`
	Params    json.RawMessage `json:"params"`
}

// params decodes a recorded command's params into a map.
func (m cdpMsg) params() map[string]any {
	var p map[string]any
	json.Unmarshal(m.Params, &p)
	return p
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// errReply as a handler result makes the command fail with that error.
type errReply struct{ cdpError }

// noAnswer as a handler result leaves the command unanswered.
var noAnswer = &struct{ x int }{}

type fakeCDP struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	msgs     []cdpMsg
	handlers map[string]func(m cdpMsg) any
	conns    []*websocket.Conn
	// up is closed while conns holds a connection (issue #118).
	up     chan struct{}
	onHTTP func(w http.ResponseWriter, r *http.Request) bool // true: handled
}

func newFakeCDP(t *testing.T) *fakeCDP {
	t.Helper()
	f := &fakeCDP{t: t, handlers: map[string]func(cdpMsg) any{}, up: make(chan struct{})}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(f.close)
	return f
}

func (f *fakeCDP) url(path string) string { return "ws://" + f.srv.Listener.Addr().String() + path }

func (f *fakeCDP) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	h := f.onHTTP
	f.mu.Unlock()
	if h != nil && h(w, r) {
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(64 << 20)
	f.mu.Lock()
	if len(f.conns) == 0 {
		close(f.up)
	}
	f.conns = append(f.conns, c)
	f.mu.Unlock()
	ctx := context.Background()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var m cdpMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		f.mu.Lock()
		f.msgs = append(f.msgs, m)
		h := f.handlers[m.Method]
		f.mu.Unlock()
		var res any = map[string]any{}
		if h != nil {
			res = h(m)
		}
		if res == noAnswer {
			continue
		}
		reply := map[string]any{"id": m.ID}
		if e, ok := res.(errReply); ok {
			reply["error"] = e.cdpError
		} else {
			reply["result"] = res
		}
		if m.SessionID != "" {
			reply["sessionId"] = m.SessionID
		}
		b, _ := json.Marshal(reply)
		c.Write(ctx, websocket.MessageText, b)
	}
}

func (f *fakeCDP) on(method string, h func(m cdpMsg) any) {
	f.mu.Lock()
	f.handlers[method] = h
	f.mu.Unlock()
}

// send writes a raw message on the latest connection. It waits for the driver's connection
// first: the client's websocket handshake can finish before serveHTTP records the
// connection, so a test that sends right after dialing could otherwise find none and the
// message would be lost (issue #118).
func (f *fakeCDP) send(v any) {
	f.mu.Lock()
	up := f.up
	f.mu.Unlock()
	select {
	case <-up:
	case <-time.After(3 * time.Second):
		f.t.Error("send: the driver did not connect to the fake endpoint within 3s")
		return
	}
	f.mu.Lock()
	var c *websocket.Conn
	if len(f.conns) > 0 {
		c = f.conns[len(f.conns)-1]
	}
	f.mu.Unlock()
	if c == nil {
		f.t.Error("send: no connection")
		return
	}
	var b []byte
	if s, ok := v.(string); ok {
		b = []byte(s)
	} else {
		b, _ = json.Marshal(v)
	}
	c.Write(context.Background(), websocket.MessageText, b)
}

// event pushes a CDP event (session "" = the page's own).
func (f *fakeCDP) event(method string, params any, session string) {
	m := map[string]any{"method": method, "params": params}
	if session != "" {
		m["sessionId"] = session
	}
	f.send(m)
}

func (f *fakeCDP) dropConns() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		c.CloseNow()
	}
	if len(f.conns) > 0 {
		f.up = make(chan struct{})
	}
	f.conns = nil
}

func (f *fakeCDP) close() {
	f.dropConns()
	f.srv.Close()
}

// calls returns the recorded commands of method ("" for all).
func (f *fakeCDP) calls(method string) []cdpMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []cdpMsg
	for _, m := range f.msgs {
		if method == "" || m.Method == method {
			out = append(out, m)
		}
	}
	return out
}

func (f *fakeCDP) methods() []string {
	var out []string
	for _, m := range f.calls("") {
		s := m.Method
		if m.SessionID != "" {
			s = m.SessionID + ":" + s
		}
		out = append(out, s)
	}
	return out
}

func (f *fakeCDP) reset() {
	f.mu.Lock()
	f.msgs = nil
	f.mu.Unlock()
}

// value is a Runtime.evaluate result holding v (returnByValue).
func value(v any) map[string]any {
	b, _ := json.Marshal(v)
	return map[string]any{"result": map[string]any{"type": "string", "value": json.RawMessage(b)}}
}

// evaluator answers Runtime.evaluate by expression; unknown expressions return undefined.
func (f *fakeCDP) evaluator(fn func(expr, session string) any) {
	f.on("Runtime.evaluate", func(m cdpMsg) any {
		expr, _ := m.params()["expression"].(string)
		if r := fn(expr, m.SessionID); r != nil {
			return r
		}
		return map[string]any{"result": map[string]any{"type": "undefined"}}
	})
}

func newBareDriver() *Driver {
	d := &Driver{info: device.WebDevice(1), inflight: map[string]time.Time{}, subs: map[int]chan driver.Event{},
		frames: map[string]*frame{}, w: 1280, h: 800}
	d.cond = sync.NewCond(&d.mu)
	return d
}

// newWebDriver wires a Driver to a fake page target (and, when given, a fake browser target).
func newWebDriver(t *testing.T, page, browser *fakeCDP) *Driver {
	t.Helper()
	d := newBareDriver()
	ctx := ctxT(t)
	var err error
	if d.page, err = dialCDP(ctx, page.url("/devtools/page/1"), d.onEvent); err != nil {
		t.Fatal(err)
	}
	if browser != nil {
		if d.browser, err = dialCDP(ctx, browser.url("/devtools/browser/1"), nil); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func waitEvent(t *testing.T, ch <-chan driver.Event, kind string) driver.Event {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.Kind == kind {
				return e
			}
		case <-timeout:
			t.Fatalf("no %q event", kind)
		}
	}
}

// state reads the driver's counters under its lock.
func state(d *Driver) (seq, solid, settled int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.seq, d.solid, d.settledSol
}

func jsonArg(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
