// Package wire is the protocol between the host and the on-device agents.
//
// One TCP connection carries everything in both directions. Each message is a single
// line of JSON. A message may carry binary data (a screenshot): it then has a "bin"
// field with the byte count, and that many raw bytes follow the newline.
//
// The host sends requests:   {"id":7,"op":"tap","x":100,"y":200}
// The agent answers each:    {"id":7,"ok":true,...} or {"id":7,"ok":false,"error":"..."}
// The agent also pushes events without being asked (no "id"):
//
//	{"ev":"changed","seq":41}   the UI changed (an accessibility event arrived)
//	{"ev":"settled","seq":41}   no change for the quiet window since seq 41
//
// The host never polls: it waits on these events.
package wire

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Msg is a decoded message. Raw keeps the full JSON so callers can decode op-specific fields.
type Msg struct {
	ID    int64  `json:"id,omitempty"`
	OK    bool   `json:"ok,omitempty"`
	Error string `json:"error,omitempty"`
	// Transient marks an error the agent says is the device's passing state, not the
	// request's own failure: the host may ask again (iOS launch, issue #126).
	Transient bool            `json:"transient,omitempty"`
	Ev        string          `json:"ev,omitempty"`
	Seq       int64           `json:"seq,omitempty"`
	Bin       int             `json:"bin,omitempty"`
	Raw       json.RawMessage `json:"-"`
	Data      []byte          `json:"-"`
}

// Decode unmarshals the full message into v.
func (m *Msg) Decode(v any) error { return json.Unmarshal(m.Raw, v) }

// Conn is a connection to one agent.
type Conn struct {
	c      net.Conn
	w      *bufio.Writer
	wmu    sync.Mutex
	nextID atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan *Msg
	subs    map[int]chan *Msg
	nextSub int
	ended   bool // the read loop has ended: no more events
	closed  chan struct{}
	err     error

	// abandoned: requests given up on (a cancel) that the device may still be running, by id
	// (CallContext); guarded by mu.
	abandoned map[int64]*abandonedOp

	lastSeq     atomic.Int64 // seq of the latest "changed" event
	settledSeq  atomic.Int64 // seq the latest "settled" event refers to
	settleCond  *sync.Cond
	settleMu    sync.Mutex
	lastChanged atomic.Int64 // unix nanos of the latest change event
	hello       *Msg

	// afterWake, when set (by tests, before the read loop starts), runs once a dropped
	// connection has woken its pending calls, so a test can see what they saw (issue #66).
	afterWake func()
}

// Hello is the agent's greeting.
func (c *Conn) Hello() *Msg { return c.hello }

// HelloVersion is the agent build version from its greeting.
func (c *Conn) HelloVersion() string {
	if c.hello == nil {
		return ""
	}
	var h struct {
		Version string `json:"version"`
	}
	c.hello.Decode(&h)
	return h.Version
}

// Dial connects to an agent at addr, retrying until timeout while it starts up.
func Dial(addr string, timeout time.Duration) (*Conn, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			// The agent sends {"ev":"hello"} first; an adb forward accepts the TCP
			// connection even before the agent listens, so wait for it. Subscribe
			// before the read loop starts, or a hello sent at once can be dropped.
			conn := buildConn(c)
			hello := conn.Subscribe()
			go conn.readLoop()
			if m := awaitHello(conn, hello); m != nil {
				conn.hello = m
				return conn, nil
			}
			conn.Close()
			lastErr = errors.New("agent did not say hello")
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("connecting to agent at %s: %w", addr, lastErr)
		}
		time.Sleep(50 * time.Millisecond) // connection setup only, never during a test
	}
}

// awaitHello returns the agent's hello from sub, or nil if the connection closes or none comes
// within 2 s. An event before the hello (Android agents before 0.1.47 could push one first) is
// skipped: it still counted when dispatched. sub is unsubscribed either way.
func awaitHello(conn *Conn, sub chan *Msg) *Msg {
	defer conn.Unsubscribe(sub)
	timeout := time.After(2 * time.Second)
	for {
		select {
		case m := <-sub:
			if m == nil {
				return nil
			}
			if m.Ev == "hello" {
				return m
			}
		case <-conn.closed:
			return nil
		case <-timeout:
			return nil
		}
	}
}

func newConn(c net.Conn) *Conn {
	conn := buildConn(c)
	go conn.readLoop()
	return conn
}

// buildConn makes a Conn without starting its read loop.
func buildConn(c net.Conn) *Conn {
	if tc, ok := c.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
	conn := &Conn{
		c:       c,
		w:       bufio.NewWriterSize(c, 64<<10),
		pending: map[int64]chan *Msg{},
		subs:    map[int]chan *Msg{},
		closed:  make(chan struct{}),
	}
	conn.abandoned = map[int64]*abandonedOp{}
	conn.settleCond = sync.NewCond(&conn.settleMu)
	return conn
}

func (c *Conn) readLoop() {
	r := bufio.NewReaderSize(c.c, 256<<10)
	var err error
	for {
		var line []byte
		line, err = r.ReadBytes('\n')
		if err != nil {
			break
		}
		m := &Msg{}
		if err = json.Unmarshal(line, m); err != nil {
			err = fmt.Errorf("bad message from agent: %w", err)
			break
		}
		m.Raw = append(json.RawMessage(nil), line...)
		if m.Bin > 0 {
			m.Data = make([]byte, m.Bin)
			if _, err = io.ReadFull(r, m.Data); err != nil {
				break
			}
		}
		c.dispatch(m)
	}
	c.mu.Lock()
	c.err = err
	c.ended = true
	// Done closes before the pending calls wake: a call that fails here returns with Done
	// already closed, so its caller knows the connection dropped and sends it again on a new
	// one (issue #66).
	close(c.closed)
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	for id, a := range c.abandoned {
		close(a.done)
		delete(c.abandoned, id)
	}
	for id, ch := range c.subs {
		close(ch)
		delete(c.subs, id)
	}
	c.mu.Unlock()
	if c.afterWake != nil {
		c.afterWake()
	}
	c.settleMu.Lock()
	c.settleCond.Broadcast()
	c.settleMu.Unlock()
}

func (c *Conn) dispatch(m *Msg) {
	if m.Ev != "" {
		switch m.Ev {
		case "changed":
			c.lastSeq.Store(m.Seq)
			c.lastChanged.Store(time.Now().UnixNano())
		case "settled":
			c.settledSeq.Store(m.Seq)
			c.settleMu.Lock()
			c.settleCond.Broadcast()
			c.settleMu.Unlock()
		}
		c.mu.Lock()
		for _, ch := range c.subs {
			select {
			case ch <- m:
			default: // a slow subscriber drops events rather than blocking the agent
			}
		}
		c.mu.Unlock()
		return
	}
	c.mu.Lock()
	ch := c.pending[m.ID]
	delete(c.pending, m.ID)
	if a := c.abandoned[m.ID]; a != nil { // a given-up request is done: dropped
		close(a.done)
		delete(c.abandoned, m.ID)
	}
	c.mu.Unlock()
	if ch != nil {
		ch <- m
	}
}

// Subscribe returns a channel of pushed events. Call Unsubscribe when done.
func (c *Conn) Subscribe() chan *Msg {
	ch := make(chan *Msg, 256)
	c.mu.Lock()
	if c.ended { // already dropped: closed at once, so the subscriber sees the drop
		c.mu.Unlock()
		close(ch)
		return ch
	}
	c.nextSub++
	c.subs[c.nextSub] = ch
	c.mu.Unlock()
	return ch
}

func (c *Conn) Unsubscribe(ch chan *Msg) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, s := range c.subs {
		if s == ch {
			delete(c.subs, id)
			return
		}
	}
}

// Seq is the sequence number of the latest UI change the agent reported.
func (c *Conn) Seq() int64 { return c.lastSeq.Load() }

// SettledSeq is the change sequence number the latest "settled" event covered.
func (c *Conn) SettledSeq() int64 { return c.settledSeq.Load() }

// Done is closed when the connection drops.
func (c *Conn) Done() <-chan struct{} { return c.closed }

// Err is why the connection dropped.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil || errors.Is(c.err, io.EOF) {
		return errors.New("agent connection closed")
	}
	return c.err
}

// Call sends a request and waits for its answer. params is merged into the request.
func (c *Conn) Call(op string, params map[string]any, timeout time.Duration) (*Msg, error) {
	return c.CallContext(context.Background(), op, params, timeout)
}

// CallContext is Call that also ends with ctx (issue #202):
//   - a ctx already done when CallContext is called sends nothing and returns ctx.Err()
//     (context.Canceled, or context.DeadlineExceeded for a deadline already past);
//   - a wait for the answer that ctx ends returns ctx.Err() when ctx was cancelled, and a
//     *ReplyTimeout when its deadline passed, as a wait cut to the time left did before:
//     callers tell a late answer by its type;
//   - an answer that has arrived when ctx ends (or the timeout passes) is returned as the
//     answer: what the agent did is reported done.
//
// The connection stays usable after a call ctx ended. Answers are matched to requests by id,
// not by order: the call's id leaves pending when it gives up, so the agent's late answer to
// it finds no caller and is dropped (dispatch), and the next call waits for its own id.
//
// The agents run requests one at a time, in order (Android: one reader thread; iOS: a serial
// queue), so a request sent after one that was given up on waits on the device until that one
// is done. The given-up request is remembered with the time it was bounded by (sent + its
// timeout): a later call's reply timeout starts once the answers to the given-up requests
// sent before it have arrived, or their bounds have passed, so it is not charged for the
// time they still take. ctx's own deadline is not moved.
func (c *Conn) CallContext(ctx context.Context, op string, params map[string]any, timeout time.Duration) (*Msg, error) {
	return c.call(ctx, op, params, timeout, true)
}

// CallUnqueued is CallContext whose reply timeout starts at once, even with given-up requests
// still ahead of it on the device. For requests whose answer only says they arrived, sent on
// the way out (exit, keep): a timeout is better there than waiting, request after request,
// for every given-up one's bound (issue #202). The request still runs on the device once the
// ones ahead of it are done.
func (c *Conn) CallUnqueued(ctx context.Context, op string, params map[string]any, timeout time.Duration) (*Msg, error) {
	return c.call(ctx, op, params, timeout, false)
}

// Abandoned is how many requests given up on (CallContext) the device may still be running:
// a request sent now waits behind them.
func (c *Conn) Abandoned() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.aheadLocked(time.Now()))
}

// call is CallContext; queued: wait for the given-up requests ahead before the reply timeout
// starts.
func (c *Conn) call(ctx context.Context, op string, params map[string]any, timeout time.Duration, queued bool) (*Msg, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id := c.nextID.Add(1)
	req := make(map[string]any, len(params)+2)
	for k, v := range params {
		req[k] = v
	}
	req["id"] = id
	req["op"] = op
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	ch := make(chan *Msg, 1)
	c.mu.Lock()
	if c.err != nil || isClosed(c.closed) {
		c.mu.Unlock()
		return nil, c.Err()
	}
	c.pending[id] = ch
	c.mu.Unlock()

	c.wmu.Lock()
	// The given-up requests already written are ahead of this one on the device. One given
	// up after this write is not: it was answered or still runs while this one waits.
	var ahead []*abandonedOp
	if queued {
		c.mu.Lock()
		ahead = c.aheadLocked(time.Now())
		c.mu.Unlock()
	}
	_, err = c.w.Write(append(b, '\n'))
	if err == nil {
		err = c.w.Flush()
	}
	c.wmu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		// A connection that cannot be written to is gone: close it and wait for the read loop
		// to end, so the call returns with Done closed and its caller reconnects (issue #66).
		c.c.Close()
		<-c.closed
		return nil, err
	}

	// start is when this request's own time begins: once the device is done with the
	// given-up requests ahead of it (bound: the latest of their bounds while they run).
	start := time.Now()
	for _, a := range ahead {
		start = later(start, a.until)
	}
	for _, a := range ahead {
		t := time.NewTimer(time.Until(a.until))
		select {
		case <-a.done:
		case <-t.C:
		case m, ok := <-ch:
			t.Stop()
			return answer(op, m, ok, c)
		case <-ctx.Done():
			t.Stop()
			return c.giveUp(ctx, op, id, ch, start, timeout, true)
		}
		t.Stop()
	}
	start = time.Now()

	var timer <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timer = t.C
	}
	select {
	case m, ok := <-ch:
		return answer(op, m, ok, c)
	case <-timer:
		return c.giveUp(ctx, op, id, ch, start, timeout, false)
	case <-ctx.Done():
		return c.giveUp(ctx, op, id, ch, start, timeout, len(ahead) > 0)
	}
}

// answer is a call's result from its reply (ok false: the connection dropped).
func answer(op string, m *Msg, ok bool, c *Conn) (*Msg, error) {
	if !ok {
		return nil, c.Err()
	}
	if !m.OK {
		if m.Error == "" {
			m.Error = "agent returned an error"
		}
		return m, &AgentError{Op: op, Msg: m.Error, Transient: m.Transient}
	}
	return m, nil
}

// giveUp ends a call whose timeout passed or whose ctx ended. An answer already taken from
// pending (arrived, or on its way to ch) is the call's result. Otherwise the request is given
// up: its late answer is dropped, and while the device may still run it (until start+timeout)
// it is remembered as ahead of the calls sent after it. behind: the call waited behind
// given-up requests, and a deadline that ends it says so.
func (c *Conn) giveUp(ctx context.Context, op string, id int64, ch chan *Msg, start time.Time, timeout time.Duration, behind bool) (*Msg, error) {
	c.mu.Lock()
	if _, waiting := c.pending[id]; !waiting {
		c.mu.Unlock()
		m, ok := <-ch // dispatch sends it, or the read loop closed ch: never blocks long
		return answer(op, m, ok, c)
	}
	delete(c.pending, id)
	// The bound is the host's timeout, which the drivers cut to the time ctx has left: the
	// device may still be busy with the request after it (a cancelled `sh` runs to its own
	// end). Past the bound, later calls are charged for it again.
	if until := start.Add(timeout); timeout > 0 && time.Now().Before(until) && !c.ended {
		c.abandoned[id] = &abandonedOp{until: until, done: make(chan struct{})}
	}
	c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			// A deadline is a reply timeout, as it was when callers only shortened timeout
			// to the time left.
			return nil, &ReplyTimeout{Op: op, After: timeout, Queued: behind}
		}
		return nil, err
	}
	return nil, &ReplyTimeout{Op: op, After: timeout}
}

// abandonedOp is a request given up on that the device may still be running: done closes when
// its answer arrives (or the connection drops); until is the bound it was sent with.
type abandonedOp struct {
	until time.Time
	done  chan struct{}
}

// aheadLocked returns the given-up requests the device may still be running, forgetting those
// whose bound has passed. c.mu is held.
func (c *Conn) aheadLocked(now time.Time) []*abandonedOp {
	var out []*abandonedOp
	for id, a := range c.abandoned {
		if !now.Before(a.until) {
			delete(c.abandoned, id)
			continue
		}
		out = append(out, a)
	}
	return out
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// ReplyTimeout means the request was sent but the agent's answer did not arrive.
// Queued: ctx's deadline ended it while it waited, at least in part, behind requests given up
// on (CallContext).
type ReplyTimeout struct {
	Op     string
	After  time.Duration
	Queued bool
}

func (e *ReplyTimeout) Error() string {
	if e.Queued {
		return fmt.Sprintf("agent did not answer %q within %s (queued behind an abandoned request)", e.Op, e.After)
	}
	return fmt.Sprintf("agent did not answer %q within %s", e.Op, e.After)
}

// AgentError is an error the agent reported for a request. Transient is the reply's
// "transient": the agent says asking again may succeed.
type AgentError struct {
	Op        string
	Msg       string
	Transient bool
}

func (e *AgentError) Error() string { return e.Op + ": " + e.Msg }

func (c *Conn) Close() error { return c.c.Close() }

func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
