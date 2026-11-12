// Package session is what the daemon serves to agents: persistent device connections,
// named devices (alice, bob), compact screen listings with stable refs, command runs that
// answer with what changed, event-driven waits and watches, and recording passed commands
// into a flow.
package session

import (
	"context"
	"encoding/json"
	"fmt"
	_ "image/jpeg"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/elforama/testbuddy/internal/connect"
	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/devlock"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/look"
)

// Dev is one connected device.
type Dev struct {
	mu        sync.Mutex // one command at a time per device
	Info      device.Info
	Name      string // alias, e.g. "alice"
	Drv       driver.Driver
	runner    *engine.Runner
	refs      *look.Refs
	last      *look.Listing
	AppID     string
	steps     []string      // YAML of commands that passed (refs resolved); see stepsMu
	lastDoEnd *look.Listing // the screen when the last `do` ended
	lock      *devlock.Lock // the device, held while the session is open

	// steps is written only with both mu and stepsMu held (by do and clear), so it can
	// be read with either: tb log takes stepsMu alone and answers at once, even during a
	// long do (issue #63).
	stepsMu sync.Mutex
}

// release stops tb's agent on the device (it holds what only one test tool can hold at a
// time), closes the connection, and frees the device for other tb processes.
func (d *Dev) release() {
	if r, ok := d.Drv.(driver.Releaser); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		r.Release(ctx)
		cancel()
	} else {
		d.Drv.Close()
	}
	d.lock.Release()
}

// Service holds the devices.
type Service struct {
	mu    sync.Mutex
	devs  map[string]*Dev   // by serial
	names map[string]string // session name -> serial; kept when the connection drops
}

func New() *Service { return &Service{devs: map[string]*Dev{}, names: map[string]string{}} }

// Register installs the daemon handlers.
func (s *Service) Register(srv *daemon.Server) {
	srv.Handle("ping", func(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
		return daemon.Response{OK: true, Text: daemon.BuildID()}
	})
	srv.Handle("devices", s.devices)
	srv.Handle("open", s.open)
	srv.Handle("close", s.close)
	srv.Handle("look", s.look)
	srv.Handle("locate", s.locate)
	srv.Handle("do", s.do)
	srv.Handle("watch", s.watch)
	srv.Handle("shot", s.shot)
	srv.Handle("log", s.log)
	srv.Handle("save", s.save)
	srv.Handle("clear", s.clear)
	srv.Handle("stop", func(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
		daemon.StopAfterReply(ctx)
		return daemon.Response{OK: true, Text: "tb daemon stopped"}
	})
	srv.OnStop(s.CloseAll)
}

// CloseAll closes every open session's driver: the daemon is stopping (issue #58). A web
// session's Chrome shuts down and its temporary profile is removed; a phone's or simulator's
// agent connection closes and the agent is kept for the next run, as at the end of a `tb
// test` run (`tb close` is what stops it). Each device's lock is freed. The drivers close
// side by side; those still closing when ctx ends are killed where they can be (web: Chrome's
// process) and left otherwise.
func (s *Service) CloseAll(ctx context.Context) {
	s.mu.Lock()
	devs := make([]*Dev, 0, len(s.devs))
	for _, d := range s.devs {
		devs = append(devs, d)
	}
	s.devs = map[string]*Dev{}
	s.names = map[string]string{}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, d := range devs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.Drv.Close()
			d.lock.Release()
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		log.Printf("closing the devices took too long; killing what can be killed")
		for _, d := range devs {
			if k, ok := d.Drv.(driver.Killer); ok {
				k.Kill()
			}
		}
	}
}

// Common args every device command takes.
type devArgs struct {
	Device   string `json:"device"`
	Platform string `json:"platform"`
}

func fail(exit int, format string, args ...any) daemon.Response {
	return daemon.Response{Exit: exit, Text: fmt.Sprintf(format, args...)}
}

// resolve finds (and connects, if needed) the device a command is for.
func (s *Service) resolve(ctx context.Context, a devArgs) (*Dev, error) {
	s.mu.Lock()
	// by alias, serial or name among connected devices
	if a.Device != "" {
		for _, d := range s.devs {
			if a.Platform != "" && a.Platform != string(d.Info.Platform) {
				continue
			}
			if d.Name == a.Device || d.Info.Serial == a.Device || strings.EqualFold(d.Info.Name, a.Device) {
				s.mu.Unlock()
				return d, nil
			}
		}
	} else if len(s.devs) == 1 && a.Platform == "" {
		for _, d := range s.devs {
			s.mu.Unlock()
			return d, nil
		}
	} else if len(s.devs) > 1 && a.Platform == "" {
		var names []string
		for _, d := range s.devs {
			names = append(names, d.label())
		}
		sort.Strings(names)
		s.mu.Unlock()
		return nil, fmt.Errorf("several devices are open (%s): say which with -d", strings.Join(names, ", "))
	}
	// A named device whose connection dropped (a `tb test` run took the device's agent):
	// connect again under the same name.
	name, want := "", a.Device
	if serial, ok := s.names[a.Device]; ok {
		name, want = a.Device, serial
	}
	s.mu.Unlock()
	info, err := device.Pick(device.List(ctx), device.Platform(a.Platform), want)
	if err != nil {
		return nil, err
	}
	return s.connect(ctx, info, name)
}

func (d *Dev) label() string {
	if d.Name != "" {
		return d.Name + " (" + d.Info.Name + ")"
	}
	return d.Info.Name + " " + d.Info.Serial
}

func (s *Service) connect(ctx context.Context, info device.Info, name string) (*Dev, error) {
	s.mu.Lock()
	if d, ok := s.devs[info.Serial]; ok {
		if info.Headless && !d.Info.Headless {
			s.mu.Unlock()
			return nil, fmt.Errorf("the browser session is already open; close it before opening it headless")
		}
		if name != "" && d.Name != "" && d.Name != name {
			// Another session (another agent, usually) has this device: opening it under a
			// new name would take that session over.
			s.mu.Unlock()
			return nil, fmt.Errorf("%s (%s) is already open as session %q. One session per device: use -d %s, or close it first (tb close -d %s)",
				info.Name, info.Serial, d.Name, d.Name, d.Name)
		}
		if name != "" {
			d.Name = name
			s.names[name] = info.Serial
		}
		s.mu.Unlock()
		return d, nil
	}
	s.mu.Unlock()
	// One tb at a time per phone or simulator (devlock): the session holds the device until
	// it is closed. A `tb test` run or another agent's session on it is told who has it.
	var lock *devlock.Lock
	if info.Platform != device.Web {
		label := info.Serial
		if name != "" {
			label = name
		}
		l, err := devlock.TryAcquire(info.Serial, fmt.Sprintf("tb session %q (tb open/do/look)", label))
		if b, busy := devlock.IsBusy(err); busy {
			return nil, fmt.Errorf("%s (%s) is in use by %s. One tb at a time per device: wait for it to finish, or use another device", info.Name, info.Serial, b.Owner)
		}
		lock = l
	}
	drv, err := connect.Open(ctx, info)
	if err != nil {
		lock.Release()
		return nil, err
	}
	d := &Dev{Info: info, Name: name, Drv: drv, refs: look.NewRefs(), lock: lock}
	d.runner = engine.New(drv, engine.Options{ArtifactsDir: filepath.Join(daemon.Dir(), "artifacts")})
	s.mu.Lock()
	s.devs[info.Serial] = d
	if name != "" {
		s.names[name] = info.Serial
	}
	s.mu.Unlock()
	go func() { // forget a device whose agent connection drops
		ev, stop := drv.Events()
		defer stop()
		for e := range ev {
			if e.Kind == "disconnected" {
				s.mu.Lock()
				if s.devs[info.Serial] == d {
					delete(s.devs, info.Serial)
				}
				s.mu.Unlock()
				d.lock.Release()
				return
			}
		}
	}()
	return d, nil
}

func decode[T any](req daemon.Request) (T, error) {
	var v T
	if len(req.Args) > 0 {
		if err := json.Unmarshal(req.Args, &v); err != nil {
			return v, err
		}
	}
	return v, nil
}

func (s *Service) devices(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	all := device.List(ctx)
	s.mu.Lock()
	open := map[string]*Dev{}
	for k, d := range s.devs {
		open[k] = d
	}
	s.mu.Unlock()
	var b strings.Builder
	type jdev struct {
		device.Info
		Session string `json:"session,omitempty"`
		Open    bool   `json:"open"`
	}
	var out []jdev
	for _, d := range all {
		jd := jdev{Info: d}
		line := d.String()
		if o := open[d.Serial]; o != nil {
			jd.Open = true
			jd.Session = o.Name
			if o.Name != "" {
				line += "  session: " + o.Name
			} else {
				line += "  connected"
			}
		}
		out = append(out, jd)
		b.WriteString(line + "\n")
	}
	if len(all) == 0 {
		b.WriteString("no devices: start an Android emulator or boot an iOS simulator\n")
	}
	js, _ := json.Marshal(out)
	return daemon.Response{OK: true, Text: b.String(), JSON: js}
}

type openArgs struct {
	devArgs
	Name     string `json:"name"`
	App      string `json:"app"`
	Headless bool   `json:"headless"`
}

func (s *Service) open(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[openArgs](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	info, err := device.Pick(device.List(ctx), device.Platform(a.Platform), a.Device)
	if err != nil {
		// maybe it is an alias of an open device
		if d, err2 := s.resolve(ctx, a.devArgs); err2 == nil {
			info = d.Info
		} else {
			return fail(2, "%v", err)
		}
	}
	if a.Headless && info.Platform != device.Web {
		return fail(2, "--headless requires a web session")
	}
	if info.Platform == device.Web && a.App != "" && !device.IsURL(a.App) {
		return fail(2, "web sessions need a URL for --app")
	}
	info.Headless = info.Headless || a.Headless
	s.mu.Lock()
	for _, d := range s.devs {
		if a.Name != "" && d.Name == a.Name && d.Info.Serial != info.Serial {
			s.mu.Unlock()
			return fail(2, "the name %q is already used by %s", a.Name, d.Info.Name)
		}
	}
	s.mu.Unlock()
	t0 := time.Now()
	d, err := s.connect(ctx, info, a.Name)
	if err != nil {
		return fail(1, "%v", err)
	}
	if a.App != "" {
		d.mu.Lock()
		d.AppID = a.App
		d.mu.Unlock()
	}
	who := d.Info.String()
	if d.Name != "" {
		who = d.Name + ": " + who
	}
	return daemon.Response{OK: true, Text: fmt.Sprintf("connected %s in %s\n", who, time.Since(t0).Round(time.Millisecond))}
}

func (s *Service) close(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[devArgs](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	if a.Device == "all" {
		s.mu.Lock()
		for k, d := range s.devs {
			d.release()
			delete(s.devs, k)
		}
		s.names = map[string]string{}
		s.mu.Unlock()
		return daemon.Response{OK: true, Text: "closed all devices\n"}
	}
	d, err := s.resolve(ctx, a)
	if err != nil {
		return fail(2, "%v", err)
	}
	s.mu.Lock()
	delete(s.devs, d.Info.Serial)
	if d.Name != "" {
		delete(s.names, d.Name)
	}
	s.mu.Unlock()
	d.release()
	return daemon.Response{OK: true, Text: "closed " + d.label() + "\n"}
}
