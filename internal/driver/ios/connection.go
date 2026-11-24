package ios

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elforama/testbuddy/internal/ci"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/wire"
)

func (d *Driver) seq(runner int64) int64 {
	d.mu.Lock()
	v := d.seqBase + runner
	d.mu.Unlock()
	for {
		last := d.lastSeq.Load()
		if v <= last || d.lastSeq.CompareAndSwap(last, v) {
			return v
		}
	}
}

func (d *Driver) runnerSeq(v int64) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return max(v-d.seqBase, 0)
}

var stampedOps = map[string]bool{
	"tap": true, "doubleTap": true, "press": true, "swipe": true, "key": true, "type": true,
	"erase": true, "clear": true, "hideKeyboard": true, "rotate": true, "launch": true,
	"terminate": true, "activate": true,
}

var (
	ridBase = strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.Itoa(os.Getpid())
	ridNext atomic.Int64
)

func replyLimit(ctx context.Context, timeout time.Duration) time.Duration {
	if dl, ok := ctx.Deadline(); ok {
		if left := time.Until(dl); left < timeout {
			timeout = max(left, 10*time.Millisecond)
		}
	}
	return timeout
}

func (d *Driver) call(ctx context.Context, op string, p map[string]any, timeout time.Duration) (*wire.Msg, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit := func(timeout time.Duration) time.Duration { return replyLimit(ctx, timeout) }
	if stampedOps[op] {
		d.ready.Store(nil)
		q := make(map[string]any, len(p)+1)
		for k, v := range p {
			q[k] = v
		}
		q["rid"] = fmt.Sprintf("%s-%d", ridBase, ridNext.Add(1))
		p = q
	}
	c := d.conn()
	m, err := c.CallContext(ctx, op, d.runnerParams(p), limit(timeout))
	var agentErr *wire.AgentError
	if err == nil || errors.As(err, &agentErr) || ctx.Err() != nil {
		return m, err
	}
	select {
	case <-c.Done():
	default:
		return m, err
	}
	if rerr := d.reconnect(ctx, c); rerr != nil {
		return nil, fmt.Errorf("%w (reconnecting to the runner failed: %v)", err, rerr)
	}
	return d.conn().CallContext(ctx, op, d.runnerParams(p), limit(timeout))
}

func (d *Driver) runnerParams(p map[string]any) map[string]any {
	since, ok := p["since"].(int64)
	if !ok {
		return p
	}
	q := make(map[string]any, len(p))
	for k, v := range p {
		q[k] = v
	}
	q["since"] = d.runnerSeq(since)
	return q
}

func (d *Driver) reconnect(ctx context.Context, dropped *wire.Conn) error {
	d.connMu.Lock()
	if d.conn() != dropped {
		d.connMu.Unlock()
		return nil
	}
	if d.closed.Load() {
		d.connMu.Unlock()
		return errors.New("the driver is closed")
	}
	a := d.reconn
	if a == nil {
		a = &reconnAttempt{done: make(chan struct{})}
		d.reconn = a
		go func() {
			start := time.Now()
			d.mu.Lock()
			oldPID := d.runnerPID
			d.mu.Unlock()
			bctx, cancel := context.WithTimeout(context.Background(), reconnectBudget())
			a.err = d.redial(bctx)
			cancel()
			if a.err == nil {
				d.mu.Lock()
				newPID := d.runnerPID
				d.mu.Unlock()
				d.note(reconnectNote(start, time.Since(start), oldPID, newPID))
			}
			d.connMu.Lock()
			d.reconn = nil
			d.connMu.Unlock()
			close(a.done)
		}()
	}
	d.connMu.Unlock()
	select {
	case <-a.done:
		return a.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func reconnectNote(dropped time.Time, took time.Duration, oldPID, newPID int) string {
	which := ""
	switch {
	case newPID != 0 && newPID == oldPID:
		which = fmt.Sprintf(", to the same runner (pid %d)", newPID)
	case newPID != 0:
		which = fmt.Sprintf(", to a new runner (pid %d)", newPID)
	}
	return fmt.Sprintf("the connection to the tb runner dropped at %s (during this flow, or before it began): tb reconnected in %.1f s%s",
		dropped.Format("15:04:05.0"), took.Seconds(), which)
}

func (d *Driver) redial(ctx context.Context) error {
	for {
		err := d.connect(ctx)
		if err == nil {
			return nil
		}
		if d.closed.Load() {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Second):
		}
	}
}

var reconnectFor = 60 * time.Second

func reconnectBudget() time.Duration {
	if ci.On() {
		return 2 * reconnectFor
	}
	return reconnectFor
}

func (d *Driver) Raw(ctx context.Context, op string, p map[string]any) (json.RawMessage, error) {
	m, err := d.call(ctx, op, p, 60*time.Second)
	if err != nil {
		return nil, err
	}
	return m.Raw, nil
}

func (d *Driver) pump(c *wire.Conn, sub <-chan *wire.Msg) {
	for m := range sub {
		var e struct {
			Pkg string `json:"pkg"`
		}
		m.Decode(&e)
		d.fanout(driver.Event{Kind: m.Ev, Seq: d.seq(m.Seq), Pkg: e.Pkg})
	}
	if d.conn() != c || d.closed.Load() || d.stopped.Load() {
		return
	}
	if err := d.reconnect(context.Background(), c); err != nil {
		d.gaveUp.Store(&lostConn{conn: c, err: err})
		d.fanout(driver.Event{Kind: "disconnected"})
	}
}

func (d *Driver) fanout(e driver.Event) {
	d.subMu.Lock()
	defer d.subMu.Unlock()
	for _, ch := range d.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (d *Driver) Events() (<-chan driver.Event, func()) {
	ch := make(chan driver.Event, 64)
	d.subMu.Lock()
	if d.subs == nil {
		d.subs = map[int]chan driver.Event{}
	}
	d.nextSub++
	id := d.nextSub
	d.subs[id] = ch
	d.subMu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			d.subMu.Lock()
			delete(d.subs, id)
			d.subMu.Unlock()
		})
	}
}

func (d *Driver) WaitSettled(ctx context.Context, timeout time.Duration) error {
	_, err := d.call(ctx, "quiet", map[string]any{"timeout": timeout.Milliseconds()}, timeout+5*time.Second)
	return err
}

func (d *Driver) Settle(ctx context.Context, since int64, noChangeMs int, timeout time.Duration) (driver.SettleResult, error) {
	m, err := d.call(ctx, "settle", map[string]any{"since": since, "noChange": noChangeMs, "timeout": timeout.Milliseconds()}, timeout+5*time.Second)
	if err != nil {
		return "", err
	}
	var r struct {
		Result string `json:"result"`
	}
	m.Decode(&r)
	return driver.SettleResult(r.Result), nil
}
