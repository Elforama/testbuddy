package changes

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

type fakeSource struct {
	mu  sync.Mutex
	seq int64
	ch  chan driver.Event
}

func newSource(seq int64) *fakeSource { return &fakeSource{seq: seq, ch: make(chan driver.Event, 64)} }

func (f *fakeSource) Seq() int64 { f.mu.Lock(); defer f.mu.Unlock(); return f.seq }
func (f *fakeSource) Events() (<-chan driver.Event, func()) {
	return f.ch, func() {}
}

func (f *fakeSource) change(kind string) {
	f.mu.Lock()
	f.seq++
	seq := f.seq
	f.mu.Unlock()
	f.ch <- driver.Event{Kind: kind, Seq: seq}
}

func (f *fakeSource) stale(kind string) { f.ch <- driver.Event{Kind: kind, Seq: f.Seq()} }

func TestWait(t *testing.T) {
	const gap = 100 * time.Millisecond
	const deadline = 2 * time.Second
	lost := func(context.CancelFunc) error { return ErrLost }
	cases := []struct {
		name     string
		gap      time.Duration
		early    bool
		before   func(*fakeSource)
		during   func(*fakeSource, context.CancelFunc)
		silent   time.Duration
		err      func(context.CancelFunc) error
		min, max time.Duration
	}{
		{name: "silence: the net fires", gap: gap, silent: gap, min: gap, max: gap + 80*time.Millisecond},
		{name: "no net: the deadline ends it", gap: 0, min: deadline, max: deadline + 100*time.Millisecond},
		{name: "one stale settled does not hold the net off", gap: gap,
			during: func(f *fakeSource, _ context.CancelFunc) { f.stale("settled") },
			silent: gap, min: gap, max: gap + 80*time.Millisecond},
		{name: "repeated stale events do not hold the net off", gap: gap,
			during: func(f *fakeSource, _ context.CancelFunc) {
				for i := 0; i < 15; i++ {
					f.stale([]string{"settled", "changed", "other"}[i%3])
					time.Sleep(10 * time.Millisecond)
				}
			},
			silent: gap, min: gap, max: gap + 80*time.Millisecond},
		{name: "news settled wakes at once", gap: gap,
			during: func(f *fakeSource, _ context.CancelFunc) { f.change("settled") }, max: 50 * time.Millisecond},
		{name: "news changed wakes after the restless interval, and disarms the net", gap: gap,
			during: func(f *fakeSource, _ context.CancelFunc) { f.change("changed") },
			min:    RestlessAfter, max: RestlessAfter + 100*time.Millisecond},
		{name: "news of an unknown kind wakes after the restless interval", gap: gap,
			during: func(f *fakeSource, _ context.CancelFunc) { f.change("something") },
			min:    RestlessAfter, max: RestlessAfter + 100*time.Millisecond},
		{name: "a change before the wait: restless", gap: 0,
			before: func(f *fakeSource) { f.mu.Lock(); f.seq++; f.mu.Unlock() },
			min:    RestlessAfter, max: RestlessAfter + 100*time.Millisecond},
		{name: "early: news changed wakes at once", gap: gap, early: true,
			during: func(f *fakeSource, _ context.CancelFunc) { f.change("changed") }, max: 50 * time.Millisecond},
		{name: "early: a change before the wait returns at once", gap: gap, early: true,
			before: func(f *fakeSource) { f.mu.Lock(); f.seq++; f.mu.Unlock() }, max: 20 * time.Millisecond},
		{name: "early: a stale event does not wake", gap: gap, early: true,
			during: func(f *fakeSource, _ context.CancelFunc) { f.stale("changed") },
			silent: gap, min: gap, max: gap + 80*time.Millisecond},
		{name: "events closed: ErrLost", gap: gap,
			during: func(f *fakeSource, _ context.CancelFunc) { close(f.ch) }, err: lost, max: 50 * time.Millisecond},
		{name: "disconnected (even stale): ErrLost", gap: gap,
			during: func(f *fakeSource, _ context.CancelFunc) { f.stale("disconnected") }, err: lost, max: 50 * time.Millisecond},
		{name: "context cancelled: its error", gap: gap,
			during: func(_ *fakeSource, cancel context.CancelFunc) { cancel() },
			err:    func(context.CancelFunc) error { return context.Canceled }, max: 50 * time.Millisecond},
		{name: "context already cancelled", gap: gap,
			err: func(cancel context.CancelFunc) error { cancel(); return context.Canceled }, max: 20 * time.Millisecond},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := newSource(5)
			if c.before != nil {
				c.before(f)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var want error
			if c.err != nil && c.during == nil {
				want = c.err(cancel)
			}
			w := Subscribe(f, c.gap, true)
			defer w.Stop()
			if c.during != nil {
				go c.during(f, cancel)
			}
			start := time.Now()
			silent, err := w.Wait(ctx, 5, start.Add(deadline), c.early)
			took := time.Since(start)
			if c.err != nil && c.during != nil {
				want = c.err(cancel)
			}
			if !errors.Is(err, want) || (want == nil && err != nil) {
				t.Errorf("err %v, want %v", err, want)
			}
			if silent != c.silent {
				t.Errorf("silent %s, want %s", silent, c.silent)
			}
			if took < c.min || took > c.max {
				t.Errorf("took %s, want %s to %s", took.Round(time.Millisecond), c.min, c.max)
			}
		})
	}
}

func TestWaitGaps(t *testing.T) {
	const gap = 40 * time.Millisecond
	for _, c := range []struct {
		name    string
		backoff bool
		want    []time.Duration
	}{
		{"back-off, then the cap", true, []time.Duration{gap, 2 * gap, 2 * gap, 2 * gap, 0, gap, 2 * gap}},
		{"no back-off", false, []time.Duration{gap, gap, gap, 0, gap}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := newSource(5)
			w := Subscribe(f, gap, c.backoff)
			defer w.Stop()
			for i, want := range c.want {
				seq := f.Seq()
				if want == 0 {
					f.change("settled")
				}
				silent, err := w.Wait(context.Background(), seq, time.Now().Add(time.Second), false)
				if err != nil || silent != want {
					t.Fatalf("wait %d: silent %s, err %v; want %s", i, silent, err, want)
				}
			}
		})
	}
}

func TestSilentAfter(t *testing.T) {
	if SilentAfter() != SilentAfter() {
		t.Fatal("not stable")
	}
	if _, set := os.LookupEnv("TB_SILENT_MS"); !set && SilentAfter() != 1500*time.Millisecond {
		t.Errorf("default %s", SilentAfter())
	}
}
