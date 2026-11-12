package android

import (
	"strings"
	"testing"
	"time"
)

// After setLocation the driver sends nothing more on its own (issue #72). It used to send the
// point again every second as an "sh" request for the rest of the run: the agent answers one
// request at a time per connection, so taps and screen reads queued behind each resend, and
// each resend took a slot in the agent's request-token cache. The span is real time: the
// driver keeps no clock of its own for this any more (the agent repeats the fix).
func TestSetLocationSendsNothingAfterwards(t *testing.T) {
	a := newFakeAgent(t) // reports sdk 34: the test providers
	d := stubbedDriver(t, stubADB(t, nil), a)
	if err := d.SetLocation(ctxT(t), 45.5, -73.5); err != nil {
		t.Fatal(err)
	}
	a.reset()
	span := 1500 * time.Millisecond // the old resend came every second
	time.Sleep(span)
	if ops := a.ops(); len(ops) != 0 {
		t.Errorf("%d requests in the %v after setLocation, want none: %q", len(ops), span, a.shCmds())
	}
}

// Each point is one "location" request (setLocation, and each step of a travel); the agent adds
// the providers and repeats the fix. Close turns it off: the agent stops the repeats and removes
// the providers (issue #72).
func TestSetLocationOneRequestPerPoint(t *testing.T) {
	a := newFakeAgent(t)
	d := stubbedDriver(t, stubADB(t, nil), a)
	a.reset() // the connection's info and ping
	points := [][2]float64{{45.5017, -73.5673}, {45.50175, -73.56735}, {45.5018, -73.5674}}
	for _, p := range points {
		if err := d.SetLocation(ctxT(t), p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}
	reqs := a.requests("location")
	if ops := a.ops(); len(ops) != len(points) || len(reqs) != len(points) {
		t.Fatalf("requests %v, want %d location requests", ops, len(points))
	}
	for i, r := range reqs {
		if num(t, r, "lat") != points[i][0] || num(t, r, "lon") != points[i][1] || num(t, r, "everyMs") != 1000 {
			t.Errorf("request %d = %v, want %v every 1000 ms", i, r, points[i])
		}
		if _, ok := r["rid"]; ok {
			t.Errorf("request %d carries a token: sending a point again is harmless, and tokens are few", i)
		}
	}
	a.reset()
	d.Close()
	if r := a.requests("location"); len(r) != 1 || r[0]["off"] != true {
		t.Errorf("Close sent %v, want location off", a.requests(""))
	}
}

// An agent that cannot set the point fails setLocation.
func TestSetLocationAgentError(t *testing.T) {
	a := newFakeAgent(t)
	a.mu.Lock()
	delete(a.handlers, "location") // an error reply
	a.mu.Unlock()
	d := stubbedDriver(t, stubADB(t, nil), a)
	if err := d.SetLocation(ctxT(t), 1, 2); err == nil || !strings.Contains(err.Error(), "setLocation") {
		t.Errorf("SetLocation = %v", err)
	}
}

// A driver with no point set sends no location request, not even at Close.
func TestNoLocationNoRequest(t *testing.T) {
	a := newFakeAgent(t)
	d := stubbedDriver(t, stubADB(t, nil), a)
	d.Close()
	if r := a.requests("location"); len(r) != 0 {
		t.Errorf("location requests %v", r)
	}
}

// The agent ends the repeats when the connection that set the point closes; a reconnect (to
// the same agent, or a restarted one) sets the point again on the new connection, so it holds
// across the drop (issue #72).
func TestLocationSetAgainAfterReconnect(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a1, a2 := newFakeAgent(t), newFakeAgent(t)
	s := stubADB(t, []stubRule{{match: "forward --list", out: forwardLine(a2.port())}})
	d := s.driver()
	c1, h, err := dialHello(a1.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d.adopt(c1, a1.port(), h)
	defer d.Close()
	if err := d.SetLocation(ctxT(t), 1.5, 2.5); err != nil {
		t.Fatal(err)
	}
	a1.dropConns()
	eventually(t, "the reconnect", func() bool { return d.conn() != c1 })
	eventually(t, "the point on the new connection", func() bool { return len(a2.requests("location")) == 1 })
	if r := a2.last("location"); num(t, r, "lat") != 1.5 || num(t, r, "lon") != 2.5 {
		t.Errorf("after the reconnect: %v", r)
	}
	if ops := a2.ops(); len(ops) != 3 { // info, ping, location
		t.Errorf("the new connection got %v", ops)
	}
	d.Close()
	if r := a2.requests("location"); len(r) != 2 || r[1]["off"] != true {
		t.Errorf("Close sent %v", r)
	}
}
