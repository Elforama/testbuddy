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

type Msg struct {
	ID        int64           `json:"id,omitempty"`
	OK        bool            `json:"ok,omitempty"`
	Error     string          `json:"error,omitempty"`
	Transient bool            `json:"transient,omitempty"`
	Ev        string          `json:"ev,omitempty"`
	Seq       int64           `json:"seq,omitempty"`
	Bin       int             `json:"bin,omitempty"`
	Raw       json.RawMessage `json:"-"`
	Data      []byte          `json:"-"`
}

func (m *Msg) Decode(v any) error { return json.Unmarshal(m.Raw, v) }

type Conn struct {
	c      net.Conn
	w      *bufio.Writer
	wmu    sync.Mutex
	nextID atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan *Msg
	subs    map[int]chan *Msg
	nextSub int
	ended   bool
	closed  chan struct{}
	err     error

	abandoned map[int64]*abandonedOp

	lastSeq     atomic.Int64
	settledSeq  atomic.Int64
	settleCond  *sync.Cond
	settleMu    sync.Mutex
	lastChanged atomic.Int64
	hello       *Msg

	afterWake func()
}

func (c *Conn) Hello() *Msg { return c.hello }

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

func Dial(addr string, timeout time.Duration) (*Conn, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
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
		time.Sleep(50 * time.Millisecond)
	}
}

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
			default:
			}
		}
		c.mu.Unlock()
		return
	}
	c.mu.Lock()
	ch := c.pending[m.ID]
	delete(c.pending, m.ID)
	if a := c.abandoned[m.ID]; a != nil {
		close(a.done)
		delete(c.abandoned, m.ID)
	}
	c.mu.Unlock()
	if ch != nil {
		ch <- m
	}
}

func (c *Conn) Subscribe() chan *Msg {
	ch := make(chan *Msg, 256)
	c.mu.Lock()
	if c.ended {
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

func (c *Conn) Seq() int64 { return c.lastSeq.Load() }

func (c *Conn) SettledSeq() int64 { return c.settledSeq.Load() }

func (c *Conn) Done() <-chan struct{} { return c.closed }

func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil || errors.Is(c.err, io.EOF) {
		return errors.New("agent connection closed")
	}
	return c.err
}

func (c *Conn) Call(op string, params map[string]any, timeout time.Duration) (*Msg, error) {
	return c.CallContext(context.Background(), op, params, timeout)
}

func (c *Conn) CallContext(ctx context.Context, op string, params map[string]any, timeout time.Duration) (*Msg, error) {
	return c.call(ctx, op, params, timeout, true)
}

func (c *Conn) CallUnqueued(ctx context.Context, op string, params map[string]any, timeout time.Duration) (*Msg, error) {
	return c.call(ctx, op, params, timeout, false)
}

func (c *Conn) Abandoned() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.aheadLocked(time.Now()))
}

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
		c.c.Close()
		<-c.closed
		return nil, err
	}

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

func (c *Conn) giveUp(ctx context.Context, op string, id int64, ch chan *Msg, start time.Time, timeout time.Duration, behind bool) (*Msg, error) {
	c.mu.Lock()
	if _, waiting := c.pending[id]; !waiting {
		c.mu.Unlock()
		m, ok := <-ch
		return answer(op, m, ok, c)
	}
	delete(c.pending, id)
	if until := start.Add(timeout); timeout > 0 && time.Now().Before(until) && !c.ended {
		c.abandoned[id] = &abandonedOp{until: until, done: make(chan struct{})}
	}
	c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &ReplyTimeout{Op: op, After: timeout, Queued: behind}
		}
		return nil, err
	}
	return nil, &ReplyTimeout{Op: op, After: timeout}
}

type abandonedOp struct {
	until time.Time
	done  chan struct{}
}

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
