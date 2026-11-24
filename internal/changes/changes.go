package changes

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

func News(e driver.Event, seq int64) bool { return e.Seq > seq }

const RestlessAfter = 400 * time.Millisecond

func SilentAfter() time.Duration { return silentAfter() }

var silentAfter = sync.OnceValue(func() time.Duration {
	if v, err := strconv.Atoi(os.Getenv("TB_SILENT_MS")); err == nil {
		return time.Duration(v) * time.Millisecond
	}
	return 1500 * time.Millisecond
})

var ErrLost = errors.New("lost the connection to the device agent")

type Source interface {
	Seq() int64
	Events() (<-chan driver.Event, func())
}

type Watch struct {
	src     Source
	events  <-chan driver.Event
	stop    func()
	first   time.Duration
	gap     time.Duration
	backoff bool
}

func Subscribe(src Source, silentAfter time.Duration, backoff bool) *Watch {
	events, stop := src.Events()
	return &Watch{src: src, events: events, stop: stop, first: silentAfter, gap: silentAfter, backoff: backoff}
}

func (w *Watch) Stop() { w.stop() }

func (w *Watch) Wait(ctx context.Context, seq int64, deadline time.Time, early bool) (silent time.Duration, err error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	gap := w.gap
	w.gap = w.first
	dl := time.NewTimer(time.Until(deadline))
	defer dl.Stop()
	var restless <-chan time.Time
	if w.src.Seq() > seq {
		if early {
			return 0, nil
		}
		restless = time.After(RestlessAfter)
	}
	var hush <-chan time.Time
	if gap > 0 {
		hush = time.After(gap)
	}
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-dl.C:
			return 0, nil
		case <-restless:
			return 0, nil
		case <-hush:
			if w.backoff {
				w.gap = min(2*gap, 2*w.first)
			} else {
				w.gap = gap
			}
			return gap, nil
		case e, ok := <-w.events:
			if !ok || e.Kind == "disconnected" {
				return 0, ErrLost
			}
			if !News(e, seq) {
				continue
			}
			hush = nil
			if e.Kind == "settled" || early {
				return 0, nil
			}
			if restless == nil {
				restless = time.After(RestlessAfter)
			}
		}
	}
}
