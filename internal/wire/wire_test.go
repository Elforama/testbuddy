package wire

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const wait = 5 * time.Second

type peer struct {
	t *testing.T
	c net.Conn
	r *bufio.Reader
	w sync.Mutex
}

func pair(t *testing.T) (*Conn, *peer) {
	t.Helper()
	a, b := net.Pipe()
	conn := newConn(a)
	p := &peer{t: t, c: b, r: bufio.NewReader(b)}
	t.Cleanup(func() { a.Close(); b.Close() })
	return conn, p
}

func (p *peer) read() map[string]any {
	p.t.Helper()
	p.c.SetReadDeadline(time.Now().Add(wait))
	line, err := p.r.ReadBytes('\n')
	if err != nil {
		p.t.Fatalf("peer read: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		p.t.Fatalf("host sent a bad line %q: %v", line, err)
	}
	return m
}

func (p *peer) send(s string) {
	p.w.Lock()
	defer p.w.Unlock()
	p.c.SetWriteDeadline(time.Now().Add(wait))
	if _, err := io.WriteString(p.c, s); err != nil {
		p.t.Errorf("peer send: %v", err)
	}
}

func (p *peer) reply(id any, fields string) {
	if fields != "" {
		fields = "," + fields
	}
	p.send(fmt.Sprintf(`{"id":%v%s}`+"\n", id, fields))
}

type callResult struct {
	m   *Msg
	err error
}

func goCall(c *Conn, op string, params map[string]any, timeout time.Duration) <-chan callResult {
	ch := make(chan callResult, 1)
	go func() {
		m, err := c.Call(op, params, timeout)
		ch <- callResult{m, err}
	}()
	return ch
}

func await(t *testing.T, ch <-chan callResult) callResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(wait):
		t.Fatal("call did not return")
		return callResult{}
	}
}

func recv(t *testing.T, ch chan *Msg) *Msg {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(wait):
		t.Fatal("no event arrived")
		return nil
	}
}

func waitDone(t *testing.T, c *Conn) {
	t.Helper()
	select {
	case <-c.Done():
	case <-time.After(wait):
		t.Fatal("connection did not report done")
	}
}

func TestCallRequestShape(t *testing.T) {
	c, p := pair(t)
	res := goCall(c, "tap", map[string]any{"x": 100, "y": 200.5, "settle": false, "id": 99, "op": "hijack"}, 0)
	req := p.read()
	if req["id"] != float64(1) || req["op"] != "tap" {
		t.Errorf("id/op = %v/%v", req["id"], req["op"])
	}
	if req["x"] != float64(100) || req["y"] != 200.5 || req["settle"] != false {
		t.Errorf("params = %v", req)
	}
	p.reply(1, `"ok":true,"result":"changed","seq":5,"injectMs":3`)
	r := await(t, res)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.m.ID != 1 || !r.m.OK || r.m.Seq != 5 {
		t.Errorf("msg = %+v", r.m)
	}
	var body struct {
		Result   string `json:"result"`
		InjectMs int    `json:"injectMs"`
	}
	if err := r.m.Decode(&body); err != nil || body.Result != "changed" || body.InjectMs != 3 {
		t.Errorf("Decode = %+v, %v", body, err)
	}
	if !strings.HasSuffix(string(r.m.Raw), "\n") || !strings.Contains(string(r.m.Raw), `"result":"changed"`) {
		t.Errorf("Raw = %q", r.m.Raw)
	}
}

func TestCallNilParams(t *testing.T) {
	c, p := pair(t)
	res := goCall(c, "ping", nil, 0)
	req := p.read()
	if len(req) != 2 || req["op"] != "ping" {
		t.Errorf("request = %v", req)
	}
	p.reply(req["id"], `"ok":true`)
	if r := await(t, res); r.err != nil {
		t.Fatal(r.err)
	}
}

func TestCallIDsIncrease(t *testing.T) {
	c, p := pair(t)
	for want := 1; want <= 4; want++ {
		res := goCall(c, "ping", nil, 0)
		req := p.read()
		if req["id"] != float64(want) {
			t.Fatalf("call %d id = %v", want, req["id"])
		}
		p.reply(req["id"], `"ok":true`)
		if r := await(t, res); r.err != nil || r.m.ID != int64(want) {
			t.Fatalf("call %d: %+v %v", want, r.m, r.err)
		}
	}
}

func TestCallAgentError(t *testing.T) {
	cases := []struct {
		name, fields, wantMsg string
		transient             bool
	}{
		{"reason given", `"ok":false,"error":"no such element"`, "no such element", false},
		{"no reason", `"ok":false`, "agent returned an error", false},
		{"ok missing", `"result":"x"`, "agent returned an error", false},
		{"transient", `"ok":false,"error":"not answering yet","transient":true`, "not answering yet", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, p := pair(t)
			res := goCall(c, "tap", nil, 0)
			p.reply(p.read()["id"], tc.fields)
			r := await(t, res)
			var ae *AgentError
			if !errors.As(r.err, &ae) {
				t.Fatalf("err = %v (%T), want *AgentError", r.err, r.err)
			}
			if ae.Op != "tap" || ae.Msg != tc.wantMsg || ae.Transient != tc.transient {
				t.Errorf("AgentError = %+v", ae)
			}
			if r.err.Error() != "tap: "+tc.wantMsg {
				t.Errorf("Error() = %q", r.err.Error())
			}
			if r.m == nil || r.m.Error != tc.wantMsg {
				t.Errorf("returned msg = %+v", r.m)
			}
		})
	}
}

func TestCallTimeout(t *testing.T) {
	c, p := pair(t)
	res := goCall(c, "slow", nil, 30*time.Millisecond)
	req := p.read()
	r := await(t, res)
	if r.err == nil || r.err.Error() != `agent did not answer "slow" within 30ms` {
		t.Fatalf("err = %v", r.err)
	}
	c.mu.Lock()
	left := len(c.pending)
	c.mu.Unlock()
	if left != 0 {
		t.Errorf("%d pending entries left after a timeout", left)
	}
	p.reply(req["id"], `"ok":true`)
	res = goCall(c, "fast", nil, time.Second)
	req2 := p.read()
	if req2["id"] != float64(2) {
		t.Errorf("next id = %v", req2["id"])
	}
	p.reply(req2["id"], `"ok":true,"v":"fresh"`)
	r = await(t, res)
	if r.err != nil || !strings.Contains(string(r.m.Raw), "fresh") {
		t.Fatalf("after late answer: %+v %v", r.m, r.err)
	}
}

func TestCallContextCancel(t *testing.T) {
	c, p := pair(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.CallContext(ctx, "tap", nil, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("issue #202: CallContext on a cancelled context = %v, want context.Canceled", err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	res := make(chan callResult, 1)
	go func() {
		m, err := c.CallContext(ctx, "sh", nil, 30*time.Second)
		res <- callResult{m, err}
	}()
	req := p.read()
	if req["op"] != "sh" {
		t.Fatalf("issue #202: first request the agent got = %v, want the sh", req)
	}
	cancel()
	if r := await(t, res); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("issue #202: cancelled wait = %+v %v, want context.Canceled", r.m, r.err)
	}
	c.mu.Lock()
	left := len(c.pending)
	c.mu.Unlock()
	if left != 0 {
		t.Errorf("issue #202: %d pending entries left after a cancel", left)
	}
	next := goCall(c, "info", nil, time.Second)
	req2 := p.read()
	p.reply(req["id"], `"ok":true,"v":"late"`)
	p.reply(req2["id"], `"ok":true,"v":"fresh"`)
	r := await(t, next)
	if r.err != nil || !strings.Contains(string(r.m.Raw), "fresh") {
		t.Fatalf("issue #202: the call after a cancelled one got %+v %v, want its own answer", r.m, r.err)
	}
	if isClosed(c.closed) {
		t.Error("issue #202: the connection closed after a cancel")
	}
}

func TestCallContextDeadlineIsReplyTimeout(t *testing.T) {
	c, p := pair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	res := make(chan callResult, 1)
	go func() {
		m, err := c.CallContext(ctx, "launch", nil, 30*time.Second)
		res <- callResult{m, err}
	}()
	p.read()
	var late *ReplyTimeout
	if r := await(t, res); !errors.As(r.err, &late) || late.Op != "launch" {
		t.Fatalf("issue #202: wait ended by a deadline = %v, want a *ReplyTimeout", r.err)
	}
}

func TestCallAfterCancelNotChargedForTheCancelled(t *testing.T) {
	c, p := pair(t)
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan callResult, 1)
	go func() {
		m, err := c.CallContext(ctx, "sh", nil, 30*time.Second)
		res <- callResult{m, err}
	}()
	req := p.read()
	cancel()
	if r := await(t, res); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("cancelled call = %v", r.err)
	}
	next := goCall(c, "info", nil, 300*time.Millisecond)
	req2 := p.read()
	time.Sleep(600 * time.Millisecond)
	p.reply(req["id"], `"ok":true,"v":"late"`)
	p.reply(req2["id"], `"ok":true,"v":"fresh"`)
	r := await(t, next)
	if r.err != nil || !strings.Contains(string(r.m.Raw), "fresh") {
		t.Fatalf("issue #202: a call queued behind a cancelled one = %+v %v, want its own answer", r.m, r.err)
	}
	c.mu.Lock()
	left := len(c.abandoned)
	c.mu.Unlock()
	if left != 0 {
		t.Errorf("issue #202: %d given-up requests remembered after their answers came", left)
	}
	start := time.Now()
	third := goCall(c, "quiet", nil, 100*time.Millisecond)
	p.read()
	var late *ReplyTimeout
	if r := await(t, third); !errors.As(r.err, &late) {
		t.Fatalf("third call = %v, want a reply timeout", r.err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("issue #202: the third call took %s, want its own 100ms", took)
	}
}

func TestCallAfterCancelBoundedByTheCancelledTimeout(t *testing.T) {
	c, p := pair(t)
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan callResult, 1)
	sent := time.Now()
	go func() {
		m, err := c.CallContext(ctx, "sh", nil, 400*time.Millisecond)
		res <- callResult{m, err}
	}()
	p.read()
	cancel()
	await(t, res)
	next := goCall(c, "info", nil, 200*time.Millisecond)
	p.read()
	var late *ReplyTimeout
	r := await(t, next)
	took := time.Since(sent)
	if !errors.As(r.err, &late) {
		t.Fatalf("issue #202: call behind an unanswered cancelled one = %v, want a reply timeout", r.err)
	}
	if took < 550*time.Millisecond || took > 3*time.Second {
		t.Errorf("issue #202: it timed out %s after the cancelled one was sent, want about 400ms + 200ms", took)
	}
	c.mu.Lock()
	ahead := c.aheadLocked(time.Now())
	c.mu.Unlock()
	if len(ahead) != 0 {
		t.Errorf("issue #202: %d given-up requests still ahead after their bound", len(ahead))
	}
}

func TestCallUnqueuedAndQueuedTimeout(t *testing.T) {
	c, p := pair(t)
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan callResult, 1)
	go func() {
		m, err := c.CallContext(ctx, "sh", nil, 30*time.Second)
		res <- callResult{m, err}
	}()
	p.read()
	cancel()
	await(t, res)
	if n := c.Abandoned(); n != 1 {
		t.Fatalf("Abandoned = %d, want 1", n)
	}

	start := time.Now()
	un := make(chan callResult, 1)
	go func() {
		m, err := c.CallUnqueued(context.Background(), "exit", nil, 200*time.Millisecond)
		un <- callResult{m, err}
	}()
	p.read()
	var late *ReplyTimeout
	if r := await(t, un); !errors.As(r.err, &late) || late.Queued {
		t.Fatalf("issue #202: unqueued call behind a given-up one = %v, want a plain reply timeout", r.err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("issue #202: the unqueued call took %s, want its own 200ms", took)
	}

	dctx, dcancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer dcancel()
	q := make(chan callResult, 1)
	go func() {
		m, err := c.CallContext(dctx, "info", nil, 5*time.Second)
		q <- callResult{m, err}
	}()
	p.read()
	r := await(t, q)
	if !errors.As(r.err, &late) || !late.Queued || !strings.Contains(r.err.Error(), "queued behind an abandoned request") {
		t.Fatalf("issue #202: queued call cut by its deadline = %v, want a reply timeout that says it was queued", r.err)
	}
}

func TestGiveUpPrefersAnArrivedAnswer(t *testing.T) {
	c, _ := pair(t)
	ch := make(chan *Msg, 1)
	c.mu.Lock()
	c.pending[41] = ch
	c.mu.Unlock()
	c.dispatch(&Msg{ID: 41, OK: true, Raw: []byte(`{"id":41,"ok":true}`)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m, err := c.giveUp(ctx, "tap", 41, ch, time.Now(), time.Second, false)
	if err != nil || m == nil || m.ID != 41 {
		t.Fatalf("issue #202: giveUp with the answer in = %+v %v, want the answer", m, err)
	}
	c.mu.Lock()
	n := len(c.abandoned)
	c.mu.Unlock()
	if n != 0 {
		t.Errorf("issue #202: an answered request was remembered as given up")
	}
}

func TestCallZeroTimeoutWaits(t *testing.T) {
	c, p := pair(t)
	res := goCall(c, "settle", nil, 0)
	req := p.read()
	time.Sleep(60 * time.Millisecond)
	select {
	case r := <-res:
		t.Fatalf("returned early: %+v %v", r.m, r.err)
	default:
	}
	p.reply(req["id"], `"ok":true`)
	if r := await(t, res); r.err != nil {
		t.Fatal(r.err)
	}
}

func TestConcurrentCalls(t *testing.T) {
	c, p := pair(t)
	const n = 50
	results := make([]<-chan callResult, n)
	for i := 0; i < n; i++ {
		results[i] = goCall(c, "echo", map[string]any{"n": i}, wait)
	}
	reqs := make([]map[string]any, n)
	ids := map[float64]bool{}
	for i := 0; i < n; i++ {
		reqs[i] = p.read()
		ids[reqs[i]["id"].(float64)] = true
	}
	if len(ids) != n {
		t.Fatalf("ids not unique: %d distinct of %d", len(ids), n)
	}
	for i := n - 1; i >= 0; i-- {
		p.reply(reqs[i]["id"], fmt.Sprintf(`"ok":true,"n":%v`, reqs[i]["n"]))
	}
	for i := 0; i < n; i++ {
		r := await(t, results[i])
		if r.err != nil {
			t.Fatalf("call %d: %v", i, r.err)
		}
		var body struct{ N int }
		r.m.Decode(&body)
		if body.N != i {
			t.Errorf("call %d got the answer for %d", i, body.N)
		}
	}
}

func TestUnknownIDIgnored(t *testing.T) {
	c, p := pair(t)
	p.reply(999, `"ok":true`)
	res := goCall(c, "ping", nil, time.Second)
	req := p.read()
	p.reply(req["id"], `"ok":true,"seq":1`)
	if r := await(t, res); r.err != nil || r.m.Seq != 1 {
		t.Fatalf("%+v %v", r.m, r.err)
	}
}

func TestEventNeverAnswersCall(t *testing.T) {
	c, p := pair(t)
	sub := c.Subscribe()
	res := goCall(c, "tap", nil, 0)
	req := p.read()
	p.send(fmt.Sprintf(`{"id":%v,"ev":"changed","seq":9}`+"\n", req["id"]))
	if ev := recv(t, sub); ev.Ev != "changed" || ev.Seq != 9 {
		t.Errorf("event = %+v", ev)
	}
	select {
	case r := <-res:
		t.Fatalf("event answered the call: %+v %v", r.m, r.err)
	default:
	}
	p.reply(req["id"], `"ok":true`)
	if r := await(t, res); r.err != nil {
		t.Fatal(r.err)
	}
}

func TestBinaryAttachment(t *testing.T) {
	c, p := pair(t)
	sub := c.Subscribe()
	res := goCall(c, "screenshot", map[string]any{"scale": 0.5}, 0)
	req := p.read()
	payload := "\x89PNG\r\n\x1a\n{\"id\":5}\n"
	p.send(fmt.Sprintf(`{"id":%v,"ok":true,"w":10,"h":20,"bin":%d}`+"\n%s", req["id"], len(payload), payload))
	p.send(`{"ev":"changed","seq":2}` + "\n")
	r := await(t, res)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if string(r.m.Data) != payload || r.m.Bin != len(payload) {
		t.Errorf("Data = %q (bin %d)", r.m.Data, r.m.Bin)
	}
	if strings.Contains(string(r.m.Raw), "PNG") {
		t.Errorf("Raw includes the binary: %q", r.m.Raw)
	}
	if ev := recv(t, sub); ev.Ev != "changed" || ev.Seq != 2 {
		t.Errorf("message after binary = %+v", ev)
	}
}

func TestBinaryEvent(t *testing.T) {
	c, p := pair(t)
	sub := c.Subscribe()
	p.send(`{"ev":"frame","bin":3}` + "\nabc")
	if ev := recv(t, sub); ev.Ev != "frame" || string(ev.Data) != "abc" {
		t.Errorf("event = %+v", ev)
	}
}

func TestTruncatedBinary(t *testing.T) {
	c, p := pair(t)
	go func() {
		p.send(`{"ev":"frame","bin":10}` + "\nabc")
		p.c.Close()
	}()
	waitDone(t, c)
	if err := c.Err(); err == nil || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("Err = %v, want unexpected EOF", err)
	}
}

func TestBadMessage(t *testing.T) {
	c, p := pair(t)
	go p.send("this is not json\n")
	waitDone(t, c)
	if err := c.Err(); err == nil || !strings.HasPrefix(err.Error(), "bad message from agent: ") {
		t.Errorf("Err = %v", err)
	}
	if _, err := c.Call("ping", nil, time.Second); err == nil || !strings.Contains(err.Error(), "bad message") {
		t.Errorf("Call after a bad message: %v", err)
	}
}

func TestPeerCloseFailsPendingCalls(t *testing.T) {
	c, p := pair(t)
	res := goCall(c, "tree", nil, 0)
	p.read()
	p.c.Close()
	r := await(t, res)
	if r.err == nil || r.err.Error() != "agent connection closed" {
		t.Errorf("pending call err = %v", r.err)
	}
	waitDone(t, c)
	if err := c.Err(); err.Error() != "agent connection closed" {
		t.Errorf("Err = %v", err)
	}
	start := time.Now()
	if _, err := c.Call("ping", nil, 0); err == nil || err.Error() != "agent connection closed" {
		t.Errorf("Call after close = %v", err)
	}
	if time.Since(start) > time.Second {
		t.Error("Call after close did not fail at once")
	}
}

func TestClose(t *testing.T) {
	c, _ := pair(t)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	waitDone(t, c)
	if c.Err() == nil {
		t.Error("Err is nil after Close")
	}
}

func TestErrWhileOpen(t *testing.T) {
	c, _ := pair(t)
	if err := c.Err(); err == nil || err.Error() != "agent connection closed" {
		t.Errorf("Err = %v", err)
	}
	select {
	case <-c.Done():
		t.Error("Done closed on an open connection")
	default:
	}
}

func TestEventsAndSeq(t *testing.T) {
	c, p := pair(t)
	if c.Seq() != 0 || c.SettledSeq() != 0 {
		t.Fatalf("initial seq %d/%d", c.Seq(), c.SettledSeq())
	}
	s1, s2 := c.Subscribe(), c.Subscribe()
	p.send(`{"ev":"changed","seq":3,"pkg":"com.app"}` + "\n")
	p.send(`{"ev":"changed","seq":5}` + "\n")
	p.send(`{"ev":"settled","seq":4}` + "\n")
	p.send(`{"ev":"ax","n":1}` + "\n")
	for _, sub := range []chan *Msg{s1, s2} {
		var got []string
		for i := 0; i < 4; i++ {
			m := recv(t, sub)
			got = append(got, fmt.Sprintf("%s:%d", m.Ev, m.Seq))
		}
		if strings.Join(got, " ") != "changed:3 changed:5 settled:4 ax:0" {
			t.Errorf("events = %v", got)
		}
	}
	if c.Seq() != 5 {
		t.Errorf("Seq = %d, want 5 (latest changed)", c.Seq())
	}
	if c.SettledSeq() != 4 {
		t.Errorf("SettledSeq = %d, want 4", c.SettledSeq())
	}
	if c.lastChanged.Load() == 0 {
		t.Error("lastChanged not recorded")
	}
	p.send(`{"ev":"settled","seq":5}` + "\n")
	p.send(`{"ev":"changed","seq":8}` + "\n")
	recv(t, s1)
	recv(t, s1)
	if c.Seq() != 8 || c.SettledSeq() != 5 {
		t.Errorf("Seq/SettledSeq = %d/%d, want 8/5", c.Seq(), c.SettledSeq())
	}
	var pkg struct{ Pkg string }
	m := &Msg{Raw: json.RawMessage(`{"ev":"changed","seq":3,"pkg":"com.app"}`)}
	if m.Decode(&pkg); pkg.Pkg != "com.app" {
		t.Errorf("pkg = %q", pkg.Pkg)
	}
}

func TestUnsubscribe(t *testing.T) {
	c, p := pair(t)
	keep, drop := c.Subscribe(), c.Subscribe()
	c.Unsubscribe(drop)
	c.Unsubscribe(make(chan *Msg))
	p.send(`{"ev":"changed","seq":1}` + "\n")
	recv(t, keep)
	select {
	case m := <-drop:
		t.Errorf("unsubscribed channel got %+v", m)
	default:
	}
	p.c.Close()
	waitDone(t, c)
	if _, ok := <-keep; ok {
		t.Error("subscribed channel not closed on drop")
	}
	select {
	case _, ok := <-drop:
		if !ok {
			t.Error("unsubscribed channel was closed")
		}
	default:
	}
}

func TestSlowSubscriberDoesNotBlock(t *testing.T) {
	c, p := pair(t)
	slow := c.Subscribe()
	var b strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&b, `{"ev":"changed","seq":%d}`+"\n", i)
	}
	go p.send(b.String())
	res := goCall(c, "ping", nil, wait)
	req := p.read()
	p.reply(req["id"], `"ok":true`)
	if r := await(t, res); r.err != nil {
		t.Fatal(r.err)
	}
	for deadline := time.Now().Add(2 * time.Second); c.Seq() < 300 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	if len(slow) != cap(slow) || cap(slow) != 256 {
		t.Errorf("slow subscriber holds %d of %d", len(slow), cap(slow))
	}
	if c.Seq() != 300 {
		t.Errorf("Seq = %d, want 300 (seq tracking is not held up by subscribers)", c.Seq())
	}
	if first := <-slow; first.Seq != 1 {
		t.Errorf("first buffered event seq = %d, want the oldest kept", first.Seq)
	}
}

func TestCallMarshalError(t *testing.T) {
	c, _ := pair(t)
	_, err := c.Call("bad", map[string]any{"ch": make(chan int)}, time.Second)
	if err == nil {
		t.Fatal("expected an encoding error")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) != 0 {
		t.Errorf("pending = %d", len(c.pending))
	}
}

type failWriter struct{ net.Conn }

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("write refused") }

func TestCallWriteError(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	c := newConn(failWriter{a})
	_, err := c.Call("tap", nil, time.Second)
	if err == nil || !strings.Contains(err.Error(), "write refused") {
		t.Fatalf("err = %v", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) != 0 {
		t.Errorf("pending = %d", len(c.pending))
	}
}

func TestCallFailsAfterDone(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	c := buildConn(a)
	returned := make(chan struct{})
	c.afterWake = func() {
		select {
		case <-returned:
		case <-time.After(wait):
		}
	}
	go c.readLoop()
	type result struct {
		err  error
		done bool
	}
	res := make(chan result, 1)
	go func() {
		_, err := c.Call("tap", nil, 0)
		res <- result{err, isClosed(c.closed)}
		close(returned)
	}()
	p := &peer{t: t, c: b, r: bufio.NewReader(b)}
	p.read()
	b.Close()
	var r result
	select {
	case r = <-res:
	case <-time.After(wait):
		t.Fatal("call did not return")
	}
	if r.err == nil || r.err.Error() != "agent connection closed" {
		t.Fatalf("err = %v", r.err)
	}
	if !r.done {
		t.Error(`the call failed with "agent connection closed" but Done was still open`)
	}
	waitDone(t, c)
}

func TestCallWriteErrorClosesConn(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	c := newConn(failWriter{a})
	if _, err := c.Call("tap", nil, time.Second); err == nil {
		t.Fatal("the call did not fail")
	}
	if !isClosed(c.closed) {
		t.Error("the call failed on a broken write but Done was still open")
	}
}

func TestAgentErrorString(t *testing.T) {
	if got := (&AgentError{Op: "tap", Msg: "boom"}).Error(); got != "tap: boom" {
		t.Errorf("Error() = %q", got)
	}
}

func TestHelloVersion(t *testing.T) {
	c, _ := pair(t)
	if c.Hello() != nil || c.HelloVersion() != "" {
		t.Errorf("no hello: %v %q", c.Hello(), c.HelloVersion())
	}
	c.hello = &Msg{Ev: "hello", Raw: json.RawMessage(`{"ev":"hello","platform":"ios","version":"abc-123","w":390}`)}
	if c.HelloVersion() != "abc-123" {
		t.Errorf("HelloVersion = %q", c.HelloVersion())
	}
	c.hello = &Msg{Ev: "hello", Raw: json.RawMessage(`{"ev":"hello"}`)}
	if c.HelloVersion() != "" {
		t.Errorf("HelloVersion without version = %q", c.HelloVersion())
	}
}

func fakeAgent(t *testing.T, serve ...func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for i := 0; ; i++ {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			h := serve[min(i, len(serve)-1)]
			go func() { h(c) }()
		}
	}()
	return ln.Addr().String()
}

func helloThenEcho(version string) func(net.Conn) {
	return func(c net.Conn) {
		defer c.Close()
		fmt.Fprintf(c, `{"ev":"hello","platform":"android","version":%q,"w":1080,"h":2400,"seq":7}`+"\n", version)
		r := bufio.NewReader(c)
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				return
			}
			var req map[string]any
			json.Unmarshal(line, &req)
			fmt.Fprintf(c, `{"id":%v,"ok":true,"op":%q}`+"\n", req["id"], req["op"])
		}
	}
}

func TestDial(t *testing.T) {
	addr := fakeAgent(t, helloThenEcho("2.0.1"))
	c, err := Dial(addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Hello() == nil || c.Hello().Ev != "hello" || c.HelloVersion() != "2.0.1" {
		t.Errorf("hello = %+v, version %q", c.Hello(), c.HelloVersion())
	}
	var h struct{ W, H int }
	c.Hello().Decode(&h)
	if h.W != 1080 || h.H != 2400 {
		t.Errorf("hello size = %+v", h)
	}
	c.mu.Lock()
	subs := len(c.subs)
	c.mu.Unlock()
	if subs != 0 {
		t.Errorf("%d subscribers left after Dial", subs)
	}
	m, err := c.Call("info", nil, time.Second)
	if err != nil || !strings.Contains(string(m.Raw), `"op":"info"`) {
		t.Fatalf("Call over TCP: %+v %v", m, err)
	}
}

func TestDialRetriesUntilHello(t *testing.T) {
	notYet := func(c net.Conn) {
		fmt.Fprint(c, `{"ev":"changed","seq":1}`+"\n")
		time.Sleep(50 * time.Millisecond)
		c.Close()
	}
	hangUp := func(c net.Conn) { c.Close() }
	addr := fakeAgent(t, notYet, hangUp, helloThenEcho("3"))
	c, err := Dial(addr, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.HelloVersion() != "3" {
		t.Errorf("version = %q", c.HelloVersion())
	}
}

func TestDialSkipsAnEventBeforeTheHello(t *testing.T) {
	var conns atomic.Int32
	eventFirst := func(c net.Conn) {
		conns.Add(1)
		fmt.Fprint(c, `{"ev":"changed","seq":3}`+"\n")
		helloThenEcho("4")(c)
	}
	addr := fakeAgent(t, eventFirst)
	c, err := Dial(addr, 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.HelloVersion() != "4" {
		t.Errorf("version = %q", c.HelloVersion())
	}
	if n := conns.Load(); n != 1 {
		t.Errorf("Dial connected %d times, want once: it dropped the connection that pushed first", n)
	}
	if c.Seq() != 3 {
		t.Errorf("Seq = %d: the event before the hello still counts", c.Seq())
	}
}

func TestDialNoHello(t *testing.T) {
	addr := fakeAgent(t, func(c net.Conn) { c.Close() })
	start := time.Now()
	_, err := Dial(addr, 150*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "connecting to agent at "+addr) || !strings.Contains(err.Error(), "agent did not say hello") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("Dial took %s", time.Since(start))
	}
}

func TestDialNoAgent(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	start := time.Now()
	_, err = Dial(addr, 120*time.Millisecond)
	if err == nil || !strings.HasPrefix(err.Error(), "connecting to agent at "+addr+": ") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "hello") {
		t.Errorf("err blames hello, want the dial error: %v", err)
	}
	if d := time.Since(start); d < 100*time.Millisecond || d > 2*time.Second {
		t.Errorf("Dial returned after %s", d)
	}
}

func TestIsClosed(t *testing.T) {
	ch := make(chan struct{})
	if isClosed(ch) {
		t.Error("open channel reported closed")
	}
	close(ch)
	if !isClosed(ch) {
		t.Error("closed channel reported open")
	}
}

func TestSubscribeAfterDropIsClosed(t *testing.T) {
	c, p := pair(t)
	p.c.Close()
	select {
	case <-c.Done():
	case <-time.After(wait):
		t.Fatal("the drop was not noticed")
	}
	select {
	case _, ok := <-c.Subscribe():
		if ok {
			t.Fatal("got an event from a dropped connection")
		}
	case <-time.After(wait):
		t.Fatal("a subscription after the drop stays open")
	}
}
