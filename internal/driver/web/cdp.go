package web

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// cdp is a minimal Chrome DevTools Protocol client over one websocket: commands with ids,
// and every event pushed to a handler. Nothing here polls.
type cdp struct {
	ws      *websocket.Conn
	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan cdpReply
	onEvent func(method string, params json.RawMessage, session string)
	done    chan struct{}
	err     error
}

// writeTimeout bounds sending one command to the browser.
const writeTimeout = 10 * time.Second

type cdpReply struct {
	Result json.RawMessage
	Err    error
}

func dialCDP(ctx context.Context, url string, onEvent func(string, json.RawMessage, string)) (*cdp, error) {
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(64 << 20) // screenshots and big pages
	c := &cdp{ws: ws, pending: map[int64]chan cdpReply{}, onEvent: onEvent, done: make(chan struct{})}
	go c.readLoop()
	return c, nil
}

func (c *cdp) readLoop() {
	ctx := context.Background()
	var err error
	for {
		var data []byte
		_, data, err = c.ws.Read(ctx)
		if err != nil {
			break
		}
		var m struct {
			ID        int64           `json:"id"`
			Method    string          `json:"method"`
			SessionID string          `json:"sessionId"`
			Params    json.RawMessage `json:"params"`
			Result    json.RawMessage `json:"result"`
			Error     *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m.ID != 0 {
			c.mu.Lock()
			ch := c.pending[m.ID]
			delete(c.pending, m.ID)
			c.mu.Unlock()
			if ch != nil {
				r := cdpReply{Result: m.Result}
				if m.Error != nil {
					r.Err = fmt.Errorf("%s (%d)", m.Error.Message, m.Error.Code)
				}
				ch <- r
			}
			continue
		}
		if m.Method != "" && c.onEvent != nil {
			c.onEvent(m.Method, m.Params, m.SessionID)
		}
	}
	c.mu.Lock()
	c.err = err
	for id, ch := range c.pending {
		ch <- cdpReply{Err: fmt.Errorf("browser connection closed: %v", err)}
		delete(c.pending, id)
	}
	c.mu.Unlock()
	close(c.done)
}

// call sends a command and decodes its result into out (which may be nil).
func (c *cdp) call(ctx context.Context, method string, params any, out any) error {
	return c.callIn(ctx, "", method, params, out)
}

// callIn sends a command to an attached target's session ("" = the connection's own target).
func (c *cdp) callIn(ctx context.Context, session, method string, params any, out any) error {
	id := c.nextID.Add(1)
	msg := map[string]any{"id": id, "method": method}
	if session != "" {
		msg["sessionId"] = session
	}
	if params != nil {
		msg["params"] = params
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	ch := make(chan cdpReply, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return fmt.Errorf("browser connection closed: %v", c.err)
	}
	c.pending[id] = ch
	c.mu.Unlock()
	// The websocket closes the whole connection when a write's context ends, so the write
	// does not take the caller's: a cancelled step (or a screenshot past its bound, issue
	// #163) must fail alone. A write that cannot finish in writeTimeout means the browser
	// stopped reading, and closing the connection is then right.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	err = c.ws.Write(wctx, websocket.MessageText, b)
	cancel()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case r := <-ch:
		if r.Err != nil {
			return fmt.Errorf("%s: %w", method, r.Err)
		}
		if out != nil && len(r.Result) > 0 {
			return json.Unmarshal(r.Result, out)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

func (c *cdp) close() { c.ws.Close(websocket.StatusNormalClosure, "") }

// lost is why the connection ended (its read loop stopped), or nil while it is open.
func (c *cdp) lost() error {
	if c == nil {
		return nil
	}
	select {
	case <-c.done:
	default:
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return fmt.Errorf("browser connection closed: %v", c.err)
}
