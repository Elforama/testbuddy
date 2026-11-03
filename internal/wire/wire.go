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
	ID    int64           `json:"id,omitempty"`
	OK    bool            `json:"ok,omitempty"`
	Error string          `json:"error,omitempty"`
	Ev    string          `json:"ev,omitempty"`
	Seq   int64           `json:"seq,omitempty"`
	Bin   int             `json:"bin,omitempty"`
	Raw   json.RawMessage `json:"-"`
	Data  []byte          `json:"-"`
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
	closed  chan struct{}
	err     error

	lastSeq     atomic.Int64 // seq of the latest "changed" event
	settledSeq  atomic.Int64 // seq the latest "settled" event refers to
	settleCond  *sync.Cond
	settleMu    sync.Mutex
	lastChanged atomic.Int64 // unix nanos of the latest change event
	hello       *Msg
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
			select {
			case m := <-hello:
				conn.Unsubscribe(hello)
				if m != nil && m.Ev == "hello" {
					conn.hello = m
					return conn, nil
				}
			case <-conn.closed:
			case <-time.After(2 * time.Second):
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
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	for id, ch := range c.subs {
		close(ch)
		delete(c.subs, id)
	}
	c.mu.Unlock()
	close(c.closed)
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
	c.mu.Unlock()
	if ch != nil {
		ch <- m
	}
}

// Subscribe returns a channel of pushed events. Call Unsubscribe when done.
func (c *Conn) Subscribe() chan *Msg {
	ch := make(chan *Msg, 256)
	c.mu.Lock()
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
	_, err = c.w.Write(append(b, '\n'))
	if err == nil {
		err = c.w.Flush()
	}
	c.wmu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	var timer <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timer = t.C
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return nil, c.Err()
		}
		if !m.OK {
			if m.Error == "" {
				m.Error = "agent returned an error"
			}
			return m, &AgentError{Op: op, Msg: m.Error}
		}
		return m, nil
	case <-timer:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("agent did not answer %q within %s", op, timeout)
	}
}

// AgentError is an error the agent reported for a request.
type AgentError struct {
	Op  string
	Msg string
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
