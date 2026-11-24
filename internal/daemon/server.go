package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"time"
)

const (
	stopRequestsLimit = 5 * time.Second
	stopHooksLimit    = 10 * time.Second
)

type Handler func(ctx context.Context, req Request, send func(Response)) Response

type Server struct {
	handlers map[string]Handler
	idle     time.Duration
	mu       sync.Mutex
	active   int
	lastUse  time.Time
	stop     chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	reqs     sync.WaitGroup
	onStop   []func(context.Context)
	stopped  chan struct{}
}

func NewServer(idle time.Duration) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{handlers: map[string]Handler{}, idle: idle, lastUse: time.Now(), stop: make(chan struct{}),
		ctx: ctx, cancel: cancel, stopped: make(chan struct{})}
}

func (s *Server) Handle(cmd string, h Handler) { s.handlers[cmd] = h }

func (s *Server) OnStop(f func(ctx context.Context)) { s.onStop = append(s.onStop, f) }

type stopAfterReplyKey struct{}

func StopAfterReply(ctx context.Context) {
	if f, ok := ctx.Value(stopAfterReplyKey{}).(*atomic.Bool); ok {
		f.Store(true)
	}
}

func (s *Server) StopOnSignals(sigs ...os.Signal) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, sigs...)
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-ch:
			signal.Stop(ch)
			log.Printf("%v, stopping", sig)
			s.Stop()
		case <-done:
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { signal.Stop(ch); close(done) }) }
}

func (s *Server) shutdown() {
	s.Stop()
	s.mu.Lock()
	s.cancel()
	s.mu.Unlock()
	drained := make(chan struct{})
	go func() { s.reqs.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(stopRequestsLimit):
		log.Printf("requests still running after %s; stopping anyway", stopRequestsLimit)
	}
	ctx, cancel := context.WithTimeout(context.Background(), stopHooksLimit)
	defer cancel()
	for _, f := range s.onStop {
		f(ctx)
	}
	close(s.stopped)
}

func (s *Server) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
}

func (s *Server) Serve() error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	path := SocketPath()
	if Running() {
		return errors.New("a tb daemon is already running")
	}
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	os.Chmod(path, 0o600)
	mine, _ := os.Stat(path)
	defer func() {
		if cur, err := os.Stat(path); err == nil && mine != nil && os.SameFile(cur, mine) {
			os.Remove(path)
		}
	}()
	log.Printf("tb daemon listening on %s (pid %d)", path, os.Getpid())
	defer s.shutdown()
	defer ln.Close()

	go func() {
		t := time.NewTicker(min(time.Minute, max(s.idle/4, 5*time.Millisecond)))
		defer t.Stop()
		for {
			select {
			case <-t.C:
				s.mu.Lock()
				idle := s.active == 0 && time.Since(s.lastUse) > s.idle
				s.mu.Unlock()
				if idle {
					log.Printf("idle for %s, exiting", s.idle)
					s.Stop()
				}
			case <-s.stop:
				ln.Close()
				return
			}
		}
	}()

	for {
		c, err := ln.Accept()
		if err != nil {
			select {
			case <-s.stop:
				return nil
			default:
				return err
			}
		}
		go s.serveConn(c)
	}
}

func (s *Server) serveConn(c net.Conn) {
	defer c.Close()
	s.mu.Lock()
	s.active++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.active--
		s.lastUse = time.Now()
		s.mu.Unlock()
	}()
	var wmu sync.Mutex
	write := func(resp Response) error {
		b, _ := json.Marshal(resp)
		wmu.Lock()
		defer wmu.Unlock()
		_, err := c.Write(append(b, '\n'))
		return err
	}
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	lines := make(chan []byte)
	go func() {
		defer cancel()
		defer close(lines)
		r := bufio.NewReaderSize(c, 1<<20)
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				return
			}
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		line, ok := <-lines
		if !ok {
			return
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			write(Response{Exit: 2, Text: "bad request: " + err.Error()})
			continue
		}
		h := s.handlers[req.Cmd]
		if h == nil {
			write(Response{Exit: 2, Text: fmt.Sprintf("unknown command %q", req.Cmd)})
			continue
		}
		s.mu.Lock()
		if s.ctx.Err() != nil {
			s.mu.Unlock()
			write(Response{Exit: 1, Text: "the tb daemon is stopping"})
			return
		}
		s.reqs.Add(1)
		s.mu.Unlock()
		keep, stopping := s.serveRequest(ctx, req, h, write)
		if stopping {
			<-s.stopped
			return
		}
		if !keep {
			return
		}
	}
}

func (s *Server) serveRequest(ctx context.Context, req Request, h Handler, write func(Response) error) (keep, stopping bool) {
	defer s.reqs.Done()
	var stopAfter atomic.Bool
	rctx := context.WithValue(ctx, stopAfterReplyKey{}, &stopAfter)
	start := time.Now()
	resp := func() (resp Response) {
		defer func() {
			if p := recover(); p != nil {
				log.Printf("panic in %s: %v", req.Cmd, p)
				resp = Response{Exit: 3, Text: fmt.Sprintf("internal error: %v", p)}
			}
		}()
		return h(rctx, req, func(part Response) {
			part.More = true
			write(part)
		})
	}()
	if ctx.Err() != nil {
		if s.ctx.Err() != nil {
			log.Printf("%s: cancelled, the daemon is stopping (ended after %s)", req.Cmd, time.Since(start).Round(time.Millisecond))
			write(Response{Exit: 1, Text: req.Cmd + " was cancelled: the tb daemon stopped"})
		} else {
			log.Printf("%s: cancelled, the client disconnected (ended after %s)", req.Cmd, time.Since(start).Round(time.Millisecond))
		}
		return false, false
	}
	resp.More = false
	err := write(resp)
	if stopAfter.Load() {
		s.Stop()
		return false, true
	}
	return err == nil, false
}
