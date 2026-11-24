package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/orchestrate"
	"github.com/elforama/testbuddy/internal/timeline"
)

type flowTimelines struct {
	on bool
	mu sync.Mutex
	by map[string]*timeline.Recorder
}

func newFlowTimelines(on bool) *flowTimelines {
	return &flowTimelines{on: on, by: map[string]*timeline.Recorder{}}
}

func (t *flowTimelines) open(dir string, trace *engine.Trace) *timeline.Recorder {
	if !t.on {
		return nil
	}
	tl := timeline.New()
	trace.Tee(tl.Line)
	tl.OnWarning(func(msg string) { trace.Printf("", 0, "%s", msg) })
	t.mu.Lock()
	old := t.by[dir]
	t.by[dir] = tl
	t.mu.Unlock()
	old.Close()
	return tl
}

func (t *flowTimelines) closeAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for dir, tl := range t.by {
		tl.Close()
		delete(t.by, dir)
	}
}

func (t *flowTimelines) close(dir string, r orchestrate.Result) string {
	t.mu.Lock()
	tl := t.by[dir]
	delete(t.by, dir)
	t.mu.Unlock()
	if tl == nil {
		return ""
	}
	defer tl.Close()
	file := r.Job.Flow.Path
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, file); err == nil && !strings.HasPrefix(rel, "..") {
			file = rel
		}
	}
	m := timeline.Meta{Flow: r.Job.Flow.Name(), File: file, Status: string(r.Status), Version: version,
		Started: r.Start, Duration: r.Duration}
	if r.Status == engine.Failed && r.Err != nil {
		m.Error = r.Err.Error()
	}
	roles := make([]string, 0, len(r.Devices))
	for role := range r.Devices {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		d := r.Devices[role]
		m.Devices = append(m.Devices, timeline.Device{Role: role, Platform: string(d.Platform), Name: d.Name, Serial: d.Serial})
	}
	tl.Finish(m)
	path := filepath.Join(dir, "timeline.html")
	if err := tl.WriteFile(path); err != nil {
		fmt.Fprintf(os.Stderr, "timeline: %v\n", err)
		return ""
	}
	return path
}
