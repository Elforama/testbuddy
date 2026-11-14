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

// seq maps a runner change count to the driver's, which never goes backwards.
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

// runnerSeq maps a driver change count back to the current runner's (0 when it is older
// than that runner).
func (d *Driver) runnerSeq(v int64) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return max(v-d.seqBase, 0)
}

// stampedOps carry a request token (rid): every op that changes the device. If the
// connection drops and the request is sent again, the runner answers it from its first run
// instead of tapping or typing twice. Reads are simply sent again.
var stampedOps = map[string]bool{
	"tap": true, "doubleTap": true, "press": true, "swipe": true, "key": true, "type": true,
	"erase": true, "clear": true, "hideKeyboard": true, "rotate": true, "launch": true,
	"terminate": true, "activate": true,
}

var (
	ridBase = strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.Itoa(os.Getpid())
	ridNext atomic.Int64
)

// replyLimit is how long call waits for the reply to a request sent now: timeout, or less when
// ctx ends sooner.
func replyLimit(ctx context.Context, timeout time.Duration) time.Duration {
	if dl, ok := ctx.Deadline(); ok {
		if left := time.Until(dl); left < timeout {
			timeout = max(left, 10*time.Millisecond)
		}
	}
	return timeout
}

func (d *Driver) call(ctx context.Context, op string, p map[string]any, timeout time.Duration) (*wire.Msg, error) {
	// A cancelled run sends nothing, and a wait for the answer ends with the run: the late
	// answer is dropped and the connection kept (wire.CallContext; issue #202).
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
	case <-c.Done(): // the connection dropped: reconnect and send it once more
	default:
		return m, err
	}
	if rerr := d.reconnect(ctx, c); rerr != nil {
		return nil, fmt.Errorf("%w (reconnecting to the runner failed: %v)", err, rerr)
	}
	return d.conn().CallContext(ctx, op, d.runnerParams(p), limit(timeout))
}

// runnerParams translates a request's driver change count ("since") to the current runner's.
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

// reconnect replaces a dropped connection: the runner on its port if it is still up, a
// fresh runner otherwise. One attempt runs per drop, with its own time budget
// (reconnectBudget), so a caller whose context ends early does not leave a half-started
// runner for the next caller to stop and start again; it is shared by everyone who notices.
func (d *Driver) reconnect(ctx context.Context, dropped *wire.Conn) error {
	d.connMu.Lock()
	if d.conn() != dropped {
		d.connMu.Unlock()
		return nil // someone else already reconnected
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
			bctx, cancel := context.WithTimeout(context.Background(), reconnectBudget())
			a.err = d.redial(bctx)
			cancel()
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

// redial connects again until ctx ends: connect reuses a runner of this build still serving
// this simulator and starts one otherwise.
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
		case <-time.After(time.Second): // between reconnect attempts only
		}
	}
}

// reconnectFor bounds one reconnect, a runner restart included.
var reconnectFor = 60 * time.Second

// reconnectBudget is reconnectFor, twice that in CI mode: a runner can take a minute to start
// on a slow CI Mac (issue #53). A CI-only allowance; outside CI the budget is as before.
func reconnectBudget() time.Duration {
	if ci.On() {
		return 2 * reconnectFor
	}
	return reconnectFor
}

// Raw runs any op and returns its JSON answer (diagnostics, research ops).
func (d *Driver) Raw(ctx context.Context, op string, p map[string]any) (json.RawMessage, error) {
	m, err := d.call(ctx, op, p, 60*time.Second)
	if err != nil {
		return nil, err
	}
	return m.Raw, nil
}

// pump forwards one connection's pushed events to the driver's subscribers. When the
// connection drops unexpectedly it reconnects, and tells subscribers only if that fails.
func (d *Driver) pump(c *wire.Conn, sub <-chan *wire.Msg) {
	for m := range sub {
		var e struct {
			Pkg string `json:"pkg"`
		}
		m.Decode(&e)
		d.fanout(driver.Event{Kind: m.Ev, Seq: d.seq(m.Seq), Pkg: e.Pkg})
	}
	if d.conn() != c || d.closed.Load() || d.stopped.Load() {
		return // replaced by a newer connection, or closed on purpose
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
		select { // never block: the consumer may have stopped reading
		case ch <- e:
		default:
		}
	}
}

// Events streams the runner's pushes. The stream outlives a reconnect; "disconnected" means
// the runner could not be reached again.
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
