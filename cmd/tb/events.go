package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/engine"
	marks "github.com/elforama/testbuddy/internal/mark"
)

type runEvents struct {
	hub *marks.Hub
	srv *marks.Server

	mu       sync.Mutex
	reversed map[string]bool
}

var reverseADB = func(ctx context.Context, serial string, port int, remove bool) error {
	p := "tcp:" + strconv.Itoa(port)
	args := []string{"reverse", p, p}
	if remove {
		args = []string{"reverse", "--remove", p}
	}
	_, err := device.NewADB(serial).Run(ctx, args...)
	return err
}

func serveEvents(a *args, con *console) (*runEvents, string) {
	if !a.flag("annotate") {
		return nil, ""
	}
	port := a.num("events-port", marks.DefaultPort)
	if port == 0 {
		return nil, ""
	}
	hub := marks.NewHub()
	srv, err := marks.Serve(hub, port, daemon.Dir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "--annotate: no event endpoint (%v); the videos show no events. Another tb test --annotate may hold port %d: pass --events-port\n", err, port)
		return nil, ""
	}
	if !con.quiet {
		con.printf("events: %s (tb mark \"text\", or POST from the app)\n", srv.URL)
		if srv.Shared {
			con.printf("  another tb run is taking events too: tb mark sends there unless given --url %s\n", srv.URL)
		}
	}
	return &runEvents{hub: hub, srv: srv, reversed: map[string]bool{}}, srv.URL
}

func (e *runEvents) reach(ctx context.Context, d device.Info) {
	if e == nil || d.Platform != device.Android {
		return
	}
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := reverseADB(c, d.Serial, e.srv.Port, false); err != nil {
		fmt.Fprintf(os.Stderr, "--annotate: %s cannot reach the event endpoint (adb reverse: %v)\n", d.Serial, err)
		return
	}
	e.mu.Lock()
	e.reversed[d.Serial] = true
	e.mu.Unlock()
}

func (e *runEvents) forJob(ds map[string]device.Info) func(role string, start, end time.Time) []engine.AnnotateEvent {
	if e == nil {
		return nil
	}
	return func(role string, start, end time.Time) []engine.AnnotateEvent {
		d := ds[role]
		var out []engine.AnnotateEvent
		for _, ev := range e.hub.Between(start, end, d.Serial, d.Name, role) {
			out = append(out, engine.AnnotateEvent{At: ev.At, Text: ev.Text})
		}
		return out
	}
}

func (e *runEvents) close() {
	if e == nil {
		return
	}
	e.srv.Close()
	e.mu.Lock()
	defer e.mu.Unlock()
	for serial := range e.reversed {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		reverseADB(c, serial, e.srv.Port, true)
		cancel()
	}
	if kept, dropped := e.hub.Count(); dropped > 0 {
		fmt.Fprintf(os.Stderr, "--annotate: kept the first %d events; %d more were dropped\n", kept, dropped)
	}
}

const markUsage = `Usage: tb mark [options] <text>...
       tb mark [options] -          one event per line from stdin

Sends events to a running tb test --record --annotate: each shows in the videos' event log,
below the step's text, at the time tb got it. Several texts are sent as one burst.
  -d, --device DEVICE   the device the event is for (serial, UDID, name or role; default: every
                        device recording)
  --url URL             the endpoint (default: $TB_EVENTS_URL, else the running tb test's)
`

func cmdMark(argv []string) int {
	return runMark(argv, os.Stdin, os.Stdout, os.Stderr)
}

func runMark(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a, err := parseArgs(argv, flagSpec{[]string{"device", "d"}, textFlag}, flagSpec{[]string{"url"}, textFlag},
		flagSpec{[]string{"help", "h"}, boolFlag})
	if err != nil {
		fmt.Fprintln(stderr, err)
		fmt.Fprint(stderr, markUsage)
		return 2
	}
	if a.flag("help") {
		fmt.Fprint(stdout, markUsage)
		return 0
	}
	texts := a.pos
	if len(texts) == 1 && texts[0] == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		texts = strings.Split(string(b), "\n")
	}
	var keep []string
	for _, t := range texts {
		if strings.TrimSpace(t) != "" {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		fmt.Fprint(stderr, markUsage)
		return 2
	}
	url, err := marks.Find(a.str("url"), daemon.Dir())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := marks.Send(ctx, url, a.str("device"), keep)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "sent %d event(s)\n", n)
	return 0
}
