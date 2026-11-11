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
	"sync"
	"time"
)

// Handler serves one request. send streams a partial response (for watch-like commands).
type Handler func(ctx context.Context, req Request, send func(Response)) Response

// Server accepts CLI connections on the unix socket.
type Server struct {
	handlers map[string]Handler
	idle     time.Duration
	mu       sync.Mutex
	active   int
	lastUse  time.Time
	stop     chan struct{}
}

func NewServer(idle time.Duration) *Server {
	return &Server{handlers: map[string]Handler{}, idle: idle, lastUse: time.Now(), stop: make(chan struct{})}
}

func (s *Server) Handle(cmd string, h Handler) { s.handlers[cmd] = h }

// Stop makes Serve return.
func (s *Server) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
}

// Serve listens until Stop, or until idle with no client for the idle duration.
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
	// Closing the listener would unlink the path, whoever's socket it is by then; the
	// deferred check below removes it instead.
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	os.Chmod(path, 0o600)
	mine, _ := os.Stat(path)
	defer func() {
		// Remove the socket only if it is still ours: a replacement daemon may already
		// have created its own at the same path.
		if cur, err := os.Stat(path); err == nil && mine != nil && os.SameFile(cur, mine) {
			os.Remove(path)
		}
	}()
	log.Printf("tb daemon listening on %s (pid %d)", path, os.Getpid())

	go func() {
		t := time.NewTicker(time.Minute) // idle check only
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
	// One goroutine reads the connection for its whole life and hands each request line to
	// the loop below. When the client goes away (Ctrl-C, a closed terminal) it sees the end
	// of the stream at once, even while a request runs, and cancels ctx: the running command
	// stops at its next cancellation point instead of finishing for nobody and keeping the
	// device busy (issue #57). A client sends its next request only after the answer to the
	// last one, so the reader is waiting on the socket whenever a request runs.
	ctx, cancel := context.WithCancel(context.Background())
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
		start := time.Now()
		resp := func() (resp Response) {
			defer func() {
				if p := recover(); p != nil {
					log.Printf("panic in %s: %v", req.Cmd, p)
					resp = Response{Exit: 3, Text: fmt.Sprintf("internal error: %v", p)}
				}
			}()
			return h(ctx, req, func(part Response) {
				part.More = true
				write(part)
			})
		}()
		if ctx.Err() != nil {
			log.Printf("%s: cancelled, the client disconnected (ended after %s)", req.Cmd, time.Since(start).Round(time.Millisecond))
			return
		}
		resp.More = false
		if err := write(resp); err != nil {
			return
		}
	}
}
