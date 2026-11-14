// Package session is what the daemon serves to agents: persistent device connections,
// named devices (alice, bob), compact screen listings with stable refs, command runs that
// answer with what changed, event-driven waits and watches, and recording passed commands
// into a flow.
package session

import (
	"context"
	"encoding/json"
	"errors"
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
//
// Locking (issues #63, #153):
//   - Service.mu guards the service's maps and every Dev's Name. It is held briefly, and never
//     while waiting for a Dev's mu.
//   - Dev.mu is the device lock: one command at a time per device. Closing a session (tb close,
//     a dropped connection, the daemon stopping) takes it too, so the driver is never closed
//     and the device never freed under a running command: closing waits for the command. A
//     command that gets the lock after its session closed fails (lockOpen).
//   - stepsMu guards the recorded steps (below).
//
// The order is Dev.mu, then Service.mu: a command may read a name while it holds its device.
type Dev struct {
	mu        sync.Mutex // the device lock: one command at a time per device
	Info      device.Info
	Name      string // alias, e.g. "alice"; guarded by Service.mu
	Drv       driver.Driver
	runner    *engine.Runner
	refs      *look.Refs
	last      *look.Listing
	AppID     string
	steps     []string      // YAML of commands that passed (refs resolved); see stepsMu
	lastDoEnd *look.Listing // the screen when the last `do` ended
	lock      *devlock.Lock // the device, held while the session is open
	closed    bool          // the session was closed (shut); guarded by mu
	// tb's agent on the device was stopped (shut with stopAgent); guarded by mu
	agentStopped bool
	// dialogMark: the JavaScript dialogs tb answered up to this mark (driver.DialogLog) were
	// printed by do or look already (dialogNotes); guarded by mu
	dialogMark int

	// steps is written only with both mu and stepsMu held (by do and clear), so it can
	// be read with either: tb log takes stepsMu alone and answers at once, even during a
	// long do (issue #63).
	stepsMu sync.Mutex
}

// shut closes the session's connection and frees the device for other tb processes, once;
// the caller holds d.mu. With stopAgent (tb close) it also stops tb's agent on the device (it
// holds what only one test tool can hold at a time); otherwise the agent is kept for the next
// run. A session already closed without stopping the agent (its connection dropped first)
// still has the agent stopped, if no other tb process has taken the device since; if one
// has, the error says the agent was left.
func (d *Dev) shut(stopAgent bool) error {
	r, canStop := d.Drv.(driver.Releaser)
	stop := stopAgent && canStop && !d.agentStopped
	release := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		r.Release(ctx)
		cancel()
		d.agentStopped = true
	}
	if !d.closed {
		d.closed = true
		if stop {
			release()
		} else {
			d.Drv.Close()
		}
		d.lock.Release()
		return nil
	}
	if !stop {
		return nil
	}
	l, err := devlock.TryAcquire(d.Info.Serial, "tb close (stopping tb's agent)")
	if b, busy := devlock.IsBusy(err); busy {
		return fmt.Errorf("tb's agent was left on it: the device is in use by %s", b.Owner)
	}
	defer l.Release()
	release()
	return nil
}

// lockOpen takes d's device lock for a command. It fails, without the lock, when the session
// was closed while the command waited for it (tb close, or its connection dropped).
func (s *Service) lockOpen(d *Dev) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return fmt.Errorf("the session on %s was closed while this command waited for it", s.labelOf(d))
	}
	return nil
}

// Service holds the devices.
type Service struct {
	mu      sync.Mutex
	devs    map[string]*Dev   // by serial
	names   map[string]string // session name -> serial; kept when the connection drops
	closing map[string]*Dev   // by serial: dropped sessions that still hold their device
	// by serial: sessions whose connection dropped, until tb close (which stops tb's agent on
	// the device without connecting to it again) or a new session on the device
	dropped  map[string]*Dev
	stopping bool // CloseAll ran: a session that connects now is closed at once

	// openDrv connects to a device: connect.Open, or a fake driver in tests.
	openDrv func(context.Context, device.Info) (driver.Driver, error)
}

func New() *Service {
	return &Service{devs: map[string]*Dev{}, names: map[string]string{}, closing: map[string]*Dev{},
		dropped: map[string]*Dev{}, openDrv: connect.Open}
}

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
// process) and left otherwise. Each close takes the device lock, as tb close does: the daemon
// has cancelled the commands and waited for them, so it is free unless one is stuck. Sessions
// whose connection dropped are among them: one still closing (it waits for the command that
// ran when it dropped) is waited for, or killed, like the others (issue #124).
func (s *Service) CloseAll(ctx context.Context) {
	s.mu.Lock()
	seen := map[*Dev]bool{}
	var devs []*Dev
	for _, m := range []map[string]*Dev{s.devs, s.closing, s.dropped} {
		for _, d := range m {
			if !seen[d] {
				seen[d] = true
				devs = append(devs, d)
			}
		}
	}
	s.devs = map[string]*Dev{}
	s.names = map[string]string{}
	s.dropped = map[string]*Dev{}
	s.stopping = true
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, d := range devs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.mu.Lock()
			defer d.mu.Unlock()
			d.shut(false)
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

// matches reports whether d is the device a names: by alias, serial or name.
func (d *Dev) matches(a devArgs) bool {
	if a.Platform != "" && a.Platform != string(d.Info.Platform) {
		return false
	}
	return d.Name == a.Device || d.Info.Serial == a.Device || strings.EqualFold(d.Info.Name, a.Device)
}

// openDev finds the open session a command is for, without connecting: nil when none is open
// on that device. With no device named, it is the one session open (on the platform, if one
// is given). The caller holds s.mu.
func (s *Service) openDev(a devArgs) (*Dev, error) {
	if a.Device != "" {
		for _, d := range s.devs {
			if d.matches(a) {
				return d, nil
			}
		}
		return nil, nil
	}
	found := onPlatform(s.devs, a.Platform)
	if len(found) > 1 {
		var names []string
		for _, d := range found {
			names = append(names, d.label())
		}
		sort.Strings(names)
		return nil, fmt.Errorf("several devices are open (%s): say which with -d", strings.Join(names, ", "))
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return nil, nil
}

// onPlatform lists the sessions in m on platform (all of them for "").
func onPlatform(m map[string]*Dev, platform string) []*Dev {
	var found []*Dev
	for _, d := range m {
		if platform == "" || platform == string(d.Info.Platform) {
			found = append(found, d)
		}
	}
	return found
}

// resolve finds (and connects, if needed) the device a command is for.
func (s *Service) resolve(ctx context.Context, a devArgs) (*Dev, error) {
	s.mu.Lock()
	if d, err := s.openDev(a); d != nil || err != nil {
		s.mu.Unlock()
		return d, err
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

// label names the device in messages; the caller holds s.mu (it reads d.Name).
func (d *Dev) label() string {
	if d.Name != "" {
		return d.Name + " (" + d.Info.Name + ")"
	}
	return d.Info.Name + " " + d.Info.Serial
}

// labelOf is label for a caller that does not hold s.mu.
func (s *Service) labelOf(d *Dev) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return d.label()
}

// nameOf is d.Name for a caller that does not hold s.mu.
func (s *Service) nameOf(d *Dev) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return d.Name
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
			s.mu.Lock()
			old := s.closing[info.Serial]
			s.mu.Unlock()
			if old != nil {
				// This daemon still holds it: the session whose connection dropped frees the
				// device once the command running on it ends.
				return nil, fmt.Errorf("%s (%s) is still closing: its connection dropped while a command ran on it. Try again in a moment", info.Name, info.Serial)
			}
			return nil, fmt.Errorf("%s (%s) is in use by %s. One tb at a time per device: wait for it to finish, or use another device", info.Name, info.Serial, b.Owner)
		}
		lock = l
	}
	drv, err := s.openDrv(ctx, info)
	if err != nil {
		lock.Release()
		return nil, err
	}
	// Subscribe before anything else, so a drop that comes at once is not missed.
	ev, stopEvents := drv.Events()
	d := &Dev{Info: info, Name: name, Drv: drv, refs: look.NewRefs(), lock: lock}
	d.runner = engine.New(drv, engine.Options{ArtifactsDir: filepath.Join(daemon.Dir(), "artifacts")})
	markDialogs(d)
	s.mu.Lock()
	if s.stopping {
		// CloseAll has run: no one would close this session.
		s.mu.Unlock()
		stopEvents()
		drv.Close()
		lock.Release()
		return nil, fmt.Errorf("the tb daemon is stopping")
	}
	s.devs[info.Serial] = d
	delete(s.dropped, info.Serial) // the new session's close stops the agent
	if name != "" {
		s.names[name] = info.Serial
	}
	s.mu.Unlock()
	go func() { // forget a device whose agent connection drops
		dropped := false
		for e := range ev {
			if e.Kind == "disconnected" {
				dropped = true
				break
			}
		}
		stopEvents()
		if !dropped {
			return
		}
		s.mu.Lock()
		if s.devs[info.Serial] == d {
			delete(s.devs, info.Serial)
			s.dropped[info.Serial] = d
		}
		s.closing[info.Serial] = d
		s.mu.Unlock()
		// Close the dropped connection (a web session's Chrome goes with it) and free the
		// device, once the command running on it has ended (issues #124, #153).
		d.mu.Lock()
		d.shut(false)
		d.mu.Unlock()
		s.mu.Lock()
		if s.closing[info.Serial] == d {
			delete(s.closing, info.Serial)
		}
		s.mu.Unlock()
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
	open := map[string]string{} // serial -> session name
	for k, d := range s.devs {
		open[k] = d.Name
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
		if name, ok := open[d.Serial]; ok {
			jd.Open = true
			jd.Session = name
			if name != "" {
				line += "  session: " + name
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
		if err := s.lockOpen(d); err != nil {
			return fail(1, "%v", err)
		}
		d.AppID = a.App
		d.mu.Unlock()
	}
	who := d.Info.String()
	if name := s.nameOf(d); name != "" {
		who = name + ": " + who
	}
	return daemon.Response{OK: true, Text: fmt.Sprintf("connected %s in %s\n", who, time.Since(t0).Round(time.Millisecond))}
}

// close ends a session, or all of them: it stops tb's agent on the device, closes the
// connection and frees the device. It takes the device lock like any command on the session,
// so it waits for a command running on the device to end instead of closing the driver under
// it (issue #153); a command still waiting for the device then fails. `close all` closes the
// devices side by side, so a busy one does not hold up the others. To stop a long command
// rather than wait for it, its client cancels it (Ctrl-C, issue #57), or `tb daemon stop`
// cancels every command; a `tb close` cancelled while it waits leaves the session open.
func (s *Service) close(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[devArgs](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	if a.Device == "all" {
		// The open sessions, and those whose connection dropped: closing one of those stops
		// tb's agent on its device, as tb close -d does. With -p, only that platform's
		// (`tb release -p ios` frees the iOS devices only, issue #184).
		s.mu.Lock()
		var devs []*Dev
		serials := map[string]bool{} // the devices closed
		for _, m := range []map[string]*Dev{s.devs, s.dropped} {
			for _, d := range onPlatform(m, a.Platform) {
				devs = append(devs, d)
				serials[d.Info.Serial] = true
			}
		}
		s.mu.Unlock()
		if a.Platform != "" && len(devs) == 0 {
			return daemon.Response{OK: true, Text: fmt.Sprintf("no %s session is open: nothing to close\n", a.Platform)}
		}
		notes := make([]string, len(devs))
		var wg sync.WaitGroup
		for i, d := range devs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if label, err := s.closeDev(ctx, d); err != nil && !errors.Is(err, errCloseCancelled) {
					notes[i] = label + ": " + err.Error()
				}
			}()
		}
		wg.Wait()
		// The names left on the devices closed are those of sessions whose connection dropped
		// (one replaced since by a session under another name). With -p, the names on other
		// platforms' devices are kept, and so are those of dropped sessions a cancelled close
		// left (tb close -d still finds them by name).
		s.mu.Lock()
		for name, serial := range s.names {
			_, open := s.devs[serial]
			_, dropped := s.dropped[serial]
			if !open && !dropped && (a.Platform == "" || serials[serial]) {
				delete(s.names, name)
			}
		}
		s.mu.Unlock()
		if ctx.Err() != nil {
			return fail(1, "tb close was cancelled: the sessions it had not closed yet are left open\n")
		}
		text := "closed all devices\n"
		if a.Platform != "" {
			text = fmt.Sprintf("closed all %s sessions\n", a.Platform)
		}
		for _, n := range notes {
			if n != "" {
				text += "  " + n + "\n"
			}
		}
		return daemon.Response{OK: true, Text: text}
	}
	// Find the session without connecting: a device with no session is not connected to only
	// to be closed again (issue #124).
	s.mu.Lock()
	d, err := s.openDev(a)
	if d == nil && err == nil {
		d, err = s.droppedDev(a)
	}
	s.mu.Unlock()
	if err != nil {
		return fail(2, "%v", err)
	}
	if d == nil {
		if a.Device == "" {
			return daemon.Response{OK: true, Text: "no tb session is open: nothing to close\n"}
		}
		info, err := device.Pick(device.List(ctx), device.Platform(a.Platform), a.Device)
		if err != nil {
			return fail(2, "%v", err)
		}
		// A device named by its name: the session that dropped on its current serial, if any.
		s.mu.Lock()
		d = s.dropped[info.Serial]
		s.mu.Unlock()
		if d == nil {
			return daemon.Response{OK: true, Text: fmt.Sprintf("no tb session is open on %s %s: nothing to close\n", info.Name, info.Serial)}
		}
	}
	label, err := s.closeDev(ctx, d)
	if errors.Is(err, errCloseCancelled) {
		return fail(1, "tb close was cancelled: %s is left open\n", label)
	}
	text := "closed " + label + "\n"
	if err != nil {
		text += "  " + err.Error() + "\n"
	}
	return daemon.Response{OK: true, Text: text}
}

var errCloseCancelled = errors.New("tb close was cancelled")

// droppedDev finds the session, among those whose connection dropped, that a names: by its
// session name (kept when the connection drops) or serial, not by the device's name (an
// emulator of that name may run on another serial since: close looks a device name up in the
// device list, then finds the session dropped on its current serial); with no device named,
// the one dropped session (on the platform, if one is given), or an error naming several. The
// caller holds s.mu.
func (s *Service) droppedDev(a devArgs) (*Dev, error) {
	if a.Device == "" {
		found := onPlatform(s.dropped, a.Platform)
		if len(found) > 1 {
			var names []string
			for _, d := range found {
				names = append(names, d.label())
			}
			sort.Strings(names)
			return nil, fmt.Errorf("several sessions whose connection dropped are left (%s): say which with -d", strings.Join(names, ", "))
		}
		if len(found) == 1 {
			return found[0], nil
		}
		return nil, nil
	}
	serial := a.Device
	if sr, ok := s.names[a.Device]; ok {
		serial = sr
	}
	d := s.dropped[serial]
	if d == nil || (a.Platform != "" && a.Platform != string(d.Info.Platform)) {
		return nil, nil
	}
	return d, nil
}

// closeDev closes one session under its device lock and returns its label. It waits for the
// command running on the device; if ctx ended meanwhile (the client went away, or the daemon
// is stopping), it leaves the session open and returns errCloseCancelled. Any other error says
// what was left on the device (tb's agent).
func (s *Service) closeDev(ctx context.Context, d *Dev) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s.mu.Lock()
	label := d.label()
	if ctx.Err() != nil {
		s.mu.Unlock()
		return label, errCloseCancelled
	}
	if s.devs[d.Info.Serial] == d {
		delete(s.devs, d.Info.Serial)
	}
	if s.dropped[d.Info.Serial] == d {
		delete(s.dropped, d.Info.Serial)
	}
	if d.Name != "" && s.names[d.Name] == d.Info.Serial {
		delete(s.names, d.Name)
	}
	s.mu.Unlock()
	return label, d.shut(true)
}
