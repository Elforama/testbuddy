package engine

import (
	"context"
	"sync"
	"time"
)

// Signals lets devices in a multi-device flow wait for each other: one device runs
// `signal: sent`, another `waitForSignal: sent`. Waiting is a channel close, not a poll.
type Signals struct {
	mu sync.Mutex
	ch map[string]chan struct{}
}

func NewSignals() *Signals { return &Signals{ch: map[string]chan struct{}{}} }

func (s *Signals) get(name string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.ch[name]
	if !ok {
		c = make(chan struct{})
		s.ch[name] = c
	}
	return c
}

// Send raises the signal. Raising it twice is harmless.
func (s *Signals) Send(name string) {
	c := s.get(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-c:
	default:
		close(c)
	}
}

// Wait blocks until the signal is raised, the timeout passes, or ctx ends.
func (s *Signals) Wait(ctx context.Context, name string, timeout time.Duration) error {
	select {
	case <-s.get(name):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(timeout):
		return failf("signal %q did not arrive within %s", name, timeout)
	}
}
